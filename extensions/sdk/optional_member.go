package sdk

import "reflect"

// omittedMember reports whether value is Go's empty value for an optional member of a host call: a nil interface, pointer, map or slice. The call leaves such a
// member out, as Pi leaves out an argument that is not given (appendEntry(customType)). json.RawMessage("null") is not empty: it is the member written as null
// (appendEntry(customType, null)).
func omittedMember(value any) bool {
	if value == nil {
		return true
	}
	switch v := reflect.ValueOf(value); v.Kind() {
	case reflect.Pointer, reflect.Map, reflect.Slice, reflect.Interface:
		return v.IsNil()
	}
	return false
}
