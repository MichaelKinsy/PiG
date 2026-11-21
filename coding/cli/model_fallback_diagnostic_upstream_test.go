package cli

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// modelFallbackSession saves a two-turn session whose last model, provider/modelID, is not available, as Pi's test does
// (model-fallback-diagnostic.test.ts createSessionWithMissingModel), and returns its path and the id of its second user message.
func modelFallbackSession(t *testing.T, projectDir, sessionDir, provider, modelID string) (string, string) {
	t.Helper()
	storage := codingagent.NewSessionManagerWithDir(projectDir, sessionDir)
	history, err := storage.Create("model-fallback", "")
	if err != nil {
		t.Fatal(err)
	}
	var lastUser string
	for _, text := range []string{"hello", "again"} {
		if lastUser, err = history.AppendMessage(agent.AgentMessage{User: &agent.UserMessage{Role: agent.RoleUser, Content: ai.UserContentBlocks{ai.TextContent{Text: text}}}}); err != nil {
			t.Fatal(err)
		}
		if _, err := history.AppendMessage(agent.AgentMessage{Assistant: &agent.AssistantMessage{
			Role: agent.RoleAssistant, Provider: provider, ModelID: modelID, API: ai.APIOpenAICompletions, StopReason: "stop",
			Content: []ai.AssistantContentBlock{ai.TextContent{Text: "hi"}}, Usage: &ai.Usage{Input: 1, Output: 1},
		}}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(history.Path()); err != nil {
		t.Fatalf("the session was not persisted: %v", err)
	}
	return history.Path(), lastUser
}

// fallbackShape is one provider shape of the acceptance matrix: how the agent directory and environment make a model available, the saved model that is gone, and the model
// the fallback rule chooses.
type fallbackShape struct {
	name string
	// models and settings are the contents of the agent directory's models.json and settings.json; keys are API keys of built-in providers in the environment.
	models, settings string
	keys             []string
	savedProvider    string
	savedModel       string
	// using is the provider/model the fallback chooses.
	using string
	// auth is the contents of the agent directory's auth.json.
	auth string
	// network says the chosen model's endpoint is the closed loopback port of models.json, so print and JSON mode, which must send a prompt, stay local.
	network bool
}

const fakeProviderModels = `{"providers":{"fake":{"baseUrl":"http://127.0.0.1:9/v1","api":"openai-completions","apiKey":"test","models":[{"id":"zzz-model"},{"id":"aaa-model"}]}}}`

// builtinProviderKeys names the environment variable of each built-in provider a shape holds a key of.
var builtinProviderKeys = map[string]string{"groq": "GROQ_API_KEY", "mistral": "MISTRAL_API_KEY", "xai": "XAI_API_KEY"}

// firstByDefaultOrder is the model Pi's findInitialModel falls back to when only these providers have a login and no default model is set: the first of defaultModelPerProvider
// whose provider is available (model-resolver.ts:589-596), read from the same shared data PiG's findInitialModel reads.
func firstByDefaultOrder(t *testing.T, providers ...string) string {
	t.Helper()
	for _, entry := range codingagent.DefaultModelPerProviderOrder {
		if slices.Contains(providers, entry.Provider) {
			return entry.Provider + "/" + entry.ModelID
		}
	}
	t.Fatalf("none of %v has a default model", providers)
	return ""
}

func fallbackShapes(t *testing.T) []fallbackShape {
	t.Helper()
	return []fallbackShape{
		// An OpenAI-compatible provider under a custom base URL and an API key, which models.json names as the default: Pi's own test.
		{name: "api-key-custom-base-url", models: fakeProviderModels, settings: `{"defaultProvider":"fake","defaultModel":"zzz-model"}`, savedProvider: "gone", savedModel: "missing-model", using: "fake/zzz-model", network: true},
		// A saved model of an OAuth provider the user is not logged in to: the model exists, its login does not.
		{name: "oauth-provider-without-login", models: fakeProviderModels, settings: `{"defaultProvider":"fake","defaultModel":"zzz-model"}`, savedProvider: "anthropic", savedModel: "claude-sonnet-4-5", using: "fake/zzz-model", network: true},
		// No default model: the first model of the only available provider, in models.json order (not sorted).
		{name: "no-default-model", models: fakeProviderModels, settings: `{}`, savedProvider: "gone", savedModel: "missing-model", using: "fake/zzz-model", network: true},
		// Keys of two built-in providers and no default model: the provider order of defaultModelPerProvider decides, not one provider.
		{name: "two-built-in-keys-groq-mistral", settings: `{}`, keys: []string{"mistral", "groq"}, savedProvider: "gone", savedModel: "missing-model", using: firstByDefaultOrder(t, "mistral", "groq")},
		{name: "two-built-in-keys-xai-mistral", settings: `{}`, keys: []string{"xai", "mistral"}, savedProvider: "gone", savedModel: "missing-model", using: firstByDefaultOrder(t, "xai", "mistral")},
	}
}

// env is the environment of a pig process whose agent directory holds the shape's files and whose environment holds its keys. A shape with no models.json leaves the agent directory without one.
func (s fallbackShape) env(t *testing.T) []string {
	t.Helper()
	env := customModelHomeEnv(t, s.models)
	var agentDir string
	for _, entry := range env {
		if dir, ok := strings.CutPrefix(entry, "PIG_CODING_AGENT_DIR="); ok {
			agentDir = dir
		}
	}
	if s.models == "" {
		if err := os.Remove(filepath.Join(agentDir, "models.json")); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(s.settings), 0o600); err != nil {
		t.Fatal(err)
	}
	if s.auth != "" {
		if err := os.WriteFile(filepath.Join(agentDir, "auth.json"), []byte(s.auth), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, provider := range s.keys {
		env = append(env, builtinProviderKeys[provider]+"=test-key")
	}
	return env
}

func (s fallbackShape) warning() string {
	return "Warning: Could not restore model " + s.savedProvider + "/" + s.savedModel + ". Using " + s.using
}

// fallbackFixture is a project directory, a session directory and a saved session of a shape.
type fallbackFixture struct {
	projectDir, sessionDir, sessionFile, lastUser string
}

func newFallbackFixture(t *testing.T, s fallbackShape) fallbackFixture {
	t.Helper()
	root := t.TempDir()
	f := fallbackFixture{projectDir: filepath.Join(root, "project"), sessionDir: filepath.Join(root, "sessions")}
	for _, dir := range []string{f.projectDir, f.sessionDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	f.sessionFile, f.lastUser = modelFallbackSession(t, f.projectDir, f.sessionDir, s.savedProvider, s.savedModel)
	return f
}

// model-fallback-diagnostic.test.ts: "is written to stderr in RPC mode when switch_session falls back to another model". Found while fixing Pi #11264: "Could not
// restore model" was shown only at interactive startup. A switch_session into a session whose model is gone fell back to another model silently, and the other
// modes never showed it. The RPC client reads the warning on stderr and never on stdout.
func TestRPCSwitchSessionReportsTheModelFallbackOnStderr(t *testing.T) {
	for _, shape := range fallbackShapes(t) {
		t.Run(shape.name, func(t *testing.T) {
			t.Parallel()
			f := newFallbackFixture(t, shape)
			p := startRPCProcessAt(t, f.projectDir, shape.env(t), "--no-session")
			p.sendJSON(map[string]any{"id": "1", "type": "switch_session", "sessionPath": f.sessionFile})
			var stdout strings.Builder
			p.await("the switch_session response", func(record rpcRecord) bool {
				fmt.Fprint(&stdout, record)
				return record["type"] == "response" && record["command"] == "switch_session"
			})
			p.closeAndWait("after switch_session")
			requireFallbackOnStderr(t, shape, p.stderr.String(), stdout.String())
		})
	}
}

// The same message for RPC startup into the session (--continue) and for RPC fork, which replaces the session with a branch of it.
func TestRPCStartAndForkReportTheModelFallbackOnStderr(t *testing.T) {
	for _, shape := range fallbackShapes(t) {
		t.Run(shape.name+"/continue", func(t *testing.T) {
			t.Parallel()
			f := newFallbackFixture(t, shape)
			p := startRPCProcessAt(t, f.projectDir, shape.env(t), "--session-dir", f.sessionDir, "--continue")
			p.sendJSON(map[string]any{"id": "1", "type": "get_state"})
			var stdout strings.Builder
			p.await("the get_state response", func(record rpcRecord) bool {
				fmt.Fprint(&stdout, record)
				return record["type"] == "response" && record["command"] == "get_state"
			})
			p.closeAndWait("after get_state")
			requireFallbackOnStderr(t, shape, p.stderr.String(), stdout.String())
		})
		t.Run(shape.name+"/fork", func(t *testing.T) {
			t.Parallel()
			f := newFallbackFixture(t, shape)
			p := startRPCProcessAt(t, f.projectDir, shape.env(t), "--no-session")
			var stdout strings.Builder
			for _, command := range []map[string]any{
				{"id": "1", "type": "switch_session", "sessionPath": f.sessionFile},
				{"id": "2", "type": "fork", "entryId": f.lastUser},
			} {
				p.sendJSON(command)
				p.await("the "+fmt.Sprint(command["type"])+" response", func(record rpcRecord) bool {
					fmt.Fprint(&stdout, record)
					return record["type"] == "response" && record["id"] == command["id"]
				})
			}
			p.closeAndWait("after fork")
			// The switch already reported the message, and the fork's replacement recomputes the same one: once.
			requireFallbackOnStderr(t, shape, p.stderr.String(), stdout.String())
		})
	}
}

// Print and JSON mode send a prompt, so they run only against the closed loopback port of models.json.
func TestPrintAndJSONReportTheModelFallbackOnStderr(t *testing.T) {
	binary := buildPigBinaryForSignalTest(t)
	for _, shape := range fallbackShapes(t) {
		if !shape.network {
			continue
		}
		for _, mode := range []string{"text", "json"} {
			t.Run(shape.name+"/"+mode, func(t *testing.T) {
				t.Parallel()
				f := newFallbackFixture(t, shape)
				cmd := exec.Command(binary, "--mode", mode, "--session-dir", f.sessionDir, "-c", "-p", "hi")
				cmd.Dir = f.projectDir
				cmd.Env = append(os.Environ(), shape.env(t)...)
				var stdout, stderr bytes.Buffer
				cmd.Stdout, cmd.Stderr = &stdout, &stderr
				cmd.WaitDelay = 10 * time.Second
				_ = cmd.Run() // the prompt fails against the closed port; only the streams matter
				requireFallbackOnStderr(t, shape, stderr.String(), stdout.String())
			})
		}
	}
}

// requireFallbackOnStderr: the warning is on stderr exactly once and never on stdout.
func requireFallbackOnStderr(t *testing.T, shape fallbackShape, stderr, stdout string) {
	t.Helper()
	if n := strings.Count(stderr, shape.warning()); n != 1 {
		t.Fatalf("stderr holds %q %d times, want once:\n%s", shape.warning(), n, stderr)
	}
	if strings.Contains(stdout, "Could not restore model") {
		t.Fatalf("stdout carries the fallback message:\n%s", stdout)
	}
}

// Without any model the message is the login guidance, not a fallback. Pi's `created.session.model` is never undefined (the agent's placeholder model), so its factory adds the
// guidance as a warning in every mode, and its `session.model ? undefined : message` option never carries it (measured on Pi main, not what the commit message says): print and
// JSON show "Warning: No models available." once on stderr before the missing-key error, RPC once at startup and not again after switch_session or fork, and never "Could not restore model".
func TestNoModelsAvailableIsReportedOnceAsAWarning(t *testing.T) {
	const guidance = "Warning: No models available."
	binary := buildPigBinaryForSignalTest(t)
	shape := fallbackShape{name: "no-model", settings: `{}`, savedProvider: "gone", savedModel: "missing-model"}
	requireOnce := func(t *testing.T, stderr, stdout string) {
		t.Helper()
		if n := strings.Count(stderr, guidance); n != 1 || strings.Contains(stderr, "Could not restore model") || strings.Contains(stdout, "No models available") {
			t.Fatalf("stderr holds %q %d times, want once, with no fallback line and a clean stdout:\nstderr:\n%s\nstdout:\n%s", guidance, n, stderr, stdout)
		}
	}
	for _, mode := range []string{"text", "json"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			f := newFallbackFixture(t, shape)
			cmd := exec.Command(binary, "--mode", mode, "--session-dir", f.sessionDir, "-c", "-p", "hi")
			cmd.Dir = f.projectDir
			cmd.Env = append(os.Environ(), shape.env(t)...)
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			cmd.WaitDelay = 10 * time.Second
			if err := cmd.Run(); err == nil {
				t.Fatal("a prompt with no model succeeded")
			}
			requireOnce(t, stderr.String(), stdout.String())
			if !strings.Contains(stderr.String(), "No API key found for the selected model.") || strings.Index(stderr.String(), guidance) > strings.Index(stderr.String(), "No API key found") {
				t.Fatalf("the warning must come before the missing-key error:\n%s", stderr.String())
			}
		})
	}
	t.Run("rpc/start-switch-fork-new", func(t *testing.T) {
		t.Parallel()
		f := newFallbackFixture(t, shape)
		p := startRPCProcessAt(t, f.projectDir, shape.env(t), "--no-session")
		var stdout strings.Builder
		for _, command := range []map[string]any{
			{"id": "1", "type": "switch_session", "sessionPath": f.sessionFile},
			{"id": "2", "type": "fork", "entryId": f.lastUser},
			{"id": "3", "type": "new_session"},
		} {
			p.sendJSON(command)
			p.await("the "+fmt.Sprint(command["type"])+" response", func(record rpcRecord) bool {
				fmt.Fprint(&stdout, record)
				return record["type"] == "response" && record["id"] == command["id"]
			})
		}
		p.closeAndWait("after new_session")
		requireOnce(t, p.stderr.String(), stdout.String())
	})
}

// docsRootRE matches a documentation path line of the guidance; the root differs by distribution (D2/D22), the file names do not.
var docsRootRE = regexp.MustCompile(`(?m)^  [^\r\n]*[/\\]docs[/\\](?:bundle-[0-9a-f]+[/\\])?(providers|models)\.md$`)

// cutDocsRoot replaces the root of the two documentation paths of the guidance and nothing else.
func cutDocsRoot(s string) string { return docsRootRE.ReplaceAllString(s, "  <DOCS>/$1.md") }

// noModelsWarningCut is the whole warning of a start without a model, with its documentation roots cut.
const noModelsWarningCut = noModelsGuidanceFirstLine + "\n  <DOCS>/providers.md\n  <DOCS>/models.md\n"

// noModelsGuidanceFirstLine is the first line of the warning of a start without a model; two documentation paths follow, whose root differs by distribution.
const noModelsGuidanceFirstLine = "Warning: No models available. Use /login to log into a provider via OAuth or API key. See:"

// requireNoModelsWarning checks stderr lines from index at: the guidance and its two documentation paths, and nothing after.
func requireNoModelsWarning(t *testing.T, lines []string, at int, stderr string) {
	t.Helper()
	if len(lines) != at+3 || lines[at] != noModelsGuidanceFirstLine ||
		!strings.HasPrefix(lines[at+1], "  ") || !strings.HasSuffix(lines[at+1], "providers.md") || !strings.HasPrefix(lines[at+2], "  ") || !strings.HasSuffix(lines[at+2], "models.md") {
		t.Fatalf("stderr must hold the no-models warning with its two documentation paths from line %d, and nothing after:\n%s", at+1, stderr)
	}
}

// Every place that lists the runtime's diagnostics lists the no-models warning, in the stream and order Pi does (main.ts:508-513 and 917-918 build the list, 948, 957, 985 and 995
// write it). Rows are probed on Pi with this backport applied, with no model at all: the argument errors print their Error line and then the warning on stderr, exit 1 and write nothing
// on stdout (cli-utils scenario 22); --help and --list-models write the warning on stderr and their own output on stdout and exit 0. With a model available there is no warning.
func TestEveryEntryPointListsTheNoModelsWarningAsPiDoes(t *testing.T) {
	binary := buildPigBinaryForSignalTest(t)
	rows := []struct {
		name       string
		args       []string
		wantError  string // the first stderr line of an argument error
		wantStdout string // a prefix of stdout; empty means stdout stays empty
		exit       int
		// modelShapes says the row also runs with a model available, where the error line stands alone.
		modelShapes bool
	}{
		{name: "provider-without-model", args: []string{"--provider", "anthropic", "--no-session", "-p"}, wantError: "Error: --provider requires --model (for example: --provider anthropic --model <pattern>)", exit: 1, modelShapes: true},
		{name: "unknown-model-name", args: []string{"--model", "zzz", "--no-session", "-p", "hi"}, wantError: `Error: Model "zzz" not found. Use --list-models to see available models.`, exit: 1},
		{name: "unknown-provider-model", args: []string{"--model", "nosuch/zzz", "--no-session", "-p", "hi"}, wantError: `Error: Model "nosuch/zzz" not found. Use --list-models to see available models.`, exit: 1},
		{name: "api-key-without-model", args: []string{"--api-key", "k", "--no-session", "-p", "hi"}, wantError: "Error: --api-key requires a model to be specified via --model, --provider/--model, or --models", exit: 1},
		{name: "help", args: []string{"--help"}, wantStdout: "pig - AI coding assistant", exit: 0},
		{name: "list-models", args: []string{"--list-models"}, wantStdout: "No models available. Use /login", exit: 0},
	}
	for _, row := range rows {
		for _, shape := range append([]fallbackShape{{name: "no-models", settings: `{}`}}, fallbackShapes(t)...) {
			if shape.name != "no-models" && !row.modelShapes {
				continue
			}
			t.Run(row.name+"/"+shape.name, func(t *testing.T) {
				t.Parallel()
				cmd := exec.Command(binary, append([]string{"--offline", "--no-extensions"}, row.args...)...)
				cmd.Dir = t.TempDir()
				cmd.Env = append(os.Environ(), shape.env(t)...)
				var stdout, stderr bytes.Buffer
				cmd.Stdout, cmd.Stderr = &stdout, &stderr
				cmd.WaitDelay = 10 * time.Second
				err := cmd.Run()
				code := 0
				if exit, ok := err.(*exec.ExitError); ok {
					code = exit.ExitCode()
				} else if err != nil {
					t.Fatal(err)
				}
				if code != row.exit {
					t.Fatalf("exit = %d, want %d\nstderr:\n%s", code, row.exit, stderr.String())
				}
				lines := strings.Split(strings.TrimRight(stderr.String(), "\n"), "\n")
				if shape.name != "no-models" {
					// A model is available: the argument error stands alone.
					if len(lines) != 1 || lines[0] != row.wantError {
						t.Fatalf("with a model the error stands alone, stderr:\n%s", stderr.String())
					}
					return
				}
				at := 0
				if row.wantError != "" {
					if lines[0] != row.wantError {
						t.Fatalf("first stderr line = %q, want %q\n%s", lines[0], row.wantError, stderr.String())
					}
					at = 1
				}
				requireNoModelsWarning(t, lines, at, stderr.String())
				if row.wantStdout == "" && stdout.Len() != 0 {
					t.Fatalf("stdout = %q, want empty", stdout.String())
				}
				if row.wantStdout != "" && !strings.HasPrefix(stdout.String(), row.wantStdout) {
					t.Fatalf("stdout starts %q, want prefix %q", stdout.String()[:min(60, stdout.Len())], row.wantStdout)
				}
			})
		}
	}
}

// The warning is one entry of the diagnostics of the construction, after the model errors and last, for the provider shapes that reach the function. Pi pushes it when its
// createAgentSession result carries a modelFallbackMessage (main.ts:917-918), which is set when no model can be chosen (sdk.ts:247, the guidance), and a saved model that cannot be
// restored sets "Could not restore" (sdk.ts:230, covered with exact text by the process tests above). A session with no saved model and a usable model has no message. Expectations
// are Pi's, probed: no models, a provider whose models have no credential, and an expired OAuth login (not usable as a login of this agent directory) all end without a model.
func TestRuntimeDiagnosticsEndWithTheNoModelsWarningForTheShapesPiGivesNoModel(t *testing.T) {
	const noKeyModels = `{"providers":{"fake":{"baseUrl":"http://127.0.0.1:9/v1","api":"openai-completions","models":[{"id":"zzz-model"}]}}}`
	shapes := []struct {
		fallbackShape
		warns bool
	}{
		{fallbackShape{name: "no-models-at-all", settings: `{}`}, true},
		{fallbackShape{name: "models-without-credentials", models: noKeyModels, settings: `{}`}, true},
		{fallbackShape{name: "oauth-expired", settings: `{}`, auth: `{"anthropic":{"type":"oauth","access":"x","refresh":"y","expires":1}}`}, false},
		{fallbackShape{name: "api-key-custom-base-url", models: fakeProviderModels, settings: `{"defaultProvider":"fake","defaultModel":"zzz-model"}`}, false},
		{fallbackShape{name: "no-default-model", models: fakeProviderModels, settings: `{}`}, false},
		{fallbackShape{name: "two-built-in-keys-groq-mistral", settings: `{}`, keys: []string{"mistral", "groq"}}, false},
	}
	for _, shape := range shapes {
		t.Run(shape.name, func(t *testing.T) {
			for _, entry := range shape.env(t) {
				key, value, _ := strings.Cut(entry, "=")
				t.Setenv(key, value)
			}
			agentDir := os.Getenv("PIG_CODING_AGENT_DIR")
			cwd := t.TempDir()
			flags := parseArgs([]string{"--print", "--no-extensions"})
			settings := codingagent.NewSettingsManager(cwd, agentDir)
			builder := &cliRuntimeBuilder{
				mode: processAppMode(flags), flags: flags, agentDir: agentDir, launchCWD: cwd,
				settingsManager: settings, settingsDiagnostics: codingagent.CollectSettingsDiagnostics(settings),
				startupUIOptions: startupUIOptions(flags, false, cwd, agentDir, settings),
				trustStore:       codingagent.NewProjectTrustStore(agentDir), trustByCWD: map[string]bool{},
				stageExtensionSDKs: func() error { return nil },
			}
			ctx := context.Background()
			build, err := builder.buildResources(ctx, cliBuildInput{CWD: cwd})
			if err != nil {
				t.Fatal(err)
			}
			defer build.Services.Close()
			manager, err := coding.NewInMemorySessionManager(cwd)
			if err != nil {
				t.Fatal(err)
			}
			if err := builder.buildSession(ctx, build, cliBuildInput{CWD: cwd, Manager: manager}); err != nil {
				t.Fatal(err)
			}
			warnings := 0
			diagnostics := build.runtimeDiagnostics()
			for i, diagnostic := range diagnostics {
				if diagnostic.Type == "warning" && strings.HasPrefix(diagnostic.Message, "No models available.") {
					warnings++
					if i != len(diagnostics)-1 {
						t.Fatalf("the warning is not the last diagnostic: %v", diagnostics)
					}
				}
			}
			if want := map[bool]int{true: 1, false: 0}[shape.warns]; warnings != want {
				t.Fatalf("no-models warnings = %d, want %d: %v", warnings, want, diagnostics)
			}
		})
	}
}

func TestCutDocsRootAcceptsWindowsAndPOSIXPaths(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "posix bundle path",
			in: "Warning: No models available. Use /login to log into a provider via OAuth or API key. See:\n" +
				"  /tmp/pig-cmd-123/agent/docs/bundle-cba86a845e86d8eb/providers.md\n" +
				"  /tmp/pig-cmd-123/agent/docs/bundle-cba86a845e86d8eb/models.md\n",
			want: noModelsWarningCut,
		},
		{
			name: "windows bundle path",
			in: "Warning: No models available. Use /login to log into a provider via OAuth or API key. See:\n" +
				"  C:\\Users\\RUNNER~1\\AppData\\Local\\Temp\\pig-cmd-123\\pig\\docs\\bundle-cba86a845e86d8eb\\providers.md\n" +
				"  C:\\Users\\RUNNER~1\\AppData\\Local\\Temp\\pig-cmd-123\\pig\\docs\\bundle-cba86a845e86d8eb\\models.md\n",
			want: noModelsWarningCut,
		},
		{
			name: "windows path with spaces and no bundle dir",
			in: "Warning: No models available. Use /login to log into a provider via OAuth or API key. See:\n" +
				"  C:\\Users\\Jane Doe\\.pig\\docs\\providers.md\n" +
				"  C:\\Users\\Jane Doe\\.pig\\docs\\models.md\n",
			want: noModelsWarningCut,
		},
		{
			name: "unrelated lines preserved",
			in: "Error: Failed to load extension \"C:\\tmp\\missing.ts\"\n" +
				"Warning: No models available. Use /login to log into a provider via OAuth or API key. See:\n" +
				"  C:\\tmp\\docs\\bundle-0123456789abcdef\\providers.md\n" +
				"  C:\\tmp\\docs\\bundle-0123456789abcdef\\models.md\n" +
				"Hint: Start without extensions using \"pig -ne\".\n",
			want: "Error: Failed to load extension \"C:\\tmp\\missing.ts\"\n" +
				noModelsWarningCut +
				"Hint: Start without extensions using \"pig -ne\".\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := cutDocsRoot(tc.in); got != tc.want {
				t.Fatalf("cutDocsRoot() =\n%q\nwant:\n%q", got, tc.want)
			}
		})
	}
}
