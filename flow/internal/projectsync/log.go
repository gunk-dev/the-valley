package projectsync

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"the-valley/flow/internal/location"
)

// LogPath returns the absolute path of the sync log new syncs append to
// (location's SyncLog).
func LogPath() (string, error) {
	l, err := location.Host()
	if err != nil {
		return "", err
	}
	return l.SyncLog()
}

// WriteLog appends a single block of timestamped sync results to the klaus
// sync log. The source argument tags where the sync was triggered from (e.g.
// "session", "launch", "cli") so a tail of the log explains who started it.
// Failures to write are swallowed — a background sync that can't log should
// not affect the foreground caller.
func WriteLog(source string, results []SyncResult) {
	path, err := LogPath()
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	writeLog(f, source, time.Now(), results)
}

func writeLog(w io.Writer, source string, now time.Time, results []SyncResult) {
	fmt.Fprintf(w, "[%s] sync source=%s projects=%d\n", now.UTC().Format(time.RFC3339), source, len(results))
	for _, r := range results {
		line := fmt.Sprintf("    %-20s %-14s %s", r.Name, r.Status, r.Branch)
		if r.Detail != "" {
			line += "  " + r.Detail
		}
		fmt.Fprintln(w, line)
	}
}
