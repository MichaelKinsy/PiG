package subprocess

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

// The author exports its configuration root for every registerProvider call. Whatever the Host does with the call, it accounts that transmission and releases it (xref.unpin) so the author's export is not pinned for the life of the process.
func TestProviderConfigRefIsAccountedWhenTheCallIsNotApplied(t *testing.T) {
	const ref = `{"$x":"ref","realm":"author-realm","id":"7","kind":"object"}`
	for _, tc := range []struct {
		name string
		args string
		run  func(h *Host, me *managedExt, call *CallPayload)
	}{
		{"args do not parse", `{"name":5,"config":{},"configRef":` + ref + `}`, func(h *Host, me *managedExt, call *CallPayload) {
			_, _ = h.handleProviderRegistrationCall(context.Background(), me, me.connection(), "c1", call)
		}},
		{"empty provider name", `{"name":" ","config":{},"configRef":` + ref + `}`, func(h *Host, me *managedExt, call *CallPayload) {
			_, _ = h.handleProviderRegistrationCall(context.Background(), me, me.connection(), "c1", call)
		}},
		{"call dropped before it ran", `{"name":"p","config":{},"configRef":` + ref + `}`, func(h *Host, me *managedExt, call *CallPayload) {
			call.ParentRequestID = "finished-request"
			h.runCall(me, me.connection(), "c1", call, nil)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rig := newOAuthProxyRig(t)
			if err := rig.host.xref.bind(rig.me.connection(), rig.me, "author-realm"); err != nil {
				t.Fatal(err)
			}
			frames := make(chan Envelope, 4)
			go func() {
				for {
					env, err := tryReadFramed(rig.client)
					if err != nil {
						return
					}
					frames <- env
				}
			}()
			tc.run(rig.host, rig.me, &CallPayload{Method: "registerProvider", Args: json.RawMessage(tc.args)})
			var env Envelope
			select {
			case env = <-frames:
			case <-time.After(3 * time.Second):
				t.Fatal("the author's export was never released")
			}
			if env.Type != MsgNotify || env.Notify == nil || env.Notify.Method != notifyXrefUnpin {
				t.Fatalf("frame = %+v, want xref.unpin for the author's export", env)
			}
			if stats := rig.host.xref.stats(); stats.Leases != 0 || stats.Holds != 0 {
				t.Fatalf("xref ledger after the call = %+v", stats)
			}
		})
	}
}
