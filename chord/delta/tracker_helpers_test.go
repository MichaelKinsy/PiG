package delta

import (
	"fmt"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

// Helpers shared by the ports of packages/chord/test/delta-tracker/*.test.ts, delta-clone.test.ts and state-*.test.ts: upstream's replay, expectAliasFree and state-fuzz random source and mutate.

// replay is apply(clone(base), clone(operations)).
func replay(t *testing.T, base any, operations []Op) any {
	t.Helper()
	return mustApply(t, clone(t, base), ops(t, mustJSON(t, operations)))
}

// expectAliasFree fails when one container appears at two paths of a revision.
func expectAliasFree(t *testing.T, value any) {
	t.Helper()
	seen := map[identity]string{}
	var visit func(current any, path string)
	visit = func(current any, path string) {
		if key, tracked := identityOf(current); tracked {
			if previous, dup := seen[key]; dup {
				t.Fatalf("container at %s aliases %s", path, previous)
			}
			seen[key] = path
		}
		switch typed := current.(type) {
		case []any:
			for at, item := range typed {
				visit(item, fmt.Sprintf("%s[%d]", path, at))
			}
		case *chordjson.Object:
			for key, item := range typed.All() {
				visit(item, path+"."+key)
			}
		}
	}
	visit(value, "$root")
}

// state-fuzz.test.ts

type fuzzDocument = *chordjson.Object

func mulberry32(seed int32) func() float64 {
	return func() float64 {
		seed += 0x6d2b79f5
		s := uint32(seed)
		value := int32(imul(s^(s>>15), 1|s))
		value = (value + int32(imul(uint32(value)^(uint32(value)>>7), 61|uint32(value)))) ^ value
		return float64(uint32(value)^(uint32(value)>>14)) / 4_294_967_296
	}
}

// imul is Math.imul on the unsigned bit patterns.
func imul(a, b uint32) uint32 { return a * b }

func fuzzMutate(t *testing.T, document fuzzDocument, choice, value int) {
	item := func() any {
		return chordjson.ObjectOf("id", float64(value), "text", fmt.Sprintf("item-%d", value), "score", float64(value%7))
	}
	items := arr(t, document.Value("items"))
	text := document.Value("text").(string)
	meta := obj(t, document.Value("meta"))
	switch choice {
	case 0:
		document.Set("text", text+fmt.Sprintf("-%d", value))
	case 1:
		document.Set("text", text[min(2, len(text)):]+fmt.Sprint(value))
	case 2:
		document.Set("items", append(items, item()))
	case 3:
		document.Set("items", slices.Insert(items, 0, item()))
	case 4:
		if len(items) > 0 {
			document.Set("items", items[1:])
		}
	case 5:
		if len(items) > 0 {
			document.Set("items", items[:len(items)-1])
		}
	case 6:
		index, remove := 0, 0
		if len(items) > 0 {
			index, remove = value%(len(items)+1), value%2
		}
		document.Set("items", slices.Replace(items, index, min(index+remove, len(items)), item()))
	case 7:
		slices.Reverse(items)
	case 8:
		slices.SortStableFunc(items, func(a, b any) int { return int(obj(t, a).Value("id").(float64) - obj(t, b).Value("id").(float64)) })
	case 9:
		if len(items) > 0 {
			obj(t, items[value%len(items)]).Set("score", float64(value))
		}
	case 10:
		meta.Set("revision", meta.Value("revision").(float64)+1)
		meta.Set("label", fmt.Sprintf("revision-%d", value))
	case 11:
		meta.Delete("label")
	case 12:
		if len(items) > 1 {
			items[1] = clone(t, items[0])
		}
	default:
		for at := range min(2, len(items)) {
			items[at] = item()
		}
	}
}
