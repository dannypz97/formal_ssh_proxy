package summarizer

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/dparv/ssh-proxy/internal/config"
)

func TestCleanTranscript(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "plain text",
			input: "ls -la\n",
			want:  "ls -la\n",
		},
		{
			name:  "ANSI color codes stripped",
			input: "\x1b[32mhello\x1b[0m",
			want:  "hello",
		},
		{
			name:  "backspace deletes prev char",
			input: "helo\x7fo",
			want:  "helo",
		},
		{
			name:  "Ctrl+C annotated",
			input: "abc\x03def",
			want:  "abc[Ctrl+C]def",
		},
		{
			name:  "Ctrl+Z annotated",
			input: "sleep 999\x1a",
			want:  "sleep 999[Ctrl+Z]",
		},
		{
			name:  "Ctrl+D annotated",
			input: "exit\x04",
			want:  "exit[Ctrl+D]",
		},
		{
			name:  "Ctrl+L annotated",
			input: "\x0cls",
			want:  "[Ctrl+L]ls",
		},
		{
			name:  "Ctrl+R annotated",
			input: "\x12search",
			want:  "[Ctrl+R]search",
		},
		{
			name:  "unknown control chars stripped",
			input: "abc\x07\x0bdef",
			want:  "abcdef",
		},
		{
			name:  "carriage return to newline",
			input: "line1\r\nline2",
			want:  "line1\nline2",
		},
		{
			name:  "tab preserved",
			input: "col1\tcol2",
			want:  "col1\tcol2",
		},
		{
			name:  "cursor movement stripped",
			input: "text\x1b[Amore",
			want:  "textmore",
		},
		{
			name:  "application-mode arrow key (SS3) stripped",
			input: "ls\x1bOA\x1bOB -l",
			want:  "ls -l",
		},
		{
			name:  "bracketed paste markers stripped",
			input: "\x1b[200~rm -rf /\x1b[201~",
			want:  "rm -rf /",
		},
		{
			name:  "backspace removes whole UTF-8 character",
			input: "caf\xc3\xa9\x7fe",
			want:  "cafe",
		},
		{
			name:  "backspace does not cross a newline",
			input: "ls\n\x7f\x7fpwd",
			want:  "ls\npwd",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := cleanTranscript(tt.input)
			if got != tt.want {
				t.Errorf("cleanTranscript(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestFormatPrompt(t *testing.T) {
	result := FormatPrompt("whoami", 0)
	if !strings.Contains(result, "whoami") {
		t.Error("prompt should contain the transcript")
	}
	if !strings.Contains(result, "security") {
		t.Error("prompt should mention security")
	}
}

func TestFormatPromptTruncation(t *testing.T) {
	long := strings.Repeat("a", 200_000)
	result := FormatPrompt(long, DefaultMaxTranscriptLen)
	if !strings.Contains(result, "[truncated]") {
		t.Error("long transcript should be truncated")
	}
}

func TestFormatPromptCustomLimit(t *testing.T) {
	input := "abcdefghij" // 10 chars
	result := FormatPrompt(input, 5)
	if !strings.Contains(result, "[truncated]") {
		t.Error("should truncate at custom limit")
	}
	if !strings.Contains(result, "abcde") {
		t.Error("should keep first 5 chars")
	}
	if strings.Contains(result, "fghij") {
		t.Error("should not contain chars beyond limit")
	}
}

func TestFormatPromptTruncationKeepsUTF8Valid(t *testing.T) {
	result := FormatPrompt("aé", 2) // "é" is 2 bytes; cutting at 2 would split it
	if !utf8.ValidString(result) {
		t.Errorf("truncated prompt is not valid UTF-8: %q", result)
	}
}

func TestNewSummarizerDisabled(t *testing.T) {
	s := NewSummarizer(&config.LLMConfig{})
	if s != nil {
		t.Error("expected nil summarizer when api_key is empty")
	}
}

func TestNewSummarizerAnthropic(t *testing.T) {
	s := NewSummarizer(&config.LLMConfig{
		Provider: "anthropic",
		APIKey:   "sk-test",
		Model:    "claude-sonnet-4-6",
	})
	if s == nil {
		t.Fatal("expected non-nil summarizer")
	}
	if _, ok := s.(*anthropicSummarizer); !ok {
		t.Errorf("expected *anthropicSummarizer, got %T", s)
	}
}

func TestNewSummarizerOpenAI(t *testing.T) {
	s := NewSummarizer(&config.LLMConfig{
		Provider: "openai",
		APIKey:   "sk-test",
		Model:    "gpt-4o",
	})
	if s == nil {
		t.Fatal("expected non-nil summarizer")
	}
	if _, ok := s.(*openAISummarizer); !ok {
		t.Errorf("expected *openAISummarizer, got %T", s)
	}
}

func TestNewSummarizerUnknown(t *testing.T) {
	s := NewSummarizer(&config.LLMConfig{
		Provider: "gemini",
		APIKey:   "key",
	})
	if s != nil {
		t.Error("expected nil for unknown provider")
	}
}

func TestNewWorkerNil(t *testing.T) {
	w := NewWorker(nil, 4)
	if w != nil {
		t.Error("expected nil worker when summarizer is nil")
	}
}

// stubSummarizer is a test double that returns a fixed response.
type stubSummarizer struct {
	response string
	err      error
	called   int
}

func (s *stubSummarizer) Validate(context.Context) error { return nil }

func (s *stubSummarizer) Summarize(_ context.Context, _ string) (string, error) {
	s.called++
	return s.response, s.err
}

func TestWorkerSubmit(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "alice_2026-01-01T00-00-00Z.log")
	os.WriteFile(logPath, []byte("ls -la\nwhoami\n"), 0o644)

	stub := &stubSummarizer{response: "No security risks found."}
	w := NewWorker(stub, 2)

	w.Submit(logPath, "alice")
	w.Wait()

	if stub.called != 1 {
		t.Errorf("summarizer called %d times, want 1", stub.called)
	}

	summaryPath := strings.TrimSuffix(logPath, ".log") + ".summary.txt"
	content, err := os.ReadFile(summaryPath)
	if err != nil {
		t.Fatalf("summary file not found: %v", err)
	}
	if string(content) != "No security risks found." {
		t.Errorf("summary = %q, want %q", content, "No security risks found.")
	}
}

func TestWorkerEmptyTranscript(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "alice_2026-01-01T00-00-00Z.log")
	os.WriteFile(logPath, []byte(""), 0o644)

	stub := &stubSummarizer{response: "should not be called"}
	w := NewWorker(stub, 2)

	w.Submit(logPath, "alice")
	w.Wait()

	if stub.called != 0 {
		t.Errorf("summarizer should not be called for empty transcript, called %d times", stub.called)
	}
}

func TestWorkerConcurrencyBound(t *testing.T) {
	dir := t.TempDir()

	// Create a slow summarizer to test concurrency bounding.
	slow := &slowSummarizer{delay: 50 * time.Millisecond}
	w := NewWorker(slow, 2) // max 2 concurrent

	// Submit 4 jobs.
	for i := 0; i < 4; i++ {
		logPath := filepath.Join(dir, fmt.Sprintf("user_%d.log", i))
		os.WriteFile(logPath, []byte("test command\n"), 0o644)
		w.Submit(logPath, "user")
	}

	w.Wait()

	if got := slow.called.Load(); got != 4 {
		t.Errorf("summarizer called %d times, want 4", got)
	}
	if peak := slow.peak.Load(); peak > 2 {
		t.Errorf("peak concurrency = %d, want <= 2", peak)
	}
}

type slowSummarizer struct {
	delay    time.Duration
	called   atomic.Int32
	inFlight atomic.Int32
	peak     atomic.Int32
}

func (s *slowSummarizer) Validate(context.Context) error { return nil }

func (s *slowSummarizer) Summarize(_ context.Context, _ string) (string, error) {
	s.called.Add(1)
	n := s.inFlight.Add(1)
	defer s.inFlight.Add(-1)
	for {
		p := s.peak.Load()
		if n <= p || s.peak.CompareAndSwap(p, n) {
			break
		}
	}
	time.Sleep(s.delay)
	return "ok", nil
}
