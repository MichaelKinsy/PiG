package subprocess

import (
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// The runtimes also write their replicas from host replies outside a state_update: a session-log page (entries and leaf), the palette the Node runtime fetches with ui.theme after ui.setTheme, and the reply of an MCP server registration (the Rust and Python SDKs' server list). Without a full push, a theme another extension or the user switches back to the last pushed one would leave the setting extension on its own palette. The host's record of what that runtime holds is then out of date, so the next push is a full snapshot rather than a skip of a payload that equals the last one sent.
func TestPushStateToResendsAfterAReplyTheRuntimeAppliesToItsState(t *testing.T) {
	for _, tc := range []struct {
		name  string
		reply func(t *testing.T, rig *pushRig)
	}{
		{"watchSessionLog", func(t *testing.T, rig *pushRig) {
			rig.bridge.SetHostAction("getEntriesPage", func(cursor, _ int) ([]json.RawMessage, int, bool, string) {
				return nil, cursor, false, "elsewhere"
			})
			rig.h.watchSessionLog("push", 0, true)
		}},
		{"registerMcpServer", func(t *testing.T, rig *pushRig) {
			args := json.RawMessage(`{"name":"srv","config":{"command":"true"}}`)
			if _, err := rig.h.handleMcpServerCall(t.Context(), rig.me, &CallPayload{Method: CallRegisterMcpServer, Args: args}); err != nil {
				t.Fatal(err)
			}
		}},
		{"ui.setTheme", func(t *testing.T, rig *pushRig) {
			rig.bridge.SetUIContext(extension.NoopUIContext)
			if _, err := rig.bridge.handleCall(t.Context(), "push", rig.conn, &CallPayload{Method: "ui.setTheme", Args: json.RawMessage(`{"theme":"light"}`)}); err != nil {
				t.Fatal(err)
			}
		}},
		{"ui.theme", func(t *testing.T, rig *pushRig) {
			rig.bridge.SetUIContext(extension.NoopUIContext)
			if _, err := rig.bridge.handleCall(t.Context(), "push", rig.conn, &CallPayload{Method: "ui.theme"}); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rig := newPushRig(t)
			rig.push(t)
			rig.nextState(t)
			rig.push(t)
			rig.expectNoFrame(t, "an identical push before the reply")
			tc.reply(t, rig)
			rig.push(t)
			state := rig.settledState(t)
			if len(state["commands"]) == 0 {
				t.Fatalf("the push after %s must be a full snapshot, got keys %v", tc.name, keysOf(state))
			}
			rig.push(t)
			rig.expectNoFrame(t, "an identical push after the full snapshot")
		})
	}
}
