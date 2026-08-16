package ai

import (
	"context"
	"fmt"
	"os"
	"strings"
)

// Provider is an LLM backend for pgbot's OPTIONAL explanation layer. Backends are
// interchangeable and each is called over plain net/http (no SDK), preserving the
// single-static-binary promise. A Provider NEVER produces findings — those are
// computed deterministically in package findings; a Provider only explains them.
type Provider interface {
	// Generate sends one system + user turn and returns the model's text.
	Generate(ctx context.Context, system, user string) (string, error)
	// ModelName is the concrete model id, for the "model X" line.
	ModelName() string
	// Vendor is the human destination the data is sent to, for the consent line.
	Vendor() string
}

// NewFromEnv picks a provider from the environment. Selection order:
//
//  1. PGBOT_AI_PROVIDER=openai|gemini forces one explicitly.
//  2. otherwise auto-detect: OPENAI_API_KEY → OpenAI; GEMINI_API_KEY /
//     GOOGLE_API_KEY → Gemini. If both keys are present, OpenAI wins (set
//     PGBOT_AI_PROVIDER=gemini to override).
//
// The key is ALWAYS read from the environment, never a flag, so it can't leak
// into shell history or the process list.
func NewFromEnv() (Provider, error) {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("PGBOT_AI_PROVIDER"))) {
	case "openai":
		return NewOpenAIFromEnv()
	case "gemini", "google":
		return NewGeminiFromEnv()
	case "":
		// fall through to auto-detect by which key is present
	default:
		return nil, fmt.Errorf("PGBOT_AI_PROVIDER=%q is not recognized (use \"openai\" or \"gemini\")",
			strings.TrimSpace(os.Getenv("PGBOT_AI_PROVIDER")))
	}
	if strings.TrimSpace(os.Getenv("OPENAI_API_KEY")) != "" {
		return NewOpenAIFromEnv()
	}
	if strings.TrimSpace(os.Getenv("GEMINI_API_KEY")) != "" || strings.TrimSpace(os.Getenv("GOOGLE_API_KEY")) != "" {
		return NewGeminiFromEnv()
	}
	return nil, fmt.Errorf("no AI key found — set OPENAI_API_KEY (OpenAI) or GEMINI_API_KEY (Google Gemini) " +
		"to use `pgbot explain`/`ask` (keys are read from the environment, never a flag)")
}
