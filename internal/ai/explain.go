package ai

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/pgrundev/pgbot/internal/model"
)

// systemPrompt hard-constrains the model to the one job it's allowed: explaining
// facts pgbot already computed. The findings are ground truth; the model may not
// add to them, and it MUST carry every caveat into any recommendation.
const systemPrompt = `You are a PostgreSQL performance expert. A tool called pgbot has already
analyzed a database and computed the findings below DETERMINISTICALLY — they are facts, not guesses.

Your only job is to explain what these findings mean together and what to address first, in plain
language, for an engineer who is not a Postgres expert.

Rules you must follow:
- Do NOT invent findings, numbers, table names, or problems. Discuss ONLY what is provided.
- Every finding may carry "caveats". If you mention a finding's fix, you MUST repeat its caveats.
  Never recommend a destructive action (dropping an index, etc.) without its caveats — a caveat
  like "replication makes these per-node counts unreliable" is load-bearing.
- A finding's "confidence" below 0.5 is a possibility, not a fact — hedge accordingly.
- Prioritize: risk first, then by impact score. Be concise — a short prioritized narrative.
- If nothing needs attention, say the database looks healthy and stop.
- Never recommend anything that EXECUTES a user's query to diagnose it (e.g. running it
  under timing); suggest only safe, non-executing steps.`

// explainPayload is the PII-free subset of the Context we send. The full Context
// is already PII-free by construction (normalized query text, no literal values),
// but we send a focused view so the model reasons about signals, not noise.
type explainPayload struct {
	Database     string          `json:"database"`
	Version      string          `json:"version"`
	ViaPooler    bool            `json:"via_pooler,omitempty"`
	WindowAgeSec *int64          `json:"window_age_seconds,omitempty"`
	ColdWindow   bool            `json:"cold_window,omitempty"`
	Suppressed   string          `json:"delta_suppressed_reason,omitempty"`
	Findings     []model.Finding `json:"findings"`
	Waits        *waitSummary    `json:"wait_profile,omitempty"`
	Events       []eventSummary  `json:"recent_events,omitempty"`
}

type waitSummary struct {
	Samples int                `json:"samples"`
	Buckets []model.WaitBucket `json:"top_buckets,omitempty"`
	ByQuery []model.QueryWaits `json:"top_queries,omitempty"`
}

type eventSummary struct {
	Kind       string  `json:"kind"`
	Object     string  `json:"object,omitempty"`
	Confidence float64 `json:"confidence"`
}

// BuildExplainPrompt returns the (system, user) prompt for a Context. The user
// prompt is the curated JSON payload; nothing PII-bearing is included.
func BuildExplainPrompt(c *model.Context) (system, user string) {
	p := explainPayload{
		Database:   c.Server.Database,
		Version:    c.Server.VersionText,
		ViaPooler:  c.Server.ViaPooler,
		ColdWindow: c.Window.ColdWindow(),
		Suppressed: c.DeltaSuppressedReason,
		Findings:   c.Findings,
	}
	if c.Window.WindowAgeSeconds != nil {
		p.WindowAgeSec = c.Window.WindowAgeSeconds
	}
	if w := c.WaitProfile; w != nil && w.Available && w.Samples > 0 {
		ws := &waitSummary{Samples: w.Samples}
		ws.Buckets = topBuckets(w.Buckets, 5)
		ws.ByQuery = topQueries(w.ByQuery, 3)
		p.Waits = ws
	}
	for _, e := range c.Events {
		p.Events = append(p.Events, eventSummary{Kind: e.Kind, Object: e.Object, Confidence: e.Confidence})
	}

	blob, _ := json.MarshalIndent(p, "", "  ")
	user = "Here is the pgbot report as JSON. Explain it per your rules.\n\n" + string(blob)
	return systemPrompt, user
}

// Explain builds the prompt and calls the model, returning the labeled-elsewhere
// explanation text. A nil/empty findings set still gets an explanation (the model
// is told to confirm health briefly).
func Explain(ctx context.Context, c *Client, mc *model.Context) (string, error) {
	if c == nil {
		return "", fmt.Errorf("no Gemini client")
	}
	system, user := BuildExplainPrompt(mc)
	return c.Generate(ctx, system, user)
}

func topBuckets(b []model.WaitBucket, n int) []model.WaitBucket {
	if len(b) > n {
		b = b[:n]
	}
	// Trim per-bucket event lists to the single top event to keep the prompt tight.
	out := make([]model.WaitBucket, 0, len(b))
	for _, bk := range b {
		if len(bk.Events) > 1 {
			bk.Events = bk.Events[:1]
		}
		out = append(out, bk)
	}
	return out
}

func topQueries(q []model.QueryWaits, n int) []model.QueryWaits {
	if len(q) > n {
		return q[:n]
	}
	return q
}
