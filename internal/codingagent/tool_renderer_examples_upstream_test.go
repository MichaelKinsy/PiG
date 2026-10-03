package codingagent_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// examplesDir is the vendored copy of .upstream/v0.99.2/packages/coding-agent/examples/extensions
// (tool-renderer-examples.test.ts:13), which vendor-pi-dist.sh keeps byte-identical to the pinned package.
var examplesDir = filepath.Join("..", "..", "coding", "extension", "host", "subprocess", "runtime-node", "shims", "pi-dist", "pi-coding-agent", "examples", "extensions")

// rendererExampleState ports getSessionState (tool-renderer-examples.test.ts:34-62): a session over an empty
// agent directory with no skills, prompt templates or context files, optionally loading one extension, with the
// given tools active. tempDir is the test's working directory (beforeEach, :23-27), shared by the baseline and the
// extension session so that the cwd line of the prompt is equal. It returns the system prompt and the session's "edit" definition.
func rendererExampleState(t *testing.T, tempDir, extensionPath string, tools []string) (string, extension.ToolDefinition, bool) {
	t.Helper()
	agentDir := filepath.Join(tempDir, "agent")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PIG_HOME", tempDir)
	services, err := coding.NewServices(coding.ServicesOptions{CWD: tempDir, AgentDir: agentDir})
	if err != nil {
		t.Fatal(err)
	}
	loader := coding.NewDefaultResourceLoader(coding.DefaultResourceLoaderOptions{CWD: tempDir, AgentDir: agentDir, SettingsManager: services.SettingsManager(), NoSkills: true, NoPromptTemplates: true, NoContextFiles: true})
	if err := loader.Reload(); err != nil {
		t.Fatal(err)
	}
	var runner *inproc.Runner
	if extensionPath != "" {
		host := subprocess.NewHost(t.TempDir())
		t.Cleanup(func() { host.Shutdown("test complete") })
		loaded, err := host.Load(t.Context(), subprocess.ExtConfig{Name: filepath.Base(extensionPath), Source: extensionPath, Enabled: true})
		if err != nil {
			t.Fatalf("extension errors: %v", err)
		}
		runner = inproc.NewRunner([]extension.Extension{*loaded}, tempDir)
	}
	active := map[string]struct{}{}
	for _, name := range tools {
		active[name] = struct{}{}
	}
	session, err := coding.NewSession(services, coding.SessionOptions{
		Model:              services.ModelRuntime().GetModel("anthropic", "claude-sonnet-4-5"),
		ResourceLoader:     loader,
		Runner:             runner,
		ActiveBuiltinTools: active,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Dispose()
	definition, ok := session.GetToolDefinition("edit")
	return session.SystemPrompt(), definition, ok
}

func TestToolRendererExamplesUpstream(t *testing.T) {
	// .upstream/v0.99.2/packages/coding-agent/test/tool-renderer-examples.test.ts:65
	// Regression test for https://github.com/earendil-works/pi/issues/10072
	for _, tc := range []struct {
		name, file string
		tools      []string
	}{
		{"built-in tool renderer", "built-in-tool-renderer.ts", []string{"read", "bash", "edit", "write"}},
		{"minimal mode", "minimal-mode.ts", []string{"read", "bash", "write", "edit", "find", "grep", "ls"}},
	} {
		t.Run("keeps the system prompt unchanged for the "+tc.name+" example", func(t *testing.T) {
			tempDir := t.TempDir()
			baseline, _, _ := rendererExampleState(t, tempDir, "", tc.tools)
			withRenderer, _, _ := rendererExampleState(t, tempDir, filepath.Join(examplesDir, tc.file), tc.tools)
			if withRenderer != baseline {
				t.Fatalf("system prompt changed by %s:\n--- baseline\n%s\n--- with renderer\n%s", tc.file, baseline, withRenderer)
			}
		})
	}
	// .upstream/v0.99.2/packages/coding-agent/test/tool-renderer-examples.test.ts:86
	// Regression test for https://github.com/earendil-works/pi/issues/10072
	t.Run("keeps minimal mode's edit tool in the default shell", func(t *testing.T) {
		_, editToolDefinition, ok := rendererExampleState(t, t.TempDir(), filepath.Join(examplesDir, "minimal-mode.ts"), []string{"edit"})
		if !ok {
			t.Fatal("minimal mode did not register the edit tool")
		}
		defaultShellDefinition := editToolDefinition
		defaultShellDefinition.RenderShell = ""

		got, want := renderEditTool(t, editToolDefinition), renderEditTool(t, defaultShellDefinition)
		if !slices.Equal(got, want) {
			t.Fatalf("edit card = %q, want the default shell %q", got, want)
		}
	})
}

// renderEditTool ports renderEditTool (tool-renderer-examples.test.ts:97-107) at width 40. Pi's renderCall runs synchronously inside ToolExecutionComponent.render, so its card already holds the example's "edit notes.txt" call line. A subprocess renderCall draws through a proxy that answers off the render loop and shows its last completed frame (D56), so the first Render has no call line yet. This waits for the extension's frame and returns the settled card, which is the card Pi compares; comparing the first frames would compare only the empty shells.
func renderEditTool(t *testing.T, definition extension.ToolDefinition) []string {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"path": "notes.txt", "oldText": "before", "newText": "after"})
	if err != nil {
		t.Fatal(err)
	}
	render := icodingagent.ToolCard(t, "edit", "edit-shell-test", raw, definition)
	deadline := time.Now().Add(40 * time.Second)
	// A request the extension does not answer within the renderer inactivity boundary (a cold node start on a loaded runner) is dropped and the proxy asks again only when the width changes. Nudge the width then, as a terminal resize would, so the wait follows the extension instead of one lost request.
	nudge := time.Now().Add(6 * time.Second)
	for {
		lines := render(40)
		if strings.Contains(strings.Join(lines, "\n"), "notes.txt") {
			return lines
		}
		if time.Now().After(deadline) {
			t.Fatalf("the extension's renderCall frame never reached the edit card: %q", lines)
		}
		if time.Now().After(nudge) {
			render(41)
			nudge = time.Now().Add(6 * time.Second)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
