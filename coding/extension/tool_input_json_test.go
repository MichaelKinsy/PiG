package extension

import (
	"encoding/json"
	"reflect"
	"testing"
)

// upstream: core/tools/{bash,read,edit,write,grep,find,ls}.ts: each input is a plain object typed by a TypeBox schema, so optional members stay absent and a member the schema does not declare survives a round trip.
func TestToolInputsAreTypedAndKeepUndeclaredMembers(t *testing.T) {
	cases := []struct {
		tool, payload string
		want          any
	}{
		{"bash", `{"command":"ls","timeout":2.5,"zeta":1,"alpha":"x"}`, BashToolInput{Command: "ls", Timeout: new(2.5), Extra: map[string]json.RawMessage{"zeta": json.RawMessage("1"), "alpha": json.RawMessage(`"x"`)}}},
		{"read", `{"path":"a","offset":3}`, ReadToolInput{Path: "a", Offset: new(3.0)}},
		{"edit", `{"path":"a","edits":[{"oldText":"o","newText":"n"}]}`, EditToolInput{Path: "a", Edits: []EditToolInputChange{{OldText: "o", NewText: "n"}}}},
		{"write", `{"path":"a","content":"c"}`, WriteToolInput{Path: "a", Content: "c"}},
		{"grep", `{"pattern":"p","glob":"*.go","ignoreCase":false,"literal":true,"context":1,"limit":5}`, GrepToolInput{Pattern: "p", Glob: new("*.go"), IgnoreCase: new(false), Literal: new(true), Context: new(1.0), Limit: new(5.0)}},
		{"find", `{"pattern":"*.ts","path":"src"}`, FindToolInput{Pattern: "*.ts", Path: new("src")}},
		{"ls", `{}`, LsToolInput{}},
	}
	for _, tc := range cases {
		t.Run(tc.tool, func(t *testing.T) {
			// A handler receives CustomToolCallEvent, whose Input is the call's arguments as a map; it narrows them to the tool's typed input by decoding, the Go form of Pi's isToolCallEventType narrowing.
			var args map[string]any
			if err := json.Unmarshal([]byte(tc.payload), &args); err != nil {
				t.Fatal(err)
			}
			event := CustomToolCallEvent{ToolName: tc.tool, Input: args}
			if !IsToolCallEventType(tc.tool, event) {
				t.Fatalf("IsToolCallEventType(%q) is false for the %s call", tc.tool, tc.tool)
			}
			raw, err := json.Marshal(event.Input)
			if err != nil {
				t.Fatal(err)
			}
			got := reflect.New(reflect.TypeOf(tc.want))
			if err := json.Unmarshal(raw, got.Interface()); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got.Elem().Interface(), tc.want) {
				t.Fatalf("input = %#v, want %#v", got.Elem().Interface(), tc.want)
			}
			out, err := json.Marshal(got.Elem().Interface())
			if err != nil {
				t.Fatal(err)
			}
			again := reflect.New(reflect.TypeOf(tc.want))
			if err := json.Unmarshal(out, again.Interface()); err != nil || !reflect.DeepEqual(again.Elem().Interface(), tc.want) {
				t.Fatalf("round trip %s: %v %#v", out, err, again.Elem().Interface())
			}
		})
	}
}

// upstream: a declared optional member that the model omitted is absent from the object, not null.
func TestToolInputOmitsAbsentOptionalMembers(t *testing.T) {
	out, err := json.Marshal(LsToolInput{Limit: new(10.0)})
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"limit":10}`; string(out) != want {
		t.Fatalf("got %s want %s", out, want)
	}
}
