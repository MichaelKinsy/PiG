package delta

import (
	"fmt"
	"slices"
	"testing"
)

// Ports the "delta tracker randomized transactions", "object emission scaling" and "security and storage-style cloning" cases of packages/chord/test/delta-tracker/tracker.test.ts onto the overlay Tracker ("defines own properties without invoking inherited setters" is TestTrackerStoresReservedAndInheritedNamesAsOwnData).
func TestTrackerRandomizedTransactions(t *testing.T) {
	// tracker.test.ts:1300-1339 mutate, once on a plain document and once on a draft.
	item := func(value int) *JsonObject { return JsonObjectOf("id", float64(value), "score", float64(value%7)) }
	byID := func(left, right any) int {
		return int(field(t, left, "id").(float64) - field(t, right, "id").(float64))
	}
	mutatePlain := func(document *JsonObject, choice, value int) {
		values := field(t, document, "values").([]any)
		meta := field(t, document, "meta").(*JsonObject)
		switch choice {
		case 0:
			document.Set("text", field(t, document, "text").(string)+fmt.Sprintf("-%d", value))
		case 1:
			document.Set("values", append(values, item(value)))
		case 2:
			document.Set("values", slices.Insert(values, 0, any(item(value))))
		case 3:
			if len(values) > 0 {
				document.Set("values", values[1:])
			}
		case 4:
			if len(values) > 0 {
				document.Set("values", values[:len(values)-1])
			}
		case 5:
			index, remove := 0, 0
			if len(values) > 0 {
				index, remove = value%(len(values)+1), value%2
			}
			document.Set("values", slices.Replace(values, index, min(index+remove, len(values)), any(item(value))))
		case 6:
			slices.Reverse(values)
		case 7:
			slices.SortStableFunc(values, byID)
		case 8:
			if len(values) > 0 {
				values[value%len(values)].(*JsonObject).Set("score", float64(value))
			}
		case 9:
			meta.Set("revision", field(t, meta, "revision").(float64)+1)
			meta.Set("label", fmt.Sprintf("r-%d", value))
		default:
			meta.Delete("label")
		}
	}
	mutateDraft := func(document *Object, choice, value int) {
		values, meta := document.Array("values"), document.Object("meta")
		var err error
		switch choice {
		case 0:
			err = document.Set("text", document.Get("text").(string)+fmt.Sprintf("-%d", value))
		case 1:
			_, err = values.Push(item(value))
		case 2:
			_, err = values.Unshift(item(value))
		case 3:
			if values.Len() > 0 {
				values.Shift()
			}
		case 4:
			if values.Len() > 0 {
				values.Pop()
			}
		case 5:
			index, remove := 0, 0
			if values.Len() > 0 {
				index, remove = value%(values.Len()+1), value%2
			}
			_, err = values.Splice(index, remove, item(value))
		case 6:
			values.Reverse()
		case 7:
			values.Sort(func(left, right any) bool {
				return left.(*Object).Get("id").(float64) < right.(*Object).Get("id").(float64)
			})
		case 8:
			if values.Len() > 0 {
				err = values.Object(value%values.Len()).Set("score", float64(value))
			}
		case 9:
			if err = meta.Set("revision", meta.Get("revision").(float64)+1); err == nil {
				err = meta.Set("label", fmt.Sprintf("r-%d", value))
			}
		default:
			meta.Delete("label")
		}
		mustNoErr(t, err)
	}

	t.Run("matches policy, detached replay, and adopted state after multi-operation transactions", func(t *testing.T) {
		// tracker.test.ts:1341-1374
		for seed := 1; seed <= 40; seed++ {
			rng := mulberry32(int32(seed))
			initial := JsonObjectOf(
				"values", rows(4, func(id int) any { return JsonObjectOf("id", float64(id), "score", 0.0) }),
				"text", "start",
				"meta", JsonObjectOf("revision", 0.0),
			)
			tracker := Track(initial)
			expected := clone(t, initial).(*JsonObject)
			for transaction := range 25 {
				baseRoot := tracker.Value()
				base := textOf(t, baseRoot)
				change := tracker.BeginChange()
				for operation := range 5 {
					choice := int(rng() * 11)
					value := seed*10_000 + transaction*10 + operation
					mutatePlain(expected, choice, value)
					mutateDraft(change.State(), choice, value)
				}
				prepared := mustPrepare(t, change)
				policy := textOf(t, prepared.Value())
				where := fmt.Sprintf("seed %d transaction %d", seed, transaction)
				if textOf(t, baseRoot) != base {
					t.Fatalf("prepare changed base %s", where)
				}
				if want := textOf(t, expected); policy != want {
					t.Fatalf("policy %s: %s, want %s", where, policy, want)
				}
				if replayed := textOf(t, replay(t, jsonOf(t, base), prepared.Ops())); replayed != policy {
					t.Fatalf("replay %s: %s, want %s", where, replayed, policy)
				}
				mustNoErr(t, tracker.Adopt(prepared))
				if textOf(t, baseRoot) != base {
					t.Fatalf("adopt changed base %s", where)
				}
				if tracker.Value() != prepared.Value() || textOf(t, tracker.Value()) != policy {
					t.Fatalf("adopt %s", where)
				}
				expectAliasFree(t, tracker.Value())
			}
		}
	})
}

func TestTrackerObjectEmissionScaling(t *testing.T) {
	t.Run("orders many reverse-depth object edits through the bounded fallback", func(t *testing.T) {
		// tracker.test.ts:1378-1396
		initial := JsonObjectOf("value", 0.0)
		for range 512 {
			initial = JsonObjectOf("value", 0.0, "next", initial)
		}
		tracker := Track(initial)
		change := tracker.BeginChange()
		var nodes []*Object
		for node := change.State(); node != nil; node = node.Object("next") {
			nodes = append(nodes, node)
		}
		for index, node := range slices.Backward(nodes) {
			mustNoErr(t, node.Set("value", float64(index+1)))
		}
		prepared := mustPrepare(t, change)
		if len(prepared.Ops()) != len(nodes) {
			t.Fatalf("%d operations, want one per node (%d)", len(prepared.Ops()), len(nodes))
		}
		expectJSONText(t, replay(t, initial, prepared.Ops()), textOf(t, prepared.Value()))
		mustNoErr(t, tracker.Adopt(prepared))
		expectAliasFree(t, tracker.Value())
	})
}

func TestTrackerSecurityAndStorageStyleCloning(t *testing.T) {
	t.Run("handles reserved own keys without prototype pollution", func(t *testing.T) {
		// tracker.test.ts:1418-1430. No emitted path names a reserved segment: the edit folds at the nearest safe ancestor.
		tracker := Track(jsonObject(t, `{"safe":{"__proto__":{"value":1}}}`))
		change := tracker.BeginChange()
		mustNoErr(t, change.State().Object("safe").Object("__proto__").Set("value", 2.0))
		prepared := mustPrepare(t, change)
		if got := field(t, prepared.Value(), "safe", "__proto__", "value"); got != 2.0 {
			t.Fatalf("safe.__proto__.value = %v", got)
		}
		expectJSONText(t, replay(t, tracker.Value(), prepared.Ops()), textOf(t, prepared.Value()))
		for _, operation := range prepared.Ops() {
			for _, segment := range PathOf(operation) {
				if text, ok := segment.(string); ok && ReservedSegments()[text] {
					t.Fatalf("emitted a reserved segment in %s", opsText(t, []Op{operation}))
				}
			}
		}
		mustNoErr(t, tracker.Adopt(prepared))
	})

	t.Run("supports a MemoryStorage-style recursive clone of Prepared.value", func(t *testing.T) {
		// tracker.test.ts:1432-1458
		var storageClone func(value any) any
		storageClone = func(value any) any {
			switch typed := value.(type) {
			case []any:
				out := make([]any, len(typed))
				for index, item := range typed {
					out[index] = storageClone(item)
				}
				return out
			case *JsonObject:
				out := NewJsonObject(typed.Len())
				for key, item := range typed.All() {
					out.Set(key, storageClone(item))
				}
				return out
			default:
				return value
			}
		}
		tracker := Track(jsonObject(t, `{"rows":[{"value":1}]}`))
		change := tracker.BeginChange()
		rows := change.State().Array("rows")
		mustNoErr(t, rows.Object(0).Set("value", 2.0))
		_, err := rows.Push(jsonOf(t, `{"value":3}`))
		mustNoErr(t, err)
		prepared := mustPrepare(t, change)
		expectJSONText(t, storageClone(prepared.Value()), `{"rows":[{"value":2},{"value":3}]}`)
		mustNoErr(t, tracker.Adopt(prepared))
	})
}

// An object placed at several keys from one draft container gives independent copies, whichever key is emitted first.
func TestTrackerAliasedDraftPlacementsStayIndependent(t *testing.T) {
	for range 50 {
		tracker := Track(jsonObject(t, `{"a":{"child":{"value":1}},"b":null,"c":null}`))
		change := tracker.BeginChange()
		state := change.State()
		mustNoErr(t, state.Set("b", state.Object("a")))
		mustNoErr(t, state.Set("c", state.Object("a")))
		prepared := mustPrepare(t, change)
		expectAliasFree(t, prepared.Value())
		expectJSONText(t, prepared.Value(), `{"a":{"child":{"value":1}},"b":{"child":{"value":1}},"c":{"child":{"value":1}}}`)
		expectJSONText(t, replay(t, tracker.Value(), prepared.Ops()), textOf(t, prepared.Value()))
	}
}
