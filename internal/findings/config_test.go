package findings

import (
	"testing"

	"github.com/pgrundev/pgbot/internal/model"
)

func cfg(params map[string]string, provider string) *model.Context {
	return &model.Context{
		Server:   model.ServerInfo{Provider: provider},
		Settings: &model.Settings{Params: params},
	}
}

func TestConfigSanity(t *testing.T) {
	if f := has(Compute(cfg(map[string]string{"fsync": "off"}, "")), "fsync_off"); f == nil || f.Severity != model.SeverityCritical {
		t.Errorf("fsync=off must be critical, got %+v", f)
	}
	if has(Compute(cfg(map[string]string{"full_page_writes": "off"}, "")), "full_page_writes_off") == nil {
		t.Error("full_page_writes=off must fire")
	}
	if has(Compute(cfg(map[string]string{"autovacuum": "off"}, "")), "autovacuum_off") == nil {
		t.Error("autovacuum=off must fire")
	}
	// random_page_cost=4 only warns on a detected cloud provider.
	if has(Compute(cfg(map[string]string{"random_page_cost": "4"}, "")), "random_page_cost_high") != nil {
		t.Error("rpc=4 with unknown provider must NOT fire")
	}
	if has(Compute(cfg(map[string]string{"random_page_cost": "4"}, "rds")), "random_page_cost_high") == nil {
		t.Error("rpc=4 on rds must fire")
	}
	// work_mem × max_connections > effective_cache_size.
	over := cfg(map[string]string{"work_mem": "64MB", "max_connections": "500", "effective_cache_size": "4GB"}, "")
	if has(Compute(over), "work_mem_overcommit") == nil {
		t.Error("64MB × 500 = 32GB > 4GB should fire work_mem_overcommit")
	}
	sane := cfg(map[string]string{"work_mem": "4MB", "max_connections": "100", "effective_cache_size": "8GB"}, "")
	if has(Compute(sane), "work_mem_overcommit") != nil {
		t.Error("4MB × 100 = 400MB < 8GB must not fire")
	}
	if has(Compute(cfg(map[string]string{"statement_timeout": "0"}, "")), "statement_timeout_unset") == nil {
		t.Error("statement_timeout=0 must fire (info)")
	}
}

func TestParseMemBytes(t *testing.T) {
	cases := map[string]int64{"4MB": 4 << 20, "8192kB": 8192 * 1024, "1GB": 1 << 30, "512": 512}
	for in, want := range cases {
		if got, ok := parseMemBytes(in); !ok || got != want {
			t.Errorf("parseMemBytes(%q) = %d,%v want %d", in, got, ok, want)
		}
	}
}
