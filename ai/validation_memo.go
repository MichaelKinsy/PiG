package ai

import (
	"reflect"
	"sync"
)

// ValidationMemo remembers which JSON-valued references (a tool result's details, a tool call's arguments) a Frozen
// transcript already validated, so a transcript that grows by one message per turn validates each value once instead of
// once per turn. A value is known by its identity: a pointer, a map, or the first element and length of a slice. A Frozen
// context promises that nothing modifies a value it references, and a caller that reuses a memo extends that promise to
// every request the memo serves. The memo holds each value it has seen, so an identity never names two values.
//
// Each normalization keeps the entries its transcript still references and drops the rest, so the memo holds the live
// transcript's values and no replaced history. The zero value is an empty memo; a nil *ValidationMemo validates every value.
// pig additive (D104): pi-ai validates nothing.
type ValidationMemo struct {
	mu   sync.Mutex
	seen map[memoKey]memoEntry
	pass uint64
}

type memoKey struct {
	typ reflect.Type
	ptr uintptr
	len int
}

type memoEntry struct {
	value any
	pass  uint64
}

// NewValidationMemo returns an empty memo.
func NewValidationMemo() *ValidationMemo {
	return &ValidationMemo{seen: map[memoKey]memoEntry{}, pass: 1}
}

func memoKeyOf(value any) (memoKey, bool) {
	rv := reflect.ValueOf(value)
	switch rv.Kind() {
	case reflect.Pointer, reflect.Map:
		if rv.IsNil() {
			return memoKey{}, false
		}
		return memoKey{typ: rv.Type(), ptr: rv.Pointer()}, true
	case reflect.Slice:
		if rv.Len() == 0 {
			return memoKey{}, false
		}
		return memoKey{typ: rv.Type(), ptr: rv.Pointer(), len: rv.Len()}, true
	default:
		return memoKey{}, false
	}
}

// validate is validateJsonValue that skips a reference this memo has already validated.
func (m *ValidationMemo) validate(value any) error {
	if m == nil {
		return validateJsonValue(value)
	}
	key, ok := memoKeyOf(value)
	if !ok {
		return validateJsonValue(value)
	}
	m.mu.Lock()
	if entry, hit := m.seen[key]; hit {
		entry.pass = m.pass
		m.seen[key] = entry
		m.mu.Unlock()
		return nil
	}
	m.mu.Unlock()
	if err := validateJsonValue(value); err != nil {
		return err
	}
	m.mu.Lock()
	if m.seen == nil {
		m.seen = map[memoKey]memoEntry{}
	}
	m.seen[key] = memoEntry{value: value, pass: m.pass}
	m.mu.Unlock()
	return nil
}

// finish ends one normalization: it forgets the values that normalization did not reference.
func (m *ValidationMemo) finish() {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for key, entry := range m.seen {
		if entry.pass != m.pass {
			delete(m.seen, key)
		}
	}
	m.pass++
}
