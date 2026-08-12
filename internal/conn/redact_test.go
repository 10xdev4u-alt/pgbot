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
