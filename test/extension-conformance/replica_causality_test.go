package extensionconformance

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// replicaCausalityCases are every subprocess SDK realization: isolated, packed, fused Go, and the Node runtime as the reference.
func replicaCausalityCases() []harnessCase {
	return append(sdkHarnessCases(), packedSDKHarnessCases()...)
}

// Pi reads the Session live, so a read after a host call that changed the model is never older than the call's result (agent-session.ts registerProvider,
// unregisterProvider, registerVirtualModel and unregisterVirtualModel call _refreshCurrentModelFromRegistry before they return). The SDKs and the Node runtime
// answer the model from the replicated state, so the host pushes its state before the reply of those calls. The probe registers the provider of the active model
// again with a new model name and reads the model in the same handler, twenty times; each name is a value no SDK defaults to, and a read that is one registration
// behind fails the row.
func TestARegistryWriteIsVisibleToTheNextModelReadAcrossSDKs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	for _, tc := range replicaCausalityCases() {
		t.Run(tc.name, func(t *testing.T) {
			h := tc.make(t)
			t.Cleanup(func() {
				if h.cleanup != nil {
					h.cleanup()
				}
				if h.host != nil {
					h.host.Shutdown("registry model conformance complete")
				}
			})
			if h.bridge == nil {
				t.Skip("the in-process harness has no replicated state")
			}
			rig := newStatePushRig(t, h)
			api := providerServicesRegistryAPI{services: rig.services}
			h.host.SetProviderCallbacks(func(name string, config extension.ProviderConfig) error {
				api.RegisterProvider(name, config)
				return nil
			}, func(name string) { api.UnregisterProvider(name) })
			command, ok := findCommand(h.runner, "registry-model-probe")
			if !ok {
				t.Fatal("registry-model-probe command missing")
			}
			run := func(name string) string {
				t.Helper()
				*h.notify = nil
				if err := command.Handler(h.runner.DispatchContext(t.Context()), name); err != nil {
					t.Fatal(err)
				}
				if len(*h.notify) != 1 {
					t.Fatalf("notifications = %v", *h.notify)
				}
				return strings.TrimSuffix((*h.notify)[0], ":info")
			}
			run("Registry Name init")
			pollUntilConformance(t, "the registry never held the provider the SDK registered", func() bool {
				return rig.services.Registry().Find("replica-reg", "m") != nil
			})
			mustSucceed(t, rig.session.SetModel(rig.services.Registry().Find("replica-reg", "m")))
			for i := range 20 {
				name := fmt.Sprintf("Registry Name %d", i)
				if got, want := run(name), "regname="+name; got != want {
					t.Fatalf("read after registerProvider %d = %q, want %q (the Session's model is now %q)", i, got, want, rig.session.Model().DisplayName)
				}
			}
		})
	}
}

// A reader that learned a host change from a host call never reads an older replica afterwards: Pi reads both live. The host's own state push is asynchronous
// (Host.hostStateChanged queues it), so the row holds that push back with the host's session id read, replaces the Session, and then moves the context usage the
// reader asks the host for: the reply that shows the usage of the replaced Session must be preceded by the state of the replaced Session, so the replicated id the
// reader reads next is the new one. An SDK or runtime whose reply overtakes the push reads the old id.
func TestAHostReplyIsNeverOlderThanTheStateItFollowsAcrossSDKs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	for _, tc := range replicaCausalityCases() {
		t.Run(tc.name, func(t *testing.T) {
			h := tc.make(t)
			t.Cleanup(func() {
				if h.cleanup != nil {
					h.cleanup()
				}
				if h.host != nil {
					h.host.Shutdown("reply order conformance complete")
				}
			})
			if h.bridge == nil {
				t.Skip("the in-process harness has no replicated state")
			}
			rig := newStatePushRig(t, h)
			var holdUntil atomic.Int64
			h.bridge.SetHostAction("getSessionID", func() string {
				if wait := time.Until(time.Unix(0, holdUntil.Load())); wait > 0 {
					time.Sleep(wait)
				}
				return rig.session.ID()
			})
			rig.run(t, h, statePushKind{
				name: "usage-then-id",
				change: func(*testing.T) {
					holdUntil.Store(time.Now().Add(300 * time.Millisecond).UnixNano())
					rig.session.ReplaceInner(codingagent.NewSession("reply-order-replaced", rig.session.CWD()))
					// The usage moves after the replacement: a reader that sees it has seen a host call that follows the change.
					rig.usageTokens.Store(250)
				},
				armed: func(got string) bool { return got == "tokens=100" },
				read:  func(got string) bool { return got == "tokens=250,id=reply-order-replaced" },
			})
		})
	}
}

// The same, for a change the host has made and not reported yet: the Session's new id is visible to a host call before the owner's report raises the host's
// epoch (Host.hostStateChanged), so a reply in that stretch would carry the new Session's context usage ahead of the push that names it. The Session brackets
// the change (ObserveReplicatedStateScope), and the host flushes before every reply while one is in flight. The row holds the report back with the bridge's
// own hook, so the stretch lasts until the reader has read.
func TestAHostReplyIsNeverOlderThanAChangeInFlightAcrossSDKs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	for _, tc := range replicaCausalityCases() {
		t.Run(tc.name, func(t *testing.T) {
			h := tc.make(t)
			t.Cleanup(func() {
				if h.cleanup != nil {
					h.cleanup()
				}
				if h.host != nil {
					h.host.Shutdown("in-flight change conformance complete")
				}
			})
			if h.bridge == nil {
				t.Skip("the in-process harness has no replicated state")
			}
			rig := newStatePushRig(t, h)
			reported := h.bridge.OnHostStateChanged
			hold := make(chan struct{})
			var holding atomic.Bool
			var holdOnce sync.Once
			release := func() { holdOnce.Do(func() { close(hold) }) }
			var changing sync.WaitGroup
			t.Cleanup(func() {
				release()
				changing.Wait()
			})
			h.bridge.OnHostStateChanged = func() {
				if holding.Load() {
					<-hold
				}
				reported()
			}
			rig.run(t, h, statePushKind{
				name: "usage-then-id",
				change: func(*testing.T) {
					holding.Store(true)
					changing.Go(func() {
						rig.session.ReplaceInner(codingagent.NewSession("in-flight-replaced", rig.session.CWD()))
					})
					pollUntilConformance(t, "the Session never took the replacement", func() bool { return rig.session.ID() == "in-flight-replaced" })
					rig.usageTokens.Store(250)
				},
				armed: func(got string) bool { return got == "tokens=100" },
				read:  func(got string) bool { return got == "tokens=250,id=in-flight-replaced" },
			})
		})
	}
}
