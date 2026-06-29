// Package daemon is the long-running process that replaces the systemd timers / launchd plists.
// One daemon runs per vault instance (registered by `knowledge-tools install`). It owns:
//   - an internal cron scheduler firing compile / synthesize / resolve on their cadences,
//   - an fsnotify watcher on inbox/.compile/request for on-demand (manual) compiles, and
//   - startup catch-up: a job whose scheduled tick elapsed while the daemon was down runs once
//     on launch (replacing systemd's Persistent=true).
//
// Cross-process serialization still goes through the per-instance file lock in internal/vault (so
// a manual `knowledge-tools compile` can't race the daemon); an in-process mutex additionally
// keeps the daemon's own jobs from overlapping.
package daemon

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/josephschmitt/knowledge-tools/cli/internal/config"
	"github.com/josephschmitt/knowledge-tools/cli/internal/jobs"
	"github.com/josephschmitt/knowledge-tools/cli/internal/site"
	"github.com/josephschmitt/knowledge-tools/cli/internal/vault"
	"github.com/robfig/cron/v3"
)

type daemon struct {
	ctx context.Context
	cfg *config.Config
	mu  sync.Mutex // serializes the daemon's own jobs (cross-process is the file lock)
}

// Run starts the daemon and blocks until ctx is cancelled (the caller wires SIGINT/SIGTERM).
func Run(ctx context.Context, cfg *config.Config) error {
	if err := cfg.RequireRepo(); err != nil {
		return err
	}
	d := &daemon{ctx: ctx, cfg: cfg}

	// Make sure the coordination dir exists so the watcher can attach and the snapshot can write.
	if err := os.MkdirAll(cfg.CompileDir(), 0o755); err != nil {
		return err
	}

	log.Printf("knowledge-tools daemon starting (instance=%s, repo=%s)", cfg.Instance, cfg.Repo)
	log.Printf("schedules: compile=%q synthesize=%q resolve=%q", cfg.CompileSchedule, cfg.SynthesizeSchedule, cfg.ResolveSchedule)

	// Publish next-run times right away.
	jobs.RefreshSchedules(cfg)

	c := cron.New(cron.WithParser(jobs.CronParser), cron.WithChain(cron.SkipIfStillRunning(cron.DefaultLogger)))
	if _, err := c.AddFunc(cfg.CompileSchedule, func() { d.runJob(jobs.JobCompile, false) }); err != nil {
		return err
	}
	if _, err := c.AddFunc(cfg.SynthesizeSchedule, func() { d.runJob(jobs.JobSynthesize, false) }); err != nil {
		return err
	}
	if _, err := c.AddFunc(cfg.ResolveSchedule, func() { d.runJob(jobs.JobResolve, false) }); err != nil {
		return err
	}
	c.Start()
	defer c.Stop()

	// Startup catch-up for ticks missed while the daemon was down.
	d.catchUp()

	// Watch for on-demand compile requests.
	watchErr := make(chan error, 1)
	go func() { watchErr <- d.watchRequests() }()

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
// is not an error.
func (d *daemon) runJob(job jobs.Job, manual bool) {
	if !d.mu.TryLock() {
		log.Printf("%s: another job is running — skipping this tick", job)
		return
	}
	defer d.mu.Unlock()

	log.Printf("%s: starting (manual=%v)", job, manual)
	var err error
	if job == jobs.JobCompile {
		err = jobs.Compile(d.ctx, d.cfg, manual)
	} else {
		err = jobs.RunIssueJob(d.ctx, d.cfg, job)
	}
	switch {
	case err == nil:
		log.Printf("%s: done", job)
	case err == vault.ErrLocked:
		log.Printf("%s: lock held by another process — skipped", job)
	default:
		log.Printf("%s: error: %v", job, err)
	}

	// Keep the published site fresh after a successful compile, when enabled. Soft so a Quartz
	// hiccup never escalates; the lock is already released, so site acquires its own.
	if job == jobs.JobCompile && err == nil && d.cfg.SiteEnable {
		log.Printf("site: rebuilding after compile")
		if serr := site.Build(d.ctx, d.cfg, site.Options{Soft: true}); serr != nil && serr != vault.ErrLocked {
			log.Printf("site: error: %v", serr)
		}
	}
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
			d.runJob(s.job, false)
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

// watchRequests watches inbox/.compile/ for the MCP's on-demand compile request file. On a
// create/write of "request" it consumes (deletes) the file and runs a manual compile. Watching the
// dir (not the file) is uniform across Linux/macOS and survives the file not existing yet — no
// systemd .path unit, no macOS WatchPaths/mtime hack.
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
	request := filepath.Join(dir, "request")
	log.Printf("watching %s for on-demand compile requests", request)

	for {
		select {
		case <-d.ctx.Done():
			return nil
		case ev, ok := <-w.Events:
			if !ok {
				return nil
			}
			if ev.Name == request && ev.Op&(fsnotify.Create|fsnotify.Write) != 0 {
				log.Printf("on-demand compile requested")
				_ = os.Remove(request) // consume so a later write re-triggers
				d.runJob(jobs.JobCompile, true)
			}
		case err, ok := <-w.Errors:
			if !ok {
				return nil
			}
			log.Printf("watcher error: %v", err)
		}
	}
}
