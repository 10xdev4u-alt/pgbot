package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pgrundev/pgbot/internal/findings"
)

// write a config file and return its path.
func writeCfg(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoad_defaultsWhenNoFile(t *testing.T) {
	// A cwd with no .pgbot.toml, and env pointing nowhere.
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("PGBOT_CONFIG", "")
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "empty"))
	t.Setenv("HOME", filepath.Join(dir, "empty"))

	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Source != "" {
		t.Errorf("expected no source, got %q", cfg.Source)
	}
	if cfg.Tunables != findings.DefaultTunables() {
		t.Errorf("expected default tunables, got %+v", cfg.Tunables)
	}
}

func TestDiscover_precedence(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	// A .pgbot.toml two directories up should be found from a nested cwd.
	writeCfg(t, dir, ".pgbot.toml", "schema = 1\n")
	nested := filepath.Join(dir, "a", "b")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(nested)
	t.Setenv("PGBOT_CONFIG", "")

	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Source == "" || !strings.HasSuffix(cfg.Source, ".pgbot.toml") {
		t.Errorf("upward search failed, source=%q", cfg.Source)
	}

	// --config wins over the upward-found file, and is a hard error if unreadable.
	if _, err := Load(filepath.Join(dir, "does-not-exist.toml")); err == nil {
		t.Error("--config to a missing file must be a hard error")
	}

	// PGBOT_CONFIG wins over the upward search.
	explicit := writeCfg(t, dir, "other.toml", "schema = 1\n[severity]\nunused_indexes = \"info\"\n")
	t.Setenv("PGBOT_CONFIG", explicit)
	cfg, err = Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Source != explicit {
		t.Errorf("PGBOT_CONFIG should win, got source=%q", cfg.Source)
	}
	if cfg.Severity["unused_indexes"] != "info" {
		t.Errorf("severity remap not parsed: %+v", cfg.Severity)
	}
}

// SECURITY: a credential-shaped key is a hard error, with its own test (B2-1).
func TestLoad_refusesCredentialKey(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("PGBOT_CONFIG", "")
	for _, body := range []string{
		"dsn = \"postgres://u:p@h/db\"\n",
		"password = \"hunter2\"\n",
		"[connection]\nurl = \"postgres://…\"\n",
	} {
		p := writeCfg(t, dir, ".pgbot.toml", body)
		_, err := Load(p)
		if err == nil {
			t.Errorf("config with credential key must be rejected: %q", body)
			continue
		}
		if !strings.Contains(err.Error(), "credential-shaped") {
			t.Errorf("error should explain the credential refusal, got: %v", err)
		}
	}
}

func TestLoad_unknownFindingWarnsButLoads(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("PGBOT_CONFIG", "")
	p := writeCfg(t, dir, ".pgbot.toml", `
schema = 1
[severity]
nonexistent_finding = "info"
[[ignore]]
finding = "also_not_real"
reason = "typo"
`)
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("unknown ids must warn, not error: %v", err)
	}
	if len(cfg.Warnings) < 2 {
		t.Errorf("expected warnings for both unknown ids, got %v", cfg.Warnings)
	}
	// The severity remap for a bogus id is dropped; the ignore rule is kept.
	if _, ok := cfg.Severity["nonexistent_finding"]; ok {
		t.Error("bogus severity remap should be dropped")
	}
}

func TestLoad_thresholdOverrides(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("PGBOT_CONFIG", "")
	p := writeCfg(t, dir, ".pgbot.toml", `
schema = 1
[thresholds]
unused_index_min_size_mb = 100
replica_lag_warn_seconds = 30
made_up_key = 5
`)
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Tunables.UnusedIndexMinBytes != 100*(1<<20) {
		t.Errorf("unused_index_min_size_mb not applied: %d", cfg.Tunables.UnusedIndexMinBytes)
	}
	if cfg.Tunables.ReplicaLagWarnSec != 30 {
		t.Errorf("replica_lag_warn_seconds not applied: %v", cfg.Tunables.ReplicaLagWarnSec)
	}
	var sawUnknown bool
	for _, w := range cfg.Warnings {
		if strings.Contains(w, "made_up_key") {
			sawUnknown = true
		}
	}
	if !sawUnknown {
		t.Errorf("unknown threshold key should warn, got %v", cfg.Warnings)
	}
}
