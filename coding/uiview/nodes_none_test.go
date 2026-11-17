package uiview

import (
	"strings"
	"testing"
)

// pig additive (D614, owner word 2026-11-12T23:22:43Z "no declared node kinds = none drawn"): a client whose nodes list is absent (nil) or empty draws no node kind at all,
// not even text, as an empty slots list draws no slot. Alone it is refused a mount and names the kind; beside a client that draws the kind it is sent a degraded snapshot
// that holds no view.
func TestAClientThatDeclaresNoNodeKindsIsSentNoKind(t *testing.T) {
	for _, tc := range []struct {
		name  string
		nodes []string
	}{{"absent", nil}, {"empty", []string{}}} {
		t.Run(tc.name, func(t *testing.T) {
			none := abilities()
			none.Nodes = tc.nodes
			t.Run("the only client", func(t *testing.T) {
				r := New(Options{})
				wantOK(t, "abilities", r.SetAbilities(none))
				_, err := r.Mount("p", mountArgs(t, "k", "hello"))
				wantCode(t, err, CodeUnsupported)
				if err != nil && !strings.Contains(err.Message, `"text"`) {
					t.Fatalf("the refusal does not name the kind: %q", err.Message)
				}
			})
			t.Run("beside a client that draws it", func(t *testing.T) {
				r := New(Options{})
				var bare []Delivery
				_, _, err := r.Attach(*none, func(d Delivery) bool { bare = append(bare, d); return true })
				wantOK(t, "attach bare", err)
				_, _, err = r.Attach(*abilities(), func(Delivery) bool { return true })
				wantOK(t, "attach text", err)
				_, err = r.Mount("p", mountArgs(t, "k", "hello"))
				wantOK(t, "mount", err)
				if len(bare) != 1 || !bare[0].Snapshot.Degraded || len(bare[0].Snapshot.View) != 0 {
					t.Fatalf("the client with no node kinds was sent %+v, want a degraded snapshot with no view", bare)
				}
			})
		})
	}
}
