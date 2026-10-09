package ai

import (
	"encoding/json"
	"math"
	"math/rand"
	"reflect"
	"testing"
)

// The plain-tree fast paths must return what the marshaling path returns, type for type. The marshaling path is the oracle.

func slowClone(value any) (copied any, failed bool) {
	defer func() {
		if recover() != nil {
			failed = true
		}
	}()
	normalized, err := normalizeJSONValue(value)
	if err != nil {
		return nil, true
	}
	return preserveJSONValueTypes(value, normalized), false
}

type marshalsItself struct{}

func (marshalsItself) MarshalJSON() ([]byte, error) { return []byte(`{"k":1}`), nil }

func randomTree(random *rand.Rand, depth int) any {
	kind := random.Intn(16)
	if depth > 4 && kind > 8 {
		kind = random.Intn(9)
	}
	switch kind {
	case 0:
		return nil
	case 1:
		return random.Intn(2) == 0
	case 2:
		return []string{"", "a", "héllo", "\u2028", "emoji \U0001F600", "tab\t\"quote\"", "bad \xff utf8", "lone \xed\xa0\x80"}[random.Intn(8)]
	case 3:
		return []float64{0, math.Copysign(0, -1), 1.5, -3, 1e21, 1e-7, math.MaxFloat64, math.NaN(), math.Inf(1)}[random.Intn(9)]
	case 4:
		return []any{int(random.Intn(100)), int64(1 << 60), uint64(math.MaxUint64), int8(-3), uint8(200), float32(0.1), float32(math.Inf(-1))}[random.Intn(7)]
	case 5:
		return json.Number([]string{"1", "2.50", "bad"}[random.Intn(3)])
	case 6:
		return json.RawMessage([]string{`{"b":1,"a":2}`, `[1, 2]`, ``, `{`}[random.Intn(4)])
	case 7:
		return marshalsItself{}
	case 8:
		return struct{ A int }{random.Intn(5)}
	case 9, 10, 11:
		object := map[string]any{}
		for range random.Intn(4) {
			key := []string{"a", "b", "n", "ключ", "k\xff"}[random.Intn(5)]
			object[key] = randomTree(random, depth+1)
		}
		switch random.Intn(5) {
		case 0:
			return JsonObject(object)
		case 1:
			if random.Intn(2) == 0 {
				return []any{map[string]any(nil), JsonObject(nil)}[random.Intn(2)]
			}
		}
		return object
	default:
		items := []any{}
		for range random.Intn(4) {
			items = append(items, randomTree(random, depth+1))
		}
		if random.Intn(6) == 0 {
			return []any(nil)
		}
		return items
	}
}

func TestPlainJSONFastPathsMatchTheMarshalingPath(t *testing.T) {
	random := rand.New(rand.NewSource(3))
	fast := 0
	for range 20000 {
		tree := randomTree(random, 0)
		want, failed := slowClone(tree)
		got, ok := clonePlainJSON(tree, &plainWalk{})
		if ok {
			fast++
			if failed {
				t.Fatalf("%#v: the fast path cloned a value the marshaling path rejects", tree)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("%#v: fast %#v, marshaling %#v", tree, got, want)
			}
		}
		_, err := normalizeJSONValue(tree)
		if isPlainJSON(tree, &plainWalk{}) && err != nil {
			t.Fatalf("%#v: reported plain but marshaling fails: %v", tree, err)
		}
		if ok != isPlainJSON(tree, &plainWalk{}) {
			t.Fatalf("%#v: clone ok=%v but validation plain=%v", tree, ok, isPlainJSON(tree, &plainWalk{}))
		}
	}
	if fast < 3000 {
		t.Fatalf("only %d of the random trees took the fast path: the test no longer exercises it", fast)
	}
}

func TestPlainJSONCloneDoesNotShareContainers(t *testing.T) {
	original := map[string]any{"list": []any{map[string]any{"n": 1.0}}, "obj": JsonObject{"x": "y"}}
	copied := cloneJSONValue(original).(map[string]any)
	copied["list"].([]any)[0].(map[string]any)["n"] = 2.0
	copied["obj"].(map[string]any)["x"] = "z"
	if original["list"].([]any)[0].(map[string]any)["n"] != 1.0 || original["obj"].(JsonObject)["x"] != "y" {
		t.Fatal("the clone shares containers with the original")
	}
}

func TestPlainJSONFallsBackOnCycles(t *testing.T) {
	viaMap := map[string]any{}
	viaMap["self"] = []any{viaMap}
	viaSlice := []any{nil}
	viaSlice[0] = map[string]any{"back": viaSlice}
	for name, tree := range map[string]any{"map": viaMap, "slice": viaSlice} {
		if _, ok := clonePlainJSON(tree, &plainWalk{}); ok {
			t.Errorf("%s: a cyclic tree took the fast path", name)
		}
		if isPlainJSON(tree, &plainWalk{}) {
			t.Errorf("%s: a cyclic tree validated as plain", name)
		}
		if err := validateJsonValue(tree); err == nil {
			t.Errorf("%s: a cyclic tree validated", name)
		}
	}
}

// TestPlainJSONHandlesDeepAndSharedTrees: depth alone is no reason to fall back, and a value met twice on different
// paths is not a cycle.
func TestPlainJSONHandlesDeepAndSharedTrees(t *testing.T) {
	deep := any("leaf")
	for range 500 {
		deep = []any{deep}
	}
	shared := map[string]any{"n": 1.0}
	for name, tree := range map[string]any{"deep": deep, "shared": []any{shared, shared, map[string]any{"again": shared}}} {
		got, ok := clonePlainJSON(tree, &plainWalk{})
		if !ok {
			t.Fatalf("%s: refused", name)
		}
		want, failed := slowClone(tree)
		if failed || !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: fast %#v, marshaling %#v (failed %v)", name, got, want, failed)
		}
	}
}
