package jv

import (
	"math/rand"
	"slices"
	"strconv"
	"testing"
)

// naiveObject is the specification of Object: a slice of members in insertion order, enumerated with the canonical array
// index keys ascending first.
type naiveObject struct {
	keys []string
	vals []any
}

func (n *naiveObject) at(key string) int { return slices.Index(n.keys, key) }

func (n *naiveObject) set(key string, v any) {
	if at := n.at(key); at >= 0 {
		n.vals[at] = v
		return
	}
	n.keys = append(n.keys, key)
	n.vals = append(n.vals, v)
}

func (n *naiveObject) del(key string) bool {
	at := n.at(key)
	if at < 0 {
		return false
	}
	n.keys = slices.Delete(n.keys, at, at+1)
	n.vals = slices.Delete(n.vals, at, at+1)
	return true
}

func (n *naiveObject) ordered() []string {
	var idx, rest []string
	for _, k := range n.keys {
		if IsIndexKey(k) {
			idx = append(idx, k)
		} else {
			rest = append(rest, k)
		}
	}
	slices.SortStableFunc(idx, func(a, b string) int {
		x, _ := strconv.Atoi(a)
		y, _ := strconv.Atoi(b)
		return x - y
	})
	return append(idx, rest...)
}

func checkAgainst(t *testing.T, step int, o *Object, n *naiveObject) {
	t.Helper()
	want := n.ordered()
	if o.Len() != len(n.keys) {
		t.Fatalf("step %d: Len %d, want %d", step, o.Len(), len(n.keys))
	}
	if got := o.Keys(); !slices.Equal(got, want) && (len(got) != 0 || len(want) != 0) {
		t.Fatalf("step %d: Keys %v, want %v", step, got, want)
	}
	var viaEntries, viaMembers []string
	o.Entries(func(k string, v any) bool {
		viaEntries = append(viaEntries, k)
		if want, _ := n.vals[n.at(k)].(float64); v != want {
			t.Fatalf("step %d: Entries value of %q is %v", step, k, v)
		}
		return true
	})
	o.Members(func(k string, v any) bool {
		viaMembers = append(viaMembers, k)
		if v != n.vals[n.at(k)] {
			t.Fatalf("step %d: Members value of %q is %v", step, k, v)
		}
		return true
	})
	if !slices.Equal(viaEntries, want) && (len(viaEntries) != 0 || len(want) != 0) {
		t.Fatalf("step %d: Entries %v, want %v", step, viaEntries, want)
	}
	if !slices.Equal(viaMembers, n.keys) && (len(viaMembers) != 0 || len(n.keys) != 0) {
		t.Fatalf("step %d: Members %v, want insertion order %v", step, viaMembers, n.keys)
	}
	for i, k := range n.keys {
		if v, ok := o.Get(k); !ok || v != n.vals[i] {
			t.Fatalf("step %d: Get(%q) = %v, %v", step, k, v, ok)
		}
	}
	// The wire form is the JSON.stringify of the specification's order.
	wantText := "{"
	for i, k := range want {
		if i > 0 {
			wantText += ","
		}
		wantText += string(AppendString(nil, k)) + ":" + string(Append(nil, n.vals[n.at(k)]))
	}
	wantText += "}"
	if got := Stringify(o); got != wantText {
		t.Fatalf("step %d: Stringify %s, want %s", step, got, wantText)
	}
}

// TestObjectMatchesANaiveOrderedMapUnderRandomSetDelete drives Object and a naive ordered map with the same random
// operations (small and large key pools, index and plain keys) and compares every view, plus Clone and Shallow copies.
func TestObjectMatchesANaiveOrderedMapUnderRandomSetDelete(t *testing.T) {
	for seed := range int64(40) {
		rng := rand.New(rand.NewSource(seed))
		pool := []int{6, 20, 60}[seed%3]
		keyOf := func() string {
			i := rng.Intn(pool)
			switch rng.Intn(4) {
			case 0:
				return "k" + strconv.Itoa(i)
			case 1:
				return "0" + strconv.Itoa(i) // not a canonical index
			case 2:
				return strconv.Itoa(i * 7)
			}
			return strconv.Itoa(i)
		}
		o, n := NewObject(), &naiveObject{}
		for step := range 1500 {
			k := keyOf()
			switch rng.Intn(5) {
			case 0, 1:
				v := float64(rng.Intn(1000))
				o.Set(k, v)
				n.set(k, v)
			default:
				if got, want := o.Delete(k), n.del(k); got != want {
					t.Fatalf("seed %d step %d: Delete(%q) = %v, want %v", seed, step, k, got, want)
				}
			}
			if o.Has(k) != (n.at(k) >= 0) {
				t.Fatalf("seed %d step %d: Has(%q)", seed, step, k)
			}
			if step%7 == 0 {
				checkAgainst(t, step, o, n)
			}
			if step%97 == 0 {
				checkAgainst(t, step, Clone(o).(*Object), n)
				shallow := o.Shallow()
				checkAgainst(t, step, shallow, n)
				// A copy is independent and keeps working after more deletes.
				copyN := &naiveObject{keys: slices.Clone(n.keys), vals: slices.Clone(n.vals)}
				for _, key := range slices.Clone(shallow.Keys()) {
					if rng.Intn(2) == 0 {
						shallow.Delete(key)
						copyN.del(key)
					}
				}
				checkAgainst(t, step, shallow, copyN)
				checkAgainst(t, step, o, n)
				if !Equal(o, Clone(o)) {
					t.Fatalf("seed %d step %d: Equal(o, Clone(o)) is false", seed, step)
				}
			}
		}
		// Deleting every member, oldest first, leaves an empty object that works.
		for _, k := range slices.Clone(n.keys) {
			o.Delete(k)
			n.del(k)
		}
		checkAgainst(t, -1, o, n)
		o.Set("again", 1.0)
		n.set("again", 1.0)
		checkAgainst(t, -2, o, n)
	}
}

// TestDeleteWhileIteratingAKeysSnapshotSeesEveryKey: Keys returns a slice an object shares; deleting the member just
// visited must not disturb the keys still to come.
func TestDeleteWhileIteratingAKeysSnapshotSeesEveryKey(t *testing.T) {
	o := NewObject()
	const n = 100
	for i := range n {
		o.Set("k"+strconv.Itoa(i), float64(i))
	}
	seen := 0
	for _, k := range o.Keys() {
		if k != "k"+strconv.Itoa(seen) {
			t.Fatalf("visit %d got %q", seen, k)
		}
		o.Delete(k)
		seen++
	}
	if seen != n || o.Len() != 0 {
		t.Fatalf("saw %d, %d left", seen, o.Len())
	}
}

// TestDeletingOldestFirstCostsConstantTimePerDelete checks that deleting an object's members in insertion order does
// not renumber the rest per delete (the task graph deletes terminal tasks in creation order). It counts slot work, not
// time: a Delete that changes the slot slices moves at most every slot it had, so the slots of the slices before each such
// Delete bound the work. Retiring slots and squeezing them out once half are retired keeps that total within a few times
// the member count; shifting and renumbering per delete makes it about n*n/2.
func TestDeletingOldestFirstCostsConstantTimePerDelete(t *testing.T) {
	for _, n := range []int{4000, 16000} {
		o := NewObject()
		keys := make([]string, n)
		for i := range keys {
			keys[i] = strconv.Itoa(i)
			o.Set(keys[i], float64(i))
		}
		moved := 0
		for _, k := range keys[:n-1] {
			before := len(o.keys)
			o.Delete(k)
			if len(o.keys) != before {
				moved += before
			}
		}
		if o.Len() != 1 {
			t.Fatalf("%d left", o.Len())
		}
		if moved > 4*n {
			t.Fatalf("deleting %d members oldest-first moved %d slots: per-delete cost grows with size", n, moved)
		}
	}
}
