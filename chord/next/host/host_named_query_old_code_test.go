package host

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/store"
)

// Ports packages/chord/test/host/named-query.test.ts @7cc21c24f6b5, "keeps a plugin on its old code when its new factory
// declares a read wrongly" (Pi 734): the three refusals of the checks staging runs against the recording `self`
// (`self.liveQuery` without `{ first: n }`, a list without a name, a list declared twice) fail the reload of the club, and
// the old code still serves the page. TestNamedQueryStagingRefuses... has the refusals of both an attempt and staging;
// this case is Pi's last assertion, which it lacks: the page of the club's old code after the failed reloads.
//
// Forced difference (Go): Pi's `self.list(...).value` reads the list's value to make the factory throw; a refusal here is
// a panic of the declaration itself, so the case does not read it.
func TestNamedQueryAPluginKeepsItsOldCodeWhenItsNewFactoryDeclaresAReadWrongly(t *testing.T) {
	chordStore := memoryStore(t)
	host, _ := nqHosted(t, chordStore, &nqProbe{})
	page := mustGet(t, host, nqTeamPage, jsonObject("team", "u12"))
	const rows = `{"rows":[{"session":"s1","title":"t1","touched":1},{"session":"s0","title":"t0","touched":0}],"more":false}`
	qrWant(t, "rows", nqPageRows(t, page), rows)
	wrong := func(declare func(self StoreSelf)) HostFacet {
		return HostFacet{Name: "@test/club", Facet: DefineFacet(func(h FacetHost, _ FacetSelf) error {
			return nqServeClubDeclaring(h, declare)
		})}
	}
	teams := func(self StoreSelf) contracts.NamedQuery[delta.JsonValue, delta.JsonValue] {
		return self.Queries(self.Table(nqTeamRows), QueriesOptions{}, map[string]QueryBuilder{"ofTeam": {
			Args:  contracts.Obj(contracts.Prop("team", contracts.Str())),
			Build: func(delta.JsonValue) *store.Query { return store.NewQuery(nqTeamRows) },
		}})["ofTeam"]
	}
	failed := func(declare func(self StoreSelf)) string {
		var text strings.Builder
		for _, failure := range reloadMust(t)(host.Reload([]HostFacet{nqPublisher(nqRows), wrong(declare)}, background, ReloadOptions{})).Failed {
			text.WriteString(failure.Error.Error())
		}
		return text.String()
	}
	sources := func(self StoreSelf) []ListSource {
		return []ListSource{{Query: teams(self), Args: jsonObject("team", "u12")}}
	}
	for _, each := range []struct {
		what    string
		declare func(self StoreSelf)
		message string
	}{
		{"a live query without a page size", func(self StoreSelf) { self.LiveQuery(teams(self), jsonObject("team", "u12"), 0) }, "self.liveQuery: { first: n } is required"},
		{"a list without a name", func(self StoreSelf) { self.List("", sources(self), ListOptions{First: 2}) }, "a list has a name"},
		{"a list declared twice", func(self StoreSelf) {
			listed := sources(self)
			self.List("a", listed, ListOptions{First: 2})
			self.List("a", listed, ListOptions{First: 2})
		}, `list "a" is declared twice`},
	} {
		if got := failed(each.declare); !strings.Contains(got, each.message) {
			t.Fatalf("%s: the reload's failures = %q, want one containing %q", each.what, got, each.message)
		}
	}
	// The old code still serves the page.
	qrWant(t, "rows", nqPageRows(t, page), rows)
	closeHosts(t, host)
}

// nqServeClubDeclaring is the club's plugin whose factory declares what declare does (Pi's `serveClub(h, ..., declare)`),
// and serves the page as the club's old code does.
func nqServeClubDeclaring(h FacetHost, declare func(self StoreSelf)) error {
	if err := h.Serve(nqClub, func(_ *delta.JsonObject, plain Self) (contracts.Implementation, error) {
		declare(plain.(StoreSelf))
		return contracts.Implementation{}, nil
	}, ServeOptions{}); err != nil {
		return err
	}
	return h.Serve(nqTeamPage, func(*delta.JsonObject, Self) (contracts.Implementation, error) {
		return contracts.Implementation{}, nil
	}, ServeOptions{})
}
