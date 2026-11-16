package store

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
)

// Tests of the `exists` node of expression.ts that read-plan.test.ts reaches only through the read plan (the commits
// of read-plan.ts and reads.ts): its data form, the columns and the `exists` a condition holds, the in-memory
// evaluator's refusal (Pi has no test of it: evaluate.ts throws "exists is not evaluated in memory").

func rpCompiled(t *testing.T, f rpSets, where func(s, _ ColumnRefs) Condition) WhereNode {
	t.Helper()
	q := NewQuery(f.Sessions).Where(where)
	must(t, q.Err())
	return *q.Condition
}

func TestReadPlanExistsData(t *testing.T) {
	f := rpFixtures(t)
	node := rpCompiled(t, f, func(s, _ ColumnRefs) Condition {
		return And(Eq(s["app"], "club"), Not(Exists(f.TeamSessions, Match{{"session", s["id"]}}, nil)),
			Or(IsNull(s["archivedAt"]), Exists(f.TeamSessions, Match{{"team", s["app"]}}, func(t ColumnRefs) Condition { return Eq(t["team"], "u14") })))
	})

	t.Run("an exists is written and read back as its JSON", func(t *testing.T) {
		want := `{"and":[{"eq":["app","club"]},{"not":{"exists":["club.team-sessions",[["session","id"]],null]}},` +
			`{"or":[{"isNull":"archivedAt"},{"exists":["club.team-sessions",[["team","app"]],{"eq":["team","u14"]}]}]}]}`
		if got := rpJSON(t, node); got != want {
			t.Fatalf("JSON = %s, want %s", got, want)
		}
		decoded, err := delta.DecodeJson([]byte(want))
		must(t, err)
		back, err := DecodeWhereNode(decoded)
		must(t, err)
		qEqual(t, back, node)
	})

	t.Run("a malformed exists is not decoded", func(t *testing.T) {
		for _, text := range []string{
			`{"exists":["club.team-sessions",[["session","id"]]]}`,
			`{"exists":[5,[["session","id"]],null]}`,
			`{"exists":["club.team-sessions","session",null]}`,
			`{"exists":["club.team-sessions",["session"],null]}`,
			`{"exists":["club.team-sessions",[["session",5]],null]}`,
			`{"exists":["club.team-sessions",[[5,"id"]],null]}`,
			`{"exists":["club.team-sessions",[["session","id"]],5]}`,
		} {
			raw, err := delta.DecodeJson([]byte(text))
			must(t, err)
			if _, err := DecodeWhereNode(raw); err == nil {
				t.Errorf("DecodeWhereNode(%s) accepted", text)
			}
		}
	})

	t.Run("a condition uses the columns of its own side, the exists' matched ones included, not the inner's", func(t *testing.T) {
		qEqual(t, ColumnsOf(node), []string{"app", "id", "archivedAt", "app"})
		qEqual(t, ColumnsOf(WhereNode{Op: WhereNot, Nodes: []WhereNode{{Op: WhereEq, Column: "a", Value: "x"}}}), []string{"a"})
	})

	t.Run("an exists is found where the condition tests it on its own side", func(t *testing.T) {
		found := ExistsIn(node)
		if len(found) != 2 || found[0].Pairs[0].Column != "id" || found[1].Pairs[0].Column != "app" {
			t.Fatalf("ExistsIn = %v", found)
		}
		// The exists nested in an exists' inner condition is the other side's: not found here.
		nested := rpCompiled(t, f, func(s, _ ColumnRefs) Condition {
			return Exists(f.TeamSessions, Match{{"session", s["id"]}}, func(t ColumnRefs) Condition {
				return Exists(f.SessionColumns, Match{{"id", t["session"]}}, nil)
			})
		})
		if found := ExistsIn(nested); len(found) != 1 || found[0].Inner == nil || len(ExistsIn(*found[0].Inner)) != 1 {
			t.Errorf("ExistsIn of a nested exists = %v", found)
		}
	})

	t.Run("is not evaluated in memory", func(t *testing.T) {
		defer func() {
			got := recover()
			err, isError := got.(error)
			if !isError || !strings.Contains(err.Error(), "exists is not evaluated in memory") {
				t.Errorf("recovered %v", got)
			}
		}()
		Matches(node, EvaluatedRow{"app": "club"})
		t.Error("Matches of an exists did not panic")
	})

	t.Run("a hand-built condition with no set or no match is refused, in a query", func(t *testing.T) {
		for name, c := range map[string]struct {
			condition Condition
			want      string
		}{
			"no set":         {Condition{Op: WhereExists, Args: []any{nil, Match{}, nil}}, "exists needs a column set"},
			"a nil set":      {Condition{Op: WhereExists, Args: []any{(*ColumnSet)(nil), Match{}, nil}}, "exists needs a column set"},
			"no match":       {Condition{Op: WhereExists, Args: []any{f.TeamSessions, nil, nil}}, "exists needs a match"},
			"an empty match": {Condition{Op: WhereExists, Args: []any{f.TeamSessions, Match{}, nil}}, "no leading prefix"},
		} {
			q := NewQuery(f.Sessions).Where(func(_, _ ColumnRefs) Condition { return c.condition })
			if got := rpMessage(t, q.Err()); !strings.Contains(got, c.want) {
				t.Errorf("%s: %q", name, got)
			}
		}
	})

	t.Run("a reference of a set the table does not expose is no column of it, whatever its name", func(t *testing.T) {
		other, err := DefineColumns("other", ColumnSetOptions{Columns: []ColumnProp{Col("id", Str())}, Key: []string{"id"}})
		must(t, err)
		q := NewQuery(f.Sessions).Where(func(_, _ ColumnRefs) Condition { return Eq(other.Refs["id"], "x") })
		if got := rpMessage(t, q.Err()); !strings.Contains(got, `no column "id"`) {
			t.Errorf("a ref of another set: %q", got)
		}
		_, err = CompileWhere(f.Notes, Eq(other.Refs["id"], 1), "where")
		if got := rpMessage(t, err); !strings.Contains(got, `no column "id"`) {
			t.Errorf("a ref of a set on a table that exposes none: %q", got)
		}
	})
}
