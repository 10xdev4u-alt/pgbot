package ai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNewGeminiFromEnv(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "")
	t.Setenv("GOOGLE_API_KEY", "")
	if _, err := NewGeminiFromEnv(); err == nil {
		t.Error("missing key must be an error")
	}
	// GOOGLE_API_KEY is accepted as a fallback (the SDK's other convention).
	t.Setenv("GOOGLE_API_KEY", "fromgoogle")
	if c, err := NewGeminiFromEnv(); err != nil || c.APIKey != "fromgoogle" {
		t.Errorf("GOOGLE_API_KEY fallback not honored: %v / %+v", err, c)
	}
	t.Setenv("GOOGLE_API_KEY", "")
	t.Setenv("GEMINI_API_KEY", "secret")
	t.Setenv("PGBOT_GEMINI_MODEL", "gemini-3-pro")
	c, err := NewGeminiFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if c.APIKey != "secret" || c.Model != "gemini-3-pro" {
		t.Errorf("env not honored: %+v", c)
	}
	if c.BaseURL != defaultBaseURL {
		t.Errorf("default base URL wrong: %s", c.BaseURL)
	}
}

func TestGenerate_success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("x-goog-api-key"); got != "k123" {
			t.Errorf("API key must travel in the x-goog-api-key header, got %q", got)
		}
		if !strings.Contains(r.URL.Path, "/models/m1:generateContent") {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		// The key must never appear in the URL (query or path).
		if strings.Contains(r.URL.String(), "k123") {
			t.Error("API key leaked into the URL")
		}
		body, _ := io.ReadAll(r.Body)
		var req genRequest
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatalf("bad request body: %v", err)
		}
		if req.SystemInstruction == nil || len(req.Contents) != 1 {
			t.Errorf("request shape wrong: %+v", req)
		}
		json.NewEncoder(w).Encode(genResponse{Candidates: []struct {
			Content      content `json:"content"`
			FinishReason string  `json:"finishReason"`
		}{{Content: content{Parts: []part{{Text: "Looks healthy."}}}, FinishReason: "STOP"}}})
	}))
	defer srv.Close()

	c := &Client{APIKey: "k123", Model: "m1", BaseURL: srv.URL, HTTP: srv.Client()}
	out, err := c.Generate(context.Background(), "be terse", "the report")
	if err != nil {
		t.Fatal(err)
	}
	if out != "Looks healthy." {
		t.Errorf("unexpected output %q", out)
	}
}

func TestGenerate_apiError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(genResponse{Error: &apiError{Code: 400, Status: "INVALID_ARGUMENT", Message: "API key not valid"}})
	}))
	defer srv.Close()

	c := &Client{APIKey: "bad", Model: "m1", BaseURL: srv.URL, HTTP: srv.Client()}
	_, err := c.Generate(context.Background(), "", "x")
	if err == nil || !strings.Contains(err.Error(), "API key not valid") {
		t.Errorf("expected a surfaced API error, got %v", err)
	}
}

func TestGenerate_noCandidates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(genResponse{}) // 200, no candidates
	}))
	defer srv.Close()
	c := &Client{APIKey: "k", Model: "m", BaseURL: srv.URL, HTTP: srv.Client()}
	if _, err := c.Generate(context.Background(), "", "x"); err == nil {
		t.Error("no candidates should be an error, not an empty success")
	}
}
