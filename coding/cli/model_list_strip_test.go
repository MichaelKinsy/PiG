package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/pigstrip"
)

// strippedAPIAuthCases are strippable APIs, each the only API of its catalog provider, with the environment that
// authenticates the provider.
var strippedAPIAuthCases = []struct {
	api             ai.API
	provider, model string
	env             map[string]string
}{
	{ai.APIBedrockConverseStream, "amazon-bedrock", "amazon.nova-2-lite-v1:0", map[string]string{"AWS_ACCESS_KEY_ID": "x", "AWS_SECRET_ACCESS_KEY": "y", "AWS_REGION": "us-east-1"}},
	{ai.APIMistralConversations, "mistral", "codestral-latest", map[string]string{"MISTRAL_API_KEY": "x"}},
}

func setStripTestEnv(t *testing.T, env map[string]string) {
	t.Helper()
	filterAllProviderEnv(t)
	t.Setenv("HOME", t.TempDir())
	for name, value := range env {
		t.Setenv(name, value)
	}
}

// `pig --list-models` with a stripped API lists none of its models, even with its provider authenticated, and still
// lists the models of other authenticated providers.
func TestPrintModelListOmitsStrippedAPIModels(t *testing.T) {
	for _, tc := range strippedAPIAuthCases {
		t.Run(string(tc.api), func(t *testing.T) {
			setStripTestEnv(t, tc.env)
			t.Setenv("ANTHROPIC_API_KEY", "x")
			dir := t.TempDir()
			if !pigstrip.Has(pigstrip.ListAPIs, string(tc.api)) {
				if out := captureStdout(func() { printModelList(codingagent.NewModelRegistry(dir), dir, "") }); !strings.Contains(out, tc.model) {
					t.Fatalf("unstripped --list-models lacks %s:\n%s", tc.model, out)
				}
			}
			t.Cleanup(pigstrip.Strip(pigstrip.ListAPIs, string(tc.api)))
			out := captureStdout(func() { printModelList(codingagent.NewModelRegistry(dir), dir, "") })
			for line := range strings.SplitSeq(out, "\n") {
				if provider, _, _ := strings.Cut(line, " "); provider == tc.provider {
					t.Fatalf("--list-models lists a model of stripped %s: %q\n%s", tc.api, line, out)
				}
			}
			if !strings.Contains(out, "claude-haiku-4-5") {
				t.Fatalf("--list-models dropped anthropic with %s stripped:\n%s", tc.api, out)
			}
		})
	}
}

// Startup never selects a model of a stripped API: not as the saved default, not as the provider default of the only
// authenticated provider, and not from the --models scope. With no other authenticated provider no model is
// available; with one, the scope falls to its model.
func TestSelectStartupModelSkipsStrippedAPIModels(t *testing.T) {
	for _, tc := range strippedAPIAuthCases {
		t.Run(string(tc.api), func(t *testing.T) {
			setStripTestEnv(t, tc.env)
			settings := codingagent.Settings{DefaultProvider: tc.provider, DefaultModel: tc.model}
			selectedRef := func(options startupModelOptions) string {
				t.Helper()
				result, err := selectStartupModel(t.Context(), options, settings, testServices(t, t.TempDir()))
				switch {
				case err != nil:
					return "error: " + err.Error()
				case result.Model == nil:
					return "none: " + result.ModelFallbackMessage
				}
				return result.Model.ProviderMeta.ProviderID + "/" + result.Model.ID
			}
			catalogRef := tc.provider + "/" + tc.model
			if !pigstrip.Has(pigstrip.ListAPIs, string(tc.api)) {
				if got := selectedRef(startupModelOptions{}); got != catalogRef {
					t.Fatalf("unstripped startup selected %s, want the saved default %s", got, catalogRef)
				}
			}
			t.Cleanup(pigstrip.Strip(pigstrip.ListAPIs, string(tc.api)))
			if got, want := selectedRef(startupModelOptions{}), "none: "+codingagent.FormatNoModelsAvailableMessage(); got != want {
				t.Fatalf("startup with only stripped %s authenticated selected %q, want %q", tc.api, got, want)
			}
			t.Setenv("ANTHROPIC_API_KEY", "x")
			scope := startupModelOptions{ScopePatterns: []string{catalogRef, "anthropic/claude-haiku-4-5"}}
			if got := selectedRef(scope); got != "anthropic/claude-haiku-4-5" {
				t.Fatalf("--models %v selected %s with %s stripped, want anthropic/claude-haiku-4-5", scope.ScopePatterns, got, tc.api)
			}
		})
	}
}

// A models.json custom model under a provider whose only API is stripped, without its own baseUrl, composes from the
// full catalog as in Stock PiG: the strip adds no `"baseUrl" is required` models.json error, `--list-models` still
// offers no model of the provider, and naming the model reports the strip.
func TestStrippedProviderCustomModelIsNeitherOfferedNorALoadError(t *testing.T) {
	for _, tc := range strippedAPIAuthCases {
		t.Run(string(tc.api), func(t *testing.T) {
			setStripTestEnv(t, tc.env)
			t.Setenv("ANTHROPIC_API_KEY", "x")
			dir := t.TempDir()
			modelsJSON := `{"providers": {"` + tc.provider + `": {"apiKey": "x", "models": [{"id": "my-custom"}]}}}`
			if err := os.WriteFile(filepath.Join(dir, "models.json"), []byte(modelsJSON), 0o600); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(pigstrip.Strip(pigstrip.ListAPIs, string(tc.api)))
			services := testServices(t, dir)
			registry := services.Registry().ModelRegistry
			if loadErr := registry.LoadError(); loadErr != "" {
				t.Fatalf("models.json with %s stripped: %s", tc.api, loadErr)
			}
			out := captureStdout(func() { printModelList(registry, dir, "") })
			for line := range strings.SplitSeq(out, "\n") {
				if provider, _, _ := strings.Cut(line, " "); provider == tc.provider {
					t.Fatalf("--list-models lists a model of stripped %s: %q\n%s", tc.api, line, out)
				}
			}
			want := pigstrip.Error("API "+string(tc.api), pigstrip.ListAPIs, string(tc.api)).Error()
			if _, _, _, err := resolveModel(tc.provider+"/my-custom", "", codingagent.Settings{}, services); err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("--model %s/my-custom with %s stripped: %v, want the strip error %q", tc.provider, tc.api, err, want)
			}
		})
	}
}

// RPC get_available_models, under a runtime strip of the active Piglet, lists no model of a stripped API, even with
// its provider authenticated.
func TestRPCGetAvailableModelsOmitsStrippedAPIModels(t *testing.T) {
	home := t.TempDir()
	env := []string{"HOME=" + home, "PIG_HOME=" + filepath.Join(home, "pig"), "PIG_CODING_AGENT_DIR=" + filepath.Join(home, "agent"), "PIG_OFFLINE=1", "FORCE_COLOR=0"}
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if strings.HasSuffix(name, "_API_KEY") || strings.HasSuffix(name, "_TOKEN") || strings.HasSuffix(name, "_KEY") || strings.HasPrefix(name, "AWS_") || name == "ANTHROPIC_FEDERATION_RULE_ID" {
			env = append(env, name+"=")
		}
	}
	var apis []string
	for _, tc := range strippedAPIAuthCases {
		apis = append(apis, string(tc.api))
		for name, value := range tc.env {
			env = append(env, name+"="+value)
		}
	}
	env = append(env, "ANTHROPIC_API_KEY=x")
	pigletPath := filepath.Join(t.TempDir(), "lean.yaml")
	if err := os.WriteFile(pigletPath, []byte("name: lean\nstrip:\n  apis: ["+strings.Join(apis, ", ")+"]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	p := startRPCProcessAt(t, t.TempDir(), env, "--no-extensions", "--no-session", "--piglet", pigletPath, "--model", "anthropic/claude-haiku-4-5")
	p.send(`{"id":"models","type":"get_available_models"}`)
	var models []any
	p.await("get_available_models", func(record rpcRecord) bool {
		if record["type"] != "response" || record["id"] != "models" {
			return false
		}
		data, _ := record["data"].(map[string]any)
		models, _ = data["models"].([]any)
		return true
	})
	p.closeAndWait("after get_available_models")
	anthropic := false
	for _, value := range models {
		model, _ := value.(map[string]any)
		provider, _ := model["provider"].(string)
		for _, tc := range strippedAPIAuthCases {
			if provider == tc.provider || model["api"] == string(tc.api) {
				t.Fatalf("get_available_models lists %s/%v of stripped %s", provider, model["id"], tc.api)
			}
		}
		anthropic = anthropic || provider == "anthropic"
	}
	if !anthropic {
		t.Fatalf("get_available_models lacks the authenticated anthropic models: %v", models)
	}
}
