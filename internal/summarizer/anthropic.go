package summarizer

import (
	"context"
	"errors"
	"fmt"
	"strings"

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

// Validate checks the API key and model by looking the model up.
func (a *anthropicSummarizer) Validate(ctx context.Context) error {
	_, err := a.client.Models.Get(ctx, a.model, anthropic.ModelGetParams{})
	var apiErr *anthropic.Error
	if errors.As(err, &apiErr) && isConfigErrorStatus(apiErr.StatusCode) {
		return fmt.Errorf("%w: anthropic returned HTTP %d for model %q", ErrInvalidConfig, apiErr.StatusCode, a.model)
	}
	return err
}

func (a *anthropicSummarizer) Summarize(ctx context.Context, transcript string) (string, error) {
	params := anthropic.BetaMessageNewParams{
		Model:     a.model,
		MaxTokens: 16000,
		System:    []anthropic.BetaTextBlockParam{{Text: systemPrompt}},
		Messages: []anthropic.BetaMessageParam{
			anthropic.NewBetaUserMessage(anthropic.NewBetaTextBlock(transcript)),
		},
	}
	// Session transcripts can contain offensive-security tooling, which may
	// trip safety classifiers on newer models. Server-side fallbacks let the
	// API re-serve a refused request on another model within the same call.
	if supportsServerFallback(a.model) {
		params.Betas = []anthropic.AnthropicBeta{anthropic.AnthropicBetaServerSideFallback2026_07_01}
		params.Fallbacks = anthropic.BetaFallbacksParamOfDefault()
	}

	resp, err := a.client.Beta.Messages.New(ctx, params)
	if err != nil {
		return "", fmt.Errorf("anthropic API call: %w", err)
	}
	if resp.StopReason == anthropic.BetaStopReasonRefusal {
		return "", fmt.Errorf("anthropic API refused to analyse the transcript")
	}

	var text strings.Builder
	for _, block := range resp.Content {
		if tb, ok := block.AsAny().(anthropic.BetaTextBlock); ok {
			text.WriteString(tb.Text)
		}
	}
	if text.Len() == 0 {
		return "", fmt.Errorf("anthropic API returned empty response (stop reason: %s)", resp.StopReason)
	}
	return text.String(), nil
}

// supportsServerFallback reports whether model accepts the server-side
// refusal fallback parameter.
func supportsServerFallback(model string) bool {
	for _, prefix := range []string{"claude-opus-5", "claude-fable-5", "claude-mythos-5"} {
		if strings.HasPrefix(model, prefix) {
			return true
		}
	}
	return false
}
