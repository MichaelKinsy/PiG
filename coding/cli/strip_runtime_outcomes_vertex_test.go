//go:build !pig_strip_google_vertex

package cli

import (
	"errors"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/pigstrip"
)

// D92: a SAVED default model of a stripped provider is not available, so a new session starts on the first available model, and says nothing about it: the selection adds no warning, error or fallback message. That is Pi's path for a saved default that is unavailable for another reason (model-resolver.ts findInitialModel step 2 requires the model found and its provider authenticated, step 3 takes the first available model, and fallbackMessage stays undefined), so the stripped default and an unknown one end the same way.
func TestRuntimeStrippedProviderSavedDefaultFallsToTheFirstAvailableModelSilently(t *testing.T) {
	setStripTestEnv(t, map[string]string{"GOOGLE_CLOUD_API_KEY": "x"})
	t.Setenv("ANTHROPIC_API_KEY", "x")
	type outcome struct{ ref, fallback, warnings, scope, errs string }
	selected := func(settings codingagent.Settings) outcome {
		t.Helper()
		result, err := selectStartupModel(t.Context(), startupModelOptions{}, settings, testServices(t, t.TempDir()))
		if err != nil || result.Model == nil {
			t.Fatalf("selectStartupModel(%+v) = %v, %v; want a model", settings, result.Model, err)
		}
		errs := ""
		if joined := errors.Join(result.Errors...); joined != nil {
			errs = joined.Error()
		}
		return outcome{result.Model.ProviderMeta.ProviderID + "/" + result.Model.ID, result.ModelFallbackMessage, strings.Join(result.Warnings, "|"), strings.Join(result.ScopeWarnings, "|"), errs}
	}
	vertexDefault := codingagent.Settings{DefaultProvider: "google-vertex", DefaultModel: "gemini-2.5-flash"}
	if got := selected(vertexDefault); got.ref != vertexModelRef {
		t.Fatalf("unstripped, the saved default selected %q, want %s", got.ref, vertexModelRef)
	}
	t.Cleanup(pigstrip.Strip(pigstrip.ListAPIs, "google-vertex"))
	stripped := selected(vertexDefault)
	if stripped.ref != "anthropic/claude-opus-5-5" || stripped.fallback != "" || stripped.warnings != "" || stripped.scope != "" || stripped.errs != "" {
		t.Fatalf("stripped saved default = %+v, want the first available model and no message of any kind", stripped)
	}
	if unknown := selected(codingagent.Settings{DefaultProvider: "google-vertex", DefaultModel: "no-such-model"}); unknown != stripped {
		t.Fatalf("a stripped saved default ended as %+v, an unknown one as %+v; want the same", stripped, unknown)
	}
}
