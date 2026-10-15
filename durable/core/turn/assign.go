// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package turn

import (
	"github.com/MichaelKinsy/PiG/durable/core/internal/delta"
	"github.com/MichaelKinsy/PiG/durable/core/jv"
)

// assignObj is assignJson (src/harness/json.ts) on an object draft: it assigns value at target[key] leaf by leaf. A
// container assignment is one full set in the document's operation log and a string leaf reassigned with a longer string
// is an append, so writing a partial message whole would store and publish the complete message on every flush.
func assignObj(target *delta.Obj, key string, value any) error {
	if object, ok := value.(*jv.Object); ok {
		if current := target.Obj(key); current != nil {
			return assignInto(current, object)
		}
	}
	if list, ok := value.([]any); ok {
		if current := target.Arr(key); current != nil && current.Len() <= len(list) {
			return assignList(current, list)
		}
	}
	if current, has := target.Lookup(key); !has || !sameLeaf(current, value) {
		return target.Set(key, value)
	}
	return nil
}

func assignArr(target *delta.Arr, index int, value any) error {
	if object, ok := value.(*jv.Object); ok {
		if current := target.Obj(index); current != nil {
			return assignInto(current, object)
		}
	}
	if list, ok := value.([]any); ok {
		if current := target.Arr(index); current != nil && current.Len() <= len(list) {
			return assignList(current, list)
		}
	}
	if !sameLeaf(target.Get(index), value) {
		return target.Set(index, value)
	}
	return nil
}

// assignInto makes the object draft hold value's members: it removes the draft's other keys and assigns each member.
func assignInto(current *delta.Obj, value *jv.Object) error {
	for _, name := range current.Keys() {
		if !value.Has(name) {
			current.Delete(name)
		}
	}
	var err error
	value.Entries(func(name string, child any) bool {
		err = assignObj(current, name, child)
		return err == nil
	})
	return err
}

func assignList(current *delta.Arr, value []any) error {
	n := current.Len()
	for i, item := range value {
		if i < n {
			if err := assignArr(current, i, item); err != nil {
				return err
			}
		} else if _, err := current.Push(item); err != nil {
			return err
		}
	}
	return nil
}

// sameLeaf is JavaScript's !== negated for a draft value against a JSON value: equal primitives only; a container never is.
func sameLeaf(current, value any) bool {
	switch v := value.(type) {
	case nil:
		return current == nil
	case bool:
		c, ok := current.(bool)
		return ok && c == v
	case float64:
		c, ok := current.(float64)
		return ok && c == v
	case string:
		c, ok := current.(string)
		return ok && c == v
	}
	return false
}
