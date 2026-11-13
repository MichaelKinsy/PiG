package viewcarrier

import (
	"encoding/json"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/uiview"
)

// textLimitJSON is the abilities of a client that takes text bytes of text in a view and is roomy in every other limit.
func textLimitJSON(text int) string {
	return `{"slots":["panel"],"nodes":["text"],"markdown":false,"imageFormats":["png"],"limits":{"nodes":64,"depth":8,"snapshotBytes":4096,"textBytes":` + strconv.Itoa(text) + `,"mounts":16,"intentArgBytes":64},"input":{"keyboard":true,"pointer":true,"text":true}}`
}

// wireClient is one more connection to the fixture's session, attached with the given text limit.
type wireClient struct {
	f *fixture
	w *wire
	c *Carrier
	n string
}

func (f *fixture) wireClient(text int) *wireClient {
	f.t.Helper()
	w := &wire{t: f.t}
	w.wake = sync.NewCond(&w.mu)
	c := f.connect(w)
	r := f.doOn(c, w, `{"id":"attach","type":"ui_view_attach","abilities":`+textLimitJSON(text)+`}`)
	if code(r) != "ok" {
		f.t.Fatalf("attach = %v", r)
	}
	return &wireClient{f: f, w: w, c: c, n: strconv.Itoa(int(r["data"].(map[string]any)["attachment"].(float64)))}
}

// event waits for the client's i-th ui_view event and returns its snapshot.
func (c *wireClient) event(i int) uiview.Snapshot {
	c.f.t.Helper()
	eventually(c.f.t, "attachment "+c.n+" was never written ui_view event "+strconv.Itoa(i+1), func() bool { return len(c.w.events()) > i })
	return parseEvent(c.f.t, c.w.events()[i]).Snapshot
}

func (c *wireClient) resync() uiview.Resync {
	c.f.t.Helper()
	r := c.f.doOn(c.c, c.w, `{"id":"rs","type":"ui_view_resync","attachment":`+c.n+`}`)
	data, _ := json.Marshal(r["data"])
	var rs uiview.Resync
	if err := json.Unmarshal(data, &rs); err != nil || code(r) != "ok" {
		c.f.t.Fatalf("resync = %v (%v)", r, err)
	}
	return rs
}

// noView reports whether a snapshot off the wire carries no view: the member is null.
func noView(s uiview.Snapshot) bool { return len(s.View) == 0 || string(s.View) == "null" }

// wireDigest is the digest of the content of a snapshot as it came over the wire.
func wireDigest(t *testing.T, s uiview.Snapshot) string {
	t.Helper()
	raw, err := json.Marshal(&s.Content)
	if err != nil {
		t.Fatal(err)
	}
	d, _ := uiview.Digest(raw)
	return d
}

// TestCarrierDegradedClientsAreSentTheDigestOfTheirOwnContent: of two wire clients a content does not fit, one is
// written the fallback and one no view, each with the digest of the content in its own line. A resync names the same
// content and digest, so each keeps its drawing; a client whose limits change is written the content it is now owed.
func TestCarrierDegradedClientsAreSentTheDigestOfTheirOwnContent(t *testing.T) {
	f := newFixture(t)
	draws := f.wireClient(4096) // draws the view, so the mount is admitted
	shows := f.wireClient(64)   // the fallback fits, the view does not
	bare := f.wireClient(1)     // not even the fallback fits
	f.mount("k", strings.Repeat("w", 100), `,"fallback":{"kind":"text","text":"short"}`)
	v, fb, none := draws.event(0), shows.event(0), bare.event(0)
	if v.Degraded || !fb.Degraded || !none.Degraded || !strings.Contains(string(fb.View), `"short"`) || !noView(none) {
		t.Fatalf("written %s (degraded=%v), %s (degraded=%v) and %s (degraded=%v), want the view, the fallback and no view", v.View, v.Degraded, fb.View, fb.Degraded, none.View, none.Degraded)
	}
	for _, s := range []struct {
		name string
		sent uiview.Snapshot
	}{{"the view", v}, {"the fallback", fb}, {"no view", none}} {
		if want := wireDigest(t, s.sent); s.sent.Digest == "" || s.sent.Digest != want {
			t.Errorf("the client shown %s was written the digest %q, the content of its line has %q", s.name, s.sent.Digest, want)
		}
	}
	if fb.Digest == none.Digest {
		t.Errorf("the fallback and no view were written with one digest %q", fb.Digest)
	}
	for _, c := range []struct {
		name string
		wc   *wireClient
		sent uiview.Snapshot
	}{{"the fallback", shows, fb}, {"no view", bare, none}} {
		if rs := c.wc.resync(); len(rs.Snapshots) != 1 || rs.Snapshots[0].Digest != c.sent.Digest || string(rs.Snapshots[0].View) != string(c.sent.View) {
			t.Errorf("the resync of the client shown %s names %+v, want the content it was written (digest %q)", c.name, rs.Snapshots, c.sent.Digest)
		}
	}

	// The two clients change places: each is written the content the other had, under that content's digest.
	if r := f.doOn(bare.c, bare.w, `{"id":"ab","type":"ui_view_abilities","attachment":`+bare.n+`,"abilities":`+textLimitJSON(64)+`}`); code(r) != "ok" {
		t.Fatalf("abilities = %v", r)
	}
	if d := bare.event(1); !strings.Contains(string(d.View), `"short"`) || d.Digest != fb.Digest {
		t.Errorf("the client whose limits now take the fallback was written %s under %q, want the fallback under %q", d.View, d.Digest, fb.Digest)
	}
	if r := f.doOn(shows.c, shows.w, `{"id":"ab","type":"ui_view_abilities","attachment":`+shows.n+`,"abilities":`+textLimitJSON(1)+`}`); code(r) != "ok" {
		t.Fatalf("abilities = %v", r)
	}
	if d := shows.event(1); !noView(d) || d.Digest != none.Digest {
		t.Errorf("the client whose limits now refuse the fallback was written %s under %q, want no view under %q", d.View, d.Digest, none.Digest)
	}
}
