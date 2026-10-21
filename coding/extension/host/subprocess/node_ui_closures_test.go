//go:build !pig_strip_node_extensions

package subprocess

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// Pi runs an extension's footer, header and editor factories and its terminal-input handler only in the TUI: rpc-mode.ts createExtensionUIContext
// makes setFooter, setHeader, setEditorComponent, onTerminalInput and the working-indicator calls no-ops, getEditorComponent answers undefined and
// setTheme answers {success:false,error:"Theme switching not supported in RPC mode"} for a name or a Theme object, never throwing.
func TestHost_Integration_NodeUIClosuresRunOnlyInTheTUI(t *testing.T) {
	skipWithoutNodeExtensions(t)
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	for _, mode := range []string{"rpc", "tui"} {
		t.Run(mode, func(t *testing.T) {
			shortSockDir(t)
			h := newTestHost(t)
			h.SetMode(mode)
			bridge := NewUIBridge(func() {})
			bridge.SetUIContext(newTestUIContext())
			h.SetUIBridge(bridge)
			var mu sync.Mutex
			var calls []string
			h.SetInboundObserver(func(_ string, env *Envelope) {
				if env.Call == nil {
					return
				}
				mu.Lock()
				calls = append(calls, env.Call.Method)
				mu.Unlock()
			})
			defer h.Shutdown("test done")
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			ext, err := h.Load(ctx, ExtConfig{Name: "ui-closures", Source: filepath.Join("testdata", "ui-closures.mjs"), Enabled: true})
			if err != nil {
				t.Fatal(err)
			}
			h.NotifyWidth(80)
			path := filepath.Join(t.TempDir(), "markers")
			if err := ext.Commands["ui_closures"].Handler(context.Background(), path); err != nil {
				t.Fatal(err)
			}
			// A second round trip after a resize: a closure kept by mistake would render again before it returns.
			h.NotifyWidth(61)
			if err := ext.Commands["ui_closures"].Handler(context.Background(), path); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			got := strings.Split(strings.TrimSpace(string(data)), "\n")
			mu.Lock()
			defer mu.Unlock()
			if mode == "tui" {
				for _, want := range []string{"footer", "footer-render", "header", "header-render", "getEditorComponent:function", "onTerminalInput:function"} {
					if !slices.Contains(got, want) {
						t.Errorf("tui: %q missing from %q: the runtime did not run the closure where Pi does", want, got)
					}
				}
				return
			}
			want := []string{"getEditorComponent:undefined", "onTerminalInput:function", `setTheme:{"success":false,"error":"Theme switching not supported in RPC mode"}`}
			want = append(want, want...)
			if !slices.Equal(got, want) {
				t.Errorf("rpc: markers = %q, want %q", got, want)
			}
			for _, method := range calls {
				switch method {
				case "ui.setFooter", "ui.setHeader", "ui.setEditorComponent", "ui.onTerminalInput", "ui.offTerminalInput", "ui.setTheme",
					"ui.setWorkingMessage", "ui.setWorkingIndicator", "ui.setWorkingVisible", "ui.setHiddenThinkingLabel":
					t.Errorf("rpc: the runtime made the host call %s, which Pi's RPC UI ignores", method)
				}
			}
		})
	}
}
