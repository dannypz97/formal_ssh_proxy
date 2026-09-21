package summarizer

import (
	"context"
	"fmt"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

type anthropicSummarizer struct {
	client anthropic.Client
	model  string
}

func newAnthropicSummarizer(apiKey, model string) *anthropicSummarizer {
	return &anthropicSummarizer{
		client: anthropic.NewClient(option.WithAPIKey(apiKey)),
		model:  model,
	}
}

func (a *anthropicSummarizer) Summarize(ctx context.Context, transcript string) (string, error) {
	prompt := FormatPrompt(transcript)

	resp, err := a.client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     a.model,
		MaxTokens: 4096,
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(prompt)),
		},
	})
	if err != nil {
		return "", fmt.Errorf("anthropic API call: %w", err)
	}

	// Extract text from response content blocks.
	var text string
	for _, block := range resp.Content {
		if tb, ok := block.AsAny().(anthropic.TextBlock); ok {
			text += tb.Text
		}
	}

	if text == "" {
		return "", fmt.Errorf("anthropic API returned empty response")
	}

	return text, nil
}
