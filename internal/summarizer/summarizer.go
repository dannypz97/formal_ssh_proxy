// Package summarizer provides async session transcript summarization via LLM.
package summarizer

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/dparv/ssh-proxy/internal/config"
)

const securityPrompt = `You are a security analyst reviewing an SSH session transcript.
The transcript below contains the raw standard input captured from a user's SSH session.
It may include terminal control characters and escape sequences — focus on the actual
commands and text entered.

Analyze the session for potential security risks. For each risk found, describe:
1. What command or action was performed
2. Why it is potentially risky
3. Severity (LOW / MEDIUM / HIGH / CRITICAL)

If the session appears benign, say so briefly.

End with a one-paragraph overall assessment.

--- SESSION TRANSCRIPT ---
%s
--- END TRANSCRIPT ---`

// Summarizer generates a security assessment of a session transcript.
type Summarizer interface {
	Summarize(ctx context.Context, transcript string) (string, error)
}

// Worker manages async summarization of completed sessions.
type Worker struct {
	summarizer Summarizer
	wg         sync.WaitGroup
	sem        chan struct{} // bounds concurrency
}

// NewWorker creates a Worker backed by the given Summarizer with bounded
// concurrency. Returns nil if summarizer is nil (feature disabled).
func NewWorker(s Summarizer, maxConcurrent int) *Worker {
	if s == nil {
		return nil
	}
	if maxConcurrent <= 0 {
		maxConcurrent = 4
	}
	return &Worker{
		summarizer: s,
		sem:        make(chan struct{}, maxConcurrent),
	}
}

// Submit queues a session transcript for async summarization.
// logPath is the path to the raw .log file; the summary is written alongside it.
func (w *Worker) Submit(logPath, username string) {
	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		w.sem <- struct{}{}        // acquire
		defer func() { <-w.sem }() // release

		w.processSession(logPath, username)
	}()
}

// Wait blocks until all pending summarizations complete.
func (w *Worker) Wait() {
	w.wg.Wait()
}

func (w *Worker) processSession(logPath, username string) {
	raw, err := os.ReadFile(logPath)
	if err != nil {
		log.Printf("summarizer: could not read log %s: %v", logPath, err)
		return
	}

	transcript := cleanTranscript(string(raw))
	if strings.TrimSpace(transcript) == "" {
		log.Printf("summarizer: empty transcript for %s, skipping", logPath)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	summary, err := w.summarizer.Summarize(ctx, transcript)
	if err != nil {
		log.Printf("summarizer: LLM call failed for %s: %v", logPath, err)
		return
	}

	// Write summary next to the log: foo.log → foo.summary.txt
	summaryPath := strings.TrimSuffix(logPath, ".log") + ".summary.txt"
	if err := os.WriteFile(summaryPath, []byte(summary), 0o640); err != nil {
		log.Printf("summarizer: could not write summary %s: %v", summaryPath, err)
		return
	}

	log.Printf("summarizer: security evaluation ready for session %s (user: %s)", summaryPath, username)
}

// NewSummarizer creates the appropriate Summarizer from config, or nil if disabled.
func NewSummarizer(cfg *config.LLMConfig) Summarizer {
	if cfg.APIKey == "" {
		return nil
	}
	switch cfg.Provider {
	case "anthropic":
		return newAnthropicSummarizer(cfg.APIKey, cfg.Model)
	case "openai":
		return newOpenAISummarizer(cfg.APIKey, cfg.Model)
	default:
		return nil
	}
}

// cleanTranscript strips common terminal escape sequences and control characters
// from raw stdin, producing more readable text for the LLM.
func cleanTranscript(raw string) string {
	var b strings.Builder
	b.Grow(len(raw))

	i := 0
	for i < len(raw) {
		ch := raw[i]

		// Skip ANSI escape sequences: ESC [ ... final_byte
		if ch == 0x1b && i+1 < len(raw) && raw[i+1] == '[' {
			i += 2 // skip ESC [
			for i < len(raw) {
				c := raw[i]
				i++
				if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || c == '~' {
					break
				}
			}
			continue
		}

		// Skip other ESC sequences (ESC + one char).
		if ch == 0x1b && i+1 < len(raw) {
			i += 2
			continue
		}

		// Convert common control chars.
		switch {
		case ch == '\n' || ch == '\r':
			b.WriteByte('\n')
		case ch == '\t':
			b.WriteByte('\t')
		case ch == 0x7f || ch == 0x08: // DEL / backspace
			// Attempt to remove the previous character.
			s := b.String()
			if len(s) > 0 {
				b.Reset()
				b.WriteString(s[:len(s)-1])
			}
		case ch < 0x20: // other control chars — skip
		default:
			b.WriteByte(ch)
		}

		i++
	}

	return b.String()
}

// FormatPrompt wraps the transcript in the security analysis prompt.
func FormatPrompt(transcript string) string {
	// Truncate very long transcripts to avoid blowing context windows.
	const maxLen = 100_000
	if len(transcript) > maxLen {
		transcript = transcript[:maxLen] + "\n... [truncated]"
	}
	return fmt.Sprintf(securityPrompt, transcript)
}
