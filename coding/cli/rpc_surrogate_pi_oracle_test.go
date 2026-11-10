package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/rpcclient"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// Pi parses each RPC line with JSON.parse and writes each frame with JSON.stringify, so a lone UTF-16 unit in a command, a dialog answer or a model's tool arguments keeps its unit end to end. The expected values come from the qualified Node.

// piEval runs body under Node with the JSON text of input (a lone unit as its escape) and decodes the JSON answer.
func piEval[T any](t *testing.T, body string, input any) T {
	t.Helper()
	encoded, err := jsstring.MarshalKeepingSurrogates(input)
	if err != nil {
		t.Fatal(err)
	}
	var out T
	if err := jsstring.UnmarshalKeepingSurrogates(testenv.NodeJSON(t, body, encoded), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestRPCDialogAnswerKeepsALoneSurrogate(t *testing.T) {
	// rpc-mode.ts resolves an input dialog with `response.value` as JSON.parse read it.
	const line = `{"type":"extension_ui_response","id":"d1","value":"a\ud800<&>"}`
	want := piEval[string](t, `return JSON.parse(input).value;`, line)
	var response rpcUIResponse
	if err := json.Unmarshal([]byte(line), &response); err != nil {
		t.Fatal(err)
	}
	got, err := response.stringValue()
	if err != nil || got != want {
		t.Fatalf("dialog value units=%x err=%v, want %x", jsstring.ToUTF16(got), err, jsstring.ToUTF16(want))
	}
}

func TestRPCCommandErrorTextKeepsALoneSurrogate(t *testing.T) {
	// rpc-mode.ts set_model: `Model not found: ${command.provider}/${command.modelId}` coerces each field as JavaScript does.
	const line = `{"type":"set_model","provider":["\ud800"],"modelId":["x\udfff",null]}`
	want := piEval[string](t, `const command = JSON.parse(input); return `+"`Model not found: ${command.provider}/${command.modelId}`"+`;`, line)
	_, message, done := rpcJSAdapt(RPCCommandEnvelope{Type: RPCCommandSetModel, Raw: json.RawMessage(line)})
	if !done || message != want {
		t.Fatalf("message units=%x done=%v, want %x", jsstring.ToUTF16(message), done, jsstring.ToUTF16(want))
	}
}

func TestRPCBashCommandRewriteKeepsALoneSurrogate(t *testing.T) {
	// A bash command that is not a string is coerced by the template literal and run as that text.
	const line = `{"type":"bash","command":["\ud800","<&>"]}`
	want := piEval[string](t, "return `${JSON.parse(input).command}`;", line)
	env, _, _ := rpcJSAdapt(RPCCommandEnvelope{Type: RPCCommandBash, Raw: json.RawMessage(line)})
	var command struct {
		Command string `json:"command"`
	}
	if err := jsstring.UnmarshalKeepingSurrogates(env.Raw, &command); err != nil || command.Command != want {
		t.Fatalf("command units=%x err=%v, want %x", jsstring.ToUTF16(command.Command), err, jsstring.ToUTF16(want))
	}
}

func TestRPCToolCallEndFrameIsWhatPisJSONStringifyWrites(t *testing.T) {
	lone := jsstring.FromUTF16([]uint16{0xd83d})
	call := ai.ToolCall{ID: "c1", Name: "tool", Arguments: ai.JsonObject{"text": "a" + lone + "<&>", "list": []any{lone}}}
	partial := jsonTestPartial(call)
	frames := drive(t, jsonTestUpdate(ai.ToolCallEndEvent{ContentIndex: 0, ToolCall: call, Partial: partial}, partial))
	if len(frames) != 1 {
		t.Fatalf("%d frames", len(frames))
	}
	encoded, err := rpcclient.SerializeJsonLine(frames[0])
	if err != nil {
		t.Fatal(err)
	}
	line := strings.TrimSuffix(string(encoded), "\n")
	want := piEval[string](t, `return JSON.stringify(JSON.parse(input));`, line)
	if line != want || !strings.Contains(line, `"text":"a\ud83d<&>"`) || !strings.Contains(line, `"list":["\ud83d"]`) {
		t.Fatalf("frame=%s\nPi's  %s", line, want)
	}
}
