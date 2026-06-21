// Package vault holds the shared host-job primitives ported from scripts/vault-lib.sh: the
// per-instance lock, the pull-before / commit-and-push-after git discipline, the headless
// claude invocation, and the per-run logger. Compile, synthesize, and resolve are all built
// on these so they behave identically and never run concurrently.
package vault

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// Logger writes timestamped lines to both a per-run log file and stdout (the bash log() did this
// with tee), while exposing the raw file writer for subprocess output that should land only in
// the log file (the bash `>>"$LOG" 2>&1` redirects).
type Logger struct {
	file *os.File
	tee  io.Writer // file + stdout
}

// NewLogger opens (creating parent dirs) the given log path for append and returns a Logger that
// tees timestamped lines to it and stdout.
func NewLogger(path string) (*Logger, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	return &Logger{file: f, tee: io.MultiWriter(f, os.Stdout)}, nil
}

// Logf writes a timestamped line to the log file and stdout.
func (l *Logger) Logf(format string, args ...any) {
	fmt.Fprintf(l.tee, "%s %s\n", NowISO(), fmt.Sprintf(format, args...))
}

// File is the raw log-file writer for subprocess stdout/stderr (log-file only, no stdout echo).
func (l *Logger) File() io.Writer { return l.file }

// Close closes the underlying log file.
func (l *Logger) Close() error { return l.file.Close() }

// NowISO is the current time as ISO-8601 with offset, e.g. 2026-06-18T13:45:00-04:00. Go's
// RFC3339 already emits the colon offset, so the GNU/BSD date branching from vault-lib.sh is gone.
func NowISO() string { return time.Now().Format(time.RFC3339) }

// EpochISO formats epoch seconds as ISO-8601; zero/negative yields the empty string (matching
// the bash epoch_iso, whose empty input produced an empty string).
func EpochISO(epoch int64) string {
	if epoch <= 0 {
		return ""
	}
	return time.Unix(epoch, 0).Format(time.RFC3339)
}
