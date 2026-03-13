package llm

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Client struct {
	baseURL    string
	wireAPI    string
	apiKey     string
	model      string
	maxRetries int
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

func NewClient(baseURL, wireAPI, apiKey, model string, timeout time.Duration, maxRetries int) *Client {
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	if strings.TrimSpace(wireAPI) == "" {
		wireAPI = "responses"
	}
	if maxRetries < 0 {
		maxRetries = 0
	}
	return &Client{
		baseURL:    strings.TrimRight(baseURL, "/"),
		wireAPI:    strings.Trim(strings.TrimSpace(wireAPI), "/"),
		apiKey:     strings.TrimSpace(apiKey),
		model:      strings.TrimSpace(model),
		maxRetries: maxRetries,
		httpClient: &http.Client{
			Timeout: timeout,
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

	var lastErr error
	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		req, reqErr := http.NewRequest(http.MethodPost, c.baseURL+"/"+c.wireAPI, bytes.NewReader(body))
		if reqErr != nil {
			return "", reqErr
		}
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
		req.Header.Set("Content-Type", "application/json")

		resp, doErr := c.httpClient.Do(req)
		if doErr != nil {
			lastErr = doErr
			continue
		}

		respBody, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr != nil {
			lastErr = readErr
			continue
		}

		if resp.StatusCode >= 300 {
			lastErr = fmt.Errorf("llm request failed: %s: %s", resp.Status, strings.TrimSpace(string(respBody)))
			continue
		}

		var parsed responsePayload
		if err := json.Unmarshal(respBody, &parsed); err != nil {
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
			lastErr = errors.New("empty llm response")
			continue
		}
		return text, nil
	}
	if lastErr != nil {
		return "", lastErr
	}
	return "", errors.New("llm request failed without a concrete error")
}
