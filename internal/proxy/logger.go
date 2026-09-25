package proxy

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// SessionLogger writes raw client stdin to a per-session log file.
type SessionLogger struct {
	file    *os.File
	LogPath string
}

// NewSessionLogger creates a log file named <username>_<timestamp>.log in
// logDir. The timestamp is the UTC session start time with millisecond
// precision; if two sessions for the same user still collide, a numeric
// suffix is added so that sessions never share (and interleave in) a file.
func NewSessionLogger(logDir, username string) (*SessionLogger, error) {
	ts := time.Now().UTC().Format("2006-01-02T15-04-05.000Z")
	base := fmt.Sprintf("%s_%s", sanitizeFilename(username), ts)

	for i := 0; i < 100; i++ {
		name := base + ".log"
		if i > 0 {
			name = fmt.Sprintf("%s-%d.log", base, i)
		}
		path := filepath.Join(logDir, name)

		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o640)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("creating session log %s: %w", path, err)
		}
		return &SessionLogger{file: f, LogPath: path}, nil
	}
	return nil, fmt.Errorf("creating session log: too many sessions named %s*.log", base)
}

// Write implements io.Writer, appending raw stdin bytes to the log file.
func (sl *SessionLogger) Write(p []byte) (int, error) {
	return sl.file.Write(p)
}

// Close closes the log file.
func (sl *SessionLogger) Close() error {
	return sl.file.Close()
}

// sanitizeFilename makes s safe to embed in a file name: ".." sequences and
// any character outside [A-Za-z0-9._@-] are replaced with "_".
func sanitizeFilename(s string) string {
	s = strings.ReplaceAll(s, "..", "_")
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '.', r == '_', r == '-', r == '@':
			return r
		}
		return '_'
	}, s)
}
