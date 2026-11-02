package host

import (
	"math/rand/v2"
	"runtime"
	"slices"
	"testing"
)

// mapModel is a JavaScript Map written for clarity: a slice of keys in insertion order and a Go map of values.
type mapModel struct {
	keys   []int
	values map[int]int
}

func (model *mapModel) set(key, value int) {
	if _, ok := model.values[key]; !ok {
		model.keys = append(model.keys, key)
	}
	model.values[key] = value
}

func (model *mapModel) delete(key int) bool {
	if _, ok := model.values[key]; !ok {
		return false
	}
	delete(model.values, key)
	model.keys = slices.DeleteFunc(model.keys, func(existing int) bool { return existing == key })
	return true
}

func orderedMapKeysAndValues(entries *orderedMap[int, int]) (keys, values []int) {
	return entries.Keys(), entries.Values()
}

// A Map keeps the place of a key that is set again, and puts a key that was deleted and set again at the end.
func TestOrderedMapKeepsMapInsertionOrder(t *testing.T) {
	entries := newOrderedMap[string, int]()
	for index, key := range []string{"a", "b", "c", "d"} {
		entries.Set(key, index)
	}
	entries.Set("b", 10)
	if keys := entries.Keys(); !slices.Equal(keys, []string{"a", "b", "c", "d"}) {
		t.Fatalf("setting a present key moved it: %v", keys)
	}
	if value, _ := entries.Get("b"); value != 10 {
		t.Fatalf("b = %d, want 10", value)
	}
	if !entries.Delete("b") || entries.Delete("b") {
		t.Fatal("Delete must report whether the key was present")
	}
	entries.Set("b", 11)
	if keys := entries.Keys(); !slices.Equal(keys, []string{"a", "c", "d", "b"}) {
		t.Fatalf("a deleted key set again must go to the end: %v", keys)
	}
	if values := entries.Values(); !slices.Equal(values, []int{0, 2, 3, 11}) {
		t.Fatalf("values %v do not follow the keys", values)
	}
	if key, value, ok := entries.First(); !ok || key != "a" || value != 0 {
		t.Fatalf("First = %q, %d, %v", key, value, ok)
	}
	entries.Delete("a")
	if key, value, ok := entries.First(); !ok || key != "c" || value != 2 {
		t.Fatalf("First after deleting the first = %q, %d, %v", key, value, ok)
	}
	entries.Clear()
	if _, _, ok := entries.First(); ok || entries.Len() != 0 || entries.Keys() != nil || entries.Has("c") {
		t.Fatal("Clear must empty the map")
	}
	entries.Set("z", 1)
	if keys := entries.Keys(); !slices.Equal(keys, []string{"z"}) {
		t.Fatalf("a cleared map must accept new keys: %v", keys)
	}
}

// DeleteFunc is a Map iteration that deletes the entries it visits: it visits the live entries in insertion order,
// once each.
func TestOrderedMapDeleteFuncVisitsLiveEntriesInOrder(t *testing.T) {
	entries := newOrderedMap[int, int]()
	for key := range 40 {
		entries.Set(key, key*10)
	}
	for key := range 10 {
		entries.Delete(key)
	}
	var visited []int
	entries.DeleteFunc(func(key, value int) bool {
		if value != key*10 {
			t.Fatalf("key %d visited with value %d", key, value)
		}
		visited = append(visited, key)
		return key%2 == 0
	})
	var wantVisited, wantKept []int
	for key := 10; key < 40; key++ {
		wantVisited = append(wantVisited, key)
		if key%2 == 1 {
			wantKept = append(wantKept, key)
		}
	}
	if !slices.Equal(visited, wantVisited) {
		t.Fatalf("visited %v, want %v", visited, wantVisited)
	}
	if keys := entries.Keys(); !slices.Equal(keys, wantKept) || entries.Len() != len(wantKept) {
		t.Fatalf("kept %v (len %d), want %v", keys, entries.Len(), wantKept)
	}
	entries.DeleteFunc(func(int, int) bool { return true })
	if entries.Len() != 0 || entries.Keys() != nil {
		t.Fatal("deleting every entry must empty the map")
	}
}

// Random operations on the map and on the model agree at every step: order, values, size and first entry.
func TestOrderedMapAgreesWithModel(t *testing.T) {
	for seed := range uint64(20) {
		random := rand.New(rand.NewPCG(seed, 7))
		entries := newOrderedMap[int, int]()
		model := &mapModel{values: map[int]int{}}
		keySpace := 4 + int(seed)*5
		for step := range 3000 {
			key := random.IntN(keySpace)
			switch operation := random.IntN(12); {
			case operation < 5:
				entries.Set(key, step)
				model.set(key, step)
			case operation < 9:
				if got, want := entries.Delete(key), model.delete(key); got != want {
					t.Fatalf("seed %d step %d: Delete(%d) = %v, want %v", seed, step, key, got, want)
				}
			case operation == 9:
				// A front delete, as flush does.
				if first, _, ok := entries.First(); ok {
					entries.Delete(first)
					model.delete(model.keys[0])
				}
			case operation == 10:
				remove := random.IntN(3)
				entries.DeleteFunc(func(key, _ int) bool { return key%3 == remove })
				for _, existing := range slices.Clone(model.keys) {
					if existing%3 == remove {
						model.delete(existing)
					}
				}
			default:
				if random.IntN(40) == 0 {
					entries.Clear()
					model = &mapModel{values: map[int]int{}}
				}
			}
			keys, values := orderedMapKeysAndValues(entries)
			if len(model.keys) == 0 {
				if len(keys) != 0 || len(values) != 0 {
					t.Fatalf("seed %d step %d: keys %v values %v, want empty", seed, step, keys, values)
				}
			} else if !slices.Equal(keys, model.keys) {
				t.Fatalf("seed %d step %d: keys %v, want %v", seed, step, keys, model.keys)
			}
			for index, existing := range keys {
				if got, ok := entries.Get(existing); !ok || got != values[index] || got != model.values[existing] {
					t.Fatalf("seed %d step %d: key %d holds %d, want %d", seed, step, existing, got, model.values[existing])
				}
			}
			_, present := model.values[key]
			if entries.Len() != len(model.keys) || entries.Has(key) != present {
				t.Fatalf("seed %d step %d: Len %d, want %d", seed, step, entries.Len(), len(model.keys))
			}
			first, _, ok := entries.First()
			if ok != (len(model.keys) > 0) || ok && first != model.keys[0] {
				t.Fatalf("seed %d step %d: First = %d, %v; want %v", seed, step, first, ok, model.keys)
			}
		}
	}
}

// A queue that adds at the back and takes from the front, as flush does, keeps its entries in a slice that follows
// its live size instead of growing with the number of entries that passed through.
func TestOrderedMapQueueStaysBounded(t *testing.T) {
	entries := newOrderedMap[int, int]()
	const live = 50
	for key := range live {
		entries.Set(key, key)
	}
	for key := live; key < 100_000; key++ {
		entries.Set(key, key)
		first, _, _ := entries.First()
		entries.Delete(first)
		if first != key-live {
			t.Fatalf("front = %d, want %d", first, key-live)
		}
	}
	if capacity := cap(entries.entries); capacity > 8*live {
		t.Fatalf("a queue of %d live entries holds %d slots", live, capacity)
	}
}

// A map that held many entries and shrank gives their memory back: the entry slice follows the live size once the
// tombstones are compacted away, and an emptied map keeps no large backing array. A link that once queued thousands
// of slots would otherwise keep them zeroed for its lifetime.
func TestOrderedMapReleasesWhatItGrewTo(t *testing.T) {
	const peak = 10_000
	fill := func() *orderedMap[int, *int] {
		entries := newOrderedMap[int, *int]()
		for key := range peak {
			entries.Set(key, new(int))
		}
		return entries
	}
	drain := func(entries *orderedMap[int, *int]) {
		for entries.Len() > 0 {
			first, _, _ := entries.First()
			entries.Delete(first)
		}
	}
	t.Run("drained", func(t *testing.T) {
		entries := fill()
		drain(entries)
		if capacity := cap(entries.entries); capacity > retainedEntries {
			t.Fatalf("an empty map that held %d entries keeps %d slots", peak, capacity)
		}
	})
	t.Run("deleted by DeleteFunc", func(t *testing.T) {
		entries := fill()
		entries.DeleteFunc(func(int, *int) bool { return true })
		if capacity := cap(entries.entries); capacity > retainedEntries {
			t.Fatalf("an empty map that held %d entries keeps %d slots", peak, capacity)
		}
	})
	t.Run("shrunk", func(t *testing.T) {
		entries := fill()
		const live = 100
		for key := range peak - live {
			entries.Delete(key)
		}
		if capacity := cap(entries.entries); capacity > 8*live {
			t.Fatalf("a map of %d live entries that held %d keeps %d slots", live, peak, capacity)
		}
		for key := peak - live; key < peak; key++ {
			if _, ok := entries.Get(key); !ok {
				t.Fatalf("key %d lost by compaction", key)
			}
		}
		keys := entries.Keys()
		if len(keys) != live || keys[0] != peak-live || keys[live-1] != peak-1 {
			t.Fatalf("order after compaction: %d keys from %d to %d", len(keys), keys[0], keys[len(keys)-1])
		}
	})
	t.Run("heap", func(t *testing.T) {
		heap := func() uint64 {
			runtime.GC()
			runtime.GC()
			var stats runtime.MemStats
			runtime.ReadMemStats(&stats)
			return stats.HeapAlloc
		}
		before := heap()
		entries := fill()
		drain(entries)
		after := heap()
		runtime.KeepAlive(entries)
		// Kept, the entry slice and the index of 10000 keys are several hundred KiB.
		if retained := int64(after) - int64(before); retained > 128<<10 {
			t.Fatalf("a drained map retains %d bytes", retained)
		}
	})
}
