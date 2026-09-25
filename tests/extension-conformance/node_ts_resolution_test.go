package extensionconformance

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

// TestNodeTypeScriptSourceResolutionAndShims pins two node-runtime contracts an
// upstream TypeScript extension depends on:
//
//   - a relative import written with the emitted ".js" extension resolves to the
//     ".ts" source, which is what TypeScript's NodeNext resolution requires and
//     what upstream pi provides through jiti; and
//   - the pi-tui / pi-coding-agent shim exports those extensions import
//     (HStack, SettingsList, CONFIG_DIR_NAME, getAgentDir, estimateTokens,
//     getSettingsListTheme) exist and are callable.
//
// Without the resolution rule the extension fails to load at all, so the
// command below never registers.
func TestNodeTypeScriptSourceResolutionAndShims(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping node TypeScript resolution bridge test in short mode")
	}
	shortSockDir(t)

	fixture := filepath.Join(findModuleRoot(t), "tests", "extension-conformance", "testdata", "node-ts-resolution-fixture", "main.ts")

	notify := &[]string{}
	status := &[]string{}
	ui := newRecordingUI(notify, status)
	bridge := subprocess.NewUIBridge(func() {})
	bridge.SetUIContext(ui)
	// Share the recorder's lock: the host calls this from the socket goroutine
	// while the test body reads what was recorded.
	bridge.SetNotifyFunc(ui.RecordNotify)

	host := subprocess.NewHost(t.TempDir())
	host.SetUIBridge(bridge)
	t.Cleanup(func() { host.Shutdown("test done") })

	built, err := subprocess.NewBuilder(t.TempDir()).Build("node-ts-resolution-fixture", fixture)
	if err != nil {
		t.Fatalf("build node launcher: %v", err)
	}
	loaded, err := host.Load(context.Background(), subprocess.ExtConfig{
		Name:    "node-ts-resolution-fixture",
		Path:    built.BinaryPath,
		Enabled: true,
	})
	if err != nil {
		t.Fatalf("load node fixture: %v", err)
	}

	runner := inproc.NewRunner([]extension.Extension{*loaded}, t.TempDir())
	runner.SetUIContext(ui)

	cmd, ok := findCommand(runner, "ts-resolution")
	if !ok {
		t.Fatal("ts-resolution command not registered: the fixture failed to load")
	}
	if err := cmd.Handler(context.Background(), ""); err != nil {
		t.Fatalf("ts-resolution command: %v", err)
	}
	waitFor(t, func() bool { return len(ui.Recorded()) > 0 })

	got := ui.Recorded()[0]
	for _, want := range []string{
		// The ".js" specifier reached helper.ts.
		"resolved-ts-source",
		// The untyped type-only import was elided instead of failing to link.
		// Reaching the handler at all proves it; the value proves the module ran.
		"requireWorks=true",
		// getAgentDir/CONFIG_DIR_NAME report pig's config root, never ".pi".
		"configDir=.pig",
		"agentDirUnderConfigRoot=true",
		// HStack allocated the full 30 columns across a fixed and a growing child.
		"stackWidth=30",
		"settingsLines=true",
		// estimateTokens keeps upstream's ceil(chars/4) heuristic: 20 chars -> 5.
		"tokens=5",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("notify %q missing %q", got, want)
		}
	}
}
