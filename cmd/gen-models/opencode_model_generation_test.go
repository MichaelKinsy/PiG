package main

import (
	"path/filepath"
	"strings"
	"testing"
)

type opencodeRun struct {
	status         int
	stdout, stderr string
	output         string
}

func generateOpencodeModels(t *testing.T, strict bool, models ...map[string]any) opencodeRun {
	t.Helper()
	root := t.TempDir()
	output := filepath.Join(root, "catalog")
	args := []string{}
	if strict {
		args = append(args, "--strict")
	}
	args = append(args, "--json-only", "--json-output", output)
	status, stdout, stderr := runGeneratorSubprocess(t, root, providerCatalog("opencode", models...), args...)
	return opencodeRun{status, stdout, stderr, output}
}

func TestOpencodeModelGeneration(t *testing.T) {
	// upstream: packages/ai/test/opencode-model-generation.test.ts:58
	t.Run("uses the protocol and base URL models.dev lists for each model", func(t *testing.T) {
		base := "https://example.test/zen"
		url := func(path string) map[string]any { return map[string]any{"base_url": base + path} }
		run := generateOpencodeModels(t, false,
			sourceModel("chat", map[string]any{"chat-completions": url("/openai/v1")}),
			sourceModel("responses", map[string]any{"responses": url("/openai/v1")}),
			sourceModel("messages", map[string]any{"messages": url("/anthropic/v1")}),
			sourceModel("gemini", map[string]any{"generate-content": url("/google/v1beta")}),
			// Native protocols win over Chat Completions regardless of listing order.
			sourceModel("both", map[string]any{"chat-completions": url("/openai/v1"), "messages": url("/anthropic/v1")}),
			sourceModel("live", map[string]any{"bidi-generate-content": url("/google/v1beta")}),
		)
		if run.status != 0 {
			t.Fatalf("status = %d, want 0\n%s\n%s", run.status, run.stdout, run.stderr)
		}
		if want := "models.dev lists no supported API for opencode/live (bidi-generate-content)"; !strings.Contains(run.stderr, want) {
			t.Errorf("stderr = %q, want it to contain %q", run.stderr, want)
		}
		generated := decodeJSONObject(t, filepath.Join(run.output, "providers", "opencode.json"))
		want := map[string][2]string{
			"chat":      {"openai-completions", base + "/openai/v1"},
			"responses": {"openai-responses", base + "/openai/v1"},
			// The Anthropic SDK appends /v1/messages itself.
			"messages": {"anthropic-messages", base + "/anthropic"},
			"gemini":   {"google-generative-ai", base + "/google/v1beta"},
			"both":     {"anthropic-messages", base + "/anthropic"},
		}
		if len(generated) != len(want) {
			t.Errorf("generated %d models, want %d", len(generated), len(want))
		}
		for id, endpoint := range want {
			model, ok := generated[id]
			if !ok {
				t.Errorf("model %s missing", id)
				continue
			}
			assertGeneratedJSON(t, generatedModelField(t, model, "id"), `"`+id+`"`)
			assertGeneratedJSON(t, generatedModelField(t, model, "api"), `"`+endpoint[0]+`"`)
			assertGeneratedJSON(t, generatedModelField(t, model, "baseUrl"), `"`+endpoint[1]+`"`)
		}
	})

	// upstream: packages/ai/test/opencode-model-generation.test.ts:89
	t.Run("fails strict generation for a model without a supported protocol", func(t *testing.T) {
		run := generateOpencodeModels(t, true, sourceModel("live", map[string]any{"bidi-generate-content": map[string]any{"base_url": "https://example.test/google/v1beta"}}))
		if run.status != 1 {
			t.Errorf("status = %d, want 1", run.status)
		}
		if want := "models.dev lists no supported API for opencode/live (bidi-generate-content)"; !strings.Contains(run.stderr, want) {
			t.Errorf("stderr = %q, want it to contain %q", run.stderr, want)
		}
	})
}
