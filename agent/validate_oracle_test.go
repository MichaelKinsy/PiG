package agent

// pi: packages/ai/src/utils/validation.ts, packages/agent/src/agent-loop.ts:726-767 (prepareToolCall)

import (
	"bytes"
	"context"
	"encoding/json"
	"os/exec"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// agentValidationSchemas are the shapes of the built-in tools' parameters (read, bash, edit, write, grep) and of plain JSON Schema tools (an MCP server's, an extension's).
// ai/validate_pi_builtin_oracle_test.go compares the declared schemas of every built-in tool with Pi's validation; this test compares the agent's preparation of a call with it.
var agentValidationSchemas = []string{
	`{"type":"object","required":["path"],"properties":{"path":{"type":"string"},"offset":{"type":"number"},"limit":{"type":"number"}}}`,
	`{"type":"object","required":["command"],"properties":{"command":{"type":"string"},"timeout":{"type":"number"}}}`,
	`{"type":"object","required":["path","edits"],"properties":{"path":{"type":"string"},"edits":{"type":"array","items":{"type":"object","required":["oldText","newText"],"properties":{"oldText":{"type":"string"},"newText":{"type":"string"}}}}}}`,
	`{"type":"object","required":["path","content"],"properties":{"path":{"type":"string"},"content":{"type":"string"}}}`,
	`{"type":"object","required":["pattern"],"properties":{"pattern":{"type":"string"},"path":{"type":"string"},"glob":{"type":"string"},"ignoreCase":{"type":"boolean"},"literal":{"type":"boolean"},"context":{"type":"number"},"limit":{"type":"number"}}}`,
	`{"type":"object","properties":{"mode":{"enum":["fast","deep"]},"n":{"type":["integer","null"]},"tags":{"anyOf":[{"type":"string"},{"type":"array","items":{"type":"string"}}]},"u":{"type":"string","format":"url"}},"additionalProperties":false}`,
	`{"type":"object","properties":{"t":{"$ref":"#/$defs/Missing"},"v":{"$ref":"#/$defs/V"}},"$defs":{"V":{"oneOf":[{"type":"integer"},{"type":"string","maxLength":3}]}}}`,
	`{"type":"object","properties":{"a":{"type":"string"},"b":{"type":"string"}},"patternProperties":{"^x-":{"type":"boolean"}},"dependentRequired":{"a":["b"]}}`,
}

var agentValidationArguments = []string{
	`{}`, `null`, `[]`, `"x"`, `7`,
	`{"path":"a.txt"}`, `{"path":"a.txt","offset":"2","limit":null}`, `{"path":3,"offset":"x"}`, `{"path":"a","extra":{"untouched":"10"}}`,
	`{"command":"ls","timeout":"10"}`, `{"command":null}`, `{"command":["ls"],"timeout":true}`,
	`{"path":"f","edits":[{"oldText":"a","newText":"b"}]}`, `{"path":"f","edits":{"oldText":"a","newText":"b"}}`, `{"path":"f","edits":[{"oldText":1,"newText":null},{"oldText":"x"}]}`, `{"path":"f","edits":[]}`, `{"path":"f","edits":"x"}`,
	`{"path":"f","content":"hello"}`, `{"path":"f","content":12}`, `{"path":"f","content":"\ud800é😀"}`, `{"content":"x"}`,
	`{"pattern":"a","ignoreCase":"true","context":"3","limit":"10"}`, `{"pattern":"a","literal":"yes"}`, `{"pattern":1,"glob":null}`,
	`{"mode":"fast","n":"3","tags":"a","u":"https://x.y/z"}`, `{"mode":"slow","n":null,"tags":["a",1],"u":"nope"}`, `{"mode":"deep","extra":1}`,
	`{"t":1,"v":"abc"}`, `{"v":"abcd"}`, `{"v":2.5}`, `{"t":null}`,
	`{"a":"x"}`, `{"a":"x","b":"y","x-1":"true","x-2":"maybe"}`, `{"b":"y","10":1,"2":2,"a":"x"}`,
}

// agentValidationProbe runs the call through the agent's preparation: Pi's prepareToolCall validates the arguments with validateToolArguments and returns the validated arguments or the error text.
func agentValidationProbe(t *testing.T, schema, args string) (arguments string, failure string) {
	t.Helper()
	var params map[string]any
	if err := json.Unmarshal([]byte(schema), &params); err != nil {
		t.Fatal(err)
	}
	tool := &fakeTool{name: "probe", params: params}
	a := mustNewAgent(AgentOptions{Tools: []AgentTool{tool}})
	call := pendingToolCall{id: "call", name: "probe", args: toolCallArguments(json.RawMessage(args))}
	got := a.testHost().prepareToolCall(context.Background(), call)
	if got.prepared == nil {
		if got.finalized == nil {
			t.Fatalf("schema %s args %s: neither prepared nor finalized", schema, args)
		}
		return "", got.finalized.result.Text()
	}
	return string(got.prepared.args), ""
}

// The agent's preparation of a tool call answers as Pi's validateToolArguments does on the same schema and arguments: the same arguments in the same member order, or the same error text.
func TestToolArgumentValidationMatchesPi(t *testing.T) {
	type probe struct{ Name, Schema, Args string }
	var probes []probe
	for _, schema := range agentValidationSchemas {
		for _, args := range agentValidationArguments {
			probes = append(probes, probe{"probe", schema, args})
		}
	}
	wire := make([]map[string]string, len(probes))
	for i, p := range probes {
		wire[i] = map[string]string{"name": p.Name, "schema": p.Schema, "args": p.Args}
	}
	payload, err := json.Marshal(wire)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "--disable-warning=ExperimentalWarning", "../ai/testdata/validate_tool_args.mjs", testenv.ModuleRoot(t))
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle (the mirror .upstream/current must be Pi b556cfe5 or later: make upstream-mirror PI_UPSTREAM_REF=b556cfe5): %v\n%s", err, &stderr)
	}
	var expected []struct {
		Ok  *string `json:"ok"`
		Err *string `json:"err"`
	}
	if err := jsstring.UnmarshalKeepingSurrogates(output, &expected); err != nil {
		t.Fatal(err)
	}
	if len(expected) != len(probes) {
		t.Fatalf("Pi answered %d of %d probes", len(expected), len(probes))
	}
	accepted, differing := 0, 0
	for i, p := range probes {
		arguments, failure := agentValidationProbe(t, p.Schema, p.Args)
		want := expected[i]
		same := false
		switch {
		case want.Err != nil:
			same = failure == *want.Err
		case want.Ok != nil:
			accepted++
			same = failure == "" && arguments == *want.Ok
		}
		if !same {
			if differing++; differing <= 8 {
				t.Errorf("schema %s\n args %s\n  Pig %q / %q", p.Schema, p.Args, arguments, failure)
			}
		}
	}
	if differing > 8 {
		t.Errorf("%d of %d probes differ from Pi", differing, len(probes))
	}
	if accepted < len(probes)/10 || accepted > len(probes)*9/10 {
		t.Errorf("probes are one-sided: %d accepted of %d", accepted, len(probes))
	}
}
