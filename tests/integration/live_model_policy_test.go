package integration

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const requiredLiveModel = "github-copilot/gpt-5-mini"

func TestLiveModelPolicy_ParityScenarios(t *testing.T) {
	root := repoRoot(t)
	dir := filepath.Join(root, "parity", "scenarios")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read scenarios: %v", err)
	}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".toml" {
			continue
		}
		if e.Name() == "live_model_policy_test.go" {
			continue
		}
		path := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		text := string(data)
		if !strings.Contains(text, `"live"`) {
			continue
		}
		if !strings.Contains(text, `model = "`+requiredLiveModel+`"`) {
			t.Errorf("live scenario %s must pin model %q", e.Name(), requiredLiveModel)
		}
	}
}

func TestLiveModelPolicy_IntegrationFiles(t *testing.T) {
	root := repoRoot(t)
	dir := filepath.Join(root, "tests", "integration")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read tests/integration: %v", err)
	}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".go" {
			continue
		}
		path := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		text := string(data)
		for _, forbidden := range []string{
			"github-copilot/" + "gpt-4o",
			"anthropic/" + "claude",
			"openai/" + "gpt-4o",
		} {
			if strings.Contains(text, forbidden) {
				t.Errorf("integration file %s contains forbidden live-test model literal %q; use %q", e.Name(), forbidden, requiredLiveModel)
			}
		}
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}
