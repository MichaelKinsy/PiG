package coding

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// upstream: packages/coding-agent/src/core/extensions/types.ts:676 defineTool is `(tool) => tool`: the definition comes back unchanged, so one passed through customTools keeps every member.
func TestDefineToolReturnsTheDefinitionUnchanged(t *testing.T) {
	tool := extension.ToolDefinition{
		Name: "echo", Label: "Echo", Description: "echoes", PromptGuidelines: []string{"use it"},
		Parameters: json.RawMessage(`{"type":"object"}`),
	}
	got := DefineTool(tool)
	if got.Name != "echo" || got.Label != "Echo" || got.Description != "echoes" || len(got.PromptGuidelines) != 1 || string(got.Parameters) != `{"type":"object"}` {
		t.Fatalf("DefineTool changed the definition: %+v", got)
	}
}

// types.ts:676 defineTool is `(tool) => tool`: no member of the definition is dropped, replaced or copied, including the callbacks and every optional member.
// mutation-checked: returning the definition without any one member (a callback, a pointer, a schema) fails.
func TestDefineToolKeepsEveryMemberOfTheDefinition(t *testing.T) {
	var tool extension.ToolDefinition
	value := reflect.ValueOf(&tool).Elem()
	for i := range value.NumField() {
		field := value.Field(i)
		switch field.Kind() {
		case reflect.String:
			field.SetString("member-" + value.Type().Field(i).Name)
		case reflect.Slice:
			if field.Type().Elem().Kind() == reflect.Uint8 {
				field.SetBytes([]byte(`{"member":"` + value.Type().Field(i).Name + `"}`))
			} else {
				field.Set(reflect.MakeSlice(field.Type(), 1, 1))
			}
		case reflect.Func:
			field.Set(reflect.MakeFunc(field.Type(), func([]reflect.Value) []reflect.Value {
				out := make([]reflect.Value, 0, field.Type().NumOut())
				for out0 := range field.Type().Outs() {
					out = append(out, reflect.Zero(out0))
				}
				return out
			}))
		case reflect.Pointer:
			field.Set(reflect.New(field.Type().Elem()))
		case reflect.Int, reflect.Int64:
			field.SetInt(1)
		case reflect.Bool:
			field.SetBool(true)
		}
	}
	got := reflect.ValueOf(DefineTool(tool))
	for i := range value.NumField() {
		name := value.Type().Field(i).Name
		want, have := value.Field(i), got.Field(i)
		switch want.Kind() {
		case reflect.Func:
			if want.IsNil() || have.IsNil() || want.Pointer() != have.Pointer() {
				t.Errorf("%s: the callback was not returned as given", name)
			}
		default:
			if !reflect.DeepEqual(want.Interface(), have.Interface()) || want.IsZero() {
				t.Errorf("%s = %v, want the given %v (a zero member means the fixture did not fill it)", name, have.Interface(), want.Interface())
			}
		}
	}
}
