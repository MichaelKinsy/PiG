package extensionconformance

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Pi calls an extension's UI closures only in the TUI. print and JSON mode bind no UI (runner.ts noOpUIContext: setFooter, setHeader, custom,
// setEditorComponent and onTerminalInput ignore what they are given, getEditorComponent answers undefined, setTheme answers "UI not available"),
// and RPC mode's UI (rpc-mode.ts createExtensionUIContext) does the same with "Theme switching not supported in RPC mode". The headless-ui-probe
// command of every SDK fixture hands the SDK a footer, a header and a custom component whose closures append "<sdk>:<kind>" to a file when they
// run, installs an editor, and (Node) subscribes to terminal input and sets a theme object. The row runs every SDK cell in every mode:
//   - tui: the markers MUST be there, so an SDK that never makes the call, or never runs the closure, cannot pass the other modes;
//   - print, json, rpc: no marker, no footer/header/editor/terminal-input/working host call, and nothing but the key-only custom call.
func TestConformance_HeadlessModesNeverRunUIClosures(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	t.Cleanup(func() { rpcRowMode = "" })
	for _, mode := range []string{"tui", "print", "json", "rpc"} {
		rpcRowMode = mode
		for _, tc := range componentKitSDKCells() {
			// Only the TUI draws a view; no other mode has a frontend.
			tc.frontend = tc.frontend && mode == "tui"
			t.Run(mode+"/"+tc.name, func(t *testing.T) {
				runHeadlessUIClosuresCell(t, mode, tc)
			})
		}
	}
}

// headlessUIFamily is the marker prefix of a fixture's SDK.
func headlessUIFamily(extName string) string {
	switch extName {
	case "sdk-fixture":
		return "go"
	case "rust-sdk-fixture":
		return "rust"
	case "python-sdk-fixture":
		return "python"
	default:
		return "node"
	}
}

func runHeadlessUIClosuresCell(t *testing.T, mode string, tc kitCell) {
	h := startKitCell(t, tc)
	family := headlessUIFamily(tc.extName)
	headless := mode != "tui"
	if headless {
		// print and JSON bind no UI; RPC binds one, whose custom() resolves undefined (rpcUIContext.RunRemoteOverlay).
		if mode == "rpc" {
			h.ui.runOverlay = func(extension.RemoteOverlayOptions, extension.RemoteOverlayHost, func(extension.RemoteOverlayHandle)) (any, bool) {
				return nil, false
			}
		} else {
			h.runner.SetUIContext(nil)
			h.bridge.SetUIContext(nil)
		}
	}
	h.wire.on.Store(true)
	path := filepath.Join(t.TempDir(), "markers")
	runKitCommand(t, h, "headless-ui-probe", path)
	if _, ok := findCommand(h.runner, "editor-install-embed"); ok {
		runKitCommand(t, h, "editor-install-embed", "")
	}
	if headless {
		// A later width and a second round trip give a closure installed by mistake the chance to render again; the command is the barrier.
		h.host.NotifyWidth(61)
		runKitCommand(t, h, "headless-ui-probe", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the probe wrote no marker file: %v", err)
	}
	got := strings.Split(strings.TrimSpace(string(data)), "\n")

	if !headless {
		for _, want := range tuiMarkers(family) {
			if !slices.Contains(got, want) {
				t.Errorf("tui: marker %q missing from %q: the SDK did not run its closure where Pi does", want, got)
			}
		}
		if family != "node" {
			waitInstalledEditor(t, h)
		}
		return
	}

	want := headlessMarkers(family, mode)
	want = append(slices.Clone(want), want...)
	if !slices.Equal(got, want) {
		t.Errorf("%s: markers = %q, want %q", mode, got, want)
	}
	if editor := h.ui.installedEditor(); editor != nil {
		t.Errorf("%s: an editor component reached the host", mode)
	}
	h.wire.mu.Lock()
	calls := slices.Clone(h.wire.calls)
	h.wire.mu.Unlock()
	for _, call := range calls {
		switch call.method {
		case "ui.setFooter", "ui.setHeader", "ui.setEditorComponent", "ui.onTerminalInput", "ui.setWorkingMessage", "ui.setWorkingIndicator":
			t.Errorf("%s: extension sent %s %s", mode, call.method, call.args)
		case "ui.custom":
			// Only RPC mode keeps the dialog scope, with the key alone; the component never leaves the SDK.
			var args map[string]json.RawMessage
			if err := json.Unmarshal(call.args, &args); err != nil || mode != "rpc" || len(args) != 1 || args["key"] == nil {
				t.Errorf("%s: extension sent ui.custom %s", mode, call.args)
			}
		}
	}
}

// tuiMarkers are what the probe records in the TUI.
func tuiMarkers(family string) []string {
	if family == "node" {
		return []string{"node:footer", "node:footer-render", "node:header", "node:header-render", "node:custom", "node:custom-render", "node:getEditorComponent:function", "node:onTerminalInput:function"}
	}
	return []string{family + ":footer", family + ":header", family + ":custom"}
}

// headlessMarkers are the only lines one probe run records outside the TUI.
func headlessMarkers(family, mode string) []string {
	if family != "node" {
		return []string{family + ":custom-opaque:undefined"}
	}
	setTheme := `{"success":false,"error":"UI not available"}`
	if mode == "rpc" {
		setTheme = `{"success":false,"error":"Theme switching not supported in RPC mode"}`
	}
	return []string{"node:getEditorComponent:undefined", "node:onTerminalInput:function", fmt.Sprintf("node:setTheme:%s", setTheme)}
}
