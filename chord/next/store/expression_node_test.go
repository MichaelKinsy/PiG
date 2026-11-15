package store

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
)

// Ports the CheckNode cases of packages/chord/test/store/read-plan.test.ts: "refuse a malformed node before anything
// is rendered" and "takes a condition as data only with canonical host keys, and no ordering of a host column".
//
// The `compile` line is here too; the `whereNode(TeamSessions, clubOnly)` line of the malformed case is in
// read_plan_test.go ("a node of another set is refused, whole, before it is added").

// ceRefused is the message CheckNode refuses node with, or "accepted"; an error that is no invalid/args fails the test.
func ceRefused(t *testing.T, node any, side Side) string {
	t.Helper()
	err := CheckNode(node, &side, "whereNode")
	if err == nil {
		return "accepted"
	}
	return rejected(t, err, data.CodeInvalid, data.ReasonArgs).Message
}

func TestReadPlanCheckNode(t *testing.T) {
	sessionColumns, err := DefineColumns("sessions.columns", ColumnSetOptions{
		Columns: []ColumnProp{
			Col("id", Str()), Col("app", Str()), Col("private", Bool()), Col("lastTouch", Int()), Col("archivedAt", Opt(Int())),
		},
		Key: []string{"id"},
	})
	if err != nil {
		t.Fatal(err)
	}
	object := delta.JsonObjectOf

	t.Run("refuse a malformed node before anything is rendered", func(t *testing.T) {
		side := Side{Name: sessionColumns.Name, ByName: sessionColumns.ByName}
		cases := []struct {
			node any
			want string
		}{
			{object("like", []any{"app", "x"}), `unknown kind "like"`},
			{object("eq", []any{"app", "x"}, "ne", []any{"app", "y"}), "one kind"},
			{object("eq", []any{"app", float64(5)}), "5 is not a str value"},
			{object("eq", []any{"nope", "x"}), `no column "nope"`},
			{object("exists", []any{"club.team-sessions", []any{}, nil}), "matched pairs"},
			{object("exists", []any{"club.team-sessions", []any{[]any{"session", "nope"}}, nil}), `no column "nope"`},
		}
		var deep any = object("eq", []any{"app", "x"})
		for range 20 {
			deep = object("not", deep)
		}
		cases = append(cases, struct {
			node any
			want string
		}{deep, "nested deeper"})
		for _, c := range cases {
			if got := ceRefused(t, c.node, side); !strings.Contains(got, c.want) {
				t.Errorf("CheckNode(%v) = %q, want it to contain %q", c.node, got, c.want)
			}
		}
	})

	t.Run("takes a condition as data only with canonical host keys, and no ordering of a host column", func(t *testing.T) {
		project, err := contracts.NewHostKind("project", contracts.Prop("id", contracts.Str()))
		if err != nil {
			t.Fatal(err)
		}
		owned, err := DefineColumns("owned", ColumnSetOptions{
			Columns: []ColumnProp{Col("id", Int()), Col("project", Host(project))}, Key: []string{"id"},
		})
		if err != nil {
			t.Fatal(err)
		}
		side := Side{Name: owned.Name, ByName: owned.ByName}
		key, err := owned.ByName["project"].Host.Key(project.Address(object("id", "p1")), "")
		if err != nil {
			t.Fatal(err)
		}
		if got := ceRefused(t, object("eq", []any{"project", key}), side); got != "accepted" {
			t.Errorf("CheckNode of the canonical key %q = %q", key, got)
		}
		// Pi: `const key = compile(Owned, eq(Owned.refs.project, Project({ id: "p1" })))`; `refused(key)` is "accepted".
		compiled, err := Compile(owned, Eq(owned.Refs["project"], project.Address(object("id", "p1"))))
		if err != nil {
			t.Fatal(err)
		}
		if got := ceRefused(t, compiled.JSON(), side); got != "accepted" {
			t.Errorf("CheckNode of compile(Owned, eq(project, address)) = %q", got)
		}
		for _, c := range []struct {
			node any
			want string
		}{
			{object("eq", []any{"project", "p1"}), `"p1" is no address key of project`},
			{object("inArray", []any{"project", []any{"session|p1"}}), "is no address key of project"},
			{object("lt", []any{"project", "project|p1"}), `lt on host(...) column "project"`},
			{object("prefix", []any{"project", "project|"}), "prefix on host(...) column"},
			{object("isNull", "project"), "isNull on host(...) column"},
		} {
			if got := ceRefused(t, c.node, side); !strings.Contains(got, c.want) {
				t.Errorf("CheckNode(%v) = %q, want it to contain %q", c.node, got, c.want)
			}
		}
	})

	// Pi's tests have no key that parses as an address without being canonical (read-plan.test.ts has `"p1"`, which
	// is no key at all, and `"session|p1"`, the key of another kind). This one is derived from Pi's code instead:
	// host-kind.ts addressOfKey (line 108-113) reads an `int()` part with `Number(part)`, so "ticket|007" is the
	// address {n: 7}; keyOfAddress of that address is "ticket|7", and expression.ts isHostKey (line 454-460) holds a
	// key to be that canonical one, so "ticket|007" is refused where "ticket|7" is taken.
	t.Run("takes only the canonical key of an address that a looser key also parses to", func(t *testing.T) {
		ticket, err := contracts.NewHostKind("ticket", contracts.Prop("n", contracts.Int()))
		if err != nil {
			t.Fatal(err)
		}
		tickets, err := DefineColumns("tickets", ColumnSetOptions{
			Columns: []ColumnProp{Col("id", Int()), Col("ticket", Host(ticket))}, Key: []string{"id"},
		})
		if err != nil {
			t.Fatal(err)
		}
		side := Side{Name: tickets.Name, ByName: tickets.ByName}
		if got := ceRefused(t, object("eq", []any{"ticket", "ticket|7"}), side); got != "accepted" {
			t.Errorf("CheckNode of the canonical key = %q", got)
		}
		if got := ceRefused(t, object("eq", []any{"ticket", "ticket|007"}), side); !strings.Contains(got, `"ticket|007" is no address key of ticket`) {
			t.Errorf("CheckNode of a key that parses but is not canonical = %q", got)
		}
	})
}
