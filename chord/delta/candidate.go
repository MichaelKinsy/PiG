package delta

import (
	"math"
	"reflect"

	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

// DiffCandidate returns the operations that turn base into candidate, a complete revision the caller built (typed state edits a decoded copy instead of a draft), and the candidate restored to base's container identity wherever it is deeply equal, so array alignment matches moved elements by identity. The candidate is validated as strict JSON and normalized; an invalid candidate returns an error. The result takes ownership of candidate. When nothing changed, the operations are empty and the result is base.
func DiffCandidate(base, candidate JsonValue) ([]Op, JsonValue, error) {
	normalized, err := normalize(candidate)
	if err != nil {
		return nil, nil, err
	}
	reconciled := (&reconciler{used: map[identity]struct{}{}}).reconcile(normalized, base)
	ops := DiffRevisions(base, reconciled)
	if len(ops) == 0 {
		return []Op{}, base, nil
	}
	return ops, reconciled, nil
}

// normalize validates a candidate revision as strict JSON and makes it alias-free: a container reached twice becomes two independent containers, Go integer and float kinds become float64, and a Go map[string]any becomes a *chordjson.Object with its keys in chordjson.MapOwnKeys order. It writes into a container only to replace a child, so a committed container the caller placed in the candidate is never written.
//
// It walks objects in own-key order, so the first invalid property is the one upstream reports (revision-validator.ts:26-33).
func normalize(root JsonValue) (JsonValue, error) {
	seen := map[identity]struct{}{}
	active := map[identity]struct{}{}
	var walk func(value any) (any, bool, error)
	walk = func(value any) (any, bool, error) {
		switch typed := value.(type) {
		case nil, bool, string:
			return typed, false, nil
		case *chordjson.Object, []any:
			return walkContainer(typed, seen, active, walk)
		case map[string]any:
			if typed == nil {
				return nil, false, typeError("Replicated state containers must be plain objects or arrays")
			}
			// The map itself is on the active path while its converted object is walked, so a cycle through it is still found.
			mapKey := identity{pointer: reflect.ValueOf(typed).Pointer(), length: -2}
			if _, cyclic := active[mapKey]; cyclic {
				return nil, false, typeError("Replicated state cannot contain cycles")
			}
			active[mapKey] = struct{}{}
			defer delete(active, mapKey)
			object := chordjson.NewObject(len(typed))
			for _, name := range chordjson.MapOwnKeys(typed) {
				object.Set(name, typed[name])
			}
			next, _, err := walkContainer(object, seen, active, walk)
			return next, true, err
		}
		n, err := normalizeNumber(value)
		if _, isFloat := value.(float64); isFloat {
			return n, false, err
		}
		return n, true, err
	}
	next, _, err := walk(root)
	if err != nil {
		return nil, err
	}
	return next, nil
}

func walkContainer(value any, seen, active map[identity]struct{}, walk func(any) (any, bool, error)) (any, bool, error) {
	key, tracked := identityOf(value)
	if tracked {
		if _, cyclic := active[key]; cyclic {
			return nil, false, typeError("Replicated state cannot contain cycles")
		}
		if _, aliased := seen[key]; aliased {
			// Placing one value at several paths yields independent containers.
			copied, err := chordjson.Copy(value)
			return copied, true, err
		}
		seen[key] = struct{}{}
		active[key] = struct{}{}
		defer delete(active, key)
	}
	switch typed := value.(type) {
	case *chordjson.Object:
		if typed == nil {
			return nil, false, typeError("Replicated state containers must be plain objects or arrays")
		}
		for name, item := range typed.All() {
			next, changed, err := walk(item)
			if err != nil {
				return nil, false, err
			}
			if changed {
				typed.Set(name, next)
			}
		}
		return typed, false, nil
	default:
		items := value.([]any)
		for at, item := range items {
			next, changed, err := walk(item)
			if err != nil {
				return nil, false, err
			}
			if changed {
				items[at] = next
			}
		}
		return items, false, nil
	}
}

func normalizeNumber(value any) (any, error) {
	n, ok := number(value)
	if !ok {
		switch reflect.ValueOf(value).Kind() {
		case reflect.Struct, reflect.Map, reflect.Slice, reflect.Array, reflect.Pointer, reflect.Interface:
			return nil, typeError("Replicated state containers must be plain objects or arrays")
		}
		return nil, typeError("Replicated state values must be strict JSON")
	}
	if math.IsNaN(n) || math.IsInf(n, 0) {
		return nil, typeError("Replicated state values must be strict JSON")
	}
	return n, nil
}

// reconciler restores base identity: a candidate container deeply equal to the base container at the same position becomes that container again, and matchMoved restores the elements of an array that moved.
//
// A base container is restored at most once, so the result stays alias-free: when a candidate container is aliased or moved, only the first claimant gets the base container and later ones keep their own.
type reconciler struct {
	used map[identity]struct{}
}

func (r *reconciler) claimed(hint JsonValue) bool {
	key, tracked := identityOf(hint)
	if !tracked {
		return false
	}
	_, used := r.used[key]
	return used
}

func (r *reconciler) claim(hint JsonValue) {
	if key, tracked := identityOf(hint); tracked {
		r.used[key] = struct{}{}
	}
}

func (r *reconciler) reconcile(work, positional JsonValue) JsonValue {
	hint := positional
	if isContainer(hint) && r.claimed(hint) {
		hint = nil
	}
	switch typed := work.(type) {
	case *chordjson.Object:
		counterpart, _ := hint.(*chordjson.Object)
		same := counterpart != nil && counterpart.Len() == typed.Len()
		for key, item := range typed.All() {
			base, exists := counterpart.Get(key)
			next := r.reconcile(item, valueIf(base, exists))
			if !strictEqual(next, item) {
				typed.Set(key, next)
			}
			same = same && exists && strictEqual(next, base)
		}
		if same {
			r.claim(counterpart)
			return counterpart
		}
		return typed
	case []any:
		counterpart, _ := hint.([]any)
		for at, item := range typed {
			var base any
			exists := at < len(counterpart)
			if exists {
				base = counterpart[at]
			}
			if next := r.reconcile(item, valueIf(base, exists)); !strictEqual(next, item) {
				typed[at] = next
			}
		}
		if counterpart == nil {
			return typed
		}
		r.matchMoved(typed, counterpart)
		same := len(counterpart) == len(typed)
		for at := 0; same && at < len(typed); at++ {
			same = strictEqual(typed[at], counterpart[at])
		}
		if same {
			r.claim(counterpart)
			return counterpart
		}
		return typed
	default:
		return work
	}
}

// matchMoved restores, by deep equality, the elements that the positional pass did not restore and that equal a base element elsewhere in the array: an element that moved. Each base element is restored at most once.
func (r *reconciler) matchMoved(work, base []any) {
	var index map[uint64][]int
	for at, item := range work {
		if !isContainer(item) || r.claimed(item) {
			continue
		}
		if index == nil {
			index = map[uint64][]int{}
			for position, candidate := range base {
				if isContainer(candidate) {
					key := hashValue(candidate)
					index[key] = append(index[key], position)
				}
			}
		}
		for _, position := range index[hashValue(item)] {
			candidate := base[position]
			if r.claimed(candidate) || !equalJson(item, candidate) {
				continue
			}
			work[at] = candidate
			r.claim(candidate)
			break
		}
	}
}

func valueIf(value any, exists bool) any {
	if exists {
		return value
	}
	return nil
}
