package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const rawLog = "\x1b[50;17Rls\r\x1b[51;17Rcat /etc/shadow\r\x04"

func TestRunStdin(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run(nil, strings.NewReader(rawLog), &out, &errOut); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	if want := "ls\ncat /etc/shadow\n[Ctrl+D]\n"; out.String() != want {
		t.Errorf("got %q, want %q", out.String(), want)
	}
}

func TestRunGlobWithHeaders(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"bob_1.log", "bob_2.log"} {
		os.WriteFile(filepath.Join(dir, n), []byte(rawLog), 0o644)
	}

	var out, errOut bytes.Buffer
	if code := run([]string{filepath.Join(dir, "*.log")}, nil, &out, &errOut); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	got := out.String()
	for _, n := range []string{"bob_1.log", "bob_2.log"} {
		if !strings.Contains(got, "==> "+filepath.Join(dir, n)+" <==") {
			t.Errorf("missing header for %s in:\n%s", n, got)
		}
	}
	if strings.Count(got, "cat /etc/shadow") != 2 {
		t.Errorf("expected both logs cleaned, got:\n%s", got)
	}
}

func TestRunWriteFiles(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "bob_1.log")
	os.WriteFile(logPath, []byte(rawLog), 0o644)

	var out, errOut bytes.Buffer
	if code := run([]string{"-w", logPath}, nil, &out, &errOut); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	b, err := os.ReadFile(filepath.Join(dir, "bob_1.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "cat /etc/shadow") {
		t.Errorf("unexpected .txt content %q", b)
	}
	if raw, _ := os.ReadFile(logPath); string(raw) != rawLog {
		t.Error("raw log was modified")
	}
}

func TestRunErrors(t *testing.T) {
	dir := t.TempDir()
	tests := []struct {
		name string
		args []string
	}{
		{"missing file", []string{filepath.Join(dir, "nope.log")}},
		{"pattern with no matches", []string{filepath.Join(dir, "*.log")}},
		{"-w with stdin", []string{"-w"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			if code := run(tt.args, strings.NewReader(""), &out, &errOut); code == 0 {
				t.Errorf("expected failure, got exit 0")
			}
		})
	}
}
