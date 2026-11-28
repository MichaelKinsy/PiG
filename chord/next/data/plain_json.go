package data

import "github.com/MichaelKinsy/PiG/chord/delta"

// SameJson reports whether value is the plain JSON kept is (a value read before), as their canonical JSON texts were
// the same: object keys in any order, -0 as 0. skip names a key of the outer objects left out of both (empty to skip
// none). Allocates nothing and counts nothing.
func SameJson(kept, value any, skip string) bool {
	return sameJsonValue(kept, value, skip)
}

func sameJsonValue(kept, value any, skip string) bool {
	if kept == nil && value == nil {
		return true
	}
	if kept == nil || value == nil {
		return false
	}
	switch l := kept.(type) {
	case bool:
		r, ok := value.(bool)
		return ok && l == r
	case string:
		r, ok := value.(string)
		return ok && l == r
	case float64:
		r, ok := value.(float64)
		return ok && l == r
	case []any:
		r, ok := value.([]any)
		if !ok || len(l) != len(r) {
			return false
		}
		for i := range l {
			if !sameJsonValue(l[i], r[i], "") {
				return false
			}
		}
		return true
	case *delta.JsonObject:
		r, ok := value.(*delta.JsonObject)
		if !ok {
			return false
		}
		if l == r {
			return true
		}
		keys := 0
		for _, k := range r.Keys() {
			if skip != "" && k == skip {
				continue
			}
			lv, exists := l.Get(k)
			if !exists || !sameJsonValue(lv, r.Value(k), "") {
				return false
			}
			keys++
		}
		for _, k := range l.Keys() {
			if skip != "" && k == skip {
				continue
			}
			keys--
		}
		return keys == 0
	default:
		return false
	}
}
