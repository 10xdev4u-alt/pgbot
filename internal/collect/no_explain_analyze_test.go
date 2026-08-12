package collect

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// TestNoExplainAnalyze is the load-bearing safety invariant, enforced in `go
// test` as well as CI (T12 #12): pgbot must NEVER run an inspected query. EXPLAIN
// (without ANALYZE) only plans; EXPLAIN ANALYZE (or the ANALYZE option in any
// order) EXECUTES it. It must not appear anywhere in the SQL pack or the Go
// sources — not even in a string literal — because there is no safe reason for
// pgbot to hold that text.
func TestNoExplainAnalyze(t *testing.T) {
	root := repoRoot(t)
	// Matches EXPLAIN ANALYZE and EXPLAIN (ANALYZE), any whitespace/case.
	pat := regexp.MustCompile(`(?i)explain\s*(\(\s*)?analyze`)

	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" || d.Name() == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}
		ext := filepath.Ext(path)
		if ext != ".go" && ext != ".sql" {
			return nil
		}
		// Don't flag this guard file itself.
		if strings.HasSuffix(path, "no_explain_analyze_test.go") {
			return nil
		}
		b, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if pat.Match(b) {
			t.Errorf("EXPLAIN ANALYZE found in %s — pgbot must never execute an inspected query", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// repoRoot walks up from this test file's directory until it finds go.mod.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate test file")
	}
	dir := filepath.Dir(file)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found walking up from test file")
		}
		dir = parent
	}
}
