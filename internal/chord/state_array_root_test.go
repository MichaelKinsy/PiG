package chord

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

// Array-rooted replicated state against Pi's tracker.ts (track([...]), replicatedState(array)). The goldens are chord/delta/testdata/pi_array_root_operations.json: 800 seeded edit scripts over an array root with the exact batch and value the pinned tracker.ts produces.

type piArrayStateCase struct {
	Seed    int                 `json:"seed"`
	Rows    bool                `json:"rows"`
	Initial []any               `json:"initial"`
	Steps   [][]json.RawMessage `json:"steps"`
	Ops     delta.Ops           `json:"ops"`
	Value   []any               `json:"value"`
}

func loadPiArrayStateCases(t *testing.T) []piArrayStateCase {
	t.Helper()
	data, err := os.ReadFile("../../chord/delta/testdata/pi_array_root_operations.json")
	if err != nil {
		t.Fatal(err)
	}
	var raw []struct {
		piArrayStateCase
		Initial json.RawMessage `json:"initial"`
		Value   json.RawMessage `json:"value"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	// Pi's rows are {id, value, tags} in that order, so the values are decoded with their key order.
	cases := make([]piArrayStateCase, len(raw))
	for index, entry := range raw {
		cases[index] = entry.piArrayStateCase
		cases[index].Initial = decodeOrdered(t, entry.Initial).([]any)
		cases[index].Value = decodeOrdered(t, entry.Value).([]any)
	}
	return cases
}

func decodeOrdered(t *testing.T, data []byte) any {
	t.Helper()
	value, err := chordjson.Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func decodeArrayStep(raw []json.RawMessage) (string, []any) {
	var kind string
	_ = json.Unmarshal(raw[0], &kind)
	rest := make([]any, len(raw)-1)
	for index, element := range raw[1:] {
		rest[index], _ = chordjson.Decode(element)
	}
	return kind, rest
}

func arrayPublications(state *MutableReplicatedState[[]any]) *[][]delta.Op {
	var batches [][]delta.Op
	state.core.subscribeOps(func(_ context.Context, ops []Op, _ int) {
		batches = append(batches, slices.Clone(ops))
	})
	return &batches
}

func applyArrayHandleStep(t *testing.T, root *delta.Array, rows bool, raw []json.RawMessage) {
	t.Helper()
	kind, rest := decodeArrayStep(raw)
	number := func(index int) int { return int(rest[index].(float64)) }
	var err error
	switch kind {
	case "push":
		_, err = root.Push(rest[0].([]any)...)
	case "unshift":
		_, err = root.Unshift(rest[0].([]any)...)
	case "shift":
		root.Shift()
	case "pop":
		root.Pop()
	case "splice":
		_, err = root.Splice(number(0), number(1), rest[2].([]any)...)
	case "reverse":
		root.Reverse()
	case "sort":
		root.Sort(func(a, b any) bool {
			if rows {
				left, right := a.(*delta.Object), b.(*delta.Object)
				if lv, rv := left.Get("value").(float64), right.Get("value").(float64); lv != rv {
					return lv < rv
				}
				return left.Get("id").(float64) < right.Get("id").(float64)
			}
			return a.(float64) < b.(float64)
		})
	case "setIndex":
		err = root.Set(number(0), rest[1])
	case "rowField":
		err = root.Object(number(0)).Set(rest[1].(string), rest[2])
	case "tag":
		_, err = root.Object(number(0)).Array("tags").Push(rest[1])
	case "length":
		root.SetLen(number(0))
	default:
		t.Fatalf("unknown step %q", kind)
	}
	if err != nil {
		t.Fatal(err)
	}
}

// applyArraySliceStep performs one script step on the typed ([]any) value of a Change.
func applyArraySliceStep(t *testing.T, array []any, rows bool, raw []json.RawMessage) []any {
	t.Helper()
	kind, rest := decodeArrayStep(raw)
	number := func(index int) int { return int(rest[index].(float64)) }
	switch kind {
	case "push":
		array = append(array, rest[0].([]any)...)
	case "unshift":
		array = append(slices.Clone(rest[0].([]any)), array...)
	case "shift":
		if len(array) > 0 {
			array = array[1:]
		}
	case "pop":
		if len(array) > 0 {
			array = array[:len(array)-1]
		}
	case "splice":
		start, remove := number(0), number(1)
		if start < 0 {
			start = max(len(array)+start, 0)
		}
		start = min(start, len(array))
		remove = min(remove, len(array)-start)
		array = slices.Concat(array[:start], rest[2].([]any), array[start+remove:])
	case "reverse":
		slices.Reverse(array)
	case "sort":
		slices.SortStableFunc(array, func(a, b any) int {
			if rows {
				left, right := a.(*chordjson.Object), b.(*chordjson.Object)
				if lv, rv := left.Value("value").(float64), right.Value("value").(float64); lv != rv {
					return int(lv - rv)
				}
				return int(left.Value("id").(float64) - right.Value("id").(float64))
			}
			return int(a.(float64) - b.(float64))
		})
	case "setIndex":
		if number(0) == len(array) {
			array = append(array, rest[1])
		} else {
			array[number(0)] = rest[1]
		}
	case "rowField":
		array[number(0)].(*chordjson.Object).Set(rest[1].(string), rest[2])
	case "tag":
		row := array[number(0)].(*chordjson.Object)
		row.Set("tags", append(row.Value("tags").([]any), rest[1]))
	case "length":
		array = array[:number(0)]
	default:
		t.Fatalf("unknown step %q", kind)
	}
	if array == nil {
		array = []any{}
	}
	return array
}

func TestEditArrayPublishesPiOperationsForRandomizedEdits(t *testing.T) {
	var failures []string
	for _, c := range loadPiArrayStateCases(t) {
		state, err := NewReplicatedState(c.Initial)
		if err != nil {
			t.Fatal(err)
		}
		batches := arrayPublications(state)
		err = state.EditArray(context.Background(), func(root *delta.Array) error {
			for _, raw := range c.Steps {
				applyArrayHandleStep(t, root, c.Rows, raw)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("seed %d: %v", c.Seed, err)
		}
		if got, want := piStateJSON(t, state.Value()), piStateJSON(t, c.Value); got != want {
			t.Fatalf("seed %d: value %s != Pi %s", c.Seed, got, want)
		}
		var want [][]delta.Op
		if len(c.Ops) > 0 {
			want = [][]delta.Op{c.Ops}
		}
		if got := piStateJSON(t, *batches); got != piStateJSON(t, want) {
			failures = append(failures, fmt.Sprintf("seed %d\n   Pi %s\n   Go %s", c.Seed, piStateJSON(t, want), got))
		}
	}
	if len(failures) > 0 {
		t.Fatalf("%d cases publish different operations than Pi:\n%s", len(failures), strings.Join(failures[:min(len(failures), 4)], "\n"))
	}
}

// A typed array root is a pointer to a slice, so a Change can grow the array.
func TestChangeOnAnArrayRootReplaysToPiValues(t *testing.T) {
	for _, c := range loadPiArrayStateCases(t) {
		initial := slices.Clone(c.Initial)
		state, err := NewReplicatedState(&initial)
		if err != nil {
			t.Fatal(err)
		}
		replayed := decodeOrdered(t, []byte(piStateJSON(t, c.Initial)))
		var batches [][]delta.Op
		state.core.subscribeOps(func(_ context.Context, ops []Op, _ int) { batches = append(batches, slices.Clone(ops)) })
		err = state.Change(context.Background(), func(array *[]any) error {
			for _, raw := range c.Steps {
				*array = applyArraySliceStep(t, *array, c.Rows, raw)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("seed %d: %v", c.Seed, err)
		}
		if got, want := piStateJSON(t, state.Value()), piStateJSON(t, c.Value); got != want {
			t.Fatalf("seed %d: value %s != Pi %s", c.Seed, got, want)
		}
		for _, batch := range batches {
			if replayed, err = delta.ApplyImmutable(replayed, batch); err != nil {
				t.Fatalf("seed %d: replay %v: %v", c.Seed, batch, err)
			}
		}
		if got, want := piStateJSON(t, replayed), piStateJSON(t, c.Value); got != want {
			t.Fatalf("seed %d: replayed %s != Pi %s", c.Seed, got, want)
		}
	}
}

func TestEditArrayLifecycle(t *testing.T) {
	ctx := context.Background()
	array, err := NewReplicatedState([]any{1.0, 2.0})
	if err != nil {
		t.Fatal(err)
	}
	batches := arrayPublications(array)
	if err := array.EditArray(ctx, func(root *delta.Array) error {
		_, err := root.Push(3.0)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if got := piStateJSON(t, *batches); got != `[[["p",[],2,0,[3]]]]` {
		t.Fatalf("batches %s", got)
	}
	boom := errors.New("boom")
	if err := array.EditArray(ctx, func(root *delta.Array) error {
		root.Reverse()
		return boom
	}); !errors.Is(err, boom) || piStateJSON(t, array.Value()) != `[1,2,3]` {
		t.Fatalf("err %v value %s", err, piStateJSON(t, array.Value()))
	}
	if err := array.Edit(ctx, func(*delta.Object) error { return nil }); err == nil {
		t.Fatal("Edit needs an object root")
	}
	object, err := NewReplicatedState(map[string]any{"a": 1.0})
	if err != nil {
		t.Fatal(err)
	}
	if err := object.EditArray(ctx, func(*delta.Array) error { return nil }); err == nil {
		t.Fatal("EditArray needs an array root")
	}
}
