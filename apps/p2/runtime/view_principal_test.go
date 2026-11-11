package runtime

import (
	"reflect"
	"testing"
)

// A view principal (apps/p2/src/runtime/principals.ts, `ViewPrincipal`; apps/p2/docs/rooms.md §1): each frame link is a principal of
// its own, revoked by its opening, its link and its viewer's revocations; it has no authority of the viewer's.

func viewPrincipal() ViewPrincipal {
	return ViewPrincipal{
		Session: "s1", View: "room", Opening: "o1", Mode: ViewWrite, ForName: "Eve", ForRole: "guest", Link: "link-1", Editor: "ed-1",
		Viewer: []Revocation{RevokeGuest("g1")},
	}
}

func TestViewPrincipalKeyIsItsLink(t *testing.T) {
	if got := Key(viewPrincipal()); got != "view:link-1" {
		t.Fatalf("Key = %q, want view:link-1", got)
	}
}

func TestViewPrincipalIsRevokedByItsOpeningItsLinkAndItsViewer(t *testing.T) {
	got := RevokedBy(viewPrincipal())
	want := []Revocation{RevokeView("s1/room/o1"), RevokeLink("link-1"), RevokeGuest("g1")}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("RevokedBy = %v, want %v", got, want)
	}
	if ViewKey("s1", "room", "o1") != "s1/room/o1" {
		t.Fatal("ViewKey")
	}
}

func TestViewPrincipalRoundTripsThroughChord(t *testing.T) {
	want := viewPrincipal()
	back, err := FromChord(ToChord(want))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(back, want) {
		t.Fatalf("round trip = %#v, want %#v", back, want)
	}
}

func TestViewPrincipalHasNoViewerPowers(t *testing.T) {
	for _, principal := range []Principal{viewPrincipal()} {
		if CanView(principal, "s1") || CanSend(principal, "s1") || IsOwner(principal) || IsGuest(principal) || IsWorker(principal) {
			t.Fatalf("%v acts as a viewer", principal)
		}
	}
}

func TestFromChordRefusesABadViewPrincipal(t *testing.T) {
	for name, edit := range map[string]func(*ViewPrincipal){
		"mode": func(p *ViewPrincipal) { p.Mode = "admin" },
		"role": func(p *ViewPrincipal) { p.ForRole = "worker" },
	} {
		bad := viewPrincipal()
		edit(&bad)
		if _, err := FromChord(ToChord(bad)); err == nil {
			t.Errorf("%s: a bad view principal was accepted", name)
		}
	}
}
