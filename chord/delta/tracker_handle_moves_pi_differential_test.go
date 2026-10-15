package delta

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

type piHandleMoveStep struct {
	Edit  []json.RawMessage `json:"edit"`
	Error *string           `json:"error"`
}

type piHandleMoveCase struct {
	Initial *JsonObject        `json:"initial"`
	Steps   []piHandleMoveStep `json:"steps"`
	Ops     Ops                `json:"ops"`
	Value   *JsonObject        `json:"value"`
}

// draftAt walks a draft by path, as the oracle's at(): nil when a segment is missing or not a container.
func draftAt(root *Object, path []any) any {
	var current any = root
	for _, segment := range path {
		switch container := current.(type) {
		case *Object:
			key, ok := segment.(string)
			if !ok {
				return nil
			}
			current = container.Get(key)
		case *Array:
			index, ok := segment.(float64)
			if !ok || int(index) >= container.Len() {
				return nil
			}
			current = container.Get(int(index))
		default:
			return nil
		}
	}
	return current
}

// applyHandleMoveStep replays one oracle step; it returns the error the step raised, also when it panicked.
func applyHandleMoveStep(root, other *Object, raw []json.RawMessage) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("panic: %v", recovered)
		}
	}()
	var kind string
	var path []any
	_ = json.Unmarshal(raw[0], &kind)
	_ = json.Unmarshal(raw[1], &path)
	rest := make([]any, len(raw)-2)
	for index, element := range raw[2:] {
		_ = json.Unmarshal(element, &rest[index])
	}
	source := func(value any) any {
		path, _ := value.([]any)
		from := root
		if len(path) > 0 && path[0] == "@other" {
			from, path = other, path[1:]
		}
		switch found := draftAt(from, path).(type) {
		case *Object, *Array:
			return found
		}
		return nil
	}
	number := func(index int) int { return int(rest[index].(float64)) }
	switch kind {
	case "place", "set", "delete":
		parent := draftAt(root, path).(*Object)
		key := rest[0].(string)
		switch kind {
		case "delete":
			parent.Delete(key)
			return nil
		case "set":
			return parent.Set(key, plainValue(rest[1]))
		}
		return parent.Set(key, source(rest[1]))
	case "edit":
		target, _ := source(path).(*Object)
		if target == nil {
			return fmt.Errorf("edit: %v is not an object", path)
		}
		return target.Set(rest[0].(string), rest[1])
	}
	array := draftAt(root, path).(*Array)
	switch kind {
	case "push":
		_, err := array.Push(source(rest[0]))
		return err
	case "setIndex":
		return array.Set(number(0), source(rest[1]))
	case "splice":
		_, err := array.Splice(number(0), number(1), source(rest[2]))
		return err
	case "fill":
		spec := rest[0].(map[string]any)
		value := plainValue(spec["value"])
		if path, ok := spec["source"]; ok {
			value = source(path)
		}
		return array.Fill(value, number(1), number(2))
	case "copyWithin":
		return array.CopyWithin(number(0), number(1), number(2))
	case "length":
		array.SetLen(number(0))
		return nil
	case "rowField":
		return array.Object(number(0)).Set(rest[1].(string), rest[2])
	case "reverse":
		array.Reverse()
		return nil
	case "unshift":
		_, err := array.Unshift(source(rest[0]))
		return err
	case "shift":
		array.Shift()
		return nil
	case "pop":
		array.Pop()
		return nil
	case "sort":
		array.Sort(func(a, b any) bool { return handleMoveSortKey(a) < handleMoveSortKey(b) })
		return nil
	}
	return fmt.Errorf("unknown step %q", kind)
}

// handleMoveSortKey is the oracle's sortKey: a number by itself, an object by its numeric value field, anything else as 0.
func handleMoveSortKey(value any) float64 {
	switch typed := value.(type) {
	case float64:
		return typed
	case *Object:
		if number, ok := typed.Get("value").(float64); ok {
			return number
		}
	}
	return 0
}

// plainValue turns a decoded oracle value into the tracker's JSON shape: objects are *JsonObject.
func plainValue(value any) any {
	encoded, _ := json.Marshal(value)
	var decoded JsonValue
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		panic(err)
	}
	return decoded
}

// Pi's tracker places a draft handle by value: the copy is the source's current content, later edits of either side stay apart, and the operations record the copy, as tracker.ts clonePlacement and its emission do. The installed Pi chord module runs seeded scripts of handle placements (into objects, push, unshift, index writes, splice, fill), copyWithin, length writes and sorts by a field of the placed copies; some placed handles belong to a second open change of the same tracker mixed with ordinary edits (testdata/pi_handle_moves.mjs). Go must raise an error exactly where Pi throws and must prepare the same value and the same operations, in order.
func TestTrackerHandlePlacementsMatchPi(t *testing.T) {
	const count, seed = 3000, 20261010
	cmd := exec.CommandContext(t.Context(), "node", "testdata/pi_handle_moves.mjs", pigversion.UpstreamPackageVersion, strconv.Itoa(count), strconv.Itoa(seed))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var cases []piHandleMoveCase
	if err := json.Unmarshal(output, &cases); err != nil {
		t.Fatalf("Pi oracle output: %v", err)
	}
	if len(cases) != count {
		t.Fatalf("Pi oracle returned %d cases, want %d", len(cases), count)
	}
	var failures []string
	for index, c := range cases {
		tracker := Track(c.Initial)
		change := tracker.BeginChange()
		state := change.State()
		other := tracker.BeginChange().State()
		if rows := other.Array("rows"); rows.Len() > 0 {
			if err := rows.Object(0).Set("value", 99.0); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := other.Object("nested").Object("b").Array("items").Push(7.0); err != nil {
			t.Fatal(err)
		}
		if _, err := other.Array("rows").Unshift(JsonObjectOf("id", 50.0, "value", 5.0)); err != nil {
			t.Fatal(err)
		}
		failed := false
		for number, step := range c.Steps {
			err := applyHandleMoveStep(state, other, step.Edit)
			want := ""
			if step.Error != nil {
				want = strings.TrimPrefix(*step.Error, "TypeError: ")
			}
			got := ""
			if err != nil {
				got = err.Error()
			}
			if (want == "") != (got == "") || !strings.Contains(got, want) {
				failures = append(failures, fmt.Sprintf("case %d step %d %s: Go error %q, Pi %q", index, number, piJSON(t, step.Edit), got, want))
				failed = true
				break
			}
		}
		if failed {
			change.Abort()
			continue
		}
		prepared, err := change.Prepare()
		if err != nil {
			failures = append(failures, fmt.Sprintf("case %d: prepare: %v", index, err))
			continue
		}
		if got, want := piJSON(t, prepared.Value()), piJSON(t, c.Value); got != want {
			failures = append(failures, fmt.Sprintf("case %d value\n   Pi %s\n   Go %s\n   steps %s", index, want, got, piJSON(t, c.Steps)))
			continue
		}
		if got, want := piJSON(t, prepared.Ops()), piJSON(t, c.Ops); got != want {
			failures = append(failures, fmt.Sprintf("case %d ops\n   Pi %s\n   Go %s\n   steps %s", index, want, got, piJSON(t, c.Steps)))
		}
	}
	if len(failures) > 0 {
		t.Fatalf("%d of %d cases differ from Pi:\n%s", len(failures), len(cases), strings.Join(failures[:min(len(failures), 5)], "\n"))
	}
}
