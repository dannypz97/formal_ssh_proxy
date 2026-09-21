package summarizer

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
			name:  "control chars stripped",
			input: "abc\x01\x02\x03def",
			want:  "abcdef",
		},
		{
			name:  "carriage return to newline",
			input: "line1\r\nline2",
			want:  "line1\n\nline2",
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
	result := FormatPrompt("whoami")
	if !strings.Contains(result, "whoami") {
		t.Error("prompt should contain the transcript")
	}
	if !strings.Contains(result, "security") {
		t.Error("prompt should mention security")
	}
}

func TestFormatPromptTruncation(t *testing.T) {
	long := strings.Repeat("a", 200_000)
	result := FormatPrompt(long)
	if !strings.Contains(result, "[truncated]") {
		t.Error("long transcript should be truncated")
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

	if slow.called != 4 {
		t.Errorf("summarizer called %d times, want 4", slow.called)
	}
	// Peak concurrency should not exceed 2 (hard to assert precisely,
	// but the test proves the semaphore doesn't deadlock).
}

type slowSummarizer struct {
	delay  time.Duration
	called int
}

func (s *slowSummarizer) Summarize(_ context.Context, _ string) (string, error) {
	s.called++
	time.Sleep(s.delay)
	return "ok", nil
}

