package conn

import "testing"

// Issue #10: Supabase (and any DBA who runs CREATE EXTENSION … SCHEMA x)
// installs pg_stat_statements outside public. pgbot's probe saw the extension
// in pg_extension but every read used the bare relation name, so the queries
// section failed with 42P01 while the capability list still said "present".
// The fix qualifies the fixed allowlisted object names with the namespace the
// catalog reports for the extension.
func TestCapabilities_ExtObject_qualifiesWithDiscoveredSchema(t *testing.T) {
	cases := []struct {
		name    string
		schemas map[string]string
		ext     string
		object  string
		want    string
	}{
		{"default schema is still qualified (search_path-independent)",
			map[string]string{"pg_stat_statements": "public"}, "pg_stat_statements", "pg_stat_statements",
			`"public"."pg_stat_statements"`},
		{"supabase-style extensions schema",
			map[string]string{"pg_stat_statements": "extensions"}, "pg_stat_statements", "pg_stat_statements_info",
			`"extensions"."pg_stat_statements_info"`},
		{"schema needing quoting is escaped, never spliced raw",
			map[string]string{"pg_stat_statements": `Ext"Odd`}, "pg_stat_statements", "pg_stat_statements",
			`"Ext""Odd"."pg_stat_statements"`},
		{"unknown schema falls back to the bare name (pre-fix behaviour)",
			nil, "pg_stat_statements", "pg_stat_statements", "pg_stat_statements"},
		{"another extension in its own schema",
			map[string]string{"hypopg": "extensions"}, "hypopg", "hypopg_create_index",
			`"extensions"."hypopg_create_index"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := Capabilities{ExtensionSchemas: tc.schemas}
			if got := c.ExtObject(tc.ext, tc.object); got != tc.want {
				t.Errorf("ExtObject(%q,%q) = %s, want %s", tc.ext, tc.object, got, tc.want)
			}
		})
	}
	// The pgss shorthand is the same thing.
	c := Capabilities{ExtensionSchemas: map[string]string{"pg_stat_statements": "extensions"}}
	if got := c.Pgss("pg_stat_statements"); got != `"extensions"."pg_stat_statements"` {
		t.Errorf("Pgss shorthand = %s", got)
	}
	if got := c.ExtensionSchema("pg_stat_statements"); got != "extensions" {
		t.Errorf("ExtensionSchema = %q", got)
	}
}
