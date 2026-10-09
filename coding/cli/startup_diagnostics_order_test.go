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

// Pi 0.87.1 main.ts:782-826 collects extension, CLI-model, and API-key diagnostics before reporting them at 895-906. Scope warnings are printed during resolveModelScope (model-resolver.ts:372-381), even for metadata commands.
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
			name          string
			args          []string
			before, after string
		}{
			{"extension only", nil, "", ""},
			{"model error", []string{"--model", "missing-model-rv-rpc"}, "", modelError},
			{"model warning", []string{"--model", "openai/unknown-rv-rpc"}, "", modelWarning},
			{"api key error", []string{"--model", "missing-model-rv-rpc", "--api-key", "fixture"}, "", modelError + apiError},
			{"scope and model", []string{"--models", "missing-scope-rv-rpc", "--model", "missing-model-rv-rpc"}, scopeWarning, modelError},
		} {
			t.Run(mode+"/"+tc.name, func(t *testing.T) {
				args := append([]string{"--mode", mode, "-e", missing}, tc.args...)
				run := runStartupDiagnosticProcess(t, binary, root, agentDir, args...)
				want := tc.before + settingsWarning + extensionError + tc.after + "Hint: Start without extensions using \"pig -ne\".\n"
				var exit *exec.ExitError
				if !errors.As(run.err, &exit) || exit.ExitCode() != 1 || run.stdout != "" || run.stderr != want {
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
			if run.err != nil || run.stderr != scopeWarning+settingsWarning || run.stdout == "" {
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
				wantError := ""
				if prompt != "" {
					wantError = "No API key found for the selected model.\n\nUse /login to log into a provider via OAuth or API key. See:\n  " + filepath.Join(root, "pig", "docs", "providers.md") + "\n  " + filepath.Join(root, "pig", "docs", "models.md") + "\n"
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
				// PiG reports the long form of the cwd; t.TempDir keeps the runner's 8.3 short form (RUNNER~1) on Windows.
				wantCWD, err := filepath.EvalSymlinks(root)
				if err != nil {
					t.Fatal(err)
				}
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
