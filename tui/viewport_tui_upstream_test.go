package tui

import (
	"bytes"
	"slices"
	"testing"
)

// Pi's TUI interface declares the Container and state members of tui.ts:454-476 and both renderers implement it
// (TuiMainScreen "regular", TuiAltScreen "fullscreen"): the members below are driven through the Go TUI interface on each
// renderer, one case per Pi member with its Pi line.
func TestTUIInterfaceMembersBehaveLikePi(t *testing.T) {
	newRenderer := map[string]func() (TUI, TuiMode){
		"main-screen": func() (TUI, TuiMode) {
			screen := NewWithOutput(&bytes.Buffer{}, 30, 6)
			screen.SetRenderDispatcher(func(func()) {})
			return screen, "regular"
		},
		"alt-screen": func() (TUI, TuiMode) {
			return newAltScreenForTest(&bytes.Buffer{}, 30, 6, TuiAltScreenOptions{}), "fullscreen"
		},
	}
	first, second := &stubComponent{lines: []string{"a"}}, &stubComponent{lines: []string{"b", "c"}}

	cases := []struct {
		name string
		run  func(t *testing.T, alt TUI, v TUI)
	}{
		{"addChild appends and children keeps insertion order (tui.ts:351)", func(t *testing.T, _ TUI, v TUI) {
			v.AddChild(first)
			v.AddChild(second)
			if got := v.Children(); !slices.Equal(got, []Component{first, second}) {
				t.Fatalf("children = %v", got)
			}
		}},
		{"removeChild removes the first identical child and ignores an absent one (tui.ts:355)", func(t *testing.T, _ TUI, v TUI) {
			v.AddChild(first)
			v.AddChild(second)
			v.AddChild(first)
			v.RemoveChild(first)
			if got := v.Children(); !slices.Equal(got, []Component{second, first}) {
				t.Fatalf("after removing the first occurrence: %v", got)
			}
			v.RemoveChild(&stubComponent{})
			if got := v.Children(); len(got) != 2 {
				t.Fatalf("removing an absent child changed the children: %v", got)
			}
		}},
		{"clear empties the children (tui.ts:362)", func(t *testing.T, _ TUI, v TUI) {
			v.AddChild(first)
			v.Clear()
			if got := v.Children(); len(got) != 0 {
				t.Fatalf("children after clear = %v", got)
			}
		}},
		{"getShowHardwareCursor and getClearOnShrink report what the setters stored (tui.ts:557-583)", func(t *testing.T, alt TUI, v TUI) {
			for _, enabled := range []bool{true, false, true} {
				alt.SetShowHardwareCursor(enabled)
				alt.SetClearOnShrink(!enabled)
				if v.GetShowHardwareCursor() != enabled || v.GetClearOnShrink() != !enabled {
					t.Fatalf("after setting %v: cursor=%v shrink=%v", enabled, v.GetShowHardwareCursor(), v.GetClearOnShrink())
				}
			}
		}},
		{"hideOverlay pops the topmost overlay and does nothing without one (tui.ts:818)", func(t *testing.T, _ TUI, v TUI) {
			v.HideOverlay()
			if v.HasOverlay() {
				t.Fatal("hideOverlay without overlays created one")
			}
			v.ShowOverlay(first, OverlayOptions{})
			v.ShowOverlay(second, OverlayOptions{})
			v.HideOverlay()
			if !v.HasOverlay() {
				t.Fatal("hideOverlay removed both overlays; it pops only the topmost")
			}
			v.HideOverlay()
			if v.HasOverlay() {
				t.Fatal("the second hideOverlay left an overlay")
			}
		}},
		{"addInputListener runs in order, its remover and removeInputListener unregister it (tui.ts:932-943)", func(t *testing.T, _ TUI, v TUI) {
			var calls []string
			one := TuiInputListener(func(data string) *TuiInputResult { calls = append(calls, "one:"+data); return nil })
			two := TuiInputListener(func(data string) *TuiInputResult { calls = append(calls, "two:"+data); return nil })
			remove := v.AddInputListener(&one)
			v.AddInputListener(&two)
			v.RunInputListeners("x")
			if !slices.Equal(calls, []string{"one:x", "two:x"}) {
				t.Fatalf("calls = %v", calls)
			}
			calls = nil
			remove()
			v.RunInputListeners("y")
			if !slices.Equal(calls, []string{"two:y"}) {
				t.Fatalf("after the remover: %v", calls)
			}
			calls = nil
			v.RemoveInputListener(&two)
			v.RemoveInputListener(&two)
			if _, consumed := v.RunInputListeners("z"); consumed || len(calls) != 0 {
				t.Fatalf("after removeInputListener: consumed=%v calls=%v", consumed, calls)
			}
		}},
		{"handleMouse is Container.handleMouse: an event is dispatched to the child under it with a child-local y (tui.ts:372)", func(t *testing.T, _ TUI, v TUI) {
			var got []TuiMouseEvent
			target := &funcMouseComponent{lines: []string{"b", "c"}, handle: func(e TuiMouseEvent) *TuiMouseEventResult {
				got = append(got, e)
				return &TuiMouseEventResult{Handled: true}
			}}
			v.AddChild(first)
			v.AddChild(target)
			if result := v.HandleMouse(TuiMouseEvent{Y: 0, Width: 30, Height: 3}); result != nil || len(got) != 0 {
				t.Fatalf("an event on the first child reached the second: %v", got)
			}
			if result := v.HandleMouse(TuiMouseEvent{Y: 2, Width: 30, Height: 3}); result == nil || len(got) != 1 || got[0].Y != 1 || got[0].Height != 2 {
				t.Fatalf("an event on the second child: result=%v events=%v, want child-local y 1 and height 2", result, got)
			}
			if v.HandleMouse(TuiMouseEvent{Y: 2, Width: 30, Height: 2}) != nil || v.HandleMouse(TuiMouseEvent{Y: -1, Width: 30, Height: 3}) != nil {
				t.Fatal("an event outside the height was dispatched")
			}
		}},
	}
	for kind, build := range newRenderer {
		for _, tc := range cases {
			t.Run(kind+"/"+tc.name, func(t *testing.T) {
				r, _ := build()
				tc.run(t, r, r)
			})
		}
		t.Run(kind+"/mode is the renderer's literal (tui-main-screen.ts:125, tui-alt-screen.ts:203)", func(t *testing.T) {
			r, want := build()
			if r.Mode() != want {
				t.Fatalf("mode = %q, want %q", r.Mode(), want)
			}
		})
	}
}
