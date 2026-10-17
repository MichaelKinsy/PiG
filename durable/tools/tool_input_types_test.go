package tools

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
)

// The tool input types are `Static<typeof schema>` upstream (bash.ts:10-21, edit.ts:27-35, write.ts:9-14): the schema's properties are the input's
// members. Each registration's Parameters must declare exactly the members of its Go input type (its json tags), and a Pi-shaped argument object
// must decode into that type.
func TestToolInputTypesMatchTheirSchemasAndDecodePiArguments(t *testing.T) {
	cases := []struct {
		name         string
		registration *durable.ToolRegistration
		input        any
		args         string
		required     []string
	}{
		{"bash", CreateBashTool(nil), BashToolInput{}, `{"command":"ls","timeout":5}`, []string{"command"}},
		{"powershell", CreatePowerShellTool(nil), PowerShellToolInput{}, `{"command":"Get-ChildItem","timeout":2.5}`, []string{"command"}},
		{"edit", CreateEditTool(), EditToolInput{}, `{"path":"a.txt","edits":[{"oldText":"a","newText":"b"}]}`, []string{"path", "edits"}},
		{"write", CreateWriteTool(), WriteToolInput{}, `{"path":"a.txt","content":"x"}`, []string{"path", "content"}},
	}
	for _, c := range cases {
		properties, _ := c.registration.Parameters["properties"].(map[string]any)
		var declared []string
		for name := range properties {
			declared = append(declared, name)
		}
		slices.Sort(declared)
		var tags []string
		typ := reflect.TypeOf(c.input)
		for field := range typ.Fields() {
			tag, _, _ := strings.Cut(field.Tag.Get("json"), ",")
			tags = append(tags, tag)
		}
		slices.Sort(tags)
		if !reflect.DeepEqual(declared, tags) {
			t.Errorf("%s: schema properties %v, input members %v", c.name, declared, tags)
		}
		var required []string
		switch list := c.registration.Parameters["required"].(type) {
		case []string:
			required = list
		case []any:
			for _, item := range list {
				required = append(required, item.(string))
			}
		}
		slices.Sort(required)
		slices.Sort(c.required)
		if !slices.Equal(required, c.required) {
			t.Errorf("%s: required %v, want %v", c.name, required, c.required)
		}
		decoded := reflect.New(typ)
		var raw any
		if err := json.Unmarshal([]byte(c.args), &raw); err != nil {
			t.Fatal(err)
		}
		value, err := durable.FromJsonValue[any](raw)
		if err != nil || value == nil {
			t.Fatalf("%s: FromJsonValue = %v, %v", c.name, value, err)
		}
		if err := json.Unmarshal([]byte(c.args), decoded.Interface()); err != nil || decoded.Elem().IsZero() {
			t.Errorf("%s: arguments %s decode to %+v, %v", c.name, c.args, decoded.Elem().Interface(), err)
		}
	}
	details, err := json.Marshal(EditToolDetails{Diff: "d", Patch: "p"})
	if err != nil || string(details) != `{"diff":"d","patch":"p"}` {
		t.Errorf("EditToolDetails without firstChangedLine = %s, %v (edit.ts:73-77: the member is optional)", details, err)
	}
	if details, _ := json.Marshal(EditToolDetails{Diff: "d", Patch: "p", FirstChangedLine: 3}); string(details) != `{"diff":"d","patch":"p","firstChangedLine":3}` {
		t.Errorf("EditToolDetails = %s", details)
	}
}
