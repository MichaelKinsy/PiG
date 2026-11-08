package subprocess

import (
	"encoding/json"
	"reflect"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/sessionentry"
)

// The entries message_end persisted are named to a Node runtime, with the sequence the message_end request carried, in the state push that follows: once each, whether or not the runtime holds the log, and a session change sends what the new session holds from its start. A runtime of another SDK holds no object to give an entry, so it is sent none.
func TestPushStateToSendsMessageEntriesToANodeRuntimeOnce(t *testing.T) {
	for _, node := range []bool{true, false} {
		name := "node"
		if !node {
			name = "other SDK"
		}
		t.Run(name, func(t *testing.T) {
			rig := newPushRig(t)
			rig.me.nodeRuntime = node
			var mu sync.Mutex
			var noted []sessionentry.MessageSeq
			session := "s1"
			rig.bridge.SetHostAction("getSessionID", func() string { mu.Lock(); defer mu.Unlock(); return session })
			rig.bridge.SetHostAction("getMessageEntries", func(cursor int) ([]sessionentry.MessageSeq, int) {
				mu.Lock()
				defer mu.Unlock()
				if cursor < 0 || cursor > len(noted) {
					cursor = 0
				}
				return append([]sessionentry.MessageSeq(nil), noted[cursor:]...), len(noted)
			})
			note := func(pairs ...sessionentry.MessageSeq) {
				mu.Lock()
				noted = append(noted, pairs...)
				mu.Unlock()
			}
			sent := func(t *testing.T) []sessionentry.MessageSeq {
				t.Helper()
				var session struct {
					MessageEntries []sessionentry.MessageSeq `json:"messageEntries"`
				}
				state := rig.nextState(t)
				if err := json.Unmarshal(state["session"], &session); err != nil {
					t.Fatal(err)
				}
				return session.MessageEntries
			}

			rig.push(t)
			if got := sent(t); len(got) != 0 {
				t.Fatalf("pairs sent before any entry was persisted: %v", got)
			}
			first := []sessionentry.MessageSeq{{ID: "e1", Seq: 1}, {ID: "e2", Seq: 2}}
			note(first...)
			rig.push(t)
			if !node {
				rig.expectNoFrame(t, "a runtime without objects is sent no pair, so nothing changed for it")
				return
			}
			if got := sent(t); !reflect.DeepEqual(got, first) {
				t.Fatalf("pairs = %v, want %v", got, first)
			}
			rig.push(t)
			rig.expectNoFrame(t, "a push with no new pair repeats nothing")
			second := []sessionentry.MessageSeq{{ID: "e3", Seq: 3}}
			note(second...)
			rig.push(t)
			if got := sent(t); !reflect.DeepEqual(got, second) {
				t.Fatalf("pairs = %v, want only the new %v", got, second)
			}
			rig.push(t)
			rig.expectNoFrame(t, "the pair went out once")

			// A session change starts over with the pairs the new session holds.
			mu.Lock()
			session = "s2"
			noted = []sessionentry.MessageSeq{{ID: "n1", Seq: 9}}
			mu.Unlock()
			rig.push(t)
			if got := sent(t); !reflect.DeepEqual(got, []sessionentry.MessageSeq{{ID: "n1", Seq: 9}}) {
				t.Fatalf("pairs after a session change = %v", got)
			}
		})
	}
}
