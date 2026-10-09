package delta

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
)

// Differential test against Pi: the operation batch tracker.ts records for a seeded edit script (testdata/pi_change_operations.json, regenerated from the pinned mirror by pi_change_operations.mjs) must equal the batch this overlay tracker records for the same edits: order, paths and values. testdata/pi_change_operations_folds.json (the --folds scripts) adds edits below reserved keys and runs of row edits, which tracker.ts folds into an ancestor set or a dense-region splice.

type piChangeCase struct {
	Seed    int                 `json:"seed"`
	Initial *JsonObject         `json:"initial"`
	Steps   [][]json.RawMessage `json:"steps"`
	Ops     Ops                 `json:"ops"`
	Value   *JsonObject         `json:"value"`
}

func piParent(root *Object, path []any) *Object {
	object := root
	for _, key := range path {
		object = object.Object(key.(string))
	}
	return object
}

func piArray(root *Object, path []any) *Array {
	return piParent(root, path[:len(path)-1]).Array(path[len(path)-1].(string))
}

func applyPiStep(t *testing.T, root *Object, raw []json.RawMessage) {
	t.Helper()
	var kind string
	var path []any
	_ = json.Unmarshal(raw[0], &kind)
	_ = json.Unmarshal(raw[1], &path)
	rest := make([]any, len(raw)-2)
	for index, element := range raw[2:] {
		_ = json.Unmarshal(element, &rest[index])
	}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	if kind == "set" || kind == "delete" {
		parent := piParent(root, path[:len(path)-1])
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
		parent := piParent(root, path[:len(path)-1])
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
	array := piArray(root, path)
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
				left, right := a.(*Object), b.(*Object)
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

func piJSON(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func TestTrackerOperationsMatchPiForRandomizedEdits(t *testing.T) {
	for _, name := range []string{"pi_change_operations.json", "pi_change_operations_folds.json"} {
		t.Run(name, func(t *testing.T) { testTrackerOperationsMatchPi(t, "testdata/"+name) })
	}
}

func testTrackerOperationsMatchPi(t *testing.T, path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cases []piChangeCase
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	var failures []string
	for _, c := range cases {
		change := Track(c.Initial).BeginChange()
		for _, raw := range c.Steps {
			applyPiStep(t, change.State(), raw)
		}
		prepared, err := change.Prepare()
		if err != nil {
			t.Fatalf("seed %d: %v", c.Seed, err)
		}
		if got, want := piJSON(t, prepared.Value()), piJSON(t, c.Value); got != want {
			t.Fatalf("seed %d: value %s != Pi %s", c.Seed, got, want)
		}
		if got, want := piJSON(t, prepared.Ops()), piJSON(t, c.Ops); got != want {
			failures = append(failures, fmt.Sprintf("seed %d steps %v\n   Pi %s\n   Go %s", c.Seed, c.Steps, want, got))
		}
	}
	if len(failures) > 0 {
		t.Fatalf("%d of %d cases record different operations than Pi:\n%s", len(failures), len(cases), strings.Join(failures[:min(len(failures), 6)], "\n"))
	}
}
