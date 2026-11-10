package extensionconformance

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/uiview"
)

func conformanceClientAbilities() *uiview.ClientAbilities {
	return &uiview.ClientAbilities{
		Slots: []uiview.Slot{uiview.SlotPanel, uiview.SlotStatus}, Nodes: []string{"text", "box"}, Markdown: true, ImageFormats: []string{"png"},
		Limits: uiview.Limits{Nodes: 64, Depth: 16, SnapshotBytes: 65536, TextBytes: 256, Mounts: 7, IntentArgBytes: 123},
		Input:  uiview.Input{Keyboard: true},
	}
}

func viewNotifies(h *harness) []string {
	var out []string
	for _, n := range *h.notify {
		// The harness records a notify as "<message>:<level>".
		if strings.HasPrefix(n, "view-") || strings.HasPrefix(n, "intent-") {
			out = append(out, strings.TrimSuffix(n, ":info"))
		}
	}
	return out
}

func waitViewNotify(t *testing.T, h *harness, want string) {
	t.Helper()
	pollUntilConformance(t, "never reported "+want, func() bool { return slices.Contains(viewNotifies(h), want) })
}

// TestConformance_UIViewMounts pins D614 in every SDK: the replicated client abilities (W3), the mount lifecycle and its
// stale and terminal rejections (W1), and the intent request, its answer and its cancellation (W2). Every value an
// extension reports comes from the host (ability limits 7 and 123, epoch 2 after another plugin's mount, a sum that adds
// the host's argument limit), so an SDK that never makes the call, or answers from a constant, cannot pass.
func TestConformance_UIViewMounts(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
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
			views := h.bridge.Views()

			// W3: before a client attaches the extension sees none; after, the host's values.
			pollUntilConformance(t, "no-client abilities never reported", func() bool {
				*h.notify = (*h.notify)[:0]
				runConformanceCommand(t, h, "ui_view_abilities")
				return slices.Contains(viewNotifies(h), "view-abilities:none")
			})
			h.bridge.SetClientAbilities(conformanceClientAbilities())
			const abilities = "view-abilities:mounts=7,argbytes=123,slots=panel+status,nodes=text+box,markdown=true"
			pollUntilConformance(t, "abilities never replicated", func() bool {
				*h.notify = (*h.notify)[:0]
				runConformanceCommand(t, h, "ui_view_abilities")
				return slices.Contains(viewNotifies(h), abilities)
			})

			// Another plugin's mount takes epoch 1, so the extension's first mount reports epoch 2.
			if _, err := views.Mount("other", mustMount(t, "o", "other")); err != nil {
				t.Fatal(err)
			}
			*h.notify = (*h.notify)[:0]
			runConformanceCommand(t, h, "ui_view_probe")
			waitViewNotify(t, h, "view-second:epoch=3,rev=1")
			want := []string{
				"view-mounted:epoch=2,rev=1,state=live",
				"view-updated:rev=2",
				"view-stale:stale",
				"view-hidden:hidden",
				"view-shown:live",
				"view-finished:finished",
				"view-terminal:terminal",
				"view-final:finished",
				"view-second:epoch=3,rev=1",
			}
			if got := viewNotifies(h); !slices.Equal(got, want) {
				t.Fatalf("probe reported\n  %q\nwant\n  %q", got, want)
			}

			// What the host holds is what the extension sent.
			var first, second *uiview.Snapshot
			for _, s := range views.Snapshots() {
				if s.Plugin != tc.extName {
					continue
				}
				s := s
				switch s.Mount.Key {
				case "probe":
					first = &s
				case "probe2":
					second = &s
				}
			}
			if first == nil || second == nil {
				t.Fatalf("snapshots lack the extension's mounts: %+v", views.Snapshots())
			}
			if first.State != uiview.StateFinished || first.Mount.Rev != 2 || !strings.Contains(string(first.View), "fixture-view-123-2") || strings.Contains(string(first.View), "stale") {
				t.Errorf("first mount = %s rev %d %s", first.State, first.Mount.Rev, first.View)
			}
			if second.State != uiview.StateLive || !strings.Contains(string(second.View), "fixture-view-123-live") || len(second.Intents) != 2 {
				t.Errorf("second mount = %s %s intents %d", second.State, second.View, len(second.Intents))
			}

			// W2: an intent reaches the extension and its answer comes back; the sum adds the host's limit.
			res := h.bridge.InvokeViewIntent(context.Background(), tc.extName, second.Mount, "", "fx.add", json.RawMessage(`{"n":20}`))
			if res.Outcome != uiview.OutcomeDone || string(res.Answer) != `{"sum":163}` {
				t.Errorf("fx.add = %+v", res)
			}
			// A malformed argument never reaches the extension.
			if res := h.bridge.InvokeViewIntent(context.Background(), tc.extName, second.Mount, "", "fx.add", json.RawMessage(`{"n":"x"}`)); res.Outcome != uiview.OutcomeRefused {
				t.Errorf("fx.add with a string = %+v", res)
			}
			// W2 cancel: cancelling the caller cancels the extension's handler, which reports the tag it was given.
			ctx, cancel := context.WithCancel(context.Background())
			out := make(chan uiview.Result, 1)
			go func() {
				out <- h.bridge.InvokeViewIntent(ctx, tc.extName, second.Mount, "", "fx.wait", json.RawMessage(`{"tag":"tag-9f3"}`))
			}()
			waitViewNotify(t, h, "intent-started:tag-9f3")
			cancel()
			select {
			case res := <-out:
				if res.Outcome != uiview.OutcomeCancelled {
					t.Errorf("cancelled intent = %+v", res)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("cancelled intent never settled")
			}
			pollUntilConformance(t, "the handler never saw the cancel", func() bool {
				runConformanceCommand(t, h, "ui_view_cancelled")
				return slices.Contains(viewNotifies(h), "intent-cancelled:tag-9f3")
			})

			// A stale ref is answered without reaching the extension.
			stale := second.Mount
			stale.Rev = 99
			if res := h.bridge.InvokeViewIntent(context.Background(), tc.extName, stale, "", "fx.add", json.RawMessage(`{"n":1}`)); res.Outcome != uiview.OutcomeStale {
				t.Errorf("stale intent = %+v", res)
			}

			// A host-side end carries its reason to the extension.
			h.bridge.ViewsSessionReplaced()
			pollUntilConformance(t, "broken state never reported", func() bool {
				runConformanceCommand(t, h, "ui_view_state")
				return slices.Contains(viewNotifies(h), "view-state:broken:session_replaced")
			})
		})
	}
}

func mustMount(t *testing.T, key, text string) uiview.MountArgs {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{"key": key, "slot": "panel", "view": map[string]any{"root": map[string]any{"kind": "text", "text": text}}})
	a, err := uiview.DecodeMount(raw)
	if err != nil {
		t.Fatal(err)
	}
	return a
}
