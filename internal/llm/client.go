package llm

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type Client struct {
	baseURL    string
	apiKey     string
	model      string
	httpClient *http.Client
}

type requestPayload struct {
	Model string       `json:"model"`
	Input []inputBlock `json:"input"`
}

type inputBlock struct {
	Role    string         `json:"role"`
	Content []contentBlock `json:"content"`
}

type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type responsePayload struct {
	Output []struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"output"`
}

func NewClient(baseURL, apiKey, model string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  strings.TrimSpace(apiKey),
		model:   strings.TrimSpace(model),
		httpClient: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

func (c *Client) Enabled() bool {
	return c.baseURL != "" && c.apiKey != "" && c.model != ""
}

func (c *Client) Generate(systemPrompt string, userPrompt string) (string, error) {
	if !c.Enabled() {
		return "", errors.New("llm client is not configured")
	}

	payload := requestPayload{
		Model: c.model,
		Input: []inputBlock{
			{
				Role: "system",
				Content: []contentBlock{{
					Type: "input_text",
					Text: systemPrompt,
				}},
			},
			{
				Role: "user",
				Content: []contentBlock{{
					Type: "input_text",
					Text: userPrompt,
				}},
			},
		},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequest(http.MethodPost, c.baseURL+"/responses", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("llm request failed: %s", resp.Status)
	}

	var parsed responsePayload
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return "", err
	}

	var out strings.Builder
	for _, item := range parsed.Output {
		for _, c := range item.Content {
			if c.Type == "output_text" || c.Type == "text" {
				if out.Len() > 0 {
					out.WriteString("\n")
				}
				out.WriteString(c.Text)
			}
		}
	}

	text := strings.TrimSpace(out.String())
	if text == "" {
		return "", errors.New("empty llm response")
	}
	return text, nil
}
