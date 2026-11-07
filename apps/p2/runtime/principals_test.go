package runtime

import (
	"context"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/next/data"
)

// apps/p2/src/runtime/principals.ts has no unit test upstream: sharing.test.ts, login-owners.test.ts, devices.test.ts
// and foreign-requests.test.ts exercise it through the server (lane A's end-to-end ports). These cases pin each
// function's rule as the TypeScript source states it.

func guest(sessions map[string]GuestMode) GuestPrincipal {
	return GuestPrincipal{ID: "g1", Name: "Eve", Home: "s1", Sessions: sessions, Revision: 3}
}

func TestKey(t *testing.T) {
	cases := []struct {
		principal Principal
		want      string
	}{
		{OwnerPrincipal{Access: AccessLocal}, "owner:local"},
		{OwnerPrincipal{Access: AccessRelay}, "owner:relay"},
		{OwnerPrincipal{Access: AccessDevice, Device: "d1"}, "owner:device:d1"},
		{guest(nil), "guest:g1:r3"},
		{WorkerPrincipal{Session: "s1", Generation: 2}, "worker:s1:2"},
		{Server, "server"},
		{Host, "host"},
	}
	for _, c := range cases {
		if got := Key(c.principal); got != c.want {
			t.Errorf("Key(%#v) = %q, want %q", c.principal, got, c.want)
		}
	}
}

func TestRevokedBy(t *testing.T) {
	canonical := func(revocations []Revocation) []string {
		out := []string{}
		for _, r := range revocations {
			out = append(out, data.CanonicalJson(r, context.Background()))
		}
		return out
	}
	cases := []struct {
		principal Principal
		want      []string
	}{
		{OwnerPrincipal{Access: AccessLocal}, []string{}},
		{OwnerPrincipal{Access: AccessRelay}, []string{`{"owners":"remote"}`}},
		{OwnerPrincipal{Access: AccessDevice, Device: "d1"}, []string{`{"device":"d1"}`, `{"owners":"remote"}`}},
		{guest(nil), []string{`{"guest":"g1"}`}},
		{WorkerPrincipal{Session: "s1"}, []string{}},
		{Server, []string{}},
	}
	for _, c := range cases {
		if got := canonical(RevokedBy(c.principal)); !reflect.DeepEqual(got, c.want) {
			t.Errorf("RevokedBy(%#v) = %v, want %v", c.principal, got, c.want)
		}
	}
}

func TestCanViewAndCanSend(t *testing.T) {
	owner := OwnerPrincipal{Access: AccessLocal}
	g := guest(map[string]GuestMode{"view": GuestView, "full": GuestFull})
	type row struct {
		principal Principal
		session   string
		view      bool
		send      bool
	}
	for _, c := range []row{
		{owner, "any", true, true},
		{g, "view", true, false},
		{g, "full", true, true},
		{g, "other", false, false},
		{WorkerPrincipal{Session: "view"}, "view", false, false},
		{nil, "view", false, false},
	} {
		if got := CanView(c.principal, c.session); got != c.view {
			t.Errorf("CanView(%#v, %q) = %v", c.principal, c.session, got)
		}
		if got := CanSend(c.principal, c.session); got != c.send {
			t.Errorf("CanSend(%#v, %q) = %v", c.principal, c.session, got)
		}
	}
}

func TestAuthorOf(t *testing.T) {
	owner := OwnerPrincipal{Access: AccessRelay}
	if got, want := AuthorOf(owner, "Mario"), (Author{Kind: AuthorOwner, ID: "owner", Name: "Mario"}); got != want {
		t.Errorf("owner author = %#v, want %#v", got, want)
	}
	if got, want := AuthorOf(guest(nil), "Mario"), (Author{Kind: AuthorGuest, ID: "g1", Name: "Eve"}); got != want {
		t.Errorf("guest author = %#v, want %#v", got, want)
	}
	if AuthorID(owner) != "owner" || AuthorID(guest(nil)) != "g1" {
		t.Errorf("AuthorID: owner %q guest %q", AuthorID(owner), AuthorID(guest(nil)))
	}
}

func TestChordRoundTrip(t *testing.T) {
	email := "eve@example.com"
	g := guest(map[string]GuestMode{"s1": GuestFull, "s2": GuestView})
	g.Email = &email
	for _, principal := range []Principal{
		OwnerPrincipal{Access: AccessLocal},
		OwnerPrincipal{Access: AccessRelay},
		OwnerPrincipal{Access: AccessDevice, Device: "d1"},
		g,
		guest(map[string]GuestMode{}),
		WorkerPrincipal{Session: "s1", Generation: 7},
		Server,
		Host,
	} {
		back, err := FromChord(ToChord(principal))
		if err != nil {
			t.Fatalf("FromChord(ToChord(%#v)): %v", principal, err)
		}
		if !reflect.DeepEqual(back, principal) {
			t.Errorf("round trip = %#v, want %#v", back, principal)
		}
	}
	if got, want := data.CanonicalJson(ToChord(OwnerPrincipal{Access: AccessDevice, Device: "d1"}), context.Background()), `{"access":"device","device":"d1","kind":"owner"}`; got != want {
		t.Errorf("owner device JSON = %s, want %s", got, want)
	}
}

func TestChordKeyAndRevocationFunctions(t *testing.T) {
	principal := ToChord(WorkerPrincipal{Session: "s1", Generation: 2})
	if got := data.PrincipalKey(principal, context.Background(), PrincipalKeyFunction); got != "worker:s1:2" {
		t.Errorf("principal key = %q", got)
	}
	revocations, err := ChordRevokedBy(ToChord(OwnerPrincipal{Access: AccessDevice, Device: "d1"}))
	if err != nil || len(revocations) != 2 {
		t.Fatalf("ChordRevokedBy = %v, %v", revocations, err)
	}
	if _, err := FromChord(nil); err == nil {
		t.Error("FromChord(nil) succeeded")
	}
}
