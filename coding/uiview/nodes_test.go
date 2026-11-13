package uiview

import (
	"encoding/json"
	"strings"
	"testing"
)

// gridMountArgs mounts a view of a node kind the host knows and a client may not draw, with a text fallback.
func gridMountArgs(t *testing.T, fallback bool) MountArgs {
	t.Helper()
	m := map[string]any{"key": "k", "slot": "panel", "view": map[string]any{"root": map[string]any{"kind": "grid", "id": "g"}}}
	if fallback {
		m["fallback"] = map[string]any{"kind": "text", "text": "plain"}
	}
	raw, _ := json.Marshal(m)
	a, err := DecodeMount(raw)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func knowsGrid(kind string) bool { return kind == "text" || kind == "grid" }

// pig additive (D614): ClientAbilities.nodes is what the client draws, and a view is sent only to a client that draws
// every node kind it holds. A client without the kind is refused when it is the only one, and is sent the fallback,
// marked degraded, when another client draws the view.
func TestAClientIsOnlySentNodeKindsItDraws(t *testing.T) {
	t.Run("the only client lacks the kind", func(t *testing.T) {
		r := New(Options{KnownKind: knowsGrid})
		wantOK(t, "abilities", r.SetAbilities(abilities()))
		_, err := r.Mount("p", gridMountArgs(t, true))
		wantCode(t, err, CodeUnsupported)
		if err != nil && !strings.Contains(err.Message, `"grid"`) {
			t.Fatalf("the refusal does not name the kind: %q", err.Message)
		}
	})
	t.Run("two clients", func(t *testing.T) {
		r := New(Options{KnownKind: knowsGrid})
		var plain, gridder []Delivery
		_, _, err := r.Attach(*abilities(), func(d Delivery) bool { plain = append(plain, d); return true })
		wantOK(t, "attach plain", err)
		with := abilities()
		with.Nodes = []string{"text", "grid"}
		_, _, err = r.Attach(*with, func(d Delivery) bool { gridder = append(gridder, d); return true })
		wantOK(t, "attach gridder", err)
		_, err = r.Mount("p", gridMountArgs(t, true))
		wantOK(t, "mount", err)
		if len(gridder) != 1 || !strings.Contains(string(gridder[0].Snapshot.View), `"grid"`) || gridder[0].Snapshot.Degraded {
			t.Fatalf("the client that draws grid was sent %+v", gridder)
		}
		if len(plain) != 1 || !plain[0].Snapshot.Degraded || strings.Contains(string(plain[0].Snapshot.View), `"grid"`) || !strings.Contains(string(plain[0].Snapshot.View), `"plain"`) {
			t.Fatalf("the client without grid was sent %+v, want the fallback marked degraded", plain)
		}
	})
	t.Run("two clients and no fallback", func(t *testing.T) {
		r := New(Options{KnownKind: knowsGrid})
		var plain []Delivery
		_, _, err := r.Attach(*abilities(), func(d Delivery) bool { plain = append(plain, d); return true })
		wantOK(t, "attach plain", err)
		with := abilities()
		with.Nodes = []string{"text", "grid"}
		_, _, err = r.Attach(*with, func(Delivery) bool { return true })
		wantOK(t, "attach gridder", err)
		_, err = r.Mount("p", gridMountArgs(t, false))
		wantOK(t, "mount", err)
		if len(plain) != 1 || !plain[0].Snapshot.Degraded || len(plain[0].Snapshot.View) != 0 {
			t.Fatalf("the client without grid was sent %+v, want a degraded snapshot with no view", plain)
		}
	})
	t.Run("a client that gains the kind", func(t *testing.T) {
		r := New(Options{KnownKind: knowsGrid})
		var got []Delivery
		a, _, err := r.Attach(*abilities(), func(d Delivery) bool { got = append(got, d); return true })
		wantOK(t, "attach", err)
		with := abilities()
		with.Nodes = []string{"text", "grid"}
		other, _, err := r.Attach(*with, func(Delivery) bool { return true })
		wantOK(t, "attach other", err)
		_, err = r.Mount("p", gridMountArgs(t, true))
		wantOK(t, "mount", err)
		wantOK(t, "update abilities", a.UpdateAbilities(*with))
		last := got[len(got)-1]
		if last.Snapshot.Degraded || !strings.Contains(string(last.Snapshot.View), `"grid"`) {
			t.Fatalf("after it declared the kind the client was sent %+v", last)
		}
		other.Detach()
	})
}
