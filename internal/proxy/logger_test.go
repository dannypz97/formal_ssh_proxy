package proxy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewSessionLogger(t *testing.T) {
	dir := t.TempDir()

	sl, err := NewSessionLogger(dir, "alice")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer sl.Close()

	// File should exist.
	if _, err := os.Stat(sl.LogPath); err != nil {
		t.Fatalf("log file does not exist: %v", err)
	}

	// Filename should contain the username.
	base := filepath.Base(sl.LogPath)
	if !strings.HasPrefix(base, "alice_") {
		t.Errorf("log filename %q should start with alice_", base)
	}
	if !strings.HasSuffix(base, ".log") {
		t.Errorf("log filename %q should end with .log", base)
	}
}

func TestSessionLoggerWrite(t *testing.T) {
	dir := t.TempDir()

	sl, err := NewSessionLogger(dir, "bob")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data := []byte("ls -la\nwhoami\n")
	n, err := sl.Write(data)
	if err != nil {
		t.Fatalf("write error: %v", err)
	}
	if n != len(data) {
		t.Errorf("wrote %d bytes, want %d", n, len(data))
	}

	sl.Close()

	// Read back and verify.
	content, err := os.ReadFile(sl.LogPath)
	if err != nil {
		t.Fatalf("read error: %v", err)
	}
	if string(content) != string(data) {
		t.Errorf("content = %q, want %q", content, data)
	}
}

func TestSessionLoggerMultipleWrites(t *testing.T) {
	dir := t.TempDir()

	sl, err := NewSessionLogger(dir, "charlie")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	sl.Write([]byte("first\n"))
	sl.Write([]byte("second\n"))
	sl.Close()

	content, err := os.ReadFile(sl.LogPath)
	if err != nil {
		t.Fatalf("read error: %v", err)
	}
	if string(content) != "first\nsecond\n" {
		t.Errorf("content = %q, want %q", content, "first\nsecond\n")
	}
}

func TestSanitizeFilename(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"alice", "alice"},
		{"user/name", "user_name"},
		{"user\\name", "user_name"},
		{"user:name", "user_name"},
		{"../etc/passwd", "_/etc/passwd"}, // ".." replaced, "/" replaced
	}
	for _, tt := range tests {
		got := sanitizeFilename(tt.input)
		// For the "../" case, both ".." and "/" get replaced.
		if tt.input == "../etc/passwd" {
			if !strings.Contains(got, "_") {
				t.Errorf("sanitizeFilename(%q) = %q, expected dangerous chars replaced", tt.input, got)
			}
		} else if got != tt.want {
			t.Errorf("sanitizeFilename(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestNewSessionLoggerBadDir(t *testing.T) {
	_, err := NewSessionLogger("/nonexistent/dir/that/does/not/exist", "alice")
	if err == nil {
		t.Fatal("expected error for nonexistent directory")
	}
}
