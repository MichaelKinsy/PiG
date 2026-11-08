package json

import (
	"cmp"
	"math/bits"
	"reflect"
	"slices"
	"sync"
)

// pig additive (key-order): the member order of the objects a decode built.
//
// A Go map has no insertion order, so a decode into map[string]any loses the order of the members of an object. A decode that records it keeps it exactly: the
// object is identified by its own map as it is built, so an object a caller moves, copies into a list of its own, edits or hands back is still the object whose
// order was read, and no other object is mistaken for it.

// ObjectOrder is the member order of the objects (map[string]any) a decode built, as the text wrote them: a repeated key stays in the place of its first
// occurrence, as JSON.parse keeps it. It knows the objects with a member (an empty object has no order, and what is added to it is in byte order) and holds them, so an address it knows is
// never the address of another object. It is a [KeyOrder].
//
// A decode fills it, or [ObjectOrder.Carry] does, before anything reads it; after that any number of goroutines may read it.
type ObjectOrder struct {
	// blocks hold the objects in the order they were recorded, in blocks that are never copied: an object is recorded for the cost of its own entry.
	blocks [][]orderEntry
	// free is the unused part of the chunk of keys the next object's keys go to. A chunk is never copied either, so the keys of an object stay where they were put.
	free []string
	// scratch is the stack of the keys of the objects a decode is in.
	scratch []string

	// count is how many objects are recorded.
	count int

	// from, for the order of the copies of another order's objects (Copies), is that order, and copies the pairs of a copy and the object it copies. A copy is
	// not looked up until a lookup is made, so a handler that asks for no order has paid for the 16 bytes of each pair only.
	from   *ObjectOrder
	copies []copyEntry

	// filter is a Bloom filter of the addresses of the objects, built at the first lookup: a lookup of a map no object of the event can be (the reply's own
	// wrapper, an object the handler built) is answered without sorting the objects, which is what a lookup of an object they hold needs.
	filterOnce sync.Once
	filter     []uint64
	filterMask uint64

	once   sync.Once
	sorted []orderEntry
}

type copyEntry struct{ object, source map[string]any }

type orderEntry struct {
	pointer uintptr
	object  map[string]any
	keys    []string
}

const (
	orderBlockFirst = 8
	orderBlockMax   = 512
	orderChunkFirst = 16
	orderChunkMax   = 1024
)

func mapPointer(m map[string]any) uintptr { return reflect.ValueOf(m).Pointer() }

// record notes the keys, in order, of the object m. keys is copied.
func (o *ObjectOrder) record(m map[string]any, keys []string) {
	if len(keys) > cap(o.free)-len(o.free) {
		o.free = make([]string, 0, max(len(keys), min(2*cap(o.free), orderChunkMax), orderChunkFirst))
	}
	start := len(o.free)
	o.free = append(o.free, keys...)
	o.add(m, o.free[start:len(o.free):len(o.free)])
}

// add notes the keys of m, which the caller does not change afterwards.
func (o *ObjectOrder) add(m map[string]any, keys []string) {
	last := len(o.blocks) - 1
	if last < 0 || len(o.blocks[last]) == cap(o.blocks[last]) {
		size := orderBlockFirst
		if last >= 0 {
			size = min(2*cap(o.blocks[last]), orderBlockMax)
		}
		o.blocks = append(o.blocks, make([]orderEntry, 0, size))
		last++
	}
	o.blocks[last] = append(o.blocks[last], orderEntry{mapPointer(m), m, keys})
	o.count++
}

// finish ends the object m whose keys, as read, are o.scratch[base:], and drops them from the stack.
func (o *ObjectOrder) finish(m map[string]any, base int) {
	keys := o.scratch[base:]
	if len(m) >= 1 {
		if len(keys) != len(m) {
			keys = firstOccurrences(keys)
		}
		o.record(m, keys)
	}
	o.scratch = o.scratch[:base]
}

// repeatedKeyScan is how many keys an object may hold before a repeated key is looked up in a set and not by scanning the keys read so far.
const repeatedKeyScan = 16

// firstOccurrences returns keys without the repeats of a key, which keep the place of their first occurrence. It reuses keys.
func firstOccurrences(keys []string) []string {
	var seen map[string]struct{}
	out := keys[:0]
	for _, key := range keys {
		if seen == nil && len(out) == repeatedKeyScan {
			seen = make(map[string]struct{}, 2*repeatedKeyScan)
			for _, kept := range out {
				seen[kept] = struct{}{}
			}
		}
		if seen != nil {
			if _, repeated := seen[key]; repeated {
				continue
			}
			seen[key] = struct{}{}
		} else if slices.Contains(out, key) {
			continue
		}
		out = append(out, key)
	}
	return out
}

// Keys implements [KeyOrder]: the keys of the object whose reflect.Value.Pointer is m. The slice is never changed.
func (o *ObjectOrder) Keys(m uintptr) ([]string, bool) {
	if o == nil {
		return nil, false
	}
	o.filterOnce.Do(o.buildFilter)
	if !o.mayHold(m) {
		return nil, false
	}
	o.once.Do(o.sort)
	i, found := slices.BinarySearchFunc(o.sorted, m, func(e orderEntry, pointer uintptr) int { return cmp.Compare(e.pointer, pointer) })
	if !found {
		return nil, false
	}
	return o.sorted[i].keys, true
}

// KeysOf is [ObjectOrder.Keys] for the map m.
func (o *ObjectOrder) KeysOf(m map[string]any) ([]string, bool) {
	if m == nil {
		return nil, false
	}
	return o.Keys(mapPointer(m))
}

// Copies returns the order of the copies of o's objects, which [ObjectOrder.Carry] names one by one: a copy of an object o knows is known with the object's
// keys, which are shared, never copied. A copy of every object o knows takes 16 bytes each.
func (o *ObjectOrder) Copies() *ObjectOrder {
	return &ObjectOrder{from: o, copies: make([]copyEntry, 0, o.count)}
}

// Carry makes to a copy of the object source, which the order this one copies from (Copies) may know.
func (o *ObjectOrder) Carry(source, to map[string]any) {
	if len(source) > 0 {
		o.copies = append(o.copies, copyEntry{to, source})
	}
}

// filterBitsPerObject is the bits of the filter for each object: two bits set in 16 per object leave about one lookup in 70 of a map that is not one of the
// objects to the sort.
const filterBitsPerObject = 16

// filterBits are the two bits of the filter that the address p sets.
func (o *ObjectOrder) filterBits(p uintptr) (first, second uint64) {
	h := uint64(p) * 0x9E3779B97F4A7C15
	return (h >> 20) & o.filterMask, (h >> 40) & o.filterMask
}

func (o *ObjectOrder) buildFilter() {
	bits := uint64(64)
	for bits < uint64(o.count+len(o.copies))*filterBitsPerObject {
		bits *= 2
	}
	o.filter, o.filterMask = make([]uint64, bits/64), bits-1
	set := func(p uintptr) {
		a, b := o.filterBits(p)
		o.filter[a/64] |= 1 << (a % 64)
		o.filter[b/64] |= 1 << (b % 64)
	}
	for _, block := range o.blocks {
		for _, e := range block {
			set(e.pointer)
		}
	}
	for _, c := range o.copies {
		set(mapPointer(c.object))
	}
}

func (o *ObjectOrder) mayHold(p uintptr) bool {
	a, b := o.filterBits(p)
	return o.filter[a/64]&(1<<(a%64)) != 0 && o.filter[b/64]&(1<<(b%64)) != 0
}

// sort puts the objects in one list by address, for the lookups. An order that nothing reads never pays for it.
func (o *ObjectOrder) sort() {
	n := 0
	for _, block := range o.blocks {
		n += len(block)
	}
	o.sorted = make([]orderEntry, 0, n+len(o.copies))
	for _, block := range o.blocks {
		o.sorted = append(o.sorted, block...)
	}
	for _, c := range o.copies {
		if keys, ok := o.from.KeysOf(c.source); ok {
			o.sorted = append(o.sorted, orderEntry{mapPointer(c.object), c.object, keys})
		}
	}
	o.copies, o.from = nil, nil
	sortOrderEntries(o.sorted)
	o.blocks, o.free, o.scratch = nil, nil, nil
}

// UnmarshalObject decodes data, a JSON document, as [Unmarshal] decodes it into a map[string]any, and returns the member order of the objects it built. The
// order is nil when data is not an object.
func UnmarshalObject(data []byte) (map[string]any, *ObjectOrder, error) {
	d := decodeStatePool.Get().(*decodeState)
	defer releaseDecodeState(d)
	if err := validate(data, &d.scan); err != nil {
		return nil, nil, err
	}
	return d.unmarshalObject(data)
}

// UnmarshalTrustedObject is [UnmarshalObject] for data that is known to hold one valid JSON value, as [UnmarshalTrusted] takes it.
func UnmarshalTrustedObject(data []byte) (map[string]any, *ObjectOrder, error) {
	d := decodeStatePool.Get().(*decodeState)
	defer releaseDecodeState(d)
	if verifyTrusted {
		if err := checkValid(data, &d.scan); err != nil {
			panic("json: UnmarshalTrustedObject of invalid JSON: " + err.Error())
		}
	}
	return d.unmarshalObject(data)
}

func (d *decodeState) unmarshalObject(data []byte) (map[string]any, *ObjectOrder, error) {
	d.init(data)
	d.scan.reset()
	d.scanWhile(scanSkipSpace)
	if d.opcode == scanBeginObject {
		order := new(ObjectOrder)
		d.order = order
		m := d.objectInterface()
		order.free, order.scratch = nil, nil
		return m, order, nil
	}
	// null leaves the map nil, and any other value is the error Unmarshal reports.
	var m map[string]any
	d.init(data)
	err := d.unmarshal(&m)
	return m, nil, err
}

// sortOrderEntries sorts s by pointer. The pointers are distinct. slices.SortFunc compiles a 12 KB copy of the pattern-defeating quicksort for this element
// shape, in every program that links the SDK (pig and every embedder); this is the same O(n log n) sort in about a tenth of that: a quicksort on the median of three that
// falls back to heapsort when it has split badly 2 log2 n times, and sorts a run of 12 or fewer by insertion, as package slices does.
func sortOrderEntries(s []orderEntry) {
	quickOrderEntries(s, 2*bits.Len(uint(len(s))))
}

func quickOrderEntries(s []orderEntry, depth int) {
	for len(s) > 12 {
		if depth == 0 {
			heapOrderEntries(s)
			return
		}
		depth--
		hi, mid := len(s)-1, len(s)/2
		if s[mid].pointer < s[0].pointer {
			s[mid], s[0] = s[0], s[mid]
		}
		if s[hi].pointer < s[mid].pointer {
			s[hi], s[mid] = s[mid], s[hi]
			if s[mid].pointer < s[0].pointer {
				s[mid], s[0] = s[0], s[mid]
			}
		}
		// Hoare partition around the median's pointer: the elements of s[:j+1] are at most the pivot, those of s[i:] at least, and s[j+1:i] is the pivot.
		pivot := s[mid].pointer
		i, j := 0, hi
		for i <= j {
			for s[i].pointer < pivot {
				i++
			}
			for s[j].pointer > pivot {
				j--
			}
			if i <= j {
				s[i], s[j] = s[j], s[i]
				i++
				j--
			}
		}
		// Recurse into the smaller part and loop on the larger, so the stack stays O(log n).
		if j+1 < len(s)-i {
			quickOrderEntries(s[:j+1], depth)
			s = s[i:]
		} else {
			quickOrderEntries(s[i:], depth)
			s = s[:j+1]
		}
	}
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j].pointer < s[j-1].pointer; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

func heapOrderEntries(s []orderEntry) {
	n := len(s)
	for i := (n - 1) / 2; i >= 0; i-- {
		siftDownOrderEntries(s, i, n)
	}
	for end := n - 1; end > 0; end-- {
		s[0], s[end] = s[end], s[0]
		siftDownOrderEntries(s, 0, end)
	}
}

func siftDownOrderEntries(s []orderEntry, root, n int) {
	for {
		child := 2*root + 1
		if child >= n {
			return
		}
		if child+1 < n && s[child].pointer < s[child+1].pointer {
			child++
		}
		if s[root].pointer >= s[child].pointer {
			return
		}
		s[root], s[child] = s[child], s[root]
		root = child
	}
}
