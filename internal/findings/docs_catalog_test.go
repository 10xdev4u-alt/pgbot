package findings

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// docsDir locates docs/findings from this test file's location.
func docsDir(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot resolve caller path")
	}
	// internal/findings/docs_catalog_test.go → repo root is two dirs up.
	root := filepath.Dir(filepath.Dir(filepath.Dir(file)))
	return filepath.Join(root, "docs", "findings")
}

// page holds a parsed docs/findings/<id>.md.
type page struct {
	id       string
	fm       map[string]string
	body     string
	sections map[string]string // "## Heading" → section text up to the next "## "
}

var reSection = regexp.MustCompile(`(?m)^## (.+)$`)

func parsePage(t *testing.T, path string) page {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	// Front-matter: between the first two --- lines.
	if !strings.HasPrefix(s, "---\n") {
		t.Fatalf("%s: missing YAML front-matter", filepath.Base(path))
	}
	end := strings.Index(s[4:], "\n---")
	if end < 0 {
		t.Fatalf("%s: unterminated front-matter", filepath.Base(path))
	}
	fmText := s[4 : 4+end]
	body := s[4+end+4:]

	fm := map[string]string{}
	for _, line := range strings.Split(fmText, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		fm[strings.TrimSpace(k)] = strings.Trim(strings.TrimSpace(v), `"`)
	}

	// Sections: split the body on "## " headings.
	sections := map[string]string{}
	idx := reSection.FindAllStringIndex(body, -1)
	names := reSection.FindAllStringSubmatch(body, -1)
	for i, loc := range idx {
		start := loc[1]
		end := len(body)
		if i+1 < len(idx) {
			end = idx[i+1][0]
		}
		sections[strings.TrimSpace(names[i][1])] = body[start:end]
	}
	return page{id: fm["id"], fm: fm, body: body, sections: sections}
}

func listPages(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(docsDir(t))
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || !strings.HasSuffix(n, ".md") || strings.HasPrefix(n, "_") || n == "README.md" {
			continue
		}
		out = append(out, n)
	}
	return out
}

// requiredSections every page must carry (B7-0 template).
var requiredSections = []string{
	"What pgbot observed", "Why it matters", "How to verify it yourself",
	"How to fix it", "When to ignore it", "What pgbot cannot see", "Related",
}

// TestDocsPages_structureAndFrontMatter validates every existing page against the
// template and the code (B7 DoD 9, 11). The reverse — every knownID HAS a page —
// is turned on in B7-1 once the catalogue is complete.
func TestDocsPages_structureAndFrontMatter(t *testing.T) {
	for _, name := range listPages(t) {
		name := name
		t.Run(name, func(t *testing.T) {
			p := parsePage(t, filepath.Join(docsDir(t), name))
			base := strings.TrimSuffix(name, ".md")

			if p.id != base {
				t.Errorf("front-matter id %q != filename %q", p.id, base)
			}
			if !KnownID(p.id) {
				t.Errorf("page %q is not a known finding id", p.id)
			}
			// Front-matter must agree with the code's catalog (DoD 9).
			if meta, ok := MetaFor(p.id); ok {
				if p.fm["dimension"] != meta.Dimension {
					t.Errorf("dimension: page %q != catalog %q", p.fm["dimension"], meta.Dimension)
				}
				if p.fm["severity"] != meta.Severity {
					t.Errorf("severity: page %q != catalog %q", p.fm["severity"], meta.Severity)
				}
				if p.fm["object"] != meta.ObjectClass {
					t.Errorf("object: page %q != catalog %q", p.fm["object"], meta.ObjectClass)
				}
				if p.fm["scope"] != meta.Scope {
					t.Errorf("scope: page %q != catalog %q", p.fm["scope"], meta.Scope)
				}
			}
			// All template sections present (DoD: template survives).
			for _, sec := range requiredSections {
				if _, ok := p.sections[sec]; !ok {
					t.Errorf("missing section %q", sec)
				}
			}
			// A runnable verification query (DoD 11).
			if !regexp.MustCompile("(?s)```sql.*?SELECT").MatchString(p.body) {
				t.Error("no runnable SQL verification query (```sql … SELECT)")
			}
			// A pasteable [[ignore]] block (DoD 11).
			if !strings.Contains(p.body, "[[ignore]]") || !strings.Contains(p.body, `finding = "`+p.id+`"`) {
				t.Error("no pasteable [[ignore]] block naming this finding")
			}
			// A per-object finding must show an object-scoped rule — a bare
			// finding-only rule over-suppresses and is exactly the failure mode the
			// aggregate-suppression decision fixed.
			if p.fm["object"] != "cluster" && !strings.Contains(p.body, "object  = ") && !strings.Contains(p.body, "object = ") {
				t.Errorf("object class %q but the [[ignore]] block has no `object =` line", p.fm["object"])
			}
		})
	}
}

// TestDocsCoverage_bidirectional is the drift guard (B7-1 DoD 8): every finding
// pgbot can emit has a page, a catalog entry, and a summary; and every page is a
// known finding. Without it the catalogue is stale within two sprints.
func TestDocsCoverage_bidirectional(t *testing.T) {
	pages := map[string]bool{}
	for _, n := range listPages(t) {
		pages[strings.TrimSuffix(n, ".md")] = true
	}
	for id := range knownIDs {
		if IsMetaFinding(id) {
			continue // config-hygiene meta-finding — documented in configuration.md
		}
		if !pages[id] {
			t.Errorf("finding %q has no docs/findings/%s.md", id, id)
		}
		if _, ok := MetaFor(id); !ok {
			t.Errorf("finding %q has no catalog Meta entry", id)
		}
		if Summary(id) == "" {
			t.Errorf("finding %q has no catalogue summary", id)
		}
	}
	for id := range pages {
		if !KnownID(id) {
			t.Errorf("docs/findings/%s.md is not a known finding id", id)
		}
	}
}

// TestScope_everyFindingClassified is the D3-0 drift guard: every finding pgbot
// can emit declares one of the four valid scopes, so a new finding cannot be
// added without its author deciding whether --profile=schema should keep it.
func TestScope_everyFindingClassified(t *testing.T) {
	for id := range knownIDs {
		s := Scope(id)
		if s == "" {
			t.Errorf("%q has no Scope — add one to its catalog entry (or metaScope)", id)
			continue
		}
		if !ValidScope(s) {
			t.Errorf("%q has invalid Scope %q (want schema|workload|history|infra)", id, s)
		}
	}
}

// TestCatalogIndex_matchesCommitted diffs the committed docs/findings/README.md
// against a fresh generation, so the index can't drift from the catalog.
func TestCatalogIndex_matchesCommitted(t *testing.T) {
	got, err := os.ReadFile(filepath.Join(docsDir(t), "README.md"))
	if err != nil {
		t.Fatalf("%v — run `go run ./tools/schemagen`", err)
	}
	if string(got) != string(CatalogIndexMarkdown()) {
		t.Error("docs/findings/README.md is stale — run `go run ./tools/schemagen` and commit it")
	}
}

// TestChecksumFailures_remediationIsSafe enforces DoD 12: the fix section never
// RECOMMENDS a page-rewriting operation. Such an op may appear only inside a
// negated paragraph (the explicit "do not" warning).
func TestChecksumFailures_remediationIsSafe(t *testing.T) {
	p := parsePage(t, filepath.Join(docsDir(t), "checksum_failures.md"))
	fix, ok := p.sections["How to fix it"]
	if !ok {
		t.Fatal("checksum_failures has no 'How to fix it' section")
	}
	rewriteOps := []string{"vacuum full", "reindex", "pg_repack"}
	for _, para := range strings.Split(fix, "\n\n") {
		low := strings.ToLower(para)
		for _, op := range rewriteOps {
			if strings.Contains(low, op) && !hasNegation(low) {
				t.Errorf("checksum_failures remediation appears to recommend %q (paragraph without a negation): %q", op, strings.TrimSpace(para))
			}
		}
	}
}

func hasNegation(low string) bool {
	for _, neg := range []string{"do not", "don't", "never", "must not", "avoid", "instead of"} {
		if strings.Contains(low, neg) {
			return true
		}
	}
	return false
}
