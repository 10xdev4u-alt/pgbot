// Package ai is pgbot's OPTIONAL explanation layer. It never produces findings —
// those are computed deterministically in package findings. It only takes an
// already-computed, PII-free Context and asks a model to explain and prioritize
// it in plain language. Everything it emits is labeled as model-generated.
//
// It talks to Google's Generative Language REST API directly over net/http so
// pgbot keeps its single-static-binary, minimal-dependency promise (no SDK).
package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	// A moving alias, on purpose: pinned versions get retired (e.g. gemini-2.5-flash
	// now 404s for new projects), and an explanation feature doesn't need a frozen
	// model. Override with $PGBOT_GEMINI_MODEL to pin one.
	defaultModel   = "gemini-flash-latest"
	defaultBaseURL = "https://generativelanguage.googleapis.com/v1beta"
)

// Client is a minimal Gemini generateContent client.
type Client struct {
	APIKey  string
	Model   string
	BaseURL string
	HTTP    *http.Client
}

// NewGeminiFromEnv builds a client from the environment. The key comes ONLY from
// GEMINI_API_KEY (or GOOGLE_API_KEY, the SDK's other convention) — never a flag,
// so it can't leak into shell history or the process list. PGBOT_GEMINI_MODEL and
// PGBOT_GEMINI_URL override the defaults. Both AI-Studio "auth" keys (AQ.…) and
// legacy standard keys (AIza…) work — they travel in the same header.
func NewGeminiFromEnv() (*Client, error) {
	key := strings.TrimSpace(os.Getenv("GEMINI_API_KEY"))
	if key == "" {
		key = strings.TrimSpace(os.Getenv("GOOGLE_API_KEY"))
	}
	if key == "" {
		return nil, fmt.Errorf("GEMINI_API_KEY (or GOOGLE_API_KEY) is not set — export it to use `pgbot explain` (the key is never read from a flag)")
	}
	model := envOr("PGBOT_GEMINI_MODEL", defaultModel)
	base := envOr("PGBOT_GEMINI_URL", defaultBaseURL)
	return &Client{
		APIKey:  key,
		Model:   model,
		BaseURL: strings.TrimRight(base, "/"),
		HTTP:    &http.Client{Timeout: 60 * time.Second},
	}, nil
}

func (c *Client) ModelName() string { return c.Model }
func (c *Client) Vendor() string    { return "Google Gemini" }

func envOr(name, def string) string {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		return v
	}
	return def
}

// ---- wire types (only the fields we use) ----

type genRequest struct {
	SystemInstruction *content         `json:"systemInstruction,omitempty"`
	Contents          []content        `json:"contents"`
	GenerationConfig  generationConfig `json:"generationConfig"`
}

type content struct {
	Role  string `json:"role,omitempty"`
	Parts []part `json:"parts"`
}

type part struct {
	Text string `json:"text"`
}

type generationConfig struct {
	Temperature     float64 `json:"temperature"`
	MaxOutputTokens int     `json:"maxOutputTokens"`
}

type genResponse struct {
	Candidates []struct {
		Content      content `json:"content"`
		FinishReason string  `json:"finishReason"`
	} `json:"candidates"`
	PromptFeedback *struct {
		BlockReason string `json:"blockReason"`
	} `json:"promptFeedback"`
	Error *apiError `json:"error"`
}

type apiError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Status  string `json:"status"`
}

// Generate sends one system + user turn and returns the model's text. It never
// retries with backoff loops — a failed explanation must not hang the CLI; the
// caller degrades to the deterministic report alone.
func (c *Client) Generate(ctx context.Context, system, user string) (string, error) {
	reqBody := genRequest{
		Contents: []content{{Role: "user", Parts: []part{{Text: user}}}},
		// Headroom for the answer AND for the internal "thinking" that current
		// Gemini models spend before the visible text — a tight cap truncates the
		// explanation mid-sentence.
		GenerationConfig: generationConfig{Temperature: 0.2, MaxOutputTokens: 8192},
	}
	if system != "" {
		reqBody.SystemInstruction = &content{Parts: []part{{Text: system}}}
	}
	buf, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}

	url := fmt.Sprintf("%s/models/%s:generateContent", c.BaseURL, c.Model)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(buf))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", c.APIKey) // header, not ?key= — keeps the key out of any URL log

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", fmt.Errorf("calling Gemini: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	var gr genResponse
	if err := json.Unmarshal(body, &gr); err != nil {
		return "", fmt.Errorf("gemini returned unparseable response (HTTP %d)", resp.StatusCode)
	}
	if gr.Error != nil {
		return "", fmt.Errorf("gemini error (%d %s): %s", gr.Error.Code, gr.Error.Status, gr.Error.Message)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("gemini HTTP %d", resp.StatusCode)
	}
	if gr.PromptFeedback != nil && gr.PromptFeedback.BlockReason != "" {
		return "", fmt.Errorf("gemini blocked the prompt: %s", gr.PromptFeedback.BlockReason)
	}
	if len(gr.Candidates) == 0 {
		return "", fmt.Errorf("gemini returned no candidates")
	}
	var sb strings.Builder
	for _, p := range gr.Candidates[0].Content.Parts {
		sb.WriteString(p.Text)
	}
	out := strings.TrimSpace(sb.String())
	if out == "" {
		return "", fmt.Errorf("gemini returned an empty explanation (finish: %s)", gr.Candidates[0].FinishReason)
	}
	return out, nil
}
