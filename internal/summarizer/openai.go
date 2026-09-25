package summarizer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

const openAIBaseURL = "https://api.openai.com/v1"

type openAISummarizer struct {
	apiKey string
	model  string
}

func newOpenAISummarizer(apiKey, model string) *openAISummarizer {
	return &openAISummarizer{apiKey: apiKey, model: model}
}

// OpenAI Chat Completions request/response types (minimal).
type chatRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatResponse struct {
	Choices []struct {
		Message chatMessage `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// Validate checks the API key and model by looking the model up.
func (o *openAISummarizer) Validate(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		openAIBaseURL+"/models/"+url.PathEscape(o.model), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+o.apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("openai API call: %w", err)
	}
	resp.Body.Close()

	switch {
	case isConfigErrorStatus(resp.StatusCode):
		return fmt.Errorf("%w: openai returned HTTP %d for model %q", ErrInvalidConfig, resp.StatusCode, o.model)
	case resp.StatusCode != http.StatusOK:
		return fmt.Errorf("openai API returned HTTP %d", resp.StatusCode)
	}
	return nil
}

func (o *openAISummarizer) Summarize(ctx context.Context, transcript string) (string, error) {
	reqBody := chatRequest{
		Model: o.model,
		Messages: []chatMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: transcript},
		},
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshalling request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		openAIBaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+o.apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("openai API call: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("openai API error (status %d): %s", resp.StatusCode, respBody)
	}

	var chatResp chatResponse
	if err := json.Unmarshal(respBody, &chatResp); err != nil {
		return "", fmt.Errorf("parsing response: %w", err)
	}

	if chatResp.Error != nil {
		return "", fmt.Errorf("openai API error: %s", chatResp.Error.Message)
	}

	if len(chatResp.Choices) == 0 {
		return "", fmt.Errorf("openai API returned no choices")
	}

	return chatResp.Choices[0].Message.Content, nil
}
