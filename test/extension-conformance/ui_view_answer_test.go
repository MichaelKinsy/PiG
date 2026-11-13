package extensionconformance

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/uiview"
)

// viewClient attaches a client that keeps the deliveries of the extension's mounts.
type viewClient struct {
	mu   sync.Mutex
	seen []uiview.Delivery
	att  *uiview.Attachment
}

func attachViewClient(t *testing.T, views *uiview.Registry, plugin string, a uiview.ClientAbilities) *viewClient {
	t.Helper()
	c := &viewClient{}
	att, _, err := views.Attach(a, func(d uiview.Delivery) bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		if d.Snapshot.Plugin == plugin {
			c.seen = append(c.seen, d)
		}
		return true
	})
	if err != nil {
		t.Fatal(err)
	}
	c.att = att
	t.Cleanup(att.Detach)
	return c
}

func (c *viewClient) deliveries() []uiview.Delivery {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.seen)
}

// TestConformance_UIViewAnswerRedraw pins, in every SDK and in both runtime modes, which changes of a mount belong to an
// intent's answer (D614). A handler that replaces the view of its own mount through the context it was given answers
// done, and the attached client holds the new view when the answer comes back: the view names the client's argument
// limit (123) plus the intent's argument, a value only that update produces. The same update made through a context
// kept from another request (the command that mounted the panel) is the extension's own work, not the handler's: it is
// applied, and the answer is stale.
func TestConformance_UIViewAnswerRedraw(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	for _, tc := range append(sdkHarnessCases(), packedSDKHarnessCases()...) {
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
			client := attachViewClient(t, views, tc.extName, *conformanceClientAbilities())
			const abilities = "view-abilities:mounts=7,argbytes=123,slots=panel+status,nodes=text+box+select-list,markdown=true"
			pollUntilConformance(t, "abilities never replicated", func() bool {
				h.ui.ClearRecorded()
				runConformanceCommand(t, h, "ui_view_abilities")
				return slices.Contains(viewNotifies(h), abilities)
			})
			runConformanceCommand(t, h, "ui_view_redraw")
			waitViewNotify(t, h, "view-redraw:rev=1")
			mount := func() uiview.Ref {
				t.Helper()
				for _, s := range views.Snapshots() {
					if s.Plugin == tc.extName && s.Mount.Key == "redraw" {
						return s.Mount
					}
				}
				t.Fatal("the redraw panel is not mounted")
				return uiview.Ref{}
			}
			raise := func(intent, arg string) uiview.Result {
				return h.bridge.InvokeViewIntentFrom(context.Background(), client.att, tc.extName, mount(), "", intent, json.RawMessage(arg))
			}

			before := len(client.deliveries())
			res := raise("fx.redraw", `{"n":20}`)
			if res.Outcome != uiview.OutcomeDone || string(res.Answer) != `{"rev":2}` {
				t.Fatalf("fx.redraw, whose handler updates its own mount = %+v, want done with rev 2", res)
			}
			// No wait: the update returned to the handler after the client's sink took the delivery.
			got := client.deliveries()
			if len(got) != before+1 || got[before].Snapshot.Mount.Rev != 2 || !strings.Contains(string(got[before].Snapshot.View), "fixture-redraw-143") {
				t.Fatalf("when fx.redraw was answered the client had been delivered %d more snapshots (%+v), want the redrawn view fixture-redraw-143 at rev 2", len(got)-before, got[before:])
			}
			if d, err := views.Diagnostics(tc.extName, uiview.StateArgs{Key: "redraw"}); err != nil || len(d.Notices) != 0 {
				t.Errorf("notices of the redraw panel = %+v (%v), want none", d.Notices, err)
			}

			// A change that does not come from the answering handler still makes the answer stale.
			res = raise("fx.foreign", `{"n":30}`)
			if res.Outcome != uiview.OutcomeStale || res.Error != "mount changed while the plugin answered" {
				t.Fatalf("fx.foreign, whose update is made outside the handler's context = %+v, want stale", res)
			}
			got = client.deliveries()
			if last := got[len(got)-1].Snapshot; last.Mount.Rev != 3 || !strings.Contains(string(last.View), "fixture-foreign-153") {
				t.Errorf("the client's last delivery = rev %d %s, want the foreign update fixture-foreign-153 at rev 3", last.Mount.Rev, last.View)
			}
		})
	}
}

func watchAbilities(mounts int, slots ...uiview.Slot) uiview.ClientAbilities {
	if slots == nil {
		slots = []uiview.Slot{}
	}
	return uiview.ClientAbilities{
		Slots: slots, Nodes: []string{"text", "box"}, ImageFormats: []string{},
		Limits: uiview.Limits{Nodes: 64, Depth: 16, SnapshotBytes: 65536, TextBytes: 256, Mounts: mounts, IntentArgBytes: 77},
		Input:  uiview.Input{Keyboard: true},
	}
}

// viewEventNotifies returns what the fixture's abilities watcher reported, in order.
func viewEventNotifies(h *harness) []string {
	var out []string
	for _, n := range viewNotifies(h) {
		if strings.HasPrefix(n, "view-event:") || strings.HasPrefix(n, "view-event-") || strings.HasPrefix(n, "view-arrived:") {
			out = append(out, n)
		}
	}
	return out
}

// TestConformance_UIViewAbilitiesEvent pins the event that tells an extension what the attached client draws changed
// (D614), in every SDK and in both runtime modes. The handler runs when the first client attaches, when the abilities
// change and when the last client leaves; it receives the client's own slot order and mount limit, which no fallback
// produces, and the replica already holds them (same=true). A mount made in the handler reaches the client with no
// command run: a client that attaches to an idle session is shown the extension's view. No client is none; a client
// that draws no slot is a record with no slots. With several clients attached the value is the merge of their abilities,
// and the handler runs when the merge changes: a client that attaches or leaves without changing it causes no event.
// The clients here attach in process (Registry.Attach), as a Go embedder's does; a wire client attaches to the same
// registry (the rpc tests of coding/cli). The host does not wait for the handler: while it blocks, the client's
// abilities call returns and the extension answers a command. A session no client attached to ran no handler.
func TestConformance_UIViewAbilitiesEvent(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	for _, tc := range append(sdkHarnessCases(), packedSDKHarnessCases()...) {
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
			runConformanceCommand(t, h, "ui_view_watch")
			waitViewNotify(t, h, "view-watch:armed")

			// No client: state snapshots arrive, the watcher does not run.
			h.bridge.HostStateChanged()
			h.bridge.HostStateChanged()
			pollUntilConformance(t, "no-client abilities never reported", func() bool {
				runConformanceCommand(t, h, "ui_view_abilities")
				return slices.Contains(viewNotifies(h), "view-abilities:none")
			})
			runConformanceCommand(t, h, "ui_view_events")
			waitViewNotify(t, h, "view-events:0")

			count := func(want string) int {
				n := 0
				for _, got := range viewNotifies(h) {
					if got == want {
						n++
					}
				}
				return n
			}
			waitEvent := func(want string, n int) {
				t.Helper()
				pollUntilConformance(t, "the watcher never reported "+want, func() bool { return count(want) >= n })
			}

			// The first client attaches to the idle session: the watcher runs and its mount reaches the client.
			client := attachViewClient(t, views, tc.extName, watchAbilities(11, uiview.SlotStatus, uiview.SlotPanel))
			waitEvent("view-event:slots=status+panel,mounts=11,same=true", 1)
			waitEvent("view-arrived:live", 1)
			if got := client.deliveries(); len(got) != 1 || got[0].Snapshot.Mount.Key != "arrived" || got[0].Snapshot.State != uiview.StateLive || !strings.Contains(string(got[0].Snapshot.View), "fixture-arrived-11") {
				t.Fatalf("the client that attached to the idle session was delivered %+v, want the mount fixture-arrived-11", got)
			}

			// Several clients: the extension sees the merge of their abilities, the value ViewAbilities returns, and the
			// watcher runs when that value changes and not otherwise. A second client that draws another slot and takes
			// more mounts changes it; a third that states what the first states does not; the second leaving changes it
			// back; the third leaving does not. The host sends a runtime its state before every request, so the extension's
			// answer to a command follows the snapshot of what is attached at the command: a watcher that ran for a client
			// that changed nothing is queued by then, ahead of the event of the next change, and the report compared below
			// holds it.
			replica := func(want string, n int) {
				t.Helper()
				runConformanceCommand(t, h, "ui_view_abilities")
				pollUntilConformance(t, "the extension never reported "+want, func() bool { return count(want) >= n })
			}
			const both = "view-abilities:mounts=20,argbytes=77,slots=status+panel+header,nodes=text+box,markdown=false"
			second := attachViewClient(t, views, tc.extName, watchAbilities(20, uiview.SlotPanel, uiview.SlotHeader))
			waitEvent("view-event:slots=status+panel+header,mounts=20,same=true", 1)
			waitEvent("view-arrived:invalid", 1)
			replica(both, 1)
			third := attachViewClient(t, views, tc.extName, watchAbilities(11, uiview.SlotStatus, uiview.SlotPanel))
			replica(both, 2)
			second.att.Detach()
			waitEvent("view-event:slots=status+panel,mounts=11,same=true", 2)
			waitEvent("view-arrived:invalid", 2)
			third.att.Detach()
			replica("view-abilities:mounts=11,argbytes=77,slots=status+panel,nodes=text+box,markdown=false", 1)

			// The abilities change.
			if err := client.att.UpdateAbilities(watchAbilities(12, uiview.SlotStatus, uiview.SlotPanel)); err != nil {
				t.Fatal(err)
			}
			waitEvent("view-event:slots=status+panel,mounts=12,same=true", 1)
			waitEvent("view-arrived:invalid", 3)

			// A handler that blocks delays neither the client's call nor the extension's requests.
			runConformanceCommand(t, h, "ui_view_block")
			waitViewNotify(t, h, "view-block:set")
			returned := make(chan *uiview.Error, 1)
			go func() {
				returned <- client.att.UpdateAbilities(watchAbilities(13, uiview.SlotStatus, uiview.SlotPanel))
			}()
			select {
			case err := <-returned:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(30 * time.Second):
				t.Fatal("the client's abilities call waited for the extension's handler")
			}
			waitEvent("view-event-blocked", 1)
			pollUntilConformance(t, "the extension did not answer a command while its handler blocked", func() bool {
				runConformanceCommand(t, h, "ui_view_abilities")
				return slices.Contains(viewNotifies(h), "view-abilities:mounts=13,argbytes=77,slots=status+panel,nodes=text+box,markdown=false")
			})
			if n := count("view-event:slots=status+panel,mounts=13,same=true"); n != 0 {
				t.Fatalf("the blocked handler reported %d times before its release", n)
			}
			runConformanceCommand(t, h, "ui_view_release")
			waitEvent("view-event:slots=status+panel,mounts=13,same=true", 1)
			waitEvent("view-arrived:invalid", 4)

			// The last client leaves; then a client that draws no slot is not "no client".
			client.att.Detach()
			waitEvent("view-event:none", 1)
			bare := attachViewClient(t, views, tc.extName, watchAbilities(5))
			waitEvent("view-event:slots=,mounts=5,same=true", 1)
			waitEvent("view-arrived:invalid", 5)
			bare.att.Detach()
			waitEvent("view-event:none", 2)

			want := []string{
				"view-event:slots=status+panel,mounts=11,same=true",
				"view-arrived:live",
				"view-event:slots=status+panel+header,mounts=20,same=true",
				"view-arrived:invalid",
				"view-event:slots=status+panel,mounts=11,same=true",
				"view-arrived:invalid",
				"view-event:slots=status+panel,mounts=12,same=true",
				"view-arrived:invalid",
				"view-event-blocked",
				"view-event-released",
				"view-event:slots=status+panel,mounts=13,same=true",
				"view-arrived:invalid",
				"view-event:none",
				"view-event:slots=,mounts=5,same=true",
				"view-arrived:invalid",
				"view-event:none",
			}
			if got := viewEventNotifies(h); !slices.Equal(got, want) {
				t.Fatalf("the watcher reported\n  %q\nwant\n  %q", got, want)
			}
			runConformanceCommand(t, h, "ui_view_events")
			waitViewNotify(t, h, "view-events:8")
		})
	}
}
