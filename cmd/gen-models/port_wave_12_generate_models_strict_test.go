package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPortWave12GenerateModelsStrict(t *testing.T) {
	// upstream: packages/ai/test/generate-models-strict.test.ts:16
	t.Run("fails before mutating generated data when an Individual model loses tool support", func(t *testing.T) {
		isolateGeneratorEnvironment(t)
		packageRoot := upstreamAIPackage(t)
		isolated := filepath.Join(t.TempDir(), "package")
		if err := os.MkdirAll(isolated, 0o755); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"scripts", "src"} {
			if err := os.CopyFS(filepath.Join(isolated, name), os.DirFS(filepath.Join(packageRoot, name))); err != nil {
				t.Fatal(err)
			}
		}
		manifest, err := os.ReadFile(filepath.Join(packageRoot, "package.json"))
		if err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(isolated, "package.json"), string(manifest))
		modelIDs := []string{"deepseek-v4-flash-0731", "deepseek-v4-pro", "deepseek-v4-pro-0813", "glm-5.2", "qwen3.6-flash", "qwen3.7-max", "qwen3.7-plus", "qwen3.8-flash", "qwen3.8-max", "qwen3.8-max-preview"}
		sources := make(map[string]any)
		for _, id := range modelIDs {
			sources[id] = map[string]any{"id": id, "name": id, "tool_call": id != "deepseek-v4-flash-0731"}
		}
		catalog := map[string]any{"alibaba-token-plan": map[string]any{"models": sources}}
		paths := []string{"src/models.generated.ts", "src/providers/qwen-token-plan-individual.models.ts", "src/providers/data/qwen-token-plan-individual.json", "src/providers/data/.manifest.json"}
		before := make(map[string][]byte)
		for _, name := range paths {
			data, err := os.ReadFile(filepath.Join(packageRoot, name))
			if err != nil {
				t.Fatal(err)
			}
			before[name] = data
		}
		// Also protect the native command's generated output, not just the upstream copied tree.
		goCatalog := filepath.Join("../..", "ai", "models_generated.go")
		goBefore, err := os.ReadFile(goCatalog)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(isolated, "ai"), 0o755); err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(isolated, "ai", "models_generated.go"), string(goBefore))
		status, stdout, stderr := runGeneratorSubprocess(t, isolated, catalog, "--strict")
		if status != 1 {
			t.Errorf("exit status = %d, want 1", status)
		}
		if want := "qwen-token-plan-individual model IDs do not match (missing: deepseek-v4-flash-0731)"; !strings.Contains(stdout+"\n"+stderr, want) {
			t.Errorf("output = %s\n%s, want diagnostic %q", stdout, stderr, want)
		}
		for _, root := range []string{packageRoot, isolated} {
			for _, name := range paths {
				data, err := os.ReadFile(filepath.Join(root, name))
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(data, before[name]) {
					t.Errorf("generator mutated %s", filepath.Join(root, name))
				}
			}
		}
		for _, path := range []string{goCatalog, filepath.Join(isolated, "ai", "models_generated.go")} {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(data, goBefore) {
				t.Errorf("generator mutated %s", path)
			}
		}
	})
}
