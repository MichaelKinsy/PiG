package extensionconformance

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/uiview"
)

// gridClientAbilities is a client that draws the given node kinds in a panel.
func gridClientAbilities(nodes ...string) uiview.ClientAbilities {
	a := *conformanceClientAbilities()
	a.Nodes = nodes
	a.Input.Pointer = true
	a.Limits.TextBytes = 4096
	return a
}

// wantGrid is the month the fixtures mount for token tok, as the wire carries it: two dimmed cells of the month before and
// 28 days, of which the first has two chips and a count of events left out, the second is today in a colour, the third
// has a chip without a colour and the last has no label.
func wantGrid(tok string) map[string]any {
	cells := []any{map[string]any{"label": "30", "dim": true}, map[string]any{"label": "31", "dim": true}}
	for d := 1; d <= 28; d++ {
		cell := map[string]any{"value": tok + "-" + strconv.Itoa(d), "label": strconv.Itoa(d)}
		switch d {
		case 1:
			cell["chips"] = []any{map[string]any{"text": tok + " standup", "color": "#3366cc"}, map[string]any{"text": "lunch", "color": "accent"}}
			cell["more"] = 2
		case 2:
			cell["mark"], cell["color"] = "current", "#ff000080"
		case 3:
			cell["chips"] = []any{map[string]any{"text": "plain"}}
		case 28:
			delete(cell, "label")
		}
		cells = append(cells, cell)
	}
	return map[string]any{
		"kind": "grid", "id": "month", "columns": 7, "head": []any{"Mo", "Tu", "We", "Th", "Fr", "Sa", "Su"}, "chipRows": 2,
		"selected": tok + "-2", "cells": cells,
	}
}

// viewCanonical is v in the canonical form of RFC 8785, so two JSON values compare member for member.
func viewCanonical(t *testing.T, v any) string {
	t.Helper()
	raw, ok := v.(json.RawMessage)
	if !ok {
		var err error
		if raw, err = json.Marshal(v); err != nil {
			t.Fatal(err)
		}
	}
	c, err := uiview.Canonical(raw)
	if err != nil {
		t.Fatalf("canonical form of %s: %v", raw, err)
	}
	return string(c)
}

// TestConformance_UIViewGrid pins the grid node of D614 in every SDK and realization. The test hands each row its own
// token; the fixture builds a month from it with its SDK's grid (a view, a title and the grid: three nodes) and mounts it,
// then tries four updates:
//   - mount: the client that declares `grid` is sent exactly that grid, member for member: the cell values, the selection
//     and one chip text carry the row's token, and a member the fixture did not set (a label, a mark, a colour, a count)
//     is absent, not null, zero or empty. An SDK that dropped a member, or made one up, is not what the client holds;
//   - cells, chips, columns, mark: 129 cells, 9 chips in a cell, 17 columns and a mark that is the row's token are
//     `invalid` in every SDK, the host counts each of them on the mount, and none of them reaches a client or changes
//     the revision. The host's checkGrid is the one validator: an SDK that checked, clamped or dropped a value would
//     answer something else;
//   - member: a cell member the record lacks is `invalid` where an SDK can write one: Node, whose view is a plain
//     object, and Lisp, whose cell is a plain table. The builders of the Go, Rust and Python kits are closed, so
//     those rows have no such line; that the rule holds for every client is pinned on the host by
//     TestGridValidatorBounds (coding/extension/host/subprocess: unknown node, cell and chip member), and that a kit
//     writes exactly the wire members by each SDK's own grid test;
//   - fallback: a second client that does not declare `grid` is sent the mount's fallback, marked degraded;
//   - intent: the intent of the grid's node, raised by the client with a cell value, reaches the extension, which
//     answers with the node and the value it was given.
func TestConformance_UIViewGrid(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	for i, tc := range append(sdkHarnessCases(), packedSDKHarnessCases()...) {
		tok := fmt.Sprintf("row%d-%d", i, 6200+41*i)
		t.Run(tc.name, func(t *testing.T) {
			h := tc.make(t)
			views := h.bridge.Views()
			var mu sync.Mutex
			var drawn, plain []uiview.Snapshot
			collect := func(into *[]uiview.Snapshot) uiview.Sink {
				return func(d uiview.Delivery) bool {
					mu.Lock()
					defer mu.Unlock()
					if d.Snapshot.Plugin == tc.extName && d.Snapshot.Mount.Key == "grid" {
						*into = append(*into, d.Snapshot)
					}
					return true
				}
			}
			att, _, aerr := views.Attach(gridClientAbilities("container", "text", "grid"), collect(&drawn))
			if aerr != nil {
				t.Fatal(aerr)
			}
			other, _, aerr := views.Attach(gridClientAbilities("container", "text"), collect(&plain))
			if aerr != nil {
				t.Fatal(aerr)
			}
			t.Cleanup(func() {
				other.Detach()
				att.Detach()
				if h.cleanup != nil {
					h.cleanup()
				}
				if h.host != nil {
					h.host.Shutdown("test done")
				}
			})
			h.ui.ClearRecorded()
			runConformanceCommandArgs(t, h, "ui_view_grid", tok)
			pollUntilConformance(t, "the probe never reported the state of its mount", func() bool {
				return slices.ContainsFunc(viewNotifies(h), func(n string) bool { return strings.HasPrefix(n, "view-g-state:") })
			})
			// The SDKs that can write a member the record lacks (see the comment above).
			writesMember := strings.Contains(tc.name, "node") || strings.Contains(tc.name, "lisp")
			want := []string{"view-g-mounted:live", "view-g-cells:invalid", "view-g-chips:invalid", "view-g-columns:invalid", "view-g-mark:invalid"}
			last := fmt.Sprintf("unknown grid cell mark %q", tok)
			if writesMember {
				want = append(want, "view-g-member:invalid")
				last = `unknown field "extra"`
			}
			want = append(want, "view-g-state:live,rev=1")
			if got := viewNotifies(h); !slices.Equal(got, want) {
				t.Fatalf("grid probe reported\n  %q\nwant\n  %q", got, want)
			}
			// The refusals are the host's: it counted every refused update on the mount (repeats of a code are one notice
			// that keeps the latest message), and the last one names the value the fixture sent last.
			diag, derr := views.Diagnostics(tc.extName, uiview.StateArgs{Key: "grid"})
			if derr != nil {
				t.Fatal(derr)
			}
			var refused []uiview.Notice
			for _, n := range diag.Notices {
				if n.Path == "update" {
					refused = append(refused, n)
				}
			}
			if len(refused) != 1 || refused[0].Code != "invalid" || refused[0].Count != len(want)-2 || !strings.Contains(refused[0].Message, last) {
				t.Errorf("the host's notices of refused updates are %+v, want one invalid notice counting %d with %q", refused, len(want)-2, last)
			}

			mu.Lock()
			sent, degraded := slices.Clone(drawn), slices.Clone(plain)
			mu.Unlock()
			// The mount, once: a refused update is sent to no client.
			if len(sent) != 1 || len(degraded) != 1 {
				t.Fatalf("the client that draws grid was sent the mount %d times and the one that does not %d times, want 1 and 1", len(sent), len(degraded))
			}
			var view struct {
				Root struct {
					Kind     string            `json:"kind"`
					Children []json.RawMessage `json:"children"`
				} `json:"root"`
			}
			if err := json.Unmarshal(sent[0].View, &view); err != nil || view.Root.Kind != "container" || len(view.Root.Children) != 2 {
				t.Fatalf("the client was sent the view %s (%v), want a container of a title and a grid", sent[0].View, err)
			}
			var title struct {
				Kind, Text string
			}
			if err := json.Unmarshal(view.Root.Children[0], &title); err != nil || title.Kind != "text" || title.Text != "Month "+tok {
				t.Errorf("the title is %s, want the text %q", view.Root.Children[0], "Month "+tok)
			}
			if got, want := viewCanonical(t, view.Root.Children[1]), viewCanonical(t, wantGrid(tok)); got != want || sent[0].Degraded || len(sent[0].Digest) != 64 {
				t.Errorf("the client was sent the grid (degraded %v, digest %q)\n  %s\nwant\n  %s", sent[0].Degraded, sent[0].Digest, got, want)
			}
			// The client that does not draw grid holds the fallback and no grid.
			var fallback struct {
				Root struct {
					Kind, Text string
				}
			}
			if err := json.Unmarshal(degraded[0].View, &fallback); err != nil || !degraded[0].Degraded || fallback.Root.Kind != "text" || fallback.Root.Text != "grid-fallback "+tok {
				t.Errorf("the client that does not draw grid was sent %s (degraded %v, %v), want the fallback text %q marked degraded", degraded[0].View, degraded[0].Degraded, err, "grid-fallback "+tok)
			}
			// Choosing a cell: the node's intent with the cell's value reaches the extension.
			arg := json.RawMessage(fmt.Sprintf(`{"value":%q}`, tok+"-2"))
			res := h.bridge.InvokeViewIntentFrom(context.Background(), att, tc.extName, sent[0].Mount, "month", "fx.pick", arg)
			if res.Outcome != uiview.OutcomeDone || len(res.Answer) == 0 {
				t.Fatalf("fx.pick = %+v", res)
			}
			if got, want := viewCanonical(t, res.Answer), viewCanonical(t, map[string]string{"node": "month", "picked": tok + "-2"}); got != want {
				t.Errorf("fx.pick answered %s, want %s", got, want)
			}
			// A cell value that is not a string never reaches the extension.
			if res := h.bridge.InvokeViewIntentFrom(context.Background(), att, tc.extName, sent[0].Mount, "month", "fx.pick", json.RawMessage(`{"value":2}`)); res.Outcome != uiview.OutcomeRefused {
				t.Errorf("fx.pick with a number = %+v", res)
			}
		})
	}
}
