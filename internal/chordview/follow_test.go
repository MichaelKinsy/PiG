package chordview

import (
	"testing"

	"github.com/MichaelKinsy/PiG/coding/uiview"
)

// TestFollowMountsThePanelsWhenAClientArrives: surfaces shown before any client attached have no panel. Once the views
// follow the registry's clients, the first client that attaches is delivered the panels with no Refresh by the caller,
// and after Close a later client causes no mount. A panel carries no intent, so no answer of an intent depends on who
// updates it.
func TestFollowMountsThePanelsWhenAClientArrives(t *testing.T) {
	r := uiview.New(uiview.Options{})
	v := Show(r, []Surface{{Name: "web", Base: func() string { return "b1" }}})
	if err := v.Refresh(); err == nil || err.Code != uiview.CodeUnsupported {
		t.Fatalf("without a client: %v", err)
	}
	v.Follow()
	var got []uiview.Delivery
	att, rs, err := r.Attach(abilities(), func(d uiview.Delivery) bool {
		got = append(got, d)
		return true
	})
	if err != nil {
		t.Fatal(err)
	}
	// The panel was mounted inside Attach, after the attach reply's snapshots were taken: it is an event.
	if len(rs.Snapshots) != 0 || len(got) != 1 || got[0].Snapshot.Plugin != Plugin || got[0].Snapshot.Mount.Key != "web" || got[0].Snapshot.State != uiview.StateLive || len(got[0].Snapshot.Intents) != 0 {
		t.Fatalf("the client that attached was delivered %+v (attach snapshots %d), want the web panel", got, len(rs.Snapshots))
	}
	att.Detach()
	v.Close()
	late, rs, err := r.Attach(abilities(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer late.Detach()
	for _, s := range rs.Snapshots {
		if s.State == uiview.StateLive {
			t.Errorf("after Close a client was shown a live panel: %+v", s.Status)
		}
	}
}
