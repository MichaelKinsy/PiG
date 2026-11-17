package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Pi main.ts createRuntime collects settings, extension, model scope, CLI-model and API-key diagnostics in that order, and main reports the initial runtime's diagnostics once, deduplicated. Pi bacd45b9c (#11973, #11264): the scope warnings are among them, after the extension diagnostics and before buildSessionOptions' diagnostics, where resolveModelScope used to print each one first, even for metadata commands; a repeated pattern is reported once.
func TestStartupDiagnosticOrderMatchesPi(t *testing.T) {
	clearAuthEnvForNoAuthTest(t)
	binary := buildPigBinaryForSignalTest(t)
	root := t.TempDir()
	agentDir := filepath.Join(root, "agent")
	settings := filepath.Join(agentDir, "settings.json")
	writeStartupFixtureFile(t, settings, `{ "theme": `)
	missing := filepath.Join(root, "missing.ts")
	settingsWarning := "Warning: Invalid settings file " + settings + ": Unexpected end of JSON input\n"
	extensionError := "Error: Failed to load extension \"" + missing + "\": Extension path does not exist: " + missing + "\n"
	modelError := "Error: Model \"missing-model-rv-rpc\" not found. Use --list-models to see available models.\n"
	modelWarning := "Warning: Model \"unknown-rv-rpc\" not found for provider \"openai\". Using custom model id.\n"
	scopeWarning := "Warning: No models match pattern \"missing-scope-rv-rpc\"\n"
	apiError := "Error: --api-key requires a model to be specified via --model, --provider/--model, or --models\n"
	for _, mode := range []string{"json", "rpc", "text"} {
		for _, tc := range []struct {
			name  string
			args  []string
			after string
			// noModels says no model could be chosen, so the runtime's diagnostics end with the no-models warning (main.ts:917-918); a model chosen with a warning leaves none.
			noModels bool
		}{
			{"extension only", nil, "", true},
			{"model error", []string{"--model", "missing-model-rv-rpc"}, modelError, true},
			{"model warning", []string{"--model", "openai/unknown-rv-rpc"}, modelWarning, false},
			{"api key error", []string{"--model", "missing-model-rv-rpc", "--api-key", "fixture"}, modelError + apiError, true},
			{"scope and model", []string{"--models", "missing-scope-rv-rpc", "--model", "missing-model-rv-rpc"}, scopeWarning + modelError, true},
			{"scope and model warning", []string{"--models", "missing-scope-rv-rpc", "--model", "openai/unknown-rv-rpc"}, scopeWarning + modelWarning, false},
			{"repeated scope pattern", []string{"--models", "missing-scope-rv-rpc,missing-scope-rv-rpc"}, scopeWarning, true},
		} {
			t.Run(mode+"/"+tc.name, func(t *testing.T) {
				args := append([]string{"--mode", mode, "-e", missing}, tc.args...)
				run := runStartupDiagnosticProcess(t, binary, root, agentDir, args...)
				want := settingsWarning + extensionError + tc.after
				if tc.noModels {
					want += noModelsWarningCut
				}
				want += "Hint: Start without extensions using \"pig -ne\".\n"
				var exit *exec.ExitError
				if !errors.As(run.err, &exit) || exit.ExitCode() != 1 || run.stdout != "" || cutDocsRoot(run.stderr) != want {
					t.Fatalf("exit=%v stdout=%q\nstderr=%q\nwant=%q", run.err, run.stdout, run.stderr, want)
				}
			})
		}
	}
	// prepareInitialMessage precedes runtime diagnostic reporting (main.ts:884-896), so an invalid @file wins over extension/model failures.
	t.Run("file error precedes runtime diagnostics", func(t *testing.T) {
		file := filepath.Join(root, "missing.txt")
		run := runStartupDiagnosticProcess(t, binary, root, agentDir, "--mode", "json", "-e", missing, "@"+file)
		var exit *exec.ExitError
		if !errors.As(run.err, &exit) || exit.ExitCode() != 1 || run.stdout != "" || run.stderr != "Error: File not found: "+file+"\n" {
			t.Fatalf("exit=%v stdout=%q stderr=%q", run.err, run.stdout, run.stderr)
		}
	})
	for _, metadata := range []string{"--help", "--list-models"} {
		t.Run(metadata, func(t *testing.T) {
			run := runStartupDiagnosticProcess(t, binary, root, agentDir, metadata, "--models", "missing-scope-rv-rpc", "--model", "missing-model-rv-rpc", "-e", missing)
			// Pi 6dfb54678 main.ts reports startupDiagnostics, runtime diagnostics included, before the help text and the model listing; c8c456c85c6e ends them with the no-models warning (main.ts:917-918, probed on Pi: --help and --list-models, exit 0).
			if run.err != nil || cutDocsRoot(run.stderr) != settingsWarning+extensionError+scopeWarning+modelError+noModelsWarningCut || run.stdout == "" {
				t.Fatalf("exit=%v stdout=%q stderr=%q", run.err, run.stdout, run.stderr)
			}
		})
	}
}

// Pi's Agent uses DEFAULT_MODEL for an absent initial model (agent.ts:57-97), so main.ts:910 does not reject it. print-mode.ts:126-139 writes the header before prompting; agent-session.ts:1687-1690 rejects the unauthenticated sentinel only when a prompt reaches model preflight.
func TestHeadlessNoModelOutputMatchesPi(t *testing.T) {
	clearAuthEnvForNoAuthTest(t)
	binary := buildPigBinaryForSignalTest(t)
	root := t.TempDir()
	agentDir := filepath.Join(root, "agent")
	for _, mode := range []string{"json", "text"} {
		for _, prompt := range []string{"", "hello"} {
			t.Run(mode+"/"+prompt, func(t *testing.T) {
				args := []string{"--mode", mode}
				if prompt != "" {
					args = append(args, prompt)
				}
				run := runStartupDiagnosticProcess(t, binary, root, agentDir, args...)
				// Each PiG build stages its docs in its own bundle directory under docs (D22): the messages name that directory. A session also leaves the lease file beside it (bundle-<hash>.lease), which is no bundle.
				bundles, globErr := filepath.Glob(filepath.Join(root, "pig", "docs", "bundle-????????????????"))
				if globErr != nil || len(bundles) != 1 {
					t.Fatalf("docs bundle directories = %v (%v), want exactly one", bundles, globErr)
				}
				guidance := "Use /login to log into a provider via OAuth or API key. See:\n  " + filepath.Join(bundles[0], "providers.md") + "\n  " + filepath.Join(bundles[0], "models.md") + "\n"
				// Pi c8c456c85c6e (#12234): with no model main.ts:917-918 adds the "No models available" guidance to the runtime diagnostics as a warning (the agent keeps its placeholder model, agent.ts:57-97, so session.model is set), so every mode writes it to stderr once (measured on the oracle carrying the commit); a prompt then fails the key check as before.
				wantError := "Warning: No models available. " + guidance
				if prompt != "" {
					wantError += "No API key found for the selected model.\n\n" + guidance
					var exit *exec.ExitError
					if !errors.As(run.err, &exit) || exit.ExitCode() != 1 {
						t.Fatalf("exit=%v, want 1", run.err)
					}
				} else if run.err != nil {
					t.Fatalf("empty invocation: %v stderr=%q", run.err, run.stderr)
				}
				if run.stderr != wantError {
					t.Errorf("stderr=%q want=%q", run.stderr, wantError)
				}
				if mode == "text" {
					if run.stdout != "" {
						t.Errorf("text stdout=%q", run.stdout)
					}
					return
				}
				// The header cwd is process.cwd(), which keeps the runner's 8.3 short form (RUNNER~1) on Windows, as Pi's does.
				wantCWD := physicalPath(t, root)
				var header struct {
					Type      string `json:"type"`
					Version   int    `json:"version"`
					ID        string `json:"id"`
					Timestamp string `json:"timestamp"`
					CWD       string `json:"cwd"`
				}
				if err := json.Unmarshal([]byte(run.stdout), &header); err != nil || header.Type != "session" || header.Version != 3 || header.ID == "" || header.Timestamp == "" || header.CWD != wantCWD || strings.Count(run.stdout, "\n") != 1 {
					t.Fatalf("stdout=%q: expected only a version-3 Session header: %v", run.stdout, err)
				}
			})
		}
	}
}

func runStartupDiagnosticProcess(t *testing.T, binary, root, agentDir string, args ...string) startupRun {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), binary, append([]string{"--offline", "--no-session", "-ne"}, args...)...)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "HOME="+root, "PIG_HOME="+filepath.Join(root, "pig"), "PIG_CODING_AGENT_DIR="+agentDir, "FORCE_COLOR=0")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	return startupRun{err: err, stdout: stdout.String(), stderr: stderr.String()}
}
