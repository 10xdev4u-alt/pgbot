package events

import (
	"testing"
	"time"

	"github.com/pgrundev/pgbot/internal/model"
)

func obj(kind, id, hash string) model.SchemaObject {
	return model.SchemaObject{Kind: kind, Identity: id, DefinitionHash: hash, Definition: kind + " " + id}
}

func find(evs []model.Event, kind, object string) *model.Event {
	for i := range evs {
		if evs[i].Kind == kind && evs[i].Object == object {
			return &evs[i]
		}
	}
	return nil
}

func TestDerive_schemaChanges(t *testing.T) {
	prevAt := time.Date(2026, 8, 12, 9, 2, 0, 0, time.UTC)
	now := time.Date(2026, 8, 12, 9, 17, 0, 0, time.UTC)

	prev := []model.SchemaObject{
		obj("index", "public.orders.orders_customer_idx", "h1"),
		obj("table", "public.orders", "t1"),
		obj("column", "public.orders.status", "c1"),
	}
	cur := &model.Context{
		CollectedAt: now,
		Settings:    &model.Settings{Overrides: map[string]string{"work_mem": "64MB"}},
		Schema: &model.SchemaFingerprint{Objects: []model.SchemaObject{
			obj("table", "public.orders", "t1"),         // unchanged
			obj("column", "public.orders.status", "c2"), // type changed (hash differs)
			obj("index", "public.orders.new_idx", "h9"), // created
			// orders_customer_idx dropped
		}},
	}
	prevSettings := map[string]string{"work_mem": "4MB"}

	evs := Derive(cur, prev, prevSettings, prevAt)

	if e := find(evs, "schema.index_dropped", "public.orders.orders_customer_idx"); e == nil {
		t.Error("expected index_dropped")
	} else {
		if e.OccurredAfter == nil || !e.OccurredAfter.Equal(prevAt) || e.OccurredBefore == nil || !e.OccurredBefore.Equal(now) {
			t.Errorf("dropped event should carry the (prevAt, now) window, got %+v", e)
		}
		if e.Confidence >= 1.0 {
			t.Error("inferred schema change must have confidence < 1.0")
		}
	}
	if find(evs, "schema.index_created", "public.orders.new_idx") == nil {
		t.Error("expected index_created")
	}
	if find(evs, "schema.column_type_changed", "public.orders.status") == nil {
		t.Error("expected column_type_changed")
	}
	if e := find(evs, "config.changed", "work_mem"); e == nil || e.Before != "4MB" || e.After != "64MB" {
		t.Errorf("expected config.changed work_mem 4MB->64MB, got %+v", e)
	}
}

func TestDerive_firstRunNoSchemaEvents(t *testing.T) {
	cur := &model.Context{CollectedAt: time.Now(), Schema: &model.SchemaFingerprint{Objects: []model.SchemaObject{obj("table", "public.x", "h")}}}
	if evs := Derive(cur, nil, nil, time.Now()); len(evs) != 0 {
		t.Errorf("no prior fingerprint should yield no schema events, got %+v", evs)
	}
}

func TestDerive_lifecycleHasRealTimestamps(t *testing.T) {
	prevAt := time.Date(2026, 8, 12, 9, 0, 0, 0, time.UTC)
	reset := time.Date(2026, 8, 12, 9, 14, 0, 0, time.UTC)
	cur := &model.Context{
		CollectedAt: time.Date(2026, 8, 12, 9, 17, 0, 0, time.UTC),
		Server:      model.ServerInfo{Database: "app"},
		Window:      model.Window{StatsResetAt: &reset},
	}
	e := find(Derive(cur, nil, nil, prevAt), "stats.reset", "app")
	if e == nil || e.Confidence != 1.0 {
		t.Fatalf("stats.reset should fire with confidence 1.0, got %+v", e)
	}
	if e.OccurredAfter == nil || !e.OccurredAfter.Equal(reset) {
		t.Error("stats.reset should carry the real reset timestamp")
	}
}

func TestDerive_redactsSecretSettings(t *testing.T) {
	cur := &model.Context{CollectedAt: time.Now(), Settings: &model.Settings{Overrides: map[string]string{
		"ssl_cert_file": "/new/path.crt",
	}}}
	prev := map[string]string{"ssl_cert_file": "/old/path.crt"}
	e := find(Derive(cur, nil, prev, time.Now()), "config.changed", "ssl_cert_file")
	if e == nil {
		t.Fatal("expected config.changed for ssl_cert_file")
	}
	if e.Before == "/old/path.crt" || e.After == "/new/path.crt" {
		t.Errorf("ssl file paths must be redacted, got before=%q after=%q", e.Before, e.After)
	}
}
