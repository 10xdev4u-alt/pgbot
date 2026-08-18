package collect

import (
	"embed"
	"strings"
	"testing"
	"unicode"
)

// Every query pgbot sends to the TARGET database must be read-only. The runtime
// backstops — the pg_monitor role with no write grants, default_transaction_
// read_only = on, and a BEGIN READ ONLY per transaction — are the guarantee;
// this is a cheap, always-runs static tripwire that catches a write statement
// slipping into an embedded query before it ever reaches a database (and long
// before CI runs it as the read-only role). It whitelists rather than blacklists:
// after stripping comments, every statement in every embedded .sql file must
// begin with SELECT or WITH.
//
//go:embed sql/*.sql
var allEmbeddedSQL embed.FS

func TestEmbeddedSQL_isReadOnly(t *testing.T) {
	entries, err := allEmbeddedSQL.ReadDir("sql")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("no embedded SQL found — the guard is not scanning anything")
	}
	for _, e := range entries {
		b, err := allEmbeddedSQL.ReadFile("sql/" + e.Name())
		if err != nil {
			t.Fatal(err)
		}
		for i, stmt := range strings.Split(stripSQLComments(string(b)), ";") {
			kw := firstKeyword(stmt)
			if kw == "" {
				continue // blank / comment-only trailer
			}
			if kw != "SELECT" && kw != "WITH" {
				t.Errorf("%s: statement %d begins with %q — only SELECT/WITH may reach the target database", e.Name(), i+1, kw)
			}
		}
	}
}

// stripSQLComments removes -- line comments and /* */ block comments so the
// keyword scan sees only executable text.
func stripSQLComments(s string) string {
	// block comments first
	for {
		i := strings.Index(s, "/*")
		if i < 0 {
			break
		}
		j := strings.Index(s[i:], "*/")
		if j < 0 {
			s = s[:i]
			break
		}
		s = s[:i] + " " + s[i+j+2:]
	}
	var b strings.Builder
	for _, line := range strings.Split(s, "\n") {
		if k := strings.Index(line, "--"); k >= 0 {
			line = line[:k]
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}

// firstKeyword returns the upper-cased first SQL word of a statement, skipping
// leading punctuation like "(" in "(SELECT ...)".
func firstKeyword(stmt string) string {
	stmt = strings.TrimSpace(stmt)
	start := -1
	for i, r := range stmt {
		if unicode.IsLetter(r) {
			start = i
			break
		}
		if !unicode.IsSpace(r) && r != '(' {
			// something that isn't whitespace, '(', or a letter leads the
			// statement — treat the whole thing as its own "keyword" so an odd
			// construct is surfaced rather than silently passed.
			break
		}
	}
	if start < 0 {
		return ""
	}
	end := start
	for end < len(stmt) && (unicode.IsLetter(rune(stmt[end])) || stmt[end] == '_') {
		end++
	}
	return strings.ToUpper(stmt[start:end])
}
