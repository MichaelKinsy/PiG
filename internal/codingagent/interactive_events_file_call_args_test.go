package codingagent

import (
	"crypto/sha256"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// A read or write call's arguments are kept from tool_execution_start for the file renderer the end event attaches
// (toolBodyRendererForCall), which reads three members: file_path, path and content. Each is a string, absent (missing or null), or a
// value of another type, and the renderer tells the three apart: a file_path of another type hides path, a content of another type is
// the invalid-content row. The table fixes what the renderer is given for every form the arguments text can take, and the rows the
// renderer then draws: they are the rows it draws for a call whose arguments are the table's expected members, and a write call
// whose content is not a string draws upstream's "[invalid content arg - expected string]" (renderers/write.ts:122). The test logs a
// digest of every drawn row, so two runs of it on two decoders can be compared.
func TestToolExecutionStartKeepsTheFileCallArguments(t *testing.T) {
	type other struct{} // a value that is neither absent nor a string
	shape := func(value any) any {
		switch v := value.(type) {
		case nil:
			return nil
		case string:
			return v
		}
		return other{}
	}
	cases := []struct {
		name, args              string
		filePath, path, content any
	}{
		{name: "path", args: `{"path":"a.txt"}`, path: "a.txt"},
		{name: "all three", args: `{"file_path":"f.txt","path":"p.txt","content":"body"}`, filePath: "f.txt", path: "p.txt", content: "body"},
		{name: "whitespace", args: " {\n\t\"path\" : \"a.txt\" ,\r\n \"content\" : \"\" } \n", path: "a.txt", content: ""},
		{name: "empty object", args: `{}`},
		{name: "empty strings", args: `{"file_path":"","path":"","content":""}`, filePath: "", path: "", content: ""},

		{name: "unknown members", args: `{"limit":5,"path":"p.txt","offset":{"path":"inner"},"more":[{"content":"x"}],"content":"c"}`, path: "p.txt", content: "c"},
		{name: "member names of another case", args: `{"Path":"x","FILE_PATH":"y","Content":"z","filePath":"w"}`},
		{name: "another case beside the exact name", args: `{"path":"exact","PATH":"folded","content":"c","CONTENT":7}`, path: "exact", content: "c"},
		{name: "another case before the exact name", args: `{"PATH":"folded","path":"exact","File_Path":null,"file_path":"f"}`, filePath: "f", path: "exact"},

		{name: "null members", args: `{"file_path":null,"path":null,"content":null}`},
		{name: "null file_path", args: `{"file_path":null,"path":"p.txt"}`, path: "p.txt"},
		{name: "number members", args: `{"file_path":7,"path":1.5,"content":0}`, filePath: other{}, path: other{}, content: other{}},
		{name: "boolean members", args: `{"file_path":false,"path":true,"content":false}`, filePath: other{}, path: other{}, content: other{}},
		{name: "array members", args: `{"file_path":[],"path":["p"],"content":[null]}`, filePath: other{}, path: other{}, content: other{}},
		{name: "object members", args: `{"file_path":{},"path":{"path":"p"},"content":{"a":[1,{"b":null}]}}`, filePath: other{}, path: other{}, content: other{}},
		{name: "number file_path hides path", args: `{"file_path":7,"path":"p.txt"}`, filePath: other{}, path: "p.txt"},

		{name: "escapes", args: `{"content":"a\n\t\"\\\/\u00e9\ud83d\ude00\u0000"}`, content: "a\n\t\"\\/\u00e9\U0001F600\x00"},
		{name: "lone surrogate", args: `{"path":"\ud800.txt","content":"x\udc00"}`, path: "\ufffd.txt", content: "x\ufffd"},
		{name: "invalid UTF-8", args: "{\"path\":\"a\xff.txt\",\"content\":\"\xc3(\"}", path: "a\ufffd.txt", content: "\ufffd("},
		{name: "escaped member names", args: `{"\u0070ath":"p.txt","conten\u0074":"c"}`, path: "p.txt", content: "c"},

		{name: "repeated string", args: `{"path":"a","path":"b"}`, path: "b"},
		{name: "repeated then null", args: `{"path":"a","content":"c","path":null,"content":null}`},
		{name: "repeated null then string", args: `{"file_path":null,"file_path":"f"}`, filePath: "f"},
		{name: "repeated string then number", args: `{"path":"a","path":3}`, path: other{}},
		{name: "repeated object then string", args: `{"file_path":{"a":1},"file_path":"s","content":[1],"content":"c"}`, filePath: "s", content: "c"},
		{name: "repeated object then object", args: `{"content":{"a":1},"content":{"b":2}}`, content: other{}},

		{name: "number out of range in an unknown member", args: `{"path":"p.txt","limit":1e999,"content":"c"}`, path: "p.txt", content: "c"},
		{name: "number out of range in a read member", args: `{"file_path":1e999,"path":"p.txt","content":-1e999}`, filePath: other{}, path: "p.txt", content: other{}},
		{name: "large numbers in range", args: `{"file_path":1e308,"content":123456789012345678901234567890}`, filePath: other{}, content: other{}},

		{name: "null", args: `null`},
		{name: "array", args: `["path"]`},
		{name: "string", args: `"path"`},
		{name: "number", args: `5`},
		{name: "empty text", args: ``},
		{name: "only whitespace", args: ` `},
		{name: "cut after a member", args: `{"path":"p.txt",`},
		{name: "cut inside a string", args: `{"path":"p.txt","content":"c`},
		{name: "cut inside an unknown member", args: `{"path":"p.txt","content":"c","limit":[1,`},
		{name: "text after the object", args: `{"path":"p.txt"} x`},
		{name: "second object", args: `{"path":"p.txt"}{"path":"q.txt"}`},
		{name: "control character in a string", args: "{\"path\":\"p\n.txt\"}"},
		{name: "control character in an unknown member", args: "{\"path\":\"p.txt\",\"note\":\"a\tb\"}"},
		{name: "single quotes", args: `{'path':'p.txt'}`},
		{name: "trailing comma", args: `{"path":"p.txt",}`},
		{name: "invalid literal in an unknown member", args: `{"path":"p.txt","flag":tru}`},
		{name: "invalid number in a read member", args: `{"path":"p.txt","content":01}`},
	}
	result := agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "one\ntwo"}}}
	drawn := sha256.New()
	for _, tool := range []string{"read", "write"} {
		for _, c := range cases {
			t.Run(tool+"/"+c.name, func(t *testing.T) {
				p := newEventProbe(t, false)
				p.handleAgentEvent(agent.ToolExecutionStartEvent{ToolCallID: "a", ToolName: tool, Args: json.RawMessage(c.args)})
				call, kept := p.toolFileCalls["a"]
				if !kept || call.Name != tool {
					t.Fatalf("kept=%v name=%q, want the %s call", kept, call.Name, tool)
				}
				for _, member := range []struct {
					name string
					want any
				}{{"file_path", c.filePath}, {"path", c.path}, {"content", c.content}} {
					if got := shape(call.Arguments[member.name]); got != member.want {
						t.Errorf("%s of %s = %#v, want %#v", member.name, c.args, got, member.want)
					}
				}
				want := ai.JsonObject{}
				for name, value := range map[string]any{"file_path": c.filePath, "path": c.path, "content": c.content} {
					switch value.(type) {
					case nil:
					case string:
						want[name] = value
					default:
						want[name] = 1.0
					}
				}
				rows := toolBodyRendererForCall(call, result, nil)(80, true)
				if wantRows := toolBodyRendererForCall(ai.ToolCall{Name: tool, Arguments: want}, result, nil)(80, true); !slices.Equal(rows, wantRows) {
					t.Errorf("rows of %s:\n%q\nwant the rows of %v:\n%q", c.args, rows, want, wantRows)
				}
				_, stringContent := c.content.(string)
				if invalid := strings.Contains(strings.Join(rows, "\n"), "[invalid content arg - expected string]"); invalid != (tool == "write" && !stringContent) {
					t.Errorf("rows of %s: invalid content row = %v\n%q", c.args, invalid, rows)
				}
				drawn.Write([]byte(tool + "\x00" + c.name + "\x00" + strings.Join(rows, "\n") + "\x00"))
			})
		}
	}
	t.Logf("digest of the drawn rows: %x", drawn.Sum(nil))
	t.Run("another tool keeps no call", func(t *testing.T) {
		p := newEventProbe(t, false)
		p.handleAgentEvent(agent.ToolExecutionStartEvent{ToolCallID: "a", ToolName: "edit", Args: json.RawMessage(`{"path":"p.txt"}`)})
		if len(p.toolFileCalls) != 0 {
			t.Fatalf("an edit call kept %d file calls", len(p.toolFileCalls))
		}
	})
}

// The tool_execution_start of a write call whose card exists: the arguments are read for the file renderer on every such event.
func BenchmarkToolExecutionStartFileCall(b *testing.B) {
	m, _ := newTickRenderProbe(b, "regular")
	event := agent.ToolExecutionStartEvent{ToolCallID: "a", ToolName: "write", Args: json.RawMessage(`{"path":"src/main.go","content":"` + strings.Repeat(`a line of the file\n`, 200) + `","note":{"a":[1,2,3],"b":"c"}}`)}
	m.handleAgentEvent(event)
	b.ReportAllocs()
	for b.Loop() {
		m.handleAgentEvent(event)
	}
}
