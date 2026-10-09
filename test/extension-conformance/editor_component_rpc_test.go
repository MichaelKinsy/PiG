package extensionconformance

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

// Pi's RPC mode answers ctx.ui.setEditorComponent with a no-op (rpc-mode.ts:277): the factory never runs, and no host
// editor ever binds. A factory that calls super would otherwise wait for a bind that cannot come.
func TestEditorComponentFactoryDoesNotRunInRPCMode(t *testing.T) {
	t.Parallel()
	for _, language := range []string{"go", "python", "rust"} {
		t.Run(language, func(t *testing.T) {
			t.Parallel()
			notify, status, actions := &[]string{}, &[]string{}, &[]string{}
			ui := newRecordingUI(notify, status)
			bridge := subprocess.NewUIBridge(func() {})
			bridge.SetUIContext(ui)
			bridge.SetNotifyFunc(ui.RecordNotify)
			bridge.SetActions(conformanceActions(actions))
			host := subprocess.NewHost(t.TempDir())
			host.SetMode("rpc")
			host.SetUIBridge(bridge)
			config := subprocess.ExtConfig{Enabled: true}
			switch language {
			case "go":
				config.Name, config.Path = "sdk-fixture", buildSDKFixture(t)
			case "python":
				config.Name, config.Path, config.RuntimeLanguage = "python-sdk-fixture", buildPythonSDKFixture(t), "python"
			case "rust":
				config.Name, config.Path = "rust-sdk-fixture", buildRustSDKFixture(t)
			}
			ext, err := host.Load(context.Background(), config)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { host.Shutdown("test done") })
			runner := inproc.NewRunner([]extension.Extension{*ext}, t.TempDir())
			bridge.SetUIPromptScope(runner)
			done := make(chan bool, 1)
			go func() { done <- runner.ExecuteCommand(context.Background(), "editor-install-eager", "") }()
			select {
			case ok := <-done:
				if !ok {
					t.Fatal("the command failed")
				}
			case <-time.After(20 * time.Second):
				t.Fatal("setEditorComponent in RPC mode is still waiting for a host editor that never binds")
			}
			if editor := ui.installedEditor(); editor != nil {
				t.Fatal("an editor component reached the host in RPC mode")
			}
			if slices.ContainsFunc(ui.Recorded(), func(entry string) bool { return strings.HasPrefix(entry, "eager") }) {
				t.Fatalf("the factory ran in RPC mode: %q", ui.Recorded())
			}
		})
	}
}
