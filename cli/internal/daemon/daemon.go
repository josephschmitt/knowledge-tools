// Package daemon is the long-running process that replaces the systemd timers / launchd plists.
// One daemon runs per vault instance (registered by `knowledge-tools install`). It owns:
//   - an internal cron scheduler firing compile / synthesize / resolve on their cadences,
//   - an fsnotify watcher on inbox/.compile/ for on-demand compile/synthesize/resolve requests, and
//   - startup catch-up: a job whose scheduled tick elapsed while the daemon was down runs once
//     on launch (replacing systemd's Persistent=true).
//
// Cross-process serialization still goes through the per-instance file lock in internal/vault (so
// a manual `knowledge-tools compile` can't race the daemon); an in-process mutex additionally
// keeps the daemon's own jobs from overlapping.
package daemon

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/josephschmitt/knowledge-tools/cli/internal/config"
	"github.com/josephschmitt/knowledge-tools/cli/internal/jobs"
	"github.com/josephschmitt/knowledge-tools/cli/internal/vault"
	"github.com/robfig/cron/v3"
)

type daemon struct {
	ctx context.Context
	cfg *config.Config
	mu  sync.Mutex // serializes the daemon's own jobs (cross-process is the file lock)
}

// requestJobs maps each on-demand request sentinel (under inbox/.compile/) to the job it triggers.
// The MCP/REST service drops these; the watcher and the startup drain consume them. Compile keeps
// its original "request" filename (unchanged service contract); synthesize/resolve mirror it.
var requestJobs = []struct {
	file string
	job  jobs.Job
}{
	{"request", jobs.JobCompile},
	{"request-synthesize", jobs.JobSynthesize},
	{"request-resolve", jobs.JobResolve},
}

// Run starts the daemon and blocks until ctx is cancelled (the caller wires SIGINT/SIGTERM).
// version is the running binary's build version, recorded to daemon.json so `status` can flag a
// stale daemon after a binary upgrade.
func Run(ctx context.Context, cfg *config.Config, version string) error {
	if err := cfg.RequireRepo(); err != nil {
		return err
	}
	d := &daemon{ctx: ctx, cfg: cfg}

	// Make sure the coordination dir exists so the watcher can attach and the snapshot can write.
	if err := os.MkdirAll(cfg.CompileDir(), 0o755); err != nil {
		return err
	}

	log.Printf("knowledge-tools daemon starting (instance=%s, repo=%s, version=%s)", cfg.Instance, cfg.Repo, version)
	log.Printf("schedules: compile=%q synthesize=%q resolve=%q", cfg.CompileSchedule, cfg.SynthesizeSchedule, cfg.ResolveSchedule)

	// Publish next-run times + this daemon's version right away.
	jobs.RefreshSchedules(cfg)
	jobs.WriteDaemonInfo(cfg, version)

	c := cron.New(cron.WithParser(jobs.CronParser), cron.WithChain(cron.SkipIfStillRunning(cron.DefaultLogger)))
	if _, err := c.AddFunc(cfg.CompileSchedule, func() { d.runJob(jobs.JobCompile, false, jobs.Overrides{}) }); err != nil {
		return err
	}
	if _, err := c.AddFunc(cfg.SynthesizeSchedule, func() { d.runJob(jobs.JobSynthesize, false, jobs.Overrides{}) }); err != nil {
		return err
	}
	if _, err := c.AddFunc(cfg.ResolveSchedule, func() { d.runJob(jobs.JobResolve, false, jobs.Overrides{}) }); err != nil {
		return err
	}
	c.Start()
	defer c.Stop()

	// Watch for on-demand job requests. Started BEFORE catch-up: a pre-existing request file
	// fires no fsnotify event, so if catch-up (which can run for minutes) attached the watcher only
	// afterward, a request landing during it would be lost. Any request that does arrive while
	// catch-up holds the in-process mutex is skipped — but the watcher keeps the file (it consumes it
	// only after the job actually runs), and the post-catch-up drain below picks it up.
	watchErr := make(chan error, 1)
	go func() { watchErr <- d.watchRequests() }()

	// Startup catch-up for ticks missed while the daemon was down.
	d.catchUp()

	// Drain on-demand requests that arrived (and were skipped under the busy mutex) during catch-up.
	for _, r := range requestJobs {
		d.handleRequest(r.file, r.job)
	}

	select {
	case <-ctx.Done():
		log.Printf("knowledge-tools daemon shutting down")
		return nil
	case err := <-watchErr:
		return err
	}
}

// runJob runs one job, skipping (not queueing) if another daemon job is already running — the file
// lock would make it a no-op anyway, so we skip early. ErrLocked (another process holds the lock)
// is not an error. Returns whether the job actually ran (false = skipped because busy), so the
// on-demand path can keep the request file for a later retry instead of consuming it on a skip.
// ov carries the per-request model/effort override (empty for scheduled/catch-up ticks).
func (d *daemon) runJob(job jobs.Job, manual bool, ov jobs.Overrides) (ran bool) {
	if !d.mu.TryLock() {
		log.Printf("%s: another job is running — skipping this tick", job)
		return false
	}
	defer d.mu.Unlock()

	log.Printf("%s: starting (manual=%v)", job, manual)
	var err error
	if job == jobs.JobCompile {
		err = jobs.Compile(d.ctx, d.cfg, manual, ov)
	} else {
		err = jobs.RunIssueJob(d.ctx, d.cfg, job, ov)
	}
	switch err {
	case nil:
		log.Printf("%s: done", job)
	case vault.ErrLocked:
		log.Printf("%s: lock held by another process — skipped", job)
	default:
		log.Printf("%s: error: %v", job, err)
	}
	return true
}

// catchUp runs each job once if its scheduled cadence elapsed since it last ran (or it has never
// run) — the daemon's stand-in for systemd Persistent=true. Compile is cheap/no-op on an empty
// inbox, so a startup compile is harmless; synthesize/resolve only run when genuinely overdue.
func (d *daemon) catchUp() {
	now := time.Now()
	type spec struct {
		job      jobs.Job
		schedule string
	}
	for _, s := range []spec{
		{jobs.JobCompile, d.cfg.CompileSchedule},
		{jobs.JobResolve, d.cfg.ResolveSchedule},
		{jobs.JobSynthesize, d.cfg.SynthesizeSchedule},
	} {
		if d.overdue(s.schedule, jobs.LastRun(d.cfg, s.job), now) {
			log.Printf("%s: overdue at startup — catching up", s.job)
			d.runJob(s.job, false, jobs.Overrides{})
		}
	}
}

// overdue reports whether a scheduled tick elapsed since lastRun (or the job never ran). A bad
// schedule is treated as not-overdue (the cron AddFunc above already surfaced the parse error).
func (d *daemon) overdue(schedule string, lastRun time.Time, now time.Time) bool {
	if lastRun.IsZero() {
		return true
	}
	sched, err := jobs.CronParser.Parse(schedule)
	if err != nil {
		return false
	}
	// The first scheduled fire after lastRun is in the past → a tick was missed.
	return !sched.Next(lastRun).After(now)
}

// watchRequests watches inbox/.compile/ for the MCP/REST service's on-demand request files (one
// per job: "request" → compile, "request-synthesize", "request-resolve"). On a create/write of one
// it consumes (deletes) the file and runs that job. Watching the dir (not each file) is uniform
// across Linux/macOS and survives the files not existing yet — no systemd .path unit, no macOS
// WatchPaths/mtime hack.
func (d *daemon) watchRequests() error {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer func() { _ = w.Close() }()

	dir := d.cfg.CompileDir()
	if err := w.Add(dir); err != nil {
		return err
	}
	log.Printf("watching %s for on-demand compile/synthesize/resolve requests", dir)

	for {
		select {
		case <-d.ctx.Done():
			return nil
		case ev, ok := <-w.Events:
			if !ok {
				return nil
			}
			if ev.Op&(fsnotify.Create|fsnotify.Write) == 0 {
				continue
			}
			for _, r := range requestJobs {
				if ev.Name == filepath.Join(dir, r.file) {
					d.handleRequest(r.file, r.job)
				}
			}
		case err, ok := <-w.Errors:
			if !ok {
				return nil
			}
			log.Printf("watcher error: %v", err)
		}
	}
}

// handleRequest runs an on-demand job if its request sentinel exists under inbox/.compile/,
// consuming the file only after the job actually runs. Removing on a skip (mutex busy) would drop
// the request silently; keeping it lets the watcher re-fire on a later write and lets the
// post-catch-up drain pick it up.
//
// The request body carries an optional per-request model/effort override written by the MCP/REST
// service (requestPayload JSON). Parsing is tolerant: a missing/legacy body (an older service wrote
// a bare ISO timestamp) or any unmarshal error degrades to no override, so the run falls back to
// the config/env chain — keeping a new daemon compatible with an old service.
func (d *daemon) handleRequest(file string, job jobs.Job) {
	request := filepath.Join(d.cfg.CompileDir(), file)
	if _, err := os.Stat(request); err != nil {
		return
	}
	log.Printf("on-demand %s requested", job)
	if d.runJob(job, true, readRequestOverrides(request)) {
		_ = os.Remove(request)
	}
}

// requestPayload is the JSON body the MCP/REST service writes into a request sentinel. requested_at
// is informational; model/effort are the optional per-request overrides. All fields are optional.
type requestPayload struct {
	Model  string `json:"model"`
	Effort string `json:"effort"`
}

// readRequestOverrides parses a request file's body into job overrides, returning the zero value on
// any read/parse error (missing body, legacy bare-timestamp body, malformed JSON) — the run then
// falls back to the config/env chain.
func readRequestOverrides(path string) jobs.Overrides {
	b, err := os.ReadFile(path)
	if err != nil {
		return jobs.Overrides{}
	}
	var p requestPayload
	if err := json.Unmarshal(b, &p); err != nil {
		return jobs.Overrides{}
	}
	return jobs.Overrides{Model: p.Model, Effort: p.Effort}
}
