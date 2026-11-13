package extensionconformance

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/coding/uiview"
)

// viewReloadCase is one realization of the conformance fixture that a host can reload: the configs its loader returns.
type viewReloadCase struct {
	name    string
	extName string
	configs func(t *testing.T) []subprocess.ExtConfig
}

func viewReloadCases() []viewReloadCase {
	nodeFixture := func(t *testing.T) string {
		requireConformanceTool(t, "node")
		return filepath.Join(findModuleRoot(t), "test", "extension-conformance", "testdata", "node-sdk-fixture", "main.mjs")
	}
	packed := func(language string) func(t *testing.T) []subprocess.ExtConfig {
		return func(t *testing.T) []subprocess.ExtConfig {
			configs, err := packedUIConfigs[language]()
			if err != nil {
				t.Fatal(err)
			}
			return slices.Clone(configs)
		}
	}
	return []viewReloadCase{
		{name: "node-packed", extName: "node-sdk-fixture", configs: func(t *testing.T) []subprocess.ExtConfig {
			peer := filepath.Join(t.TempDir(), "node-cell-peer.mjs")
			if err := os.WriteFile(peer, []byte("export default function () {}\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			return []subprocess.ExtConfig{{Name: "node-sdk-fixture", Source: nodeFixture(t), Enabled: true}, {Name: "node-cell-peer", Source: peer, Enabled: true}}
		}},
		{name: "node-isolated", extName: "node-sdk-fixture", configs: func(t *testing.T) []subprocess.ExtConfig {
			return []subprocess.ExtConfig{{Name: "node-sdk-fixture", Source: nodeFixture(t), Enabled: true, Isolation: "strict"}}
		}},
		{name: "go-isolated", extName: "sdk-fixture", configs: func(t *testing.T) []subprocess.ExtConfig {
			return []subprocess.ExtConfig{{Name: "sdk-fixture", Path: buildSDKFixture(t), Enabled: true}}
		}},
		{name: "go-packed", extName: "sdk-fixture", configs: packed("go")},
		{name: "python-isolated", extName: "python-sdk-fixture", configs: func(t *testing.T) []subprocess.ExtConfig {
			return []subprocess.ExtConfig{{Name: "python-sdk-fixture", Path: buildPythonSDKFixture(t), Enabled: true, RuntimeLanguage: "python"}}
		}},
		{name: "python-packed", extName: "python-sdk-fixture", configs: packed("python")},
		{name: "rust-isolated", extName: "rust-sdk-fixture", configs: func(t *testing.T) []subprocess.ExtConfig {
			return []subprocess.ExtConfig{{Name: "rust-sdk-fixture", Path: buildRustSDKFixture(t), Enabled: true}}
		}},
		{name: "rust-packed", extName: "rust-sdk-fixture", configs: packed("rust")},
		{name: "lisp-packed", extName: lispFixtureName, configs: packed("lisp")},
	}
}

// TestConformance_UIViewMountsEndAtAReload pins, in every SDK (Lisp packed) and in the isolated and packed realizations, the reload
// rule of plugin UI mounts (D614): a reload replaces the extension's instance and a mount belongs to the instance that
// made it. The client is delivered the mount once more, broken with reason owner_gone; an intent raised on it
// afterwards is refused and reaches no handler; and the instance the reload loaded mounts the same key again under
// a new epoch, where before the rule its mount failed with "already mounted".
func TestConformance_UIViewMountsEndAtAReload(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	for _, tc := range viewReloadCases() {
		t.Run(tc.name, func(t *testing.T) {
			configs := tc.configs(t)
			notify, status, actions := &[]string{}, &[]string{}, &[]string{}
			ui := newRecordingUI(notify, status)
			bridge := subprocess.NewUIBridge(func() {})
			bridge.SetUIContext(ui)
			bridge.SetActions(conformanceActions(actions))
			host := subprocess.NewHost(t.TempDir())
			host.SetHostInfo(conformanceHostInfo)
			host.SetUIBridge(bridge)
			host.SetConfigLoader(func() ([]subprocess.ExtConfig, error) { return slices.Clone(configs), nil })
			t.Cleanup(func() { host.Shutdown("test done") })
			ctx := context.Background()
			// load reloads the host and returns the redraw command of the instance it loaded.
			load := func() extension.RegisteredCommand {
				t.Helper()
				loaded, err := host.Reload(ctx)
				if err != nil || len(loaded) != len(configs) {
					t.Fatalf("load: %d extensions, %v (%+v)", len(loaded), err, host.LastReloadReport())
				}
				runner := inproc.NewRunner(loaded, t.TempDir())
				bridge.SetUIPromptScope(runner)
				command, ok := findCommand(runner, "ui_view_redraw")
				if !ok {
					t.Fatal("ui_view_redraw is not registered")
				}
				return command
			}
			client := attachViewClient(t, bridge.Views(), tc.extName, *conformanceClientAbilities())
			// mounted runs the redraw command and returns the live mount it is delivered after the first n deliveries.
			mounted := func(command extension.RegisteredCommand, n int) uiview.Snapshot {
				t.Helper()
				if err := command.Handler(ctx, ""); err != nil {
					t.Fatalf("ui_view_redraw: %v", err)
				}
				var live uiview.Snapshot
				pollUntilConformance(t, "the redraw panel was not delivered live", func() bool {
					for _, d := range client.deliveries()[n:] {
						if d.Snapshot.Mount.Key == "redraw" && d.Snapshot.State == uiview.StateLive {
							live = d.Snapshot
							return true
						}
					}
					return false
				})
				return live
			}
			first := mounted(load(), 0)
			before := len(client.deliveries())

			again := load()
			after := client.deliveries()[before:]
			if len(after) != 1 || after[0].Snapshot.Mount != first.Mount || after[0].Snapshot.State != uiview.StateBroken || after[0].Snapshot.Reason != uiview.ReasonOwnerGone {
				t.Fatalf("the reload delivered %+v, want the mount %+v once, broken with reason owner_gone", after, first.Mount)
			}
			if res := bridge.InvokeViewIntent(ctx, tc.extName, first.Mount, "", "fx.redraw", json.RawMessage(`{"n":20}`)); res.Outcome != uiview.OutcomeRefused || res.Error != "mount is broken" {
				t.Fatalf("an intent on the mount of the replaced instance = %+v, want refused: mount is broken", res)
			}
			if got := client.deliveries()[before:]; len(got) != 1 {
				t.Fatalf("the intent on the mount of the replaced instance delivered %+v", got[1:])
			}

			second := mounted(again, before+1)
			if second.Mount.Epoch <= first.Mount.Epoch {
				t.Fatalf("the reloaded instance's mount is %+v, want an epoch above %d", second.Mount, first.Mount.Epoch)
			}
			if res := bridge.InvokeViewIntent(ctx, tc.extName, second.Mount, "", "fx.redraw", json.RawMessage(`{"n":20}`)); res.Outcome != uiview.OutcomeDone {
				t.Fatalf("an intent on the reloaded instance's mount = %+v, want done", res)
			}
		})
	}
}
