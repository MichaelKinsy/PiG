package subprocess

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

const throwerExtension = `export default function (pi) {
  pi.on("tool_call", (event) => {
    switch (event.input.mode) {
      case "string": throw "nope";
      case "error": throw new Error("boom");
      case "empty": throw new Error("");
      case "message-object": throw { message: "not an Error" };
      case "undefined": throw undefined;
      case "number": throw 7;
    }
  });
  pi.registerTool({
    name: "thrower",
    description: "throws",
    parameters: { type: "object", properties: { mode: { type: "string" } } },
    async execute(_id, params) {
      if (params.mode === "empty") throw new Error("");
      throw { message: "not an Error" };
    },
  });
}
`

// A tool_call handler's throw reaches the host as Pi's agent-session.ts _beforeToolCall words it: an Error is rethrown with its own message, even an empty one, and any other thrown value becomes `Extension failed, blocking execution: ${String(err)}`. Pi 1.1.0 print --mode json with a throwing tool_call handler reports exactly these tool result texts.
func TestToolCallHandlerThrowIsWordedAsPi(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "thrower.mjs")
	if err := os.WriteFile(path, []byte(throwerExtension), 0o600); err != nil {
		t.Fatal(err)
	}
	config := ExtConfig{Name: "thrower", Source: path, Enabled: true}
	host := NewHostWithConfigRoot(root, filepath.Join(root, "config"))
	t.Cleanup(func() { host.Shutdown("test") })
	loaded, errs := host.LoadAll(t.Context(), []ExtConfig{config})
	if len(errs) != 0 || len(loaded) != 1 {
		t.Fatalf("loaded=%v errors=%v", loaded, errs)
	}
	runner := inproc.NewRunner(loaded, root)
	for mode, want := range map[string]string{
		"string":         "Extension failed, blocking execution: nope",
		"error":          "boom",
		"empty":          "",
		"message-object": "Extension failed, blocking execution: [object Object]",
		"undefined":      "Extension failed, blocking execution: undefined",
		"number":         "Extension failed, blocking execution: 7",
	} {
		_, err := runner.EmitToolCall(t.Context(), extension.CustomToolCallEvent{
			ToolCallEventBase: extension.ToolCallEventBase{Type: "tool_call", ToolCallID: mode},
			ToolName:          "bash",
			Input:             map[string]any{"mode": mode},
		})
		if err == nil {
			t.Errorf("%s: EmitToolCall returned no error", mode)
			continue
		}
		if err.Error() != want {
			t.Errorf("%s: error = %q, want %q", mode, err.Error(), want)
		}
	}
}

// A tool's thrown value reaches the host as Pi's agent-loop.ts reports it, `error instanceof Error ? error.message : String(error)`: an Error with an empty message is empty, not "Error", and an object with a message property is "[object Object]".
func TestToolExecuteThrowIsWordedAsPi(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "thrower.mjs")
	if err := os.WriteFile(path, []byte(throwerExtension), 0o600); err != nil {
		t.Fatal(err)
	}
	config := ExtConfig{Name: "thrower", Source: path, Enabled: true}
	host := NewHostWithConfigRoot(root, filepath.Join(root, "config"))
	t.Cleanup(func() { host.Shutdown("test") })
	loaded, errs := host.LoadAll(t.Context(), []ExtConfig{config})
	if len(errs) != 0 || len(loaded) != 1 {
		t.Fatalf("loaded=%v errors=%v", loaded, errs)
	}
	tool := loaded[0].Tools["thrower"].Definition
	for mode, want := range map[string]string{"empty": "", "message-object": "[object Object]"} {
		_, err := tool.Execute(t.Context(), mode, json.RawMessage(`{"mode":"`+mode+`"}`), nil)
		if err == nil || err.Error() != want {
			t.Errorf("%s: error = %v, want %q", mode, err, want)
		}
	}
}
