package conn

import (
	"strings"
	"testing"
)

func TestScrubQueryText_stripsPII(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"email literal", "SELECT * FROM users WHERE email = 'alice@example.com'"},
		{"uuid literal", "SELECT * FROM t WHERE id = '550e8400-e29b-41d4-a716-446655440000'"},
		{"numbers", "UPDATE accounts SET balance = 4200 WHERE id = 17"},
		{"dollar quoted", "SELECT $tag$ secret alice@x.com 42 $tag$"},
		{"escaped quote", "SELECT 'O''Brien can''t'"},
		{"bare email in comment", "SELECT 1 -- ping bob@corp.io"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out := ScrubQueryText(c.in)
			if strings.Contains(out, "@") {
				t.Errorf("email leaked: %q -> %q", c.in, out)
			}
			for _, bad := range []string{"alice", "example.com", "4200", "550e8400", "secret", "O'Brien"} {
				if strings.Contains(out, bad) {
					t.Errorf("literal %q leaked: %q -> %q", bad, c.in, out)
				}
			}
		})
	}
}

// P0-2: the dollar-quoted region must be replaced with the LITERAL marker, not
// silently deleted by Expand semantics — a reader needs the signal that content
// was removed.
func TestScrubQueryText_dollarQuotedShowsMarker(t *testing.T) {
	out := ScrubQueryText("DO $body$ BEGIN PERFORM secret_thing(42); END $body$")
	if !strings.Contains(out, "$REDACTED$") {
		t.Errorf("dollar-quoted region must show the literal $REDACTED$ marker, got %q", out)
	}
	if strings.Contains(out, "secret_thing") || strings.Contains(out, "42") {
		t.Errorf("dollar-quoted body content leaked: %q", out)
	}
}

// FuzzScrubQueryText asserts the invariants that actually matter: for ANY input,
// no email-, uuid-, or standalone-number-shaped substring survives. (A bare '@'
// operator or a digit inside an identifier like col1 is not PII and may remain,
// which is why we re-run the shape regexes rather than a naive "no '@'" check.)
// The seed corpus runs under plain `go test`; CI also runs it under -fuzz.
func FuzzScrubQueryText(f *testing.F) {
	for _, s := range []string{
		"SELECT * FROM users WHERE email = 'alice@example.com'",
		"SELECT * FROM t WHERE id = '550e8400-e29b-41d4-a716-446655440000'",
		"UPDATE accounts SET balance = 4200 WHERE id = 17",
		"DO $body$ BEGIN PERFORM x(); END $body$",
		"SELECT a @@ b",              // bare full-text operator — not an email
		"SELECT col1, t2.c3 FROM t2", // digits inside identifiers — not literals
		"SELECT 'O''Brien'",
		"", "$$", "'unterminated", "a@b.co1", "1@1.11",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, in string) {
		out := ScrubQueryText(in)
		if m := reEmail.FindString(out); m != "" {
			t.Errorf("email-shaped substring survived: %q -> %q (%q)", in, out, m)
		}
		if m := reUUID.FindString(out); m != "" {
			t.Errorf("uuid-shaped substring survived: %q -> %q (%q)", in, out, m)
		}
		for _, loc := range reNumber.FindAllStringIndex(out, -1) {
			if loc[0] > 0 && out[loc[0]-1] == '$' {
				continue // a $N pg_stat_statements placeholder is allowed
			}
			t.Errorf("standalone number survived: %q -> %q (at %d)", in, out, loc[0])
			break
		}
	})
}

// P0/PII: pgss normalizes DML to $N (which must survive scrubbing) but stores
// UTILITY statements (DO blocks) verbatim (which can carry real literals and must
// be scrubbed). ScrubQueryText now runs over pgss text, so both must hold.
func TestScrubQueryText_placeholdersAndUtilityLiterals(t *testing.T) {
	dml := ScrubQueryText("SELECT * FROM t WHERE id = $1 AND created_at > $2")
	if !strings.Contains(dml, "$1") || !strings.Contains(dml, "$2") {
		t.Errorf("normalized $N placeholders must be preserved, got %q", dml)
	}
	util := ScrubQueryText("DO $$ BEGIN INSERT INTO people(email) VALUES('alice@example.com'); END $$")
	if strings.Contains(util, "@example.com") || strings.Contains(util, "alice") {
		t.Errorf("DO-block literal leaked through pgss text: %q", util)
	}
}

func TestScrubQueryText_keepsShape(t *testing.T) {
	out := ScrubQueryText("SELECT id, name FROM orders WHERE customer_id = 99 AND status = 'paid'")
	for _, want := range []string{"SELECT", "orders", "customer_id", "status"} {
		if !strings.Contains(out, want) {
			t.Errorf("query shape lost %q: %q", want, out)
		}
	}
}

func TestRedactConnString(t *testing.T) {
	cases := map[string]string{
		"postgres://user:s3cr3t@host:5432/db":        "s3cr3t",
		"postgresql://u:p%40ss@h/db?sslmode=require": "p%40ss",
		"host=h user=u password=topsecret dbname=d":  "topsecret",
		"host=h password='sp ace' dbname=d":          "sp ace",
	}
	for in, secret := range cases {
		out := RedactConnString(in)
		if strings.Contains(out, secret) {
			t.Errorf("secret leaked: %q -> %q", in, out)
		}
		if !strings.Contains(out, "REDACTED") {
			t.Errorf("no redaction marker: %q -> %q", in, out)
		}
	}
}

func TestRedactConnString_noPasswordIsUnchangedShape(t *testing.T) {
	out := RedactConnString("postgres://user@host/db")
	if !strings.Contains(out, "user@host") {
		t.Errorf("mangled a password-less URL: %q", out)
	}
}
