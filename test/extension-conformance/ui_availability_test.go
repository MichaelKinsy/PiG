package extensionconformance

import (
	"fmt"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Pi 0.87.1 runner.ts:320-351,578-580: availability follows UI identity,
// not whether actions are bound. No-op dialogs return immediately without callbacks.
func TestUIAvailabilityAcrossSDKs(t *testing.T) {
	t.Parallel()
	cases := allHarnessCases()
	for _, language := range []string{"go", "python", "rust"} {
		cases = append(cases, harnessCase{name: "packed-" + language, make: func(t *testing.T) *harness { return makePackedUIHarness(t, language) }})
	}
	for _, tc := range cases {
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
			for _, hasUI := range []bool{true, false, true, false} {
				var ui extension.UIContext = h.ui
				if !hasUI {
					ui = nil
				}
				h.runner.SetUIContext(ui)
				if h.bridge != nil {
					h.bridge.SetUIContext(ui)
				}
				h.ui.ClearRecorded()
				*h.actions = nil
				command, ok := findCommand(h.runner, "ui-availability")
				if !ok {
					t.Fatal("ui-availability not registered")
				}
				cc := h.runner.CreateCommandContext()
				ctx := extension.WithCommandContext(extension.WithContext(t.Context(), cc.Context), cc)
				if err := command.Handler(ctx, ""); err != nil {
					t.Fatal(err)
				}
				selected := ""
				if hasUI {
					selected = "second"
				}
				want := fmt.Sprintf("appendEntry:ui-availability:hasUI=%t selected=%s", hasUI, selected)
				if !slices.Equal(*h.actions, []string{want}) {
					t.Fatalf("actions = %v, want %s", *h.actions, want)
				}
				var notifications []string
				for _, msg := range h.ui.Recorded() {
					if msg == "availability-notify:info" {
						notifications = append(notifications, msg)
					}
				}
				wantNotify := []string{}
				if hasUI {
					wantNotify = append(wantNotify, "availability-notify:info")
				}
				if !slices.Equal(notifications, wantNotify) {
					t.Fatalf("notifications = %v, want %v", notifications, wantNotify)
				}
			}
		})
	}
}
