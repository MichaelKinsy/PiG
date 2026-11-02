package extensionconformance

import (
	"encoding/json"
	"slices"
	"testing"
	"time"
)

// Pi's getSelectPageIndex (packages/tui/src/keybindings.ts): tui.select.pageUp and tui.select.pageDown move a selection by one page, clamped to
// 0..itemCount-1 without wrapping; another key answers undefined, and a page key on an empty list answers 0. Every SDK's helper must answer
// Pi's values with the host's default keybindings.
func TestSelectPageIndexAcrossSDKs(t *testing.T) {
	const pageUp, pageDown = "\x1b[5~", "\x1b[6~"
	rows := []struct {
		name                  string
		data                  string
		selected, count, page int
		want                  string
	}{
		{"page-down", pageDown, 2, 20, 3, "select-page=5"},
		{"page-up-clamps-at-the-first-item", pageUp, 2, 20, 3, "select-page=0"},
		{"page-down-clamps-at-the-last-item", pageDown, 18, 20, 5, "select-page=19"},
		{"page-up", pageUp, 10, 20, 4, "select-page=6"},
		{"empty-list", pageDown, 0, 0, 5, "select-page=0"},
		{"not-a-page-key", "x", 2, 20, 3, "select-page=none"},
	}
	cases := sdkHarnessCases()
	for _, language := range []string{"go", "rust", "python", "lisp"} {
		cases = append(cases, harnessCase{name: "packed-" + language, make: func(t *testing.T) *harness { return makePackedUIHarness(t, language) }})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := tc.make(t)
			cleanupHarness(t, h)
			command, ok := findCommand(h.runner, "select-page-probe")
			if !ok {
				t.Fatal("select-page-probe command missing")
			}
			for _, row := range rows {
				*h.notify = nil
				args, err := json.Marshal(map[string]any{"data": row.data, "selected": row.selected, "count": row.count, "page": row.page})
				if err != nil {
					t.Fatal(err)
				}
				done := make(chan error, 1)
				go func() { done <- command.Handler(h.runner.DispatchContext(t.Context()), string(args)) }()
				select {
				case err := <-done:
					if err != nil {
						t.Fatalf("%s: %v", row.name, err)
					}
				case <-time.After(30 * time.Second):
					t.Fatalf("%s: the probe did not return", row.name)
				}
				if want := []string{row.want + ":info"}; !slices.Equal(*h.notify, want) {
					t.Errorf("%s: outcome = %q, want %q", row.name, *h.notify, want)
				}
			}
		})
	}
}
