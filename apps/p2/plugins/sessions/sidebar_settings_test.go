package sessions_test

import (
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	"github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions"
	"github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
)

// Ports apps/p2/test/p2/sidebar-settings.test.ts: sidebar settings are the owner's, start empty, and are kept across a
// restart of the server (the same store opened by a second host).
func TestSidebarSettingsAreTheOwnersStartEmptyAndSurviveARestart(t *testing.T) {
	store := openStore(t)
	server := p2test.NewTestHost(t, p2test.TestHostOptions{Facets: []host.Facet{sessions.Server}, Store: store})
	settings, err := p2test.HoldIn(server.Client(owner), contract.SidebarSettings, delta.NewJsonObject(0), "owner")
	if err != nil {
		t.Fatal(err)
	}
	defer settings.Release()
	empty := func(value delta.JsonValue) bool { return value != nil && marshal(t, value) == "[]" }
	if _, err := p2test.Until(settings.Member("order"), empty, 0); err != nil {
		t.Fatalf("order starts empty: %v", err)
	}
	for member, want := range map[string]string{"windows": "{}", "collapsed": "{}"} {
		if got, _ := settings.Member(member).Value(); marshal(t, got) != want {
			t.Errorf("%s starts as %s, want %s", member, marshal(t, got), want)
		}
	}
	// A repeated key in the order counts once.
	if _, err := settings.Call("setOrder", delta.JsonObjectOf("order", []any{"folders", "recent", "folders"}), p2test.Background); err != nil {
		t.Fatal(err)
	}
	if _, err := settings.Call("setWindow", delta.JsonObjectOf("key", "recent", "window", "1w"), p2test.Background); err != nil {
		t.Fatal(err)
	}
	if _, err := settings.Call("setCollapsed", delta.JsonObjectOf("key", "projects", "collapsed", true), p2test.Background); err != nil {
		t.Fatal(err)
	}
	if got, _ := settings.Member("order").Value(); !reflect.DeepEqual(got, []any{"folders", "recent"}) {
		t.Fatalf("order = %v, want [folders recent]", got)
	}

	guest := p2runtime.GuestPrincipal{ID: "guest", Name: "Guest", Home: "unknown", Sessions: map[string]p2runtime.GuestMode{"unknown": p2runtime.GuestFull}}
	denied, err := server.Client(guest).Hold(p2test.Background, contract.SidebarSettings, delta.NewJsonObject(0), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	failure, err := p2test.FailureOf(denied)
	if err != nil {
		t.Fatal(err)
	}
	p2test.ExpectChord(t, failure, data.CodeForbidden)
	denied.Release()

	server.Close()
	restarted := p2test.NewTestHost(t, p2test.TestHostOptions{Facets: []host.Facet{sessions.Server}, Store: store})
	after, err := p2test.HoldIn(restarted.Client(owner), contract.SidebarSettings, delta.NewJsonObject(0), "owner")
	if err != nil {
		t.Fatal(err)
	}
	defer after.Release()
	if _, err := p2test.Until(after.Member("order"), func(value delta.JsonValue) bool { return value != nil && marshal(t, value) == `["folders","recent"]` }, 0); err != nil {
		t.Fatalf("order after a restart: %v", err)
	}
	for member, want := range map[string]string{"windows": `{"recent":"1w"}`, "collapsed": `{"projects":true}`} {
		if got, _ := after.Member(member).Value(); marshal(t, got) != want {
			t.Errorf("%s after a restart = %s, want %s", member, marshal(t, got), want)
		}
	}
}
