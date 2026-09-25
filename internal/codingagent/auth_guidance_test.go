package codingagent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProviderLoginHelpPrefersMaterializedPigDocs(t *testing.T) {
	pigHome := t.TempDir()
	t.Setenv("PIG_HOME", pigHome)
	docsDir := filepath.Join(pigHome, "docs")
	if err := os.MkdirAll(docsDir, 0o700); err != nil {
		t.Fatalf("mkdir docs dir: %v", err)
	}
	for _, name := range []string{"providers.md", "models.md"} {
		if err := os.WriteFile(filepath.Join(docsDir, name), []byte("# test docs\n"), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	msg := ProviderLoginHelp()
	wantProviders := filepath.Join(pigHome, "docs", "providers.md")
	wantModels := filepath.Join(pigHome, "docs", "models.md")
	if !strings.Contains(msg, wantProviders) || !strings.Contains(msg, wantModels) {
		t.Fatalf("ProviderLoginHelp = %q, want materialized pig docs %q and %q", msg, wantProviders, wantModels)
	}
	if strings.Contains(msg, ".upstream/current") {
		t.Fatalf("ProviderLoginHelp should not prefer source docs when pig docs exist: %q", msg)
	}
}
