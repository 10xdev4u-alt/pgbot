package model_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/pgrundev/pgbot/internal/advisor"
	"github.com/pgrundev/pgbot/internal/model"
)

// repoRoot resolves the repository root from this test's location.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot resolve caller path")
	}
	return filepath.Dir(filepath.Dir(filepath.Dir(file))) // internal/model/ → root
}

// TestSchema_matchesModel is the drift guard (B7-2 DoD 10): the committed JSON
// Schema files must equal what reflection over the current Go types produces. If
// this fails, a contract field changed without regenerating — run
// `go run ./tools/schemagen` and commit the result (bumping SchemaVersion if the
// change is breaking).
func TestSchema_matchesModel(t *testing.T) {
	cases := []struct {
		file string
		want []byte
	}{
		{"pgbot-context-" + model.SchemaVersion + ".json", model.ContextSchema()},
		{"pgbot-advise-" + advisor.SchemaVersion + ".json", advisor.ReportSchema()},
	}
	for _, c := range cases {
		path := filepath.Join(repoRoot(t), "schema", c.file)
		got, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("%s: %v — run `go run ./tools/schemagen`", c.file, err)
			continue
		}
		if string(got) != string(c.want) {
			t.Errorf("%s is stale — the model changed but the committed schema didn't.\n"+
				"Run `go run ./tools/schemagen` and commit the result.", c.file)
		}
	}
}
