package uiview

import (
	"encoding/json"
	"strings"
	"testing"
)

// contentDigest is the digest of the content a client was sent, as the client computes it.
func contentDigest(t *testing.T, s Snapshot) string {
	t.Helper()
	raw, err := json.Marshal(&s.Content)
	if err != nil {
		t.Fatal(err)
	}
	d, _ := Digest(raw)
	return d
}

// TestDegradedClientsAreSentTheDigestOfTheirOwnContent: two clients a content does not fit can differ in what they are
// sent, the fallback or no view, and each is sent the digest of its own content. A resync names that content with that
// digest, so each client keeps what it draws. When a client's limits change so that it is owed the other content, it
// is delivered that content: a digest shared by the two would make the delivery look like the one already sent.
func TestDegradedClientsAreSentTheDigestOfTheirOwnContent(t *testing.T) {
	const fallback = `{"root":{"id":"fb","kind":"text","text":"short"}}`
	for _, order := range []string{"the client shown the fallback attaches first", "the client shown no view attaches first"} {
		t.Run(order, func(t *testing.T) {
			r := New(Options{})
			_, _, _ = attach(t, r, richAbilities()) // draws the view, so the mount is admitted
			shows := *abilities()                   // 64 bytes of text: the fallback fits, the view does not
			bare := *abilities()
			bare.Limits.TextBytes = 3 // not even the fallback fits
			var cf, cn *client
			var af, an *Attachment
			if strings.HasPrefix(order, "the client shown the fallback") {
				cf, af, _ = attach(t, r, shows)
				cn, an, _ = attach(t, r, bare)
			} else {
				cn, an, _ = attach(t, r, bare)
				cf, af, _ = attach(t, r, shows)
			}
			rec := mountRecord("k", map[string]any{"fallback": map[string]any{"kind": "text", "id": "fb", "text": "short"}})
			rec["view"] = map[string]any{"root": map[string]any{"kind": "text", "id": "v", "text": strings.Repeat("w", 100)}}
			args, err := decodeRecord(t, rec)
			wantOK(t, "decode", err)
			if _, err := r.Mount("p", args); err != nil {
				t.Fatal(err)
			}
			if len(cf.got) != 1 || len(cn.got) != 1 {
				t.Fatalf("the mount was delivered %d and %d times", len(cf.got), len(cn.got))
			}
			f, n := cf.got[0].Snapshot, cn.got[0].Snapshot
			if !f.Degraded || string(f.View) != fallback || !n.Degraded || n.View != nil {
				t.Fatalf("sent %s (degraded=%v) and %s (degraded=%v), want the fallback and no view", f.View, f.Degraded, n.View, n.Degraded)
			}
			if want := contentDigest(t, f); f.Digest == "" || f.Digest != want {
				t.Errorf("the client shown the fallback was sent the digest %q, its content has %q", f.Digest, want)
			}
			if want := contentDigest(t, n); n.Digest == "" || n.Digest != want {
				t.Errorf("the client shown no view was sent the digest %q, its content has %q", n.Digest, want)
			}
			if f.Digest == n.Digest {
				t.Errorf("the fallback and no view were sent with one digest %q", f.Digest)
			}

			// A resync names what each client draws: the same content under the same digest.
			for _, c := range []struct {
				name string
				att  *Attachment
				sent Snapshot
			}{{"the fallback", af, f}, {"no view", an, n}} {
				rs, err := c.att.Resync()
				wantOK(t, "resync", err)
				if len(rs.Snapshots) != 1 || rs.Snapshots[0].Digest != c.sent.Digest || string(rs.Snapshots[0].View) != string(c.sent.View) {
					t.Errorf("the resync of the client shown %s names %+v, want the content it was sent (digest %q)", c.name, rs.Snapshots, c.sent.Digest)
				}
			}

			// The two clients change places: each is delivered the content the other had, under that content's digest.
			wantOK(t, "update abilities", an.UpdateAbilities(shows))
			if len(cn.got) != 2 {
				t.Fatalf("the client whose limits now take the fallback was delivered %d times, want the fallback", len(cn.got)-1)
			}
			if d := cn.got[1].Snapshot; string(d.View) != fallback || !d.Degraded || d.Digest != contentDigest(t, d) {
				t.Errorf("the client whose limits now take the fallback was sent %s under %q, its content has %q", d.View, d.Digest, contentDigest(t, d))
			}
			wantOK(t, "update abilities", af.UpdateAbilities(bare))
			if len(cf.got) != 2 {
				t.Fatalf("the client whose limits now refuse the fallback was delivered %d times, want no view", len(cf.got)-1)
			}
			if d := cf.got[1].Snapshot; d.View != nil || !d.Degraded || d.Digest != contentDigest(t, d) {
				t.Errorf("the client whose limits now refuse the fallback was sent %s under %q, its content has %q", d.View, d.Digest, contentDigest(t, d))
			}
		})
	}
}

// TestClientMovingBetweenContentsIsDeliveredEachChangeOnce: one client's limits move it between the view, the fallback
// and no view, forwards and back. Every move that changes what it is owed is delivered exactly once, with that
// content under that content's digest, the same digest each time the content comes back; a change of its limits that
// leaves it owed what it draws, and the same abilities again, deliver nothing.
func TestClientMovingBetweenContentsIsDeliveredEachChangeOnce(t *testing.T) {
	const fallback = `{"root":{"id":"fb","kind":"text","text":"short"}}`
	r := New(Options{})
	_, _, _ = attach(t, r, richAbilities()) // draws the view whatever the moving client's limits are
	limits := func(text int) ClientAbilities {
		a := *abilities()
		a.Limits.TextBytes = text
		return a
	}
	c, att, _ := attach(t, r, limits(256))
	rec := mountRecord("k", map[string]any{"fallback": map[string]any{"kind": "text", "id": "fb", "text": "short"}})
	rec["view"] = map[string]any{"root": map[string]any{"kind": "text", "id": "v", "text": strings.Repeat("w", 100)}}
	args, err := decodeRecord(t, rec)
	wantOK(t, "decode", err)
	if _, err := r.Mount("p", args); err != nil {
		t.Fatal(err)
	}
	if len(c.got) != 1 || c.got[0].Snapshot.Degraded || !strings.Contains(string(c.got[0].Snapshot.View), `"id":"v"`) {
		t.Fatalf("the mount was delivered %d times, first %+v, want the view once", len(c.got), c.got)
	}
	digests := map[string]string{"view": c.got[0].Snapshot.Digest}
	for i, step := range []struct {
		text int
		want string // the content owed after the step, or "" when the step changes nothing
	}{
		{64, "fallback"}, {3, "none"}, {64, "fallback"}, {70, ""}, {64, ""}, {256, "view"}, {3, "none"}, {3, ""}, {2, ""}, {256, "view"}, {64, "fallback"},
	} {
		before := len(c.got)
		wantOK(t, "update abilities", att.UpdateAbilities(limits(step.text)))
		sent := c.got[before:]
		if step.want == "" {
			if len(sent) != 0 {
				t.Fatalf("step %d (text limit %d) changes nothing the client is owed and delivered %d times: %s degraded=%v digest %q", i, step.text, len(sent), sent[0].Snapshot.View, sent[0].Snapshot.Degraded, sent[0].Snapshot.Digest)
			}
			continue
		}
		if len(sent) != 1 {
			t.Fatalf("step %d (text limit %d) owes the client %s and delivered %d times, want once", i, step.text, step.want, len(sent))
		}
		s := sent[0].Snapshot
		var drawn string
		switch {
		case s.View == nil && s.Degraded:
			drawn = "none"
		case string(s.View) == fallback && s.Degraded:
			drawn = "fallback"
		case !s.Degraded && strings.Contains(string(s.View), `"id":"v"`):
			drawn = "view"
		}
		if drawn != step.want {
			t.Fatalf("step %d (text limit %d) delivered %s degraded=%v, want %s", i, step.text, s.View, s.Degraded, step.want)
		}
		if want := contentDigest(t, s); s.Digest == "" || s.Digest != want {
			t.Errorf("step %d (text limit %d) delivered %s under the digest %q, its content has %q", i, step.text, step.want, s.Digest, want)
		}
		if held, ok := digests[step.want]; ok && held != s.Digest {
			t.Errorf("step %d (text limit %d): %s came back under the digest %q, it had %q", i, step.text, step.want, s.Digest, held)
		}
		digests[step.want] = s.Digest
	}
	if len(digests) != 3 || digests["view"] == digests["fallback"] || digests["fallback"] == digests["none"] || digests["view"] == digests["none"] {
		t.Errorf("the three contents have the digests %v, want three different ones", digests)
	}
}
