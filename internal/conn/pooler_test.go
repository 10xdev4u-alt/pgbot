package conn

import (
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestIsKnownPoolerEndpoint(t *testing.T) {
	cases := []struct {
		host string
		port uint16
		want bool
	}{
		{"db.abc.supabase.co", 6543, true},            // Supabase transaction pooler
		{"db.abc.supabase.co", 5432, false},           // Supabase direct
		{"ep-cool-name-pooler.neon.tech", 5432, true}, // Neon pooled
		{"ep-cool-name.neon.tech", 5432, false},       // Neon direct
		{"my-rds.amazonaws.com", 5432, false},
		{"127.0.0.1", 6432, false}, // generic PgBouncer on a nonstandard port — undetectable by signature
	}
	for _, c := range cases {
		cc := &pgx.ConnConfig{}
		cc.Host = c.host
		cc.Port = c.port
		if got := isKnownPoolerEndpoint(cc); got != c.want {
			t.Errorf("isKnownPoolerEndpoint(%s:%d) = %v, want %v", c.host, c.port, got, c.want)
		}
	}
}

func TestPoolerHint(t *testing.T) {
	mk := func(host string, port uint16) *pgx.ConnConfig {
		cc := &pgx.ConnConfig{}
		cc.Host, cc.Port = host, port
		return cc
	}
	if h := poolerHint(mk("x-pooler.neon.tech", 5432)); !strings.Contains(h, "Neon") {
		t.Errorf("Neon hint wrong: %q", h)
	}
	if h := poolerHint(mk("x.supabase.co", 6543)); !strings.Contains(h, "Supabase") {
		t.Errorf("Supabase hint wrong: %q", h)
	}
	if h := poolerHint(mk("host", 5432)); !strings.Contains(h, "PgBouncer") {
		t.Errorf("generic hint wrong: %q", h)
	}
}

func TestPoolerMessages(t *testing.T) {
	p := PoolerInfo{Hint: "the Supabase transaction pooler (port 6543)"}
	if !strings.Contains(p.Note(), "rates are still correct") {
		t.Error("Note should reassure that rates are correct")
	}
	if !strings.Contains(p.Note(), "--strict-pooler") {
		t.Error("Note should mention the strict flag")
	}
	if !strings.Contains(p.StrictMessage(), "port 5432, not 6543") {
		t.Error("StrictMessage should give the fix")
	}
}
