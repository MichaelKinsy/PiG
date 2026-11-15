package host

import (
	"context"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/store"
)

// pi: packages/chord/src/host/named-query.ts
// Ports packages/chord/test/host/named-query.test.ts (Pi main 0db9861ec018; slice 14 §3.1, §5, test list 2, 7, 11, 18): what
// `self.queries` returns implements a `queries` member; a joined query's mapping gets the row and its partner, projected;
// `page` reads without a live read; the member check and staging; and a column set's binding across its publisher's reloads.
//
// Forced differences: a queries member is a Go map of NamedQuery values, so Pi's `5` (not an object) has two Go forms, a nil
// map and a map of a foreign NamedQuery (the same refusal); `self.queries` takes builders as a map of QueryBuilder (a plain
// builder: Args nil, the contract lists the query; `[argsSchema, builder]`: Args set).

var (
	nqTeamRows = hdTable("team-rows", store.Col("team", store.Str()), store.Col("session", store.Host(Session)))
	nqTeamItem = exObj(exProp("session", exStr()), exProp("title", exStr()), exProp("touched", exInt()))
	nqClub     = exLocal("app.club", contracts.Params{},
		contracts.Mem("teams", contracts.Queries(nqTeamItem, []contracts.Property{exProp("ofTeam", exObj(exProp("team", exStr())))})),
		contracts.Mem("add", contracts.NewMethod(exObj(exProp("team", exStr()), exProp("session", exStr())))))
)

// nqNamed is what a queries member of the club implements.
type nqNamed = map[string]contracts.NamedQuery[delta.JsonValue, delta.JsonValue]

// nqForeign is a NamedQuery that `self.queries` did not return.
type nqForeign struct{}

func (nqForeign) Page(delta.JsonValue, int64, context.Context, contracts.QueryPageOptions) (contracts.QueryPage[delta.JsonValue], error) {
	return contracts.QueryPage[delta.JsonValue]{}, nil
}

// nqBroken is the failure messages of an attempt of the club whose factory returns `teams` for its queries member
// (`broken`): the activation failures' messages, any other report's kind.
func nqBroken(t *testing.T, teams func(self StoreSelf) nqNamed) []string {
	t.Helper()
	chordStore := memoryStore(t)
	host, errs := loaded(t, func(h FacetHost, _ FacetSelf) error {
		return h.Serve(nqClub, func(_ *delta.JsonObject, plain Self) (contracts.Implementation, error) {
			return contracts.Implementation{Members: contracts.VariantImplementation{
				Queries: map[string]nqNamed{"teams": teams(plain.(StoreSelf))},
				Methods: map[string]contracts.MethodFunction{"add": func(delta.JsonValue, context.Context) (delta.JsonValue, error) { return nil, nil }},
			}}, nil
		}, ServeOptions{})
	}, loadedOptions{Store: chordStore})
	// The page's refusal is the report: Pi's `.catch(() => {})`.
	_, _ = host.Get(background, nqClub, jsonObject(), GetOptions{})
	var messages []string
	for _, err := range errs.All() {
		if err.Failure.Error != nil {
			messages = append(messages, err.Failure.Error.Message)
		} else {
			messages = append(messages, string(err.Kind))
		}
	}
	return messages
}

func nqExpectOne(t *testing.T, messages []string, want string) {
	t.Helper()
	if len(messages) != 1 || !strings.Contains(messages[0], want) {
		t.Fatalf("messages %q, want one containing %q", messages, want)
	}
}

func TestNamedQueryTheMemberCheckRefusesAnythingButWhatSelfQueriesReturnedAListedQueryWithoutABuilderAndASecondSchema(t *testing.T) {
	nqExpectOne(t, nqBroken(t, func(StoreSelf) nqNamed { return nqNamed{} }),
		`teams: no builder for the listed query "ofTeam"`)
	// Pi's `5`: no object at all, and an object holding what self.queries did not return.
	nqExpectOne(t, nqBroken(t, func(StoreSelf) nqNamed { return nil }),
		"teams: a queries member is what self.queries returned")
	nqExpectOne(t, nqBroken(t, func(StoreSelf) nqNamed { return nqNamed{"ofTeam": nqForeign{}} }),
		"teams: a queries member is what self.queries returned")
	twice := nqBroken(t, func(self StoreSelf) nqNamed {
		return self.Queries(self.Table(nqTeamRows), QueriesOptions{Map: func(store.Row, PartnerRow) (delta.JsonValue, error) { return nil, nil }},
			map[string]QueryBuilder{"ofTeam": {
				Args:  exObj(exProp("team", exStr())),
				Build: func(delta.JsonValue) *store.Query { return store.NewQuery(nqTeamRows) },
			}})
	})
	nqExpectOne(t, twice, "the contract has its argument schema; give only the builder")
	// Mine (Pi's check has no case of its own): a query the contract does not list brings its schema.
	private := nqBroken(t, func(self StoreSelf) nqNamed {
		build := func(delta.JsonValue) *store.Query { return store.NewQuery(nqTeamRows) }
		return self.Queries(self.Table(nqTeamRows), QueriesOptions{}, map[string]QueryBuilder{"ofTeam": {Build: build}, "extra": {Build: build}})
	})
	nqExpectOne(t, private, `teams: query "extra" is not listed and brings no argument schema`)
}

var (
	nqPlain   = hdTable("plain", store.Col("id", store.Int()), store.Col("n", store.Int()))
	nqCounted = exLocal("app.counted", contracts.Params{}, contracts.Mem("n", exInt()))
)

// nqServeCounted serves Counted, its member n the value declare returns (declare runs in the factory, the declarations of
// the case first).
func nqServeCounted(h FacetHost, declare func(self StoreSelf)) error {
	return h.Serve(nqCounted, func(_ *delta.JsonObject, plain Self) (contracts.Implementation, error) {
		self := plain.(StoreSelf)
		declare(self)
		return exImpl(map[string]chord.Watchable[delta.JsonValue]{
			"n": self.Derive(nil, func([]any) (delta.JsonValue, error) { return float64(1), nil }),
		}, nil), nil
	}, ServeOptions{})
}

// nqRefused is the refusal of `declare` by an attempt (the Get of a host serving it) and by staging (the reload of a host
// that served Counted without it).
func nqRefused(t *testing.T, declare func(self StoreSelf)) (attempt, staged string) {
	t.Helper()
	live, _ := loaded(t, func(h FacetHost, _ FacetSelf) error { return nqServeCounted(h, declare) }, loadedOptions{Store: memoryStore(t)})
	_, err := live.Get(background, nqCounted, jsonObject(), GetOptions{})
	if err != nil {
		attempt = err.Error()
	}
	host, _ := loaded(t, func(h FacetHost, _ FacetSelf) error { return nqServeCounted(h, func(StoreSelf) {}) }, loadedOptions{Store: memoryStore(t)})
	report := reloadMust(t)(reloadApp(host, func(h FacetHost) error { return nqServeCounted(h, declare) }))
	for _, failure := range report.Failed {
		staged += failure.Error.Error()
	}
	return attempt, staged
}

func TestNamedQueryStagingRefusesTheQueriesAnAttemptRefusesTheNamedBuildersAndTheTable(t *testing.T) {
	for _, each := range []struct {
		what    string
		declare func(self StoreSelf)
		message string
	}{
		{"a table the factory did not declare", func(self StoreSelf) {
			self.Queries(nil, QueriesOptions{}, nil)
		}, "self.queries: the table is not one this factory declared"},
		{"a table of another definition", func(self StoreSelf) {
			self.Table(nqPlain)
			self.Queries(store.NewTableHandle(nqTeamRows, "x", nil, nil), QueriesOptions{}, nil)
		}, "self.queries: the table is not one this factory declared"},
		// Mine: a handle of a recording that stands for a table this factory did not declare.
		{"a recorded handle of another definition", func(self StoreSelf) {
			self.Table(nqPlain)
			self.Queries(store.NewTableHandle(nqTeamRows, "x", nil, inertTableOwner{}), QueriesOptions{}, nil)
		}, "self.queries: the table is not one this factory declared"},
		// Mine: Pi's namedBuilders check has no case of its own.
		{"a builder that is none", func(self StoreSelf) {
			self.Queries(self.Table(nqPlain), QueriesOptions{}, map[string]QueryBuilder{"all": {}})
		}, `self.queries: "all" has no builder`},
	} {
		t.Run("refuses "+each.what, func(t *testing.T) {
			attempt, staged := nqRefused(t, each.declare)
			for _, got := range []string{attempt, staged} {
				if !strings.Contains(got, each.message) {
					t.Fatalf("refusal %q, want %q", got, each.message)
				}
			}
		})
	}
}
