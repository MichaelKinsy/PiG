package extensionconformance

import (
	"context"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

type stateBarrierUI struct {
	*recordingUI
	expanded atomic.Bool
}

func (u *stateBarrierUI) Select(context.Context, string, []string, extension.ExtensionUIDialogOptions) (string, error) {
	u.expanded.Store(true)
	return "chosen", nil
}
func (u *stateBarrierUI) GetToolsExpanded() bool { return u.expanded.Load() }
func (u *stateBarrierUI) GetTheme(name string) (extension.Theme, error) {
	if name != "light" {
		return nil, nil
	}
	return map[string]any{"name": "light", "foregrounds": map[string]string{"accent": "\x1b[38;2;1;2;3m"}, "backgrounds": map[string]string{}, "mode": "truecolor"}, nil
}
func (u *stateBarrierUI) SetTheme(name any) extension.SetThemeResult {
	return extension.SetThemeResult{Success: false, Error: "Theme not found: " + name.(string)}
}

// Pi interactive-mode.ts:2572-2585,2623: getters and the synchronous theme result reflect actual host state, including a dialog's expansion change.
func TestUIStateBarriersAcrossSDKs(t *testing.T) {
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
			ui := &stateBarrierUI{recordingUI: h.ui}
			h.runner.SetUIContext(ui)
			if h.bridge != nil {
				h.bridge.SetUIContext(ui)
			}
			command, ok := findCommand(h.runner, "ui-state-barrier")
			if !ok {
				t.Fatal("ui-state-barrier not registered")
			}
			cc := h.runner.CreateCommandContext()
			ctx := extension.WithCommandContext(extension.WithContext(t.Context(), cc.Context), cc)
			h.ui.ClearRecorded()
			if err := command.Handler(ctx, ""); err != nil {
				t.Fatal(err)
			}
			want := []string{"selected=chosen expanded=true named=light missing=true success=false error=Theme not found: missing:info"}
			// UI prompt handlers are intentionally unawaited in Pi and have their own ordering conformance. This row owns the complete state report, not their scheduling.
			got := slices.DeleteFunc(h.ui.Recorded(), func(line string) bool { return strings.HasPrefix(line, "ui_prompt:") })
			if !slices.Equal(got, want) {
				t.Fatalf("state = %v, want %v", got, want)
			}
		})
	}
}
