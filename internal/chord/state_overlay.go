package chord

import (
	"errors"
	"fmt"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

// prepareCandidate prepares the revision a typed Change produced. A candidate whose root kind differs from the tracked one replaces the root. Otherwise the edits that turn the base into the candidate are replayed through the overlay draft, so the published batch is the one tracker.ts records for them, in its order.
func (core *stateCore) prepareCandidate(candidate JsonValue, orders ...keyOrder) (*delta.Prepared, error) {
	base := core.tracker.Root()
	_, baseObject := base.(*chordjson.Object)
	if _, object := candidate.(*chordjson.Object); object != baseObject {
		return core.tracker.PrepareReplaceRoot(candidate)
	}
	// The diff restores the base's container identity where the candidate is deeply equal, so moved elements are matched by content; DiffCandidate only computes the edits.
	ops, restored, err := delta.DiffCandidate(base, candidate)
	if err != nil {
		return nil, err
	}
	if len(ops) > 0 && delta.Verb(ops[0]) == "r" {
		return core.tracker.PrepareReplaceRoot(restored)
	}
	change := core.tracker.BeginChange()
	var draft any = change.State()
	if !baseObject {
		draft = change.Elements()
	}
	for _, op := range ops {
		if err := replayEdit(draft, op, restored); err != nil {
			change.Abort()
			return nil, err
		}
	}
	for _, order := range orders {
		if err := reorderKeys(draft, order); err != nil {
			change.Abort()
			return nil, err
		}
	}
	prepared, err := change.Prepare()
	if err != nil {
		change.Abort()
	}
	return prepared, err
}

// keyOrder is the key order of an object a typed change edited in place, at its path in the candidate.
type keyOrder struct {
	path delta.Path
	keys []string
}

// reorderKeys deletes and sets again the keys an in-place edit moved, so the draft records the delete-and-re-add upstream's Proxy records (tracker.ts emitObjectOperations encodes it as "d" then "s").
func reorderKeys(root any, order keyOrder) error {
	found, err := draftAt(root, order.path)
	if err != nil {
		return err
	}
	object, ok := found.(*delta.Object)
	if !ok {
		return nil
	}
	for _, key := range chordjson.MovedKeys(object.Keys(), order.keys) {
		value := object.Get(key)
		object.Delete(key)
		if err := object.Set(key, value); err != nil {
			return err
		}
	}
	return nil
}

// replayEdit performs one operation of the diff on the draft. A string append or truncation sets the string the candidate holds at the path, which the overlay re-encodes as its own "a" or "t".
func replayEdit(root any, op delta.Op, candidate any) error {
	path := delta.PathOf(op)
	switch delta.Verb(op) {
	case "s", "a", "t":
		parent, err := draftAt(root, path[:len(path)-1])
		if err != nil {
			return err
		}
		value := op[2]
		if delta.Verb(op) != "s" {
			if value, err = valueAt(candidate, path); err != nil {
				return err
			}
		}
		return setEdit(parent, path[len(path)-1], value)
	case "d":
		parent, err := draftAt(root, path[:len(path)-1])
		if err != nil {
			return err
		}
		object, ok := parent.(*delta.Object)
		key, _ := path[len(path)-1].(string)
		if !ok {
			return fmt.Errorf("replicated state edit: delete below a non-object at %v", path)
		}
		object.Delete(key)
		return nil
	case "p", "m":
		target, err := draftAt(root, path)
		if err != nil {
			return err
		}
		array, ok := target.(*delta.Array)
		if !ok {
			return fmt.Errorf("replicated state edit: array operation on a non-array at %v", path)
		}
		if delta.Verb(op) == "m" {
			permutation := make([]int, len(op[2].([]any)))
			for position, source := range op[2].([]any) {
				permutation[position] = editIndex(source)
			}
			return array.Reorder(permutation)
		}
		_, err = array.Splice(editIndex(op[2]), editIndex(op[3]), op[4].([]any)...)
		return err
	}
	return fmt.Errorf("replicated state edit: unsupported operation %q", delta.Verb(op))
}

func editIndex(value any) int {
	switch number := value.(type) {
	case int:
		return number
	case float64:
		return int(number)
	}
	return -1
}

// draftAt returns the draft handle, or the value, at path of the draft.
func draftAt(root any, path delta.Path) (any, error) {
	current := root
	for _, segment := range path {
		switch container := current.(type) {
		case *delta.Object:
			key, _ := segment.(string)
			current = container.Get(key)
		case *delta.Array:
			current = container.Get(editIndex(segment))
		default:
			return nil, fmt.Errorf("replicated state edit: path %v leaves the draft", path)
		}
		if current == nil {
			return nil, fmt.Errorf("replicated state edit: path %v does not resolve", path)
		}
	}
	return current, nil
}

func setEdit(parent any, segment any, value any) error {
	switch container := parent.(type) {
	case *delta.Object:
		key, _ := segment.(string)
		return container.Set(key, value)
	case *delta.Array:
		return container.Set(editIndex(segment), value)
	}
	return errors.New("replicated state edit: set below a primitive")
}

func valueAt(root any, path delta.Path) (any, error) {
	current := root
	for _, segment := range path {
		switch container := current.(type) {
		case *chordjson.Object:
			key, _ := segment.(string)
			current = container.Value(key)
		case []any:
			index := editIndex(segment)
			if index < 0 || index >= len(container) {
				return nil, fmt.Errorf("replicated state edit: candidate has no value at %v", path)
			}
			current = container[index]
		default:
			return nil, fmt.Errorf("replicated state edit: candidate has no value at %v", path)
		}
	}
	return current, nil
}
