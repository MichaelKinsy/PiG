package extensionconformance

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

// Pi's setModel keeps the object it is given (agent-session.ts:2644-2652: agent.state.model = model). An extension that edits a registry model
// (Pi's test 9631 sets inputLimits on one) or builds one by hand sees that object as ctx.model, in model_select and in requests. PiG's Node runtime
// sends `${model.provider}/${model.id}` (runtime-node/runtime.mjs:2614) and the host rebuilds the model from its registry, so every other member is lost.
// The extension runs alone in its Node process (isolated) and beside a peer in one shared Node realm (packed); both must keep the object.
func TestSetModelKeepsTheObjectNode(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	if _, err := exec.LookPath("node"); err != nil {
		t.Fatalf("node is required: %v", err)
	}
	t.Setenv("ANTHROPIC_API_KEY", "conformance-key")
	path := filepath.Join(findModuleRoot(t), "test", "extension-conformance", "testdata", "set-model-object", "main.mjs")
	for _, topology := range []string{"isolated", "packed"} {
		for _, tc := range []struct {
			name, object string
			wantWindow   int
			wantBase     string
		}{
			{"registry-model-edited", `{"provider":"anthropic","id":"claude-haiku-4-5","contextWindow":4321}`, 4321, ""},
			{"hand-built-on-a-known-provider", `{"provider":"anthropic","id":"claude-handmade-9","api":"anthropic-messages","baseUrl":"https://gateway.invalid/v1","contextWindow":4321,"maxTokens":99,"reasoning":false,"input":["text"],"cost":{"input":1,"output":2,"cacheRead":0,"cacheWrite":0}}`, 4321, "https://gateway.invalid/v1"},
		} {
			t.Run(topology+"/"+tc.name, func(t *testing.T) {
				notify, status, actions := &[]string{}, &[]string{}, &[]string{}
				ui := newRecordingUI(notify, status)
				bridge := subprocess.NewUIBridge(hostEffects.notify)
				bridge.SetUIContext(ui)
				bridge.SetNotifyFunc(ui.RecordNotify)
				bridge.SetActions(conformanceActions(actions))
				h := subprocess.NewHost(harnessHostCwd(t))
				h.SetMode(harnessHostMode())
				h.SetHostInfo(conformanceHostInfo)
				h.ShareCells(conformanceCellRegistry)
				h.SetUIBridge(bridge)
				var exts []extension.Extension
				if topology == "isolated" {
					ext, err := h.Load(context.Background(), subprocess.ExtConfig{Name: "set-model-object", Source: path, Enabled: true})
					if err != nil {
						t.Fatal(err)
					}
					exts = []extension.Extension{*ext}
				} else {
					peer := filepath.Join(t.TempDir(), "node-cell-peer.mjs")
					if err := os.WriteFile(peer, []byte("export default function () {}\n"), 0o600); err != nil {
						t.Fatal(err)
					}
					var errs []error
					exts, errs = h.LoadAll(context.Background(), []subprocess.ExtConfig{
						{Name: "set-model-object", Source: path, Enabled: true},
						{Name: "node-cell-peer", Source: peer, Enabled: true},
					})
					if len(errs) != 0 || len(exts) != 2 {
						t.Fatalf("packed Node LoadAll: %d loaded, %v", len(exts), errs)
					}
				}
				t.Cleanup(func() { h.Shutdown("test done") })
				runner := inproc.NewRunner(exts, t.TempDir())
				bridge.SetUIPromptScope(runner)
				services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: t.TempDir(), AgentDir: t.TempDir()})
				if err != nil {
					t.Fatal(err)
				}
				session, err := coding.NewSession(services, coding.SessionOptions{NoSession: true, SkipBuiltinTools: true, Model: ai.GetBuiltinModel("anthropic", "claude-sonnet-4-5")})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = session.Close() })
				coding.BindSessionExtensionActions(nil, bridge, func() *coding.Session { return session }, extension.ContextActions{}, nil)
				command, ok := findCommand(runner, "set_model_object")
				if !ok {
					t.Fatal("command missing")
				}
				if !json.Valid([]byte(tc.object)) {
					t.Fatal("bad object")
				}
				ui.ClearRecorded()
				if err := command.Handler(context.Background(), tc.object); err != nil {
					t.Fatalf("setModel: %v", err)
				}
				waitFor(t, func() bool {
					return slices.ContainsFunc(ui.Recorded(), func(s string) bool { return strings.HasPrefix(s, "setModelObject=") })
				})
				got := session.Model()
				if gotWindow := got.Capabilities.ContextWindow; gotWindow != tc.wantWindow {
					t.Errorf("session model contextWindow = %d, Pi keeps the object's %d", gotWindow, tc.wantWindow)
				}
				if got.ProviderMeta.BaseURL != tc.wantBase {
					t.Errorf("session model baseUrl = %q, Pi keeps the object's %q", got.ProviderMeta.BaseURL, tc.wantBase)
				}
				t.Logf("notifications: %q", ui.Recorded())
			})
		}
	}
}
