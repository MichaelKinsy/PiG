package json

import (
	"cmp"
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

	once   sync.Once
	sorted []orderEntry
}

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

// Carry gives the object to the order it has in from: to is a copy of from, which from knows. The keys are shared, never copied.
func (o *ObjectOrder) Carry(from *ObjectOrder, source, to map[string]any) {
	if keys, ok := from.KeysOf(source); ok {
		o.add(to, keys)
	}
}

// sort puts the objects in one list by address, for the lookups. An order that nothing reads never pays for it.
func (o *ObjectOrder) sort() {
	n := 0
	for _, block := range o.blocks {
		n += len(block)
	}
	o.sorted = make([]orderEntry, 0, n)
	for _, block := range o.blocks {
		o.sorted = append(o.sorted, block...)
	}
	slices.SortFunc(o.sorted, func(a, b orderEntry) int { return cmp.Compare(a.pointer, b.pointer) })
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
