package cli

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/pigstrip"
)

const (
	vertexStripMessage = "Provider google-vertex: API google-vertex is stripped from the Piglet \"lean\" (strip.apis: google-vertex)"
	vertexModelRef     = "google-vertex/gemini-2.5-flash"
)

// runStripPig runs the built pig with a Piglet that strips the google-vertex API (strip.apis) when strip is true, the provider authenticated by its API key, and returns its output and exit status.
func runStripPig(t *testing.T, strip bool, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	clearAuthEnvForNoAuthTest(t)
	binary := buildPigBinaryForSignalTest(t)
	root := t.TempDir()
	agentDir := filepath.Join(root, "agent")
	if err := os.MkdirAll(agentDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if strip {
		path := filepath.Join(root, "lean.yaml")
		if err := os.WriteFile(path, []byte("name: lean\nstrip:\n  apis: [google-vertex]\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		args = append([]string{"--piglet", path}, args...)
	}
	cmd := exec.CommandContext(t.Context(), binary, append([]string{"--offline", "--no-session", "-ne"}, args...)...)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "HOME="+root, "PIG_HOME="+filepath.Join(root, "pig"), "PIG_CODING_AGENT_DIR="+agentDir, "FORCE_COLOR=0", "NO_COLOR=1", "GOOGLE_CLOUD_API_KEY=x")
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err := cmd.Run()
	if exit := (*exec.ExitError)(nil); errors.As(err, &exit) {
		code = exit.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return out.String(), errOut.String(), code
}

// D92: with strip.apis naming google-vertex at run time, `--model google-vertex/gemini-2.5-flash` takes the unknown-model path that asks ai.StrippedModelError first: it prints the strip error and exits 1. Without the strip the same model is accepted (the control: the process then runs past model resolution, here to the missing prompt's usage).
func TestRuntimeStrippedProviderModelFlagPrintsTheStripErrorAndExits1(t *testing.T) {
	stdout, stderr, code := runStripPig(t, true, "--model", vertexModelRef, "-p", "hi")
	if want := "Error: " + vertexStripMessage; code != 1 || stdout != "" || !strings.HasPrefix(stderr, want) {
		t.Fatalf("exit %d, stdout %q, stderr %q; want exit 1 and stderr starting %q", code, stdout, stderr, want)
	}
	_, stderr, _ = runStripPig(t, false, "--model", vertexModelRef, "--list-models")
	if strings.Contains(stderr, "stripped") || strings.Contains(stderr, "not found") {
		t.Fatalf("without the strip: stderr %q", stderr)
	}
}

// D92: a --models pattern that matches only the stripped provider's models warns `No models match pattern "google-vertex/*"` (the scope resolves against the available models, which leave the stripped API's out). Without the strip the pattern matches and nothing is said.
func TestRuntimeStrippedProviderModelsPatternWarnsNoMatch(t *testing.T) {
	stdout, stderr, code := runStripPig(t, true, "--models", "google-vertex/*", "--list-models")
	if want := "Warning: No models match pattern \"google-vertex/*\"\n"; code != 0 || !strings.Contains(stderr, want) || strings.Contains(stdout, "google-vertex ") {
		t.Fatalf("exit %d, stdout %q, stderr %q; want exit 0, the warning %q and no google-vertex model listed", code, stdout, stderr, want)
	}
	stdout, stderr, code = runStripPig(t, false, "--models", "google-vertex/*", "--list-models")
	if code != 0 || strings.Contains(stderr, "No models match") || !strings.Contains(stdout, "google-vertex") {
		t.Fatalf("without the strip: exit %d, stdout %q, stderr %q; want the pattern to match", code, stdout, stderr)
	}
}

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
