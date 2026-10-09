package cli

import (
	"encoding/json"
	"testing"
)

// Each row is what Pi 1.1.0's rpc-mode.ts answered for the command (scenario 49 compares the live pair): an error message, or the typed value its
// handler went on with.
func TestRPCJSAdaptMatchesPiForUntypedFields(t *testing.T) {
	for _, tc := range []struct {
		name, line, message string
		rewritten           map[string]any
	}{
		{"prompt missing", `{"type":"prompt"}`, "Cannot read properties of undefined (reading 'startsWith')", nil},
		{"prompt number", `{"type":"prompt","message":5}`, "text.startsWith is not a function", nil},
		{"steer array", `{"type":"steer","message":["a"]}`, "text.startsWith is not a function", nil},
		{"follow_up null", `{"type":"follow_up","message":null}`, "Cannot read properties of null (reading 'startsWith')", nil},
		{"prompt string", `{"type":"prompt","message":"hi"}`, "", nil},
		{"set_model empty", `{"type":"set_model"}`, "Model not found: undefined/undefined", nil},
		{"set_model null id", `{"type":"set_model","provider":"fixture","modelId":null}`, "Model not found: fixture/null", nil},
		{"set_model composite", `{"type":"set_model","provider":{"a":1},"modelId":["x","y"]}`, "Model not found: [object Object]/x,y", nil},
		{"set_model numbers", `{"type":"set_model","provider":true,"modelId":1.5}`, "Model not found: true/1.5", nil},
		{"set_model exponent", `{"type":"set_model","provider":null,"modelId":1e21}`, "Model not found: null/1e+21", nil},
		{"set_session_name missing", `{"type":"set_session_name"}`, "Cannot read properties of undefined (reading 'trim')", nil},
		{"set_session_name null", `{"type":"set_session_name","name":null}`, "Cannot read properties of null (reading 'trim')", nil},
		{"set_session_name object", `{"type":"set_session_name","name":{}}`, "command.name.trim is not a function", nil},
		{"switch_session missing", `{"type":"switch_session"}`, "Cannot read properties of undefined (reading 'startsWith')", nil},
		{"switch_session bool", `{"type":"switch_session","sessionPath":true}`, "normalized.startsWith is not a function", nil},
		{"fork number", `{"type":"fork","entryId":5}`, "Invalid entry ID for forking", nil},
		{"get_entries null", `{"type":"get_entries","since":null}`, "Entry not found: null", nil},
		{"get_entries object", `{"type":"get_entries","since":{"a":1}}`, "Entry not found: [object Object]", nil},
		{"get_entries absent", `{"type":"get_entries"}`, "", nil},
		{"bash missing", `{"type":"bash"}`, "", map[string]any{"command": "undefined"}},
		{"bash array", `{"type":"bash","command":["echo","hi"]}`, "", map[string]any{"command": "echo,hi"}},
		{"bash exclude truthy", `{"type":"bash","command":"x","excludeFromContext":"yes"}`, "", map[string]any{"excludeFromContext": true}},
		{"bash exclude zero", `{"type":"bash","command":"x","excludeFromContext":0}`, "", map[string]any{"excludeFromContext": false}},
		{"thinking object", `{"type":"set_thinking_level","level":{"a":1}}`, "", map[string]any{"level": "\x00"}},
		{"auto compaction string", `{"type":"set_auto_compaction","enabled":"false"}`, "", map[string]any{"enabled": true}},
		{"auto compaction missing", `{"type":"set_auto_compaction"}`, "", map[string]any{"enabled": false}},
		{"auto retry empty array", `{"type":"set_auto_retry","enabled":[]}`, "", map[string]any{"enabled": true}},
		{"new_session object parent", `{"type":"new_session","parentSession":{"a":1}}`, "", map[string]any{"parentSession": nil}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env, err := parseRPCCommand([]byte(tc.line))
			if err != nil {
				t.Fatal(err)
			}
			adapted, message, failed := rpcJSAdapt(env)
			if failed != (tc.message != "") || message != tc.message {
				t.Fatalf("message = %q (failed %v), want %q", message, failed, tc.message)
			}
			var fields map[string]any
			if err := json.Unmarshal(adapted.Raw, &fields); err != nil {
				t.Fatal(err)
			}
			for name, want := range tc.rewritten {
				if got, present := fields[name]; got != want || (want == nil && present) {
					t.Errorf("field %s = %v (present %v), want %v", name, got, present, want)
				}
			}
		})
	}
}

// Pi 1.1.0 answers for the images of prompt, steer and follow_up (scenario 52 compares the live pair): prompt converts each image (Buffer.from(data,
// "base64"), then mimeType.split) and fails on the first malformed one; steer and follow_up only spread the array.
func TestRPCJSAdaptImagesMatchPi(t *testing.T) {
	const buffer = "The first argument must be of type string or an instance of Buffer, ArrayBuffer, or Array or an Array-like Object. Received "
	for _, tc := range []struct {
		name, line, message string
		kept                *int
	}{
		{"prompt string", `{"type":"prompt","message":"m","images":"x"}`, buffer + "undefined", nil},
		{"prompt object", `{"type":"prompt","message":"m","images":{}}`, "images is not iterable", nil},
		{"prompt number", `{"type":"prompt","message":"m","images":5}`, "images is not iterable", nil},
		{"prompt true", `{"type":"prompt","message":"m","images":true}`, "images is not iterable", nil},
		{"prompt false", `{"type":"prompt","message":"m","images":false}`, "", nil},
		{"prompt null element", `{"type":"prompt","message":"m","images":[null]}`, "Cannot read properties of null (reading 'data')", nil},
		{"prompt number element", `{"type":"prompt","message":"m","images":[5]}`, buffer + "undefined", nil},
		{"prompt no data", `{"type":"prompt","message":"m","images":[{"type":"image","mimeType":"image/png"}]}`, buffer + "undefined", nil},
		{"prompt data null", `{"type":"prompt","message":"m","images":[{"data":null,"mimeType":"image/png"}]}`, buffer + "null", nil},
		{"prompt data number", `{"type":"prompt","message":"m","images":[{"data":5,"mimeType":"image/png"}]}`, buffer + "type number (5)", nil},
		{"prompt data float", `{"type":"prompt","message":"m","images":[{"data":1.5,"mimeType":"image/png"}]}`, buffer + "type number (1.5)", nil},
		{"prompt data exponent", `{"type":"prompt","message":"m","images":[{"data":1e21,"mimeType":"image/png"}]}`, buffer + "type number (1e+21)", nil},
		{"prompt data negative zero", `{"type":"prompt","message":"m","images":[{"data":-0.0,"mimeType":"image/png"}]}`, buffer + "type number (-0)", nil},
		{"prompt data bool", `{"type":"prompt","message":"m","images":[{"data":true,"mimeType":"image/png"}]}`, buffer + "type boolean (true)", nil},
		{"prompt data object", `{"type":"prompt","message":"m","images":[{"data":{},"mimeType":"image/png"}]}`, buffer + "an instance of Object", nil},
		{"prompt no mime", `{"type":"prompt","message":"m","images":[{"data":"aGk="}]}`, "Cannot read properties of undefined (reading 'split')", nil},
		{"prompt mime null", `{"type":"prompt","message":"m","images":[{"data":"aGk=","mimeType":null}]}`, "Cannot read properties of null (reading 'split')", nil},
		{"prompt mime number", `{"type":"prompt","message":"m","images":[{"data":"aGk=","mimeType":5}]}`, "mimeType.split is not a function", nil},
		{"prompt second image bad", `{"type":"prompt","message":"m","images":[{"data":"aGk=","mimeType":"image/png"},{"type":"image"}]}`, buffer + "undefined", nil},
		{"prompt good", `{"type":"prompt","message":"m","images":[{"data":"aGk=","mimeType":"image/png"}]}`, "", nil},
		{"steer object", `{"type":"steer","message":"m","images":{}}`, "Spread syntax requires ...iterable[Symbol.iterator] to be a function", nil},
		{"follow_up true", `{"type":"follow_up","message":"m","images":true}`, "Spread syntax requires ...iterable[Symbol.iterator] to be a function", nil},
		{"steer number", `{"type":"steer","message":"m","images":5}`, "Spread syntax requires ...iterable[Symbol.iterator] to be a function", nil},
		{"steer string queues", `{"type":"steer","message":"m","images":"ab"}`, "", new(0)},
		{"steer malformed queues", `{"type":"steer","message":"m","images":[null,5,{"type":"image"},{"data":"aGk=","mimeType":"image/png"}]}`, "", new(1)},
		{"steer false", `{"type":"steer","message":"m","images":0}`, "", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env, err := parseRPCCommand([]byte(tc.line))
			if err != nil {
				t.Fatal(err)
			}
			adapted, message, failed := rpcJSAdapt(env)
			if failed != (tc.message != "") || message != tc.message {
				t.Fatalf("message = %q (failed %v), want %q", message, failed, tc.message)
			}
			if failed {
				return
			}
			var fields struct {
				Images *[]json.RawMessage `json:"images"`
			}
			if err := json.Unmarshal(adapted.Raw, &fields); err != nil {
				t.Fatalf("adapted command does not decode: %v", err)
			}
			if tc.kept != nil && (fields.Images == nil || len(*fields.Images) != *tc.kept) {
				t.Fatalf("images = %v, want %d kept", fields.Images, *tc.kept)
			}
		})
	}
}
