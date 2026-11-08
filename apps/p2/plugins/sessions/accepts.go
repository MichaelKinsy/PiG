package sessions

import (
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
)

// Ports the accepts tables of apps/p2/plugins/sessions/server.ts: the older versions of `Catalog` and `Sessions` the
// provider still serves, rows mapped back for consumers built before `/tree`.

// sessionV4 is a row as `Sessions` v4 and `Catalog` v3 consumers read it: a null branch label is the session title.
func sessionV4(value delta.JsonValue) (delta.JsonValue, error) {
	row, ok := value.(*delta.JsonObject)
	if !ok {
		return value, nil
	}
	out := row.Clone()
	title := row.Value("title")
	branches, _ := row.Value("branches").([]any)
	mapped := make([]any, len(branches))
	for i, each := range branches {
		branch, _ := each.(*delta.JsonObject)
		label := branch.Value("label")
		if label == nil {
			label = title
		}
		mapped[i] = delta.JsonObjectOf("conversation", branch.Value("conversation"), "label", label)
	}
	out.Set("branches", mapped)
	return out, nil
}

// sessionV3 is a row as `Sessions` v3 and `Catalog` v2 consumers read it: before `/tree`, without `main` and
// `branches`.
func sessionV3(value delta.JsonValue) (delta.JsonValue, error) {
	row, ok := value.(*delta.JsonObject)
	if !ok {
		return value, nil
	}
	out := row.Clone()
	out.Delete("main")
	out.Delete("branches")
	return out, nil
}

// optionalRow maps a row or null.
func optionalRow(rowOf func(delta.JsonValue) (delta.JsonValue, error)) contracts.Mapping {
	return func(value delta.JsonValue) (delta.JsonValue, error) {
		if value == nil {
			return nil, nil
		}
		return rowOf(value)
	}
}

// rowsOf maps each row of a list.
func rowsOf(rowOf func(delta.JsonValue) (delta.JsonValue, error)) contracts.Mapping {
	return func(value delta.JsonValue) (delta.JsonValue, error) {
		rows, ok := value.([]any)
		if !ok {
			return value, nil
		}
		out := make([]any, len(rows))
		for i, row := range rows {
			mapped, err := rowOf(row)
			if err != nil {
				return nil, err
			}
			out[i] = mapped
		}
		return out, nil
	}
}

// same is a mapping of arguments that are a subset of the current ones (no chosen IDs): used as they are.
func same(value delta.JsonValue) (delta.JsonValue, error) { return value, nil }

func state(name string, mapping contracts.Mapping) contracts.Entry[contracts.StateMapping] {
	return contracts.Entry[contracts.StateMapping]{Name: name, Value: contracts.StateMapping{Func: mapping}}
}

func method(name string, args, result contracts.Mapping) contracts.Entry[contracts.MethodMapping] {
	return contracts.Entry[contracts.MethodMapping]{Name: name, Value: contracts.MethodMapping{Args: args, Result: result}}
}

// catalogAccepts are the `Catalog` consumers built before its current version (a server plugin not reloaded yet): rows
// are mapped back.
func catalogAccepts() contracts.AcceptsTable {
	for_ := func(rowOf func(delta.JsonValue) (delta.JsonValue, error)) contracts.VersionMappings {
		return contracts.VersionMappings{
			State: []contracts.Entry[contracts.StateMapping]{state("sessions", rowsOf(rowOf))},
			Methods: []contracts.Entry[contracts.MethodMapping]{
				method("get", nil, rowOf),
				method("create", nil, rowOf),
				// Older arguments are a subset of the current ones.
				method("update", same, rowOf),
			},
		}
	}
	return contracts.AcceptsTable{{Name: "2", Value: for_(sessionV3)}, {Name: "3", Value: for_(sessionV4)}}
}

// sessionsAccepts are the older `Sessions` consumers (a page or TUI loaded before a deploy, a worker not restarted yet)
// read rows mapped back; v4's `treeSelf` labels are text, which the current version takes as it is. None of them sees
// `operations`.
func sessionsAccepts() contracts.AcceptsTable {
	lifecycle := func(rowOf func(delta.JsonValue) (delta.JsonValue, error)) []contracts.Entry[contracts.MethodMapping] {
		return []contracts.Entry[contracts.MethodMapping]{
			method("create", same, rowOf), method("clone", same, rowOf), method("fork", same, rowOf),
		}
	}
	variants := func(rowOf func(delta.JsonValue) (delta.JsonValue, error)) []contracts.Entry[[]contracts.Entry[contracts.StateMapping]] {
		return []contracts.Entry[[]contracts.Entry[contracts.StateMapping]]{
			{Name: "owner", Value: []contracts.Entry[contracts.StateMapping]{state("sessions", rowsOf(rowOf))}},
			{Name: "guest", Value: []contracts.Entry[contracts.StateMapping]{state("sessions", rowsOf(rowOf))}},
			{Name: "worker", Value: []contracts.Entry[contracts.StateMapping]{state("session", optionalRow(rowOf))}},
		}
	}
	v4Methods := append(lifecycle(sessionV4), method("treeSelf", same, nil))
	return contracts.AcceptsTable{
		{Name: "3", Value: contracts.VersionMappings{VariantState: variants(sessionV3), Methods: lifecycle(sessionV3)}},
		{Name: "4", Value: contracts.VersionMappings{VariantState: variants(sessionV4), Methods: v4Methods}},
		{Name: "5", Value: contracts.VersionMappings{Methods: []contracts.Entry[contracts.MethodMapping]{
			method("create", same, nil), method("clone", same, nil), method("fork", same, nil),
		}}},
	}
}
