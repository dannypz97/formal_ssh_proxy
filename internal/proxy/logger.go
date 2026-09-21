package proxy

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// SessionLogger writes raw client stdin to a per-session log file.
type SessionLogger struct {
	file     *os.File
	LogPath  string
	Username string
}

// NewSessionLogger creates a log file named <username>_<timestamp>.log in logDir.
func NewSessionLogger(logDir, username string) (*SessionLogger, error) {
	ts := time.Now().UTC().Format("2006-01-02T15-04-05Z")
	// Sanitize username for safe filenames.
	safe := sanitizeFilename(username)
	name := fmt.Sprintf("%s_%s.log", safe, ts)
	path := filepath.Join(logDir, name)

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o640)
	if err != nil {
		return nil, fmt.Errorf("creating session log %s: %w", path, err)
	}

	return &SessionLogger{
		file:     f,
		LogPath:  path,
		Username: username,
	}, nil
}

// Write implements io.Writer, appending raw stdin bytes to the log file.
func (sl *SessionLogger) Write(p []byte) (int, error) {
	return sl.file.Write(p)
}

// Close flushes and closes the log file.
func (sl *SessionLogger) Close() error {
	return sl.file.Close()
}

// sanitizeFilename replaces characters that are unsafe in filenames.
func sanitizeFilename(s string) string {
	replacer := strings.NewReplacer(
		"/", "_",
		"\\", "_",
		":", "_",
		"..", "_",
	)
	return replacer.Replace(s)
}
