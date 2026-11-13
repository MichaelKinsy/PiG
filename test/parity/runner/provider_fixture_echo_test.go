//go:build parity

package runner

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRenderRequestPart(t *testing.T) {
	const body = "{ \"model\" : \"strict\",\n \"tools\": [ {\"type\":\"function\", \"function\": {\"name\":\"b\", \"strict\": false, \"parameters\": {\"type\":\"object\",\"required\":[\"z\",\"a\"],\"properties\":{\"z\":{},\"a\":{\"const\":1.0}},\"additionalProperties\":false}}},\n" +
		" {\"name\":\"a/b\",\"m~n\":[1e3, 1.0, -0, 10000000000000000000000, 0.1e-2]} ],\n" +
		" \"text\": \"caf\\u00e9 \\/ <tag> & \\u003c \\\"q\\\" \\n\", \"dup\": 1, \"dup\": {\"x\": 2, \"x\": 3}, \"none\": null, \"flag\": true, \"empty\": {}, \"list\": [] }"
	for _, tc := range []struct{ name, pointer, want string }{
		{"member order is the order received, not sorted", "/tools/0/function", `{"name":"b","strict":false,"parameters":{"type":"object","required":["z","a"],"properties":{"z":{},"a":{"const":1.0}},"additionalProperties":false}}`},
		{"a scalar member", "/tools/0/function/strict", `false`},
		{"an empty schema node", "/tools/0/function/parameters/properties/z", `{}`},
		{"number literals stay as written", "/tools/1/m~0n", `[1e3,1.0,-0,10000000000000000000000,0.1e-2]`},
		{"~1 names a slash in a member name", "/tools/1/name", `"a/b"`},
		{"strings take one escape spelling", "/text", `"café / <tag> & < \"q\" \n"`},
		{"a repeated member name resolves to its last value, which keeps its own repeats", "/dup", `{"x":2,"x":3}`},
		{"the last repeat again", "/dup/x", `3`},
		{"null is present", "/none", `null`},
		{"true", "/flag", `true`},
		{"an empty object", "/empty", `{}`},
		{"an empty array", "/list", `[]`},
		{"a missing member", "/thread", `absent`},
		{"an index past the end", "/tools/2", `absent`},
		{"an index that is not a number", "/tools/first", `absent`},
		{"an index with a leading zero", "/tools/00", `absent`},
		{"a member of a scalar", "/model/x", `absent`},
		{"a member of null", "/none/x", `absent`},
		{"a member of an empty array", "/list/0", `absent`},
	} {
		got, err := renderRequestPart([]byte(body), tc.pointer)
		if err != nil || got != tc.want {
			t.Errorf("%s: renderRequestPart(%q) = %q, %v; want %q", tc.name, tc.pointer, got, err, tc.want)
		}
	}
	whole, err := renderRequestPart([]byte(` {"b" : 1 , "a" : [ ] , "b" : 2} `), "")
	if want := `{"b":1,"a":[],"b":2}`; err != nil || whole != want {
		t.Errorf("the whole body = %q, %v; want %q (repeated members kept, in order)", whole, err, want)
	}
	if got, err := renderRequestPart([]byte(`{"tools":`), "/tools"); err == nil {
		t.Errorf("a cut body rendered as %q, want an error", got)
	}
}

func TestEchoRequestPointer(t *testing.T) {
	for _, tc := range []struct {
		text, pointer string
		echo          bool
	}{
		{"ECHO_REQUEST", "", true},
		{"ECHO_REQUEST /tools", "/tools", true},
		{"ECHO_REQUEST /tools/0/function\n", "/tools/0/function", true},
		{"ECHO_REQUEST tools", "", false},
		{"ECHO_REQUEST /tools /thread", "", false},
		{"ECHO_REQUESTS", "", false},
		{"echo_request /tools", "", false},
		{"please ECHO_REQUEST /tools", "", false},
		{"", "", false},
	} {
		if pointer, echo := echoRequestPointer(tc.text); pointer != tc.pointer || echo != tc.echo {
			t.Errorf("echoRequestPointer(%q) = %q, %v; want %q, %v", tc.text, pointer, echo, tc.pointer, tc.echo)
		}
	}
}

// Every API's handler answers the trigger of a user turn with the part of the body it received; the same text in a tool result, and a text that only looks like the trigger, are echoed as they are.
func TestProviderFixtureEchoesTheRequest(t *testing.T) {
	const tools = `"tools":[{"name":"z","strict":false},{"name":"a"}]`
	const rendered = `[{"name":"z","strict":false},{"name":"a"}]`
	inReply := func(text string) string {
		quoted, err := json.Marshal(text)
		if err != nil {
			t.Fatal(err)
		}
		return string(quoted)
	}
	for _, tc := range []struct{ api, body, contains string }{
		{"openai-completions", `{"messages":[{"role":"user","content":"ECHO_REQUEST /tools"}],` + tools + `}`, inReply(rendered)},
		{"openai-completions", `{` + tools + `,"messages":[{"role":"user","content":[{"type":"text","text":"ECHO_REQUEST /tools/0/strict"}]}]}`, `"content":"false"`},
		{"openai-completions", `{"messages":[{"role":"user","content":"ECHO_REQUEST /tools"}]}`, `"content":"absent"`},
		{"openai-completions", `{"messages":[{"role":"tool","content":"ECHO_REQUEST /tools"}],` + tools + `}`, `"content":"ECHO_REQUEST /tools"`},
		{"openai-completions", `{"messages":[{"role":"user","content":"ECHO_REQUEST tools"}],` + tools + `}`, `"content":"ECHO_REQUEST tools"`},
		{"mistral-conversations", `{"messages":[{"role":"user","content":[{"type":"text","text":"ECHO_REQUEST /tools"}]}],` + tools + `}`, inReply(rendered)},
		{"anthropic-messages", `{"messages":[{"role":"user","content":[{"type":"text","text":"ECHO_REQUEST /tools"}]}],` + tools + `}`, `"text":` + inReply(rendered)},
		{"anthropic-messages", `{"messages":[{"role":"user","content":"ECHO_REQUEST /thread"}],` + tools + `}`, `"text":"absent"`},
		{"anthropic-messages", `{"thread":{"type":"create"},"messages":[{"role":"user","content":"ECHO_REQUEST /thread"}]}`, `"text":` + inReply(`{"type":"create"}`)},
		{"anthropic-messages", `{"messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"t","content":"ECHO_REQUEST /tools"}]}],` + tools + `}`, `"text":"ECHO_REQUEST /tools"`},
		{"google-generative-ai", `{"contents":[{"role":"user","parts":[{"text":"ECHO_REQUEST /tools"}]}],` + tools + `}`, `"text":` + inReply(rendered)},
		{"google-generative-ai", `{"contents":[{"role":"user","parts":[{"text":"ECHO_REQUEST"}]}]}`, `"text":` + inReply(`{"contents":[{"role":"user","parts":[{"text":"ECHO_REQUEST"}]}]}`)},
	} {
		w := httptest.NewRecorder()
		providerFixtures[tc.api].handler().ServeHTTP(w, httptest.NewRequest("POST", "/", strings.NewReader(tc.body)))
		if w.Code != 200 || !strings.Contains(w.Body.String(), tc.contains) {
			t.Errorf("%s fixture output for %s: %d %s; want it to contain %s", tc.api, tc.body, w.Code, w.Body.String(), tc.contains)
		}
	}
}
