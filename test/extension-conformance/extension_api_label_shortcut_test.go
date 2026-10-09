package extensionconformance

import (
	"context"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// TestConformance_SetLabelAndShortcut pins two ExtensionAPI members in every SDK. Pi's `pi.setLabel(entryId, label)`
// (extensions/types.ts:1721) reaches the host with its entry id and label, and `pi.registerShortcut(shortcut, options)`
// (extensions/types.ts:1644) is listed by the runner under the key with its description.
func TestConformance_SetLabelAndShortcut(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	t.Parallel()

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
			cmd, ok := findCommand(h.runner, "set_label")
			if !ok {
				t.Fatal("set_label command not registered")
			}
			if err := cmd.Handler(context.Background(), ""); err != nil {
				t.Fatalf("set_label command: %v", err)
			}
			waitFor(t, func() bool { return slices.Contains(*h.actions, "setLabel:label-entry:conformance-label") })

			shortcuts := h.runner.Shortcuts(map[string][]string{})
			shortcut, ok := shortcuts[extension.KeyID("ctrl+alt+y")]
			if !ok || shortcut.Description != "Conformance shortcut" {
				t.Fatalf("registerShortcut ctrl+alt+y = %+v (registered: %v), want the fixture's description", shortcut, shortcuts)
			}
		})
	}
}
