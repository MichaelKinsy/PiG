package codingagent

import (
	"encoding/json"
	"reflect"
)

// Catalog keys must not evaluate user-defined marshalers an extra time. SDK wire data has these canonical JSON shapes; other Go values use the uncached projection.
func catalogJSONData(value any, active map[any]bool) bool {
	switch value := value.(type) {
	case nil, bool, string, float32, float64, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, json.Number:
		return true
	case []string, []float64, []int, map[string]string:
		return true
	case map[string]any:
		if len(value) == 0 {
			return true
		}
		identity := reflect.ValueOf(value)
		if active[identity] {
			return false
		}
		active[identity] = true
		defer delete(active, identity)
		for _, child := range value {
			if !catalogJSONData(child, active) {
				return false
			}
		}
		return true
	case []any:
		if len(value) == 0 {
			return true
		}
		identity := struct {
			first  *any
			length int
		}{&value[0], len(value)}
		if active[identity] {
			return false
		}
		active[identity] = true
		defer delete(active, identity)
		for _, child := range value {
			if !catalogJSONData(child, active) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func catalogMapCacheable(values map[string]any) bool {
	if len(values) == 0 {
		return true
	}
	return catalogJSONData(values, make(map[any]bool))
}
