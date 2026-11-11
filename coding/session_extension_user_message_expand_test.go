package coding

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// sendExpandExtension is a Node extension whose command sends "/greet Alice" as a user message: with the options its argument names
// ("true" or "false" for expandPromptTemplates), or with no options at all for "default".
const sendExpandExtension = `export default function (pi) {
  pi.registerCommand("send-expand", {
    description: "send /greet Alice as a user message",
    handler: async (args) => {
      if (args === "default") pi.sendUserMessage("/greet Alice");
      else pi.sendUserMessage("/greet Alice", { expandPromptTemplates: args === "true" });
    },
  });
}
`

func loadSendExpandExtension(t *testing.T, bridge *subprocess.UIBridge) extension.Extension {
	t.Helper()
	path := filepath.Join(t.TempDir(), "index.mjs")
	if err := os.WriteFile(path, []byte(sendExpandExtension), 0o600); err != nil {
		t.Fatal(err)
	}
	host := subprocess.NewHost(t.TempDir())
	t.Cleanup(func() { host.Shutdown("send-expand test complete") })
	host.SetUIBridge(bridge)
	loaded, err := host.Load(t.Context(), subprocess.ExtConfig{Name: "send-expand", Source: path, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	return *loaded
}

// Pi: packages/coding-agent/src/core/extensions/types.ts:1788-1792 (sendUserMessage options.expandPromptTemplates) and agent-session.ts:2530-2557:
// an extension's pi.sendUserMessage("/greet Alice", {expandPromptTemplates: true}) reaches the model as the prompt template's text, and
// without the option (or with false) as the literal text. The extension is a real Node subprocess, the bridge is the production
// BindSessionExtensionActions binding, and the Session is real, so a host that drops the option on the way fails here.
func TestSubprocessSendUserMessageExpandsPromptTemplatesInTheSession(t *testing.T) {
	for _, tc := range []struct {
		name, arg string
		want      string
		unwanted  string
	}{
		{"expandPromptTemplates true", "true", "expanded:Alice", "/greet Alice"},
		{"expandPromptTemplates false", "false", "/greet Alice", "expanded:Alice"},
		{"no options", "default", "/greet Alice", "expanded:Alice"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("PIG_HOME", t.TempDir())
			bridge := subprocess.NewUIBridge(func() {})
			ext := loadSendExpandExtension(t, bridge)
			sink := []string{}
			h := newBoundaryHarness(t, harnessOptions{extension: ext}, boundaryCapture(t, &sink, "done"))
			h.session.SetPromptResources([]PromptTemplate{{Name: "greet", Content: "expanded:$1"}}, nil)
			BindSessionExtensionActions(nil, bridge, func() *Session { return h.session }, extension.ContextActions{}, nil)

			if err := h.session.Prompt(t.Context(), "/send-expand "+tc.arg); err != nil {
				t.Fatal(err)
			}
			waitForProviderCalls(t, h, 1)
			if err := h.session.FlushEvents(t.Context()); err != nil {
				t.Fatal(err)
			}
			h.provider.mu.Lock()
			requests := append([]string(nil), h.provider.requests...)
			h.provider.mu.Unlock()
			if len(requests) != 1 {
				t.Fatalf("model requests = %d, want 1: %v", len(requests), requests)
			}
			if got := requests[0]; !strings.Contains(got, tc.want) || strings.Contains(got, tc.unwanted) {
				t.Errorf("the model received %s, want %q and not %q", got, tc.want, tc.unwanted)
			}
		})
	}
}

// The graph-bound path (GraphExtensionRunner.bindBridges, an extension runner of another language driving the loaded Node extension) forwards the same option: the host
// hands the runner's sendUserMessage function the options as the extension sent them.
func TestGraphBridgeSendUserMessageForwardsExpandPromptTemplates(t *testing.T) {
	for _, tc := range []struct {
		name, arg, want string
	}{
		{"expandPromptTemplates true", "true", `{"expandPromptTemplates":true}`},
		{"expandPromptTemplates false", "false", `{"expandPromptTemplates":false}`},
		{"no options", "default", `{}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("PIG_HOME", t.TempDir())
			bridge := subprocess.NewUIBridge(func() {})
			ext := loadSendExpandExtension(t, bridge)
			var mu sync.Mutex
			var options []string
			scope := &mcpFakeScope{}
			scope.serve = func(_ string, args []json.RawMessage) (any, error) {
				mu.Lock()
				defer mu.Unlock()
				options = append(options, string(args[1]))
				return nil, nil
			}
			g := &GraphExtensionRunner{scope: scope, runner: inproc.NewRunner(nil, t.TempDir()), bridges: []*subprocess.UIBridge{bridge}}
			g.bindBridges(callbackIDs{"sendUserMessage": "send"}, callbackIDs{})

			command, ok := ext.Commands["send-expand"]
			if !ok {
				t.Fatal("send-expand command missing")
			}
			if err := command.Handler(context.Background(), tc.arg); err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(10 * time.Second)
			for {
				mu.Lock()
				n := len(options)
				mu.Unlock()
				if n > 0 {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("the graph's sendUserMessage function was never called")
				}
				time.Sleep(2 * time.Millisecond)
			}
			mu.Lock()
			defer mu.Unlock()
			if len(options) != 1 || options[0] != tc.want {
				t.Errorf("the graph's sendUserMessage received options %v, want [%s]", options, tc.want)
			}
		})
	}
}
