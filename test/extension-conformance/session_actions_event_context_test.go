package extensionconformance

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// TestConformance_CommandOnlySessionActionsRefusedFromEventContext pins Pi v1.5.2's runner.ts:1004-1038: newSession, fork, navigateTree, switchSession and reload
// exist only on createCommandContext(). The ExtensionContext every event handler receives (createContext(), runner.ts:877-987) has no such member, so a handler that calls one
// gets a TypeError ("ctx.switchSession is not a function") in its own handler and the session carries on. Each SDK's event handler must see that refusal, and the host action
// must never run, where the same calls from a command run it.
func TestConformance_CommandOnlySessionActionsRefusedFromEventContext(t *testing.T) {
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
			if h.bridge == nil {
				t.Skip("no host bridge for " + tc.name)
			}
			var mu sync.Mutex
			var calls []string
			record := func(name string) {
				mu.Lock()
				defer mu.Unlock()
				calls = append(calls, name)
			}
			h.bridge.SetHostAction("newSession", func(context.Context, *extension.NewSessionOptions) (extension.CancelledResult, error) {
				record("new")
				return extension.CancelledResult{}, nil
			})
			h.bridge.SetHostAction("fork", func(context.Context, string, *extension.ForkOptions) (extension.CancelledResult, error) {
				record("fork")
				return extension.CancelledResult{}, nil
			})
			h.bridge.SetHostAction("navigateTree", func(context.Context, string, *extension.NavigateTreeOptions) (extension.CancelledResult, error) {
				record("navigate")
				return extension.CancelledResult{}, nil
			})
			h.bridge.SetHostAction("switchSession", func(context.Context, string, *extension.SwitchSessionOptions) (extension.CancelledResult, error) {
				record("switch")
				return extension.CancelledResult{}, nil
			})
			h.bridge.SetHostAction("reload", func(context.Context) error {
				record("reload")
				return nil
			})

			// noticeWith is the last notice that starts with prefix, "" before there is one.
			noticeWith := func(prefix string) string {
				var got string
				for _, notice := range *h.notify {
					if strings.HasPrefix(notice, prefix) {
						got = notice
					}
				}
				return got
			}
			noHostActions := func(what string) {
				t.Helper()
				mu.Lock()
				ran := slices.Clone(calls)
				mu.Unlock()
				if len(ran) != 0 {
					t.Fatalf("host session actions ran from %s: %v", what, ran)
				}
			}

			*h.notify = nil
			runConformanceCommandArgs(t, h, "event_session_actions_on", "session_start")
			if _, err := h.runner.Emit(t.Context(), extension.SessionStartEvent{Type: "session_start", Reason: "startup"}); err != nil {
				t.Fatalf("session_start emit: %v", err)
			}
			const want = "event_actions:new=rejected,fork=rejected,navigate=rejected,switch=rejected,reload=rejected:info"
			pollUntilConformance(t, "the SDK's session_start handler never reported its session actions", func() bool {
				return noticeWith("event_actions:") != ""
			})
			if got := noticeWith("event_actions:"); got != want {
				t.Fatalf("session actions from a session_start handler answered %q, want %q", got, want)
			}
			noHostActions("a session_start handler")

			// Pi's JavaScript shows no such member on an event context: typeof is "undefined" and a call throws the TypeError of a missing function. The Node runtime does not
			// hand the members to an event context at all; the other SDKs have one static Context type, so the host's refusal (D617) is all they can show.
			const wantShape = "undefined,undefined,undefined,undefined,undefined;TypeError:ctx.switchSession is not a function:info"
			isNode := strings.Contains(tc.name, "node")
			if isNode {
				if got := noticeWith("event_shape:"); got != "event_shape:"+wantShape {
					t.Fatalf("an event context in the Node runtime shows %q, want %q", got, "event_shape:"+wantShape)
				}
			}

			// The handler's request has ended. A context it kept is still an event context: Pi's createContext() object never gains the members, so a call through it
			// later is refused as well, and no host action runs.
			*h.notify = nil
			runConformanceCommand(t, h, "event_session_actions_stored")
			pollUntilConformance(t, "the SDK never reported the session actions of the event context a handler kept", func() bool {
				return noticeWith("stored_actions:") != ""
			})
			const wantStored = "stored_actions:new=rejected,fork=rejected,navigate=rejected,switch=rejected,reload=rejected:info"
			if got := noticeWith("stored_actions:"); got != wantStored {
				t.Fatalf("session actions through an event context kept after its request ended answered %q, want %q", got, wantStored)
			}
			if isNode {
				if got := noticeWith("stored_shape:"); got != "stored_shape:"+wantShape {
					t.Fatalf("a kept event context in the Node runtime shows %q, want %q", got, "stored_shape:"+wantShape)
				}
			}
			noHostActions("an event context kept after its request ended")

			// The same calls from a command context run every host action.
			runConformanceCommand(t, h, "session_actions_unbound")
			pollUntilConformance(t, "the command's session actions never reached the host", func() bool {
				mu.Lock()
				defer mu.Unlock()
				return len(calls) >= 5
			})
			mu.Lock()
			defer mu.Unlock()
			if want := []string{"new", "fork", "navigate", "switch", "reload"}; !slices.Equal(calls, want) {
				t.Fatalf("a command's session actions reached the host as %v, want %v", calls, want)
			}
		})
	}
}
