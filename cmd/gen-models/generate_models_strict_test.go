package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// snapshotTree maps every file below root to its content.
func snapshotTree(t *testing.T, root string) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		relative, _ := filepath.Rel(root, path)
		files[relative] = string(data)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func tokenPlanCatalog(toolless string) map[string]any {
	var models []map[string]any
	for _, id := range []string{"deepseek-v4-flash-0731", "deepseek-v4-pro", "deepseek-v4-pro-0813", "glm-5.2", "qwen3.6-flash", "qwen3.7-max", "qwen3.7-plus", "qwen3.8-flash", "qwen3.8-max", "qwen3.8-max-preview"} {
		model := sourceModel(id, map[string]any{})
		if id == toolless {
			model["capabilities"] = map[string]any{"tools": map[string]any{"supported": false}, "reasoning": map[string]any{"supported": false}}
		}
		models = append(models, model)
	}
	return providerCatalog("alibaba-token-plan", models...)
}

func TestGenerateModelsStrict(t *testing.T) {
	// upstream: packages/ai/test/generate-models-strict.test.ts:18
	t.Run("fails before mutating generated data when an Individual model loses tool support", func(t *testing.T) {
		isolateGeneratorEnvironment(t)
		root := t.TempDir()
		dataDir := filepath.Join(root, "package", "data")
		// Prior generated data: a successful run over a valid catalog.
		status, stdout, stderr := runGeneratorSubprocess(t, root, tokenPlanCatalog(""), "--data-output", dataDir)
		if status != 0 {
			t.Fatalf("seeding run status = %d, want 0\n%s\n%s", status, stdout, stderr)
		}
		if _, err := os.Stat(filepath.Join(dataDir, "qwen-token-plan-individual.json")); err != nil {
			t.Fatal(err)
		}
		before := snapshotTree(t, filepath.Dir(dataDir))

		status, stdout, stderr = runGeneratorSubprocess(t, root, tokenPlanCatalog("deepseek-v4-flash-0731"), "--strict", "--data-output", dataDir)
		if status != 1 {
			t.Errorf("exit status = %d, want 1", status)
		}
		if want := "qwen-token-plan-individual model IDs do not match (missing: deepseek-v4-flash-0731)"; !strings.Contains(stdout+"\n"+stderr, want) {
			t.Errorf("output = %s\n%s, want diagnostic %q", stdout, stderr, want)
		}
		if after := snapshotTree(t, filepath.Dir(dataDir)); !reflect.DeepEqual(after, before) {
			t.Errorf("generator mutated the data directory or left staging files: before %d files, after %d files", len(before), len(after))
		}
		entries, err := os.ReadDir(filepath.Dir(dataDir))
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 1 || entries[0].Name() != "data" {
			t.Errorf("parent of the data directory = %v, want only data", entries)
		}
	})
}
