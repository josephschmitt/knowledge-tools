package jobs

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/josephschmitt/knowledge-tools/cli/internal/config"
	"github.com/josephschmitt/knowledge-tools/cli/internal/vault"
	"github.com/robfig/cron/v3"
)

// schedules.json mirrors scripts/vault-lib.sh:refresh_schedules and is consumed verbatim by
// service/src/vault.ts (readJobSchedules → vault_status.jobs). Each timestamp is a quoted ISO
// string or JSON null.
//
// Unlike the bash version — which shelled out to `systemctl show ... LastTriggerUSec /
// NextElapseUSecRealtime` and degraded to all-null on non-systemd/macOS hosts — the daemon owns
// the schedule, so next_run_at is computed from the configured cron expression (real on every
// platform) and last_run_at from each job's last-run epoch file (written on every run attempt).

type jobSchedule struct {
	LastRunAt *string `json:"last_run_at"`
	NextRunAt *string `json:"next_run_at"`
}

type schedulesJobs struct {
	Compile    jobSchedule `json:"compile"`
	Synthesize jobSchedule `json:"synthesize"`
	Resolve    jobSchedule `json:"resolve"`
}

type schedulesFile struct {
	Instance  string        `json:"instance"`
	UpdatedAt string        `json:"updated_at"`
	Jobs      schedulesJobs `json:"jobs"`
}

// CronParser is the cron grammar used everywhere: 5-field standard cron plus @descriptors and a
// CRON_TZ= prefix. Shared so the daemon's scheduler and the schedules.json snapshot agree.
var CronParser = cron.NewParser(
	cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor,
)

// nextRunISO parses a cron schedule and returns the next fire time after now as a quoted-ready ISO
// string, or nil when the schedule is empty/unparseable (→ JSON null).
func nextRunISO(schedule string, now time.Time) *string {
	if schedule == "" {
		return nil
	}
	sched, err := CronParser.Parse(schedule)
	if err != nil {
		return nil
	}
	iso := sched.Next(now).Format(time.RFC3339)
	return &iso
}

// lastRunISO returns the job's last-run time as an ISO string, or nil when it has never run.
func lastRunISO(cfg *config.Config, job Job) *string {
	if iso := vault.EpochISO(readEpoch(lastRunFile(cfg, job))); iso != "" {
		return &iso
	}
	return nil
}

// recordRun stamps a job's last-run epoch file with the current time. Best-effort.
func recordRun(cfg *config.Config, job Job) {
	_ = writeEpochNow(lastRunFile(cfg, job))
}

// RefreshSchedules writes inbox/.compile/schedules.json atomically with each job's last/next run.
// Best-effort: never returns an error so it's safe to defer on every job exit path (the bash
// `trap 'refresh_schedules' EXIT`). A failure to write leaves the previous snapshot in place.
func RefreshSchedules(cfg *config.Config) {
	now := time.Now()
	snap := schedulesFile{
		Instance:  cfg.Instance,
		UpdatedAt: now.Format(time.RFC3339),
		Jobs: schedulesJobs{
			Compile:    jobSchedule{LastRunAt: lastRunISO(cfg, JobCompile), NextRunAt: nextRunISO(cfg.JobSchedule(string(JobCompile)), now)},
			Synthesize: jobSchedule{LastRunAt: lastRunISO(cfg, JobSynthesize), NextRunAt: nextRunISO(cfg.JobSchedule(string(JobSynthesize)), now)},
			Resolve:    jobSchedule{LastRunAt: lastRunISO(cfg, JobResolve), NextRunAt: nextRunISO(cfg.JobSchedule(string(JobResolve)), now)},
		},
	}
	_ = writeJSONAtomic(filepath.Join(cfg.CompileDir(), "schedules.json"), snap)
}

// writeJSONAtomic marshals v (indented, trailing newline) and writes it via a temp file + rename,
// so a reader (the MCP service) never sees a half-written file. Used for status.json and
// schedules.json. Creates parent dirs.
func writeJSONAtomic(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp." + strconv.Itoa(os.Getpid())
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
