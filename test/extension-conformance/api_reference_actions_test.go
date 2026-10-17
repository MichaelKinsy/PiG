package extensionconformance

import (
	"context"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// hostActionCommands maps each fixture command to the host action every SDK must produce for it. Every SDK fixture registers these
// commands and makes the same call through its own `pi` object (testfixture/fixture.go, main.mjs, main.rs, main.py). Pi declares the
// calls at packages/coding-agent/src/core/extensions/types.ts:1692 (sendMessage), :1715 (setSessionName), :1708 (appendEntry) and
// :1721 (setLabel).
var hostActionCommands = []struct{ command, action string }{
	{"send_message", "sendMessage:notice:hello-custom:steer:true"},
	{"set_session_name", "setSessionName:conformance-session"},
	{"append_entry", "appendEntry:conformance-entry:hello-entry"},
	{"set_label", "setLabel:label-entry:conformance-label"},
}

// TestConformance_HostActionsAcrossSDKs requires every SDK to reach the host with the same actions for the same calls: ExtensionAPI.sendMessage
// (packages/coding-agent/src/core/extensions/types.ts:1692), setSessionName (:1715), appendEntry (:1708) and setLabel (:1721).
func TestConformance_HostActionsAcrossSDKs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	t.Parallel()

	for want, member := range map[string]any{
		"SendMessage":    extension.API.SendMessage,
		"SetSessionName": extension.API.SetSessionName,
		"AppendEntry":    extension.API.AppendEntry,
		"SetLabel":       extension.API.SetLabel,
	} {
		requireAPIMember(t, want, member)
	}
	for _, tc := range sdkHarnessCases() {
		t.Run(tc.name, func(t *testing.T) {
			h := tc.make(t)
			t.Cleanup(func() {
				if h.cleanup != nil {
					h.cleanup()
				}
				if h.host != nil {
					h.host.Shutdown("test done")
				}
			})
			*h.actions = (*h.actions)[:0]
			for _, step := range hostActionCommands {
				cmd, ok := findCommand(h.runner, step.command)
				if !ok {
					t.Fatalf("%s command not registered", step.command)
				}
				if err := cmd.Handler(context.Background(), ""); err != nil {
					t.Fatalf("%s command: %v", step.command, err)
				}
			}
			for _, step := range hostActionCommands {
				waitFor(t, func() bool { return slices.Contains(*h.actions, step.action) })
			}
		})
	}
}
