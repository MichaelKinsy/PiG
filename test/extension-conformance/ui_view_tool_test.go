package extensionconformance

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/uiview"
)

// viewToolLine is one snapshot a client was sent, as the members the row pins: the state and revision, the title, the
// first focus entry with its request, and the intent a hidden mount takes.
func viewToolLine(s *uiview.Snapshot) string {
	focus := "none"
	if len(s.Focus) == 1 {
		focus = s.Focus[0].Node + ":" + s.Focus[0].Request
	}
	var open []string
	for _, d := range s.Intents {
		if d.WhenHidden {
			open = append(open, d.Name)
		}
	}
	return fmt.Sprintf("%s rev=%d title=%q focus=%s whenHidden=%s of %d", s.State, s.Mount.Rev, s.Title, focus, strings.Join(open, "+"), len(s.Intents))
}

// TestConformance_UIViewToolShowsAndFocuses pins, in every SDK and realization, that an agent can drive a view: a tool
// the extension registered, called through the tool-execution path, mounts a panel with a title, hides it, shows it
// and updates it with a new title and a focus request on the update; and that the user can open the view again once it
// is hidden, through the one intent a hidden mount takes, whose handler shows it.
//
// The title and the focused node are the test's own, different in every case, so an SDK that drops `title`, rewrites
// or drops `request: update`, drops `whenHidden`, or answers from a constant cannot produce these lines.
func TestConformance_UIViewToolShowsAndFocuses(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	for _, tc := range sdkHarnessCases() {
		t.Run(tc.name, func(t *testing.T) {
			h := tc.make(t)
			views := h.bridge.Views()
			var mu sync.Mutex
			var got []string
			var last uiview.Snapshot
			att, _, aerr := views.Attach(*conformanceClientAbilities(), func(d uiview.Delivery) bool {
				mu.Lock()
				defer mu.Unlock()
				if d.Snapshot.Plugin == tc.extName && d.Snapshot.Mount.Key == "shown" {
					got = append(got, viewToolLine(&d.Snapshot))
					last = d.Snapshot
				}
				return true
			})
			if aerr != nil {
				t.Fatal(aerr)
			}
			t.Cleanup(func() {
				att.Detach()
				if h.cleanup != nil {
					h.cleanup()
				}
				if h.host != nil {
					h.host.Shutdown("test done")
				}
			})
			sent := func() ([]string, uiview.Snapshot) {
				mu.Lock()
				defer mu.Unlock()
				return slices.Clone(got), last
			}
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()

			runConformanceCommand(t, h, "ui_view_tool")
			var tool extension.ToolDefinition
			waitFor(t, func() bool {
				definition, ok := h.runner.GetToolDefinition("view_show")
				tool = definition
				return ok
			})
			call := func(id string, params map[string]string) string {
				t.Helper()
				raw, err := json.Marshal(params)
				if err != nil {
					t.Fatal(err)
				}
				result, err := tool.Execute(ctx, id, raw, nil)
				if err != nil {
					t.Fatalf("view_show %s: %v", raw, err)
				}
				return result.Text()
			}
			title, node := "Trough of "+tc.name, "inbox-"+tc.name
			if text := call("tc-view-show", map[string]string{"title": title, "node": node}); text != "view-tool:live,rev=2" {
				t.Fatalf("the tool answered %q, want view-tool:live,rev=2", text)
			}
			want := []string{
				fmt.Sprintf("live rev=1 title=%q focus=%s: whenHidden=fx.reopen of 2", title, node),
				fmt.Sprintf("hidden rev=1 title=%q focus=%s: whenHidden=fx.reopen of 2", title, node),
				fmt.Sprintf("live rev=1 title=%q focus=%s: whenHidden=fx.reopen of 2", title, node),
				fmt.Sprintf("live rev=2 title=%q focus=%s:update whenHidden=fx.reopen of 2", title+"/2", node),
			}
			lines, _ := sent()
			if !slices.Equal(lines, want) {
				t.Fatalf("the client was sent\n  %q\nwant\n  %q", lines, want)
			}

			// The user opens the view once it is hidden: every intent is refused but the one declared whenHidden, whose
			// handler shows the mount.
			if text := call("tc-view-hide", map[string]string{"op": "hide"}); text != "view-tool:hidden" {
				t.Fatalf("the tool answered %q, want view-tool:hidden", text)
			}
			_, hidden := sent()
			if hidden.State != uiview.StateHidden {
				t.Fatalf("the client's last snapshot is %s, want hidden", hidden.State)
			}
			if res := h.bridge.InvokeViewIntentFrom(ctx, att, tc.extName, hidden.Mount, "", "fx.tap", json.RawMessage(`{}`)); res.Outcome != uiview.OutcomeRefused || res.Error != "mount is hidden" {
				t.Fatalf("an intent of the hidden mount = %+v, want refused: mount is hidden", res)
			}
			if res := h.bridge.InvokeViewIntentFrom(ctx, att, tc.extName, hidden.Mount, "", "fx.reopen", json.RawMessage(`{}`)); res.Outcome != uiview.OutcomeDone {
				t.Fatalf("the whenHidden intent of the hidden mount = %+v, want done", res)
			}
			want = append(want,
				fmt.Sprintf("hidden rev=2 title=%q focus=%s:update whenHidden=fx.reopen of 2", title+"/2", node),
				fmt.Sprintf("live rev=2 title=%q focus=%s:update whenHidden=fx.reopen of 2", title+"/2", node))
			if lines, _ = sent(); !slices.Equal(lines, want) {
				t.Fatalf("after the user opened the view the client was sent\n  %q\nwant\n  %q", lines, want)
			}
		})
	}
}
