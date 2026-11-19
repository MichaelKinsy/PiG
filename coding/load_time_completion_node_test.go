//go:build !pig_strip_node_extensions

package coding

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDiscoverAndLoadExtensionsAnswersLoadTimeCompletions(t *testing.T) {
	t.Parallel()
	module, answers := loadTimeCompletionModule(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	root := t.TempDir()
	cwd, agentDir := filepath.Join(root, "project"), filepath.Join(root, "agent")
	for _, dir := range []string{cwd, agentDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	result, err := DiscoverAndLoadExtensions(ctx, []string{module}, cwd, agentDir)
	if err != nil {
		t.Fatal(err)
	}
	defer result.Close()
	if len(result.Errors) != 0 || len(result.Extensions) != 1 {
		t.Fatalf("errors = %v, extensions = %d, want the module loaded", result.Errors, len(result.Extensions))
	}
	requireLoadTimeAnswers(t, answers)
}

func TestDefaultResourceLoaderAnswersLoadTimeCompletions(t *testing.T) {
	t.Parallel()
	module, answers := loadTimeCompletionModule(t)
	root := t.TempDir()
	cwd, agentDir := filepath.Join(root, "project"), filepath.Join(root, "agent")
	for _, dir := range []string{cwd, agentDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	loader := NewDefaultResourceLoader(DefaultResourceLoaderOptions{CWD: cwd, AgentDir: agentDir, NoExtensions: true, AdditionalExtensionPaths: []string{module}})
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if err := loader.ReloadWith(ctx); err != nil {
		t.Fatal(err)
	}
	defer loader.Close()
	requireLoadTimeAnswers(t, answers)
}
