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
// Not ported here (they need the ReadPlan and the join, in the commits of read-plan.ts and query.ts): the `compile`
// line of the host-key case, the `query(Sessions).whereNode(TeamSessions, clubOnly)` line of the malformed case.

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
}
