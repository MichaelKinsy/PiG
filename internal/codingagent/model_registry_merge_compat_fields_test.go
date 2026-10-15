package codingagent

import (
	"encoding/json"
	"reflect"
	"strconv"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// Pi packages/coding-agent/src/core/provider-composer.ts:122-129 mergeCompat: `{ ...base, ...override }` copies every compat key the
// override defines and keeps every other base key. The four nested keys (:131-141) merge key by key and have their own tests. This
// drives each remaining ai.OpenAICompat field (requiresToolResultName and its siblings) through mergeCompat with the three states
// the spread distinguishes: the override wins over a base value, the base survives an override that leaves the key out, and the
// override supplies a key the base lacks.
// mutation-checked: the mutant "mergeCompat ignores the override's RequiresToolResultName" (model_registry.go:1820-1822) fails it.
func TestMergeCompatSpreadsEveryScalarCompatField(t *testing.T) {
	nested := map[string]bool{"OpenRouterRouting": true, "ChatTemplateKwargs": true, "VercelGatewayRouting": true, "ChatTemplateArgs": true}
	typ := reflect.TypeFor[ai.OpenAICompat]()
	// set fills field i of c with a value that depends on salt, so base and override differ.
	set := func(c *ai.OpenAICompat, i int, salt int) {
		field := reflect.ValueOf(c).Elem().Field(i)
		switch field.Type().Kind() {
		case reflect.Pointer:
			switch field.Type().Elem().Kind() {
			case reflect.Bool:
				v := salt == 1
				field.Set(reflect.ValueOf(&v))
			case reflect.Float64:
				v := float64(salt)
				field.Set(reflect.ValueOf(&v))
			default:
				t.Fatalf("%s: unhandled pointer field type %s", typ.Field(i).Name, field.Type())
			}
		case reflect.String:
			field.SetString(map[int]string{1: "first", 2: "second"}[salt])
		case reflect.Slice:
			field.Set(reflect.MakeSlice(field.Type(), salt, salt))
		case reflect.Map:
			// Extra, the members PiG does not model: the spread copies an override member and keeps a base member the same way.
			field.Set(reflect.ValueOf(map[string]json.RawMessage{"customOption": json.RawMessage(strconv.Itoa(salt))}))
		default:
			t.Fatalf("%s: unhandled field type %s", typ.Field(i).Name, field.Type())
		}
	}
	for i := range typ.NumField() {
		name := typ.Field(i).Name
		if nested[name] {
			continue
		}
		t.Run(name, func(t *testing.T) {
			var base, override ai.OpenAICompat
			set(&base, i, 1)
			set(&override, i, 2)
			got := reflect.ValueOf(mergeCompat((*providerCompat)(&base), (*providerCompat)(&override))).Elem().Field(i).Interface()
			if want := reflect.ValueOf(override).Field(i).Interface(); !reflect.DeepEqual(got, want) {
				t.Errorf("override did not win over the base: got %v, want %v", got, want)
			}
			got = reflect.ValueOf(mergeCompat((*providerCompat)(&base), (*providerCompat)(&ai.OpenAICompat{}))).Elem().Field(i).Interface()
			if want := reflect.ValueOf(base).Field(i).Interface(); !reflect.DeepEqual(got, want) {
				t.Errorf("base value lost when the override omits it: got %v, want %v", got, want)
			}
			got = reflect.ValueOf(mergeCompat((*providerCompat)(&ai.OpenAICompat{}), (*providerCompat)(&override))).Elem().Field(i).Interface()
			if want := reflect.ValueOf(override).Field(i).Interface(); !reflect.DeepEqual(got, want) {
				t.Errorf("override value not supplied when the base lacks it: got %v, want %v", got, want)
			}
		})
	}
}
