package conn

import (
	"strings"
	"testing"
)

func TestDetectProvider(t *testing.T) {
	cases := []struct {
		name string
		m    providerMarkers
		want Provider
	}{
		{"supabase host", providerMarkers{Host: "db.abc.supabase.co"}, ProviderSupabase},
		{"neon host", providerMarkers{Host: "ep-x.neon.tech"}, ProviderNeon},
		{"azure host", providerMarkers{Host: "srv.postgres.database.azure.com"}, ProviderAzure},
		{"azure by setting", providerMarkers{Host: "10.0.0.5", HasAzure: true}, ProviderAzure},
		{"aurora by fn", providerMarkers{Host: "x.rds.amazonaws.com", IsAurora: true}, ProviderAurora},
		{"rds host", providerMarkers{Host: "x.rds.amazonaws.com", HasRDS: true}, ProviderRDS},
		{"cloudsql by setting", providerMarkers{Host: "127.0.0.1", HasCloudSQL: true}, ProviderCloudSQL},
		{"self-hosted", providerMarkers{Host: "127.0.0.1"}, ProviderUnknown},
	}
	for _, c := range cases {
		if got := detectProvider(c.m); got != c.want {
			t.Errorf("%s: detectProvider = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestPgssInstructions(t *testing.T) {
	cases := map[Provider]string{
		ProviderRDS:      "parameter group",
		ProviderAurora:   "reboot",
		ProviderCloudSQL: "cloudsql.enable_pg_stat_statements",
		ProviderAzure:    "azure.extensions",
		ProviderSupabase: "preloaded",
		ProviderNeon:     "preloaded",
		ProviderUnknown:  "shared_preload_libraries",
	}
	for p, want := range cases {
		if got := p.PgssInstructions(); !strings.Contains(got, want) {
			t.Errorf("%s instructions missing %q: %q", p, want, got)
		}
		if !strings.Contains(p.PgssInstructions(), "CREATE EXTENSION pg_stat_statements") {
			t.Errorf("%s instructions should always include the CREATE EXTENSION step", p)
		}
	}
}
