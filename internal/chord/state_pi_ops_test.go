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

// Replicated state against Pi's tracker.ts. The golden batches come from chord/delta/testdata/pi_change_operations.json (regenerated from the pinned mirror by pi_change_operations.mjs): 1,500 seeded edit scripts with the exact operation batch Pi records and the value it produces.
//
// Edit drives the overlay draft, so it must publish Pi's batch exactly. Change edits a typed value, whose edit history Go cannot observe, so it must publish a batch that replays to Pi's value and is accepted by an ordered replica.

type piStateCase struct {
	Seed    int                 `json:"seed"`
	Initial *chordjson.Object   `json:"initial"`
	Steps   [][]json.RawMessage `json:"steps"`
	Ops     delta.Ops           `json:"ops"`
	Value   *chordjson.Object   `json:"value"`
}

// loadPiStateCases reads both golden sets: the ordinary scripts and the --folds scripts with reserved-key folds and dense-region splices.
func loadPiStateCases(t *testing.T) []piStateCase {
	t.Helper()
	var cases []piStateCase
	for _, name := range []string{"pi_change_operations.json", "pi_change_operations_folds.json"} {
		data, err := os.ReadFile("../../chord/delta/testdata/" + name)
		if err != nil {
			t.Fatal(err)
		}
		var set []piStateCase
		if err := json.Unmarshal(data, &set); err != nil {
			t.Fatal(err)
		}
		cases = append(cases, set...)
	}
	return cases
}

func piStateJSON(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

// publications records the batches a state publishes after its hydration.
func publications[T any](t *testing.T, state *MutableReplicatedState[T]) *[][]delta.Op {
	t.Helper()
	var batches [][]delta.Op
	state.core.subscribeOps(func(_ context.Context, ops []Op, _ int) {
		batches = append(batches, slices.Clone(ops))
	})
	return &batches
}

func TestEditPublishesPiOperationsForRandomizedEdits(t *testing.T) {
	var failures []string
	for _, c := range loadPiStateCases(t) {
		state, err := NewReplicatedState(c.Initial)
		if err != nil {
			t.Fatal(err)
		}
		batches := publications(t, state)
		err = state.Edit(context.Background(), func(root *delta.Object) error {
			for _, raw := range c.Steps {
				applyPiHandleStep(t, root, raw)
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

func TestChangeReplaysToPiValuesForRandomizedEdits(t *testing.T) {
	for _, c := range loadPiStateCases(t) {
		state, err := NewReplicatedState(c.Initial)
		if err != nil {
			t.Fatal(err)
		}
		replica := piStateJSON(t, c.Initial)
		batches := publications(t, state)
		err = state.Change(context.Background(), func(root *chordjson.Object) error {
			for _, raw := range c.Steps {
				applyPiMapStep(t, root, raw)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("seed %d: %v", c.Seed, err)
		}
		if got, want := piStateJSON(t, state.Value()), piStateJSON(t, c.Value); got != want {
			t.Fatalf("seed %d: value %s != Pi %s", c.Seed, got, want)
		}
		replayed, _ := chordjson.Decode([]byte(replica))
		for _, batch := range *batches {
			var err error
			if replayed, err = delta.ApplyImmutable(replayed, batch); err != nil {
				t.Fatalf("seed %d: replay %v: %v", c.Seed, batch, err)
			}
		}
		if got, want := piStateJSON(t, replayed), piStateJSON(t, c.Value); got != want {
			t.Fatalf("seed %d: replayed %s != Pi %s", c.Seed, got, want)
		}
	}
}

func piHandleParent(root *delta.Object, path []any) *delta.Object {
	object := root
	for _, key := range path {
		object = object.Object(key.(string))
	}
	return object
}

func piHandleArray(root *delta.Object, path []any) *delta.Array {
	return piHandleParent(root, path[:len(path)-1]).Array(path[len(path)-1].(string))
}

func applyPiHandleStep(t *testing.T, root *delta.Object, raw []json.RawMessage) {
	t.Helper()
	var kind string
	var path []any
	_ = json.Unmarshal(raw[0], &kind)
	_ = json.Unmarshal(raw[1], &path)
	rest := make([]any, len(raw)-2)
	for index, element := range raw[2:] {
		rest[index], _ = chordjson.Decode(element)
	}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	if kind == "set" || kind == "delete" {
		parent := piHandleParent(root, path[:len(path)-1])
		key := path[len(path)-1].(string)
		if kind == "set" {
			must(parent.Set(key, rest[0]))
		} else {
			parent.Delete(key)
		}
		return
	}
	if kind == "rowRange" {
		rows := root.Array("rows")
		for index := int(rest[0].(float64)); index < int(rest[1].(float64)); index++ {
			must(rows.Object(index).Set("value", rest[2].(float64)+float64(index%7)))
		}
		return
	}
	if kind == "rowField" {
		rows := root.Array("rows")
		if row := rows.Object(int(rest[0].(float64))); row != nil {
			must(row.Set(rest[1].(string), rest[2]))
		}
		return
	}
	if kind == "text" {
		parent := piHandleParent(root, path[:len(path)-1])
		key := path[len(path)-1].(string)
		current, _ := parent.Get(key).(string)
		switch rest[0] {
		case "append":
			current += rest[1].(string)
		case "cut":
			current = current[:max(len(current)-1, 0)] + rest[1].(string)
		default:
			current = rest[1].(string)
		}
		must(parent.Set(key, current))
		return
	}
	array := piHandleArray(root, path)
	rows := path[0] == "rows"
	number := func(index int) int { return int(rest[index].(float64)) }
	switch kind {
	case "push":
		_, err := array.Push(rest[0].([]any)...)
		must(err)
	case "unshift":
		_, err := array.Unshift(rest[0].([]any)...)
		must(err)
	case "shift":
		array.Shift()
	case "pop":
		array.Pop()
	case "splice":
		_, err := array.Splice(number(0), number(1), rest[2].([]any)...)
		must(err)
	case "reverse":
		array.Reverse()
	case "sort":
		array.Sort(func(a, b any) bool {
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
		must(array.Set(number(0), rest[1]))
	case "append":
		if rows {
			row := array.Object(0).Snapshot()
			row.Set("value", rest[1])
			must(array.Set(0, row))
		} else {
			must(array.Set(0, rest[1]))
		}
	default:
		t.Fatalf("unknown step %q", kind)
	}
}

// applyPiMapStep performs one script step on the typed (*chordjson.Object) value of a Change.
func applyPiMapStep(t *testing.T, root *chordjson.Object, raw []json.RawMessage) {
	t.Helper()
	var kind string
	var path []any
	_ = json.Unmarshal(raw[0], &kind)
	_ = json.Unmarshal(raw[1], &path)
	rest := make([]any, len(raw)-2)
	for index, element := range raw[2:] {
		rest[index], _ = chordjson.Decode(element)
	}
	parentOf := func(path []any) *chordjson.Object {
		current := root
		for _, key := range path {
			current = current.Value(key.(string)).(*chordjson.Object)
		}
		return current
	}
	number := func(index int) int { return int(rest[index].(float64)) }
	switch kind {
	case "set":
		parentOf(path[:len(path)-1]).Set(path[len(path)-1].(string), rest[0])
		return
	case "delete":
		parentOf(path[:len(path)-1]).Delete(path[len(path)-1].(string))
		return
	case "rowRange":
		rows := root.Value("rows").([]any)
		for index := number(0); index < number(1); index++ {
			rows[index].(*chordjson.Object).Set("value", rest[2].(float64)+float64(index%7))
		}
		return
	case "rowField":
		rows := root.Value("rows").([]any)
		if index := number(0); index < len(rows) {
			rows[index].(*chordjson.Object).Set(rest[1].(string), rest[2])
		}
		return
	case "text":
		parent, key := parentOf(path[:len(path)-1]), path[len(path)-1].(string)
		current, _ := parent.Value(key).(string)
		switch rest[0] {
		case "append":
			current += rest[1].(string)
		case "cut":
			current = current[:max(len(current)-1, 0)] + rest[1].(string)
		default:
			current = rest[1].(string)
		}
		parent.Set(key, current)
		return
	}
	parent, key := parentOf(path[:len(path)-1]), path[len(path)-1].(string)
	array := parent.Value(key).([]any)
	rows := path[0] == "rows"
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
	case "append":
		if rows {
			row := array[0].(*chordjson.Object).Clone()
			row.Set("value", rest[1])
			array[0] = row
		} else {
			array[0] = rest[1]
		}
	default:
		t.Fatalf("unknown step %q", kind)
	}
	if array == nil {
		array = []any{}
	}
	parent.Set(key, array)
}

// tracker.test.ts lifecycle on the Edit path: a settled draft is revoked, a failed or panicking edit aborts without publishing, an edit that restores the prior value publishes nothing, and an array root has no draft.
func TestEditLifecycle(t *testing.T) {
	ctx := context.Background()
	newState := func(t *testing.T) (*MutableReplicatedState[map[string]any], *[][]delta.Op) {
		t.Helper()
		state, err := NewReplicatedState(map[string]any{"count": 1.0, "items": []any{1.0, 2.0}})
		if err != nil {
			t.Fatal(err)
		}
		return state, publications(t, state)
	}
	t.Run("publishes the recorded batch and revokes the draft", func(t *testing.T) {
		state, batches := newState(t)
		var held *delta.Object
		if err := state.Edit(ctx, func(root *delta.Object) error {
			held = root
			return root.Set("count", 2.0)
		}); err != nil {
			t.Fatal(err)
		}
		if got := piStateJSON(t, *batches); got != `[[["s",["count"],2]]]` {
			t.Fatalf("batches %s", got)
		}
		defer func() {
			if recovered := recover(); recovered == nil {
				t.Fatal("a settled draft must be revoked")
			}
		}()
		_ = held.Set("count", 3.0)
	})
	t.Run("rolls back callback failures and revokes escaped drafts", func(t *testing.T) {
		// state.test.ts:34 on the draft path.
		state, err := NewReplicatedState(map[string]any{"nested": map[string]any{"value": 1.0}})
		if err != nil {
			t.Fatal(err)
		}
		batches := publications(t, state)
		previous := state.core.tracker.Value()
		var escaped *delta.Object
		err = state.Edit(ctx, func(root *delta.Object) error {
			escaped = root.Object("nested")
			if err := escaped.Set("value", 2.0); err != nil {
				return err
			}
			return errors.New("stop")
		})
		if err == nil || err.Error() != "stop" {
			t.Fatalf("err %v", err)
		}
		if got := state.core.tracker.Value(); container(got) != container(previous) || len(*batches) != 0 || state.Sequence() != 0 {
			t.Fatalf("a failed edit was published: %v %v", got, *batches)
		}
		defer func() {
			if recovered := recover(); !errors.Is(asError(recovered), delta.ErrRevoked) {
				t.Fatalf("an escaped draft must be revoked, recovered %v", recovered)
			}
		}()
		_ = escaped.Get("value")
	})
	t.Run("aborts on an error or a panic and keeps the value", func(t *testing.T) {
		state, batches := newState(t)
		boom := errors.New("boom")
		if err := state.Edit(ctx, func(root *delta.Object) error {
			_ = root.Set("count", 9.0)
			return boom
		}); !errors.Is(err, boom) {
			t.Fatalf("err %v", err)
		}
		if err := state.Edit(ctx, func(root *delta.Object) error {
			_ = root.Set("count", 9.0)
			panic("kaput")
		}); err == nil || !strings.Contains(err.Error(), "kaput") {
			t.Fatalf("err %v", err)
		}
		if got := piStateJSON(t, state.Value()); got != `{"count":1,"items":[1,2]}` || len(*batches) != 0 {
			t.Fatalf("value %s batches %v", got, *batches)
		}
	})
	t.Run("publishes nothing when the edits restore the value", func(t *testing.T) {
		state, batches := newState(t)
		before := state.Sequence()
		if err := state.Edit(ctx, func(root *delta.Object) error {
			if err := root.Set("count", 5.0); err != nil {
				return err
			}
			return root.Set("count", 1.0)
		}); err != nil {
			t.Fatal(err)
		}
		if len(*batches) != 0 || state.Sequence() != before {
			t.Fatalf("batches %v sequence %d", *batches, state.Sequence())
		}
	})
	t.Run("rejects reentrant changes and array roots", func(t *testing.T) {
		state, _ := newState(t)
		err := state.Edit(ctx, func(*delta.Object) error {
			return state.Edit(ctx, func(*delta.Object) error { return nil })
		})
		if err == nil || !strings.Contains(err.Error(), "reentrantly") {
			t.Fatalf("err %v", err)
		}
		array, err := NewReplicatedState([]any{1.0})
		if err != nil {
			t.Fatal(err)
		}
		if err := array.Edit(ctx, func(*delta.Object) error { return nil }); err == nil {
			t.Fatal("an array root has no object draft")
		}
	})
	t.Run("keeps a typed Change on an array root working", func(t *testing.T) {
		array, err := NewReplicatedState([]any{1.0})
		if err != nil {
			t.Fatal(err)
		}
		if err := array.Change(ctx, func(values []any) error { values[0] = 2.0; return nil }); err != nil {
			t.Fatal(err)
		}
		if got := piStateJSON(t, array.Value()); got != `[2]` {
			t.Fatalf("value %s", got)
		}
	})
}

func asError(value any) error {
	err, _ := value.(error)
	return err
}

// tracker.ts prepareReplace takes any object root, so replacing an object root with an array (or back) records one root replacement; the state keeps working on the new root, including the draft path once the root is an object again. Probed against the pinned tracker.ts: [["r",[1,{"b":2}]]], then [["r",{"a":1}]], then [] for an equal replacement.
func TestReplaceChangesTheRootKind(t *testing.T) {
	ctx := context.Background()
	state, err := NewReplicatedState[any](map[string]any{"a": 1.0})
	if err != nil {
		t.Fatal(err)
	}
	var batches [][]delta.Op
	state.core.subscribeOps(func(_ context.Context, ops []Op, _ int) { batches = append(batches, slices.Clone(ops)) })
	if err := state.Replace(ctx, []any{1.0, map[string]any{"b": 2.0}}); err != nil {
		t.Fatal(err)
	}
	if err := state.Change(ctx, func(value any) error {
		value.([]any)[1].(*chordjson.Object).Set("b", 3.0)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := state.Replace(ctx, map[string]any{"a": 1.0}); err != nil {
		t.Fatal(err)
	}
	if err := state.Replace(ctx, map[string]any{"a": 1.0}); err != nil {
		t.Fatal(err)
	}
	if err := state.Edit(ctx, func(root *delta.Object) error { return root.Set("b", 2.0) }); err != nil {
		t.Fatal(err)
	}
	want := `[[["r",[1,{"b":2}]]],[["s",[1,"b"],3]],[["r",{"a":1}]],[["s",["b"],2]]]`
	if got := piStateJSON(t, batches); got != want {
		t.Fatalf("batches %s\nwant    %s", got, want)
	}
	if got := piStateJSON(t, state.Value()); got != `{"a":1,"b":2}` {
		t.Fatalf("value %s", got)
	}
}
