package ai

import "testing"

// clearAIEnv blanks every key/override NewFromEnv reads, so each case starts clean.
func clearAIEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"PGBOT_AI_PROVIDER", "OPENAI_API_KEY", "GEMINI_API_KEY", "GOOGLE_API_KEY"} {
		t.Setenv(k, "")
	}
}

func TestNewFromEnv_selection(t *testing.T) {
	t.Run("no keys errors", func(t *testing.T) {
		clearAIEnv(t)
		if _, err := NewFromEnv(); err == nil {
			t.Error("no key should error")
		}
	})
	t.Run("gemini key picks Gemini", func(t *testing.T) {
		clearAIEnv(t)
		t.Setenv("GEMINI_API_KEY", "g")
		p, err := NewFromEnv()
		if err != nil || p.Vendor() != "Google Gemini" {
			t.Fatalf("expected Gemini, got %v / %v", p, err)
		}
	})
	t.Run("openai key picks OpenAI", func(t *testing.T) {
		clearAIEnv(t)
		t.Setenv("OPENAI_API_KEY", "sk")
		p, err := NewFromEnv()
		if err != nil || p.Vendor() != "OpenAI" {
			t.Fatalf("expected OpenAI, got %v / %v", p, err)
		}
	})
	t.Run("both present: OpenAI wins", func(t *testing.T) {
		clearAIEnv(t)
		t.Setenv("OPENAI_API_KEY", "sk")
		t.Setenv("GEMINI_API_KEY", "g")
		p, _ := NewFromEnv()
		if p.Vendor() != "OpenAI" {
			t.Errorf("with both keys OpenAI should win, got %q", p.Vendor())
		}
	})
	t.Run("PGBOT_AI_PROVIDER=gemini forces Gemini even with an OpenAI key", func(t *testing.T) {
		clearAIEnv(t)
		t.Setenv("OPENAI_API_KEY", "sk")
		t.Setenv("GEMINI_API_KEY", "g")
		t.Setenv("PGBOT_AI_PROVIDER", "gemini")
		p, err := NewFromEnv()
		if err != nil || p.Vendor() != "Google Gemini" {
			t.Fatalf("forced gemini not honored: %v / %v", p, err)
		}
	})
	t.Run("unknown provider errors", func(t *testing.T) {
		clearAIEnv(t)
		t.Setenv("PGBOT_AI_PROVIDER", "llama")
		if _, err := NewFromEnv(); err == nil {
			t.Error("unknown provider should error")
		}
	})
}
