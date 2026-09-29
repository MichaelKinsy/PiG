package codingagent

import (
	"encoding/json"
	"math"
	"reflect"

	"github.com/MichaelKinsy/PiG/ai"
)

var catalogProviderField = func() int {
	field, _ := reflect.TypeFor[ai.Model]().FieldByName("Provider")
	return field.Index[0]
}()

// ModelInfo uses ProviderMeta.ProviderID for eligible models. Comparing only data fields avoids copying every Model just to erase its runtime Provider on a cache hit.
func equalCatalogModels(a, b []*ai.Model) bool {
	if len(a) != len(b) {
		return false
	}
	for i, model := range a {
		if model == nil || b[i] == nil {
			if model != b[i] {
				return false
			}
			continue
		}
		left, right := reflect.ValueOf(model).Elem(), reflect.ValueOf(b[i]).Elem()
		for field := range left.NumField() {
			if field == catalogProviderField {
				continue
			}
			if !equalCatalogInput(left.FieldByIndex([]int{field}), right.FieldByIndex([]int{field})) {
				return false
			}
		}
	}
	return true
}

// equalCatalogInput compares an acyclic, data-only snapshot. Floating-point bits matter: JSON preserves signed zero even though Go equality does not.
func equalCatalogInput(a, b reflect.Value) bool {
	if a.Type() != b.Type() {
		return false
	}
	switch a.Kind() {
	case reflect.Interface, reflect.Pointer:
		if a.IsNil() || b.IsNil() {
			return a.IsNil() == b.IsNil()
		}
		return equalCatalogInput(a.Elem(), b.Elem())
	case reflect.Map:
		if a.IsNil() != b.IsNil() || a.Len() != b.Len() {
			return false
		}
		iter := a.MapRange()
		for iter.Next() {
			other := b.MapIndex(iter.Key())
			if !other.IsValid() || !equalCatalogInput(iter.Value(), other) {
				return false
			}
		}
		return true
	case reflect.Slice, reflect.Array:
		if a.Len() != b.Len() || (a.Kind() == reflect.Slice && a.IsNil() != b.IsNil()) {
			return false
		}
		for i := range a.Len() {
			if !equalCatalogInput(a.Index(i), b.Index(i)) {
				return false
			}
		}
		return true
	case reflect.Struct:
		for i := range a.NumField() {
			if !equalCatalogInput(a.FieldByIndex([]int{i}), b.FieldByIndex([]int{i})) {
				return false
			}
		}
		return true
	case reflect.Float32:
		return math.Float32bits(float32(a.Float())) == math.Float32bits(float32(b.Float()))
	case reflect.Float64:
		return math.Float64bits(a.Float()) == math.Float64bits(b.Float())
	default:
		return a.Equal(b)
	}
}

var catalogMarshalerType = reflect.TypeFor[json.Marshaler]()

// cloneCatalogInput owns every mutable part of a data-only model snapshot. Unknown marshalers cannot be described by their fields and stay on the uncached path.
func cloneCatalogInput(value reflect.Value) (reflect.Value, bool) {
	out := reflect.New(value.Type()).Elem()
	if !copyCatalogInput(out, value) {
		return reflect.Value{}, false
	}
	return out, true
}

// Copy directly into the owned destination instead of allocating and assigning intermediate structs.
func copyCatalogInput(out, value reflect.Value) bool {
	if !out.CanSet() || (value.Type().Implements(catalogMarshalerType) && value.Type() != reflect.TypeFor[*ai.ModelCompat]() && value.Type() != reflect.TypeFor[ai.ModelCompat]()) {
		return false
	}
	switch value.Kind() {
	case reflect.Interface:
		if value.IsNil() {
			out.SetZero()
			return true
		}
		child := reflect.New(value.Elem().Type()).Elem()
		if !copyCatalogInput(child, value.Elem()) {
			return false
		}
		out.Set(child)
	case reflect.Pointer:
		if value.IsNil() {
			out.SetZero()
			return true
		}
		out.Set(reflect.New(value.Type().Elem()))
		return copyCatalogInput(out.Elem(), value.Elem())
	case reflect.Map:
		if value.IsNil() {
			out.SetZero()
			return true
		}
		if value.Type().Key().Kind() != reflect.String {
			return false
		}
		out.Set(reflect.MakeMapWithSize(value.Type(), value.Len()))
		iter := value.MapRange()
		for iter.Next() {
			child := reflect.New(value.Type().Elem()).Elem()
			if !copyCatalogInput(child, iter.Value()) {
				return false
			}
			out.SetMapIndex(iter.Key(), child)
		}
	case reflect.Slice, reflect.Array:
		if value.Kind() == reflect.Slice {
			if value.IsNil() {
				out.SetZero()
				return true
			}
			out.Set(reflect.MakeSlice(value.Type(), value.Len(), value.Len()))
		}
		for i := range value.Len() {
			if !copyCatalogInput(out.Index(i), value.Index(i)) {
				return false
			}
		}
	case reflect.Struct:
		for i := range value.NumField() {
			if !copyCatalogInput(out.FieldByIndex([]int{i}), value.FieldByIndex([]int{i})) {
				return false
			}
		}
	case reflect.Bool, reflect.String, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Float32, reflect.Float64:
		out.Set(value)
	default:
		return false
	}
	return true
}
