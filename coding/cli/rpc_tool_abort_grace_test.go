package cli

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

const toolAbortGraceExtension = `export default function (pi) {
  pi.registerTool({
    name: "hang_tool", label: "hang_tool", description: "Ignores its abort signal and never settles.",
    parameters: { type: "object", properties: {} },
    execute: async (_id, _params, _signal, onUpdate) => {
      onUpdate({ content: [{ type: "text", text: "waiting" }], details: {} });
      return new Promise(() => {});
    },
  });
  pi.registerTool({
    name: "coop_tool", label: "coop_tool", description: "Rejects with its own message when its signal aborts.",
    parameters: { type: "object", properties: {} },
    execute: async (_id, _params, signal, onUpdate) => {
      onUpdate({ content: [{ type: "text", text: "waiting" }], details: {} });
      return new Promise((_resolve, reject) => signal.addEventListener("abort", () => reject(new Error("coop_tool stopped by its signal")), { once: true }));
    },
  });
}
`

// pig divergence (D111): through a real Session in RPC mode, an abort while an extension tool ignores its signal ends the tool with Pi's abort text "Operation aborted" (agent-loop.ts:623) once subprocess.ToolAbortGrace has passed, the run settles, and the Session accepts the next prompt. A tool that honours its signal keeps Pi's exact outcome: its thrown message is the tool result (agent-loop.ts:831-853), well before the grace period ends.
func TestRPCAbortedExtensionToolSettlesAndFreesTheSession(t *testing.T) {
	t.Parallel()
	fixture := filepath.Join(t.TempDir(), "tool-abort-grace.mjs")
	if err := os.WriteFile(fixture, []byte(toolAbortGraceExtension), 0o600); err != nil {
		t.Fatal(err)
	}
	p := startRPCProcess(t, []string{"PIG_TEST_FAUX=1"}, "--model", "test-faux/faux-1", "--no-session", "-e", fixture)

	abortRunningTool := func(id, tool string) (string, bool, time.Duration) {
		t.Helper()
		p.send(`{"id":"` + id + `","type":"prompt","message":"Run: ext tool ` + tool + ` {}"}`)
		p.await(tool+" reporting that it waits", func(record rpcRecord) bool {
			return record["type"] == "tool_execution_update" && record["toolName"] == tool
		})
		aborted := time.Now()
		p.send(`{"id":"abort-` + id + `","type":"abort"}`)
		var text string
		var isError, ended, settled bool
		var elapsed time.Duration
		p.await(tool+" ending and the abort settling", func(record rpcRecord) bool {
			switch record["type"] {
			case "tool_execution_end":
				if record["toolName"] != tool {
					return false
				}
				elapsed = time.Since(aborted)
				ended = true
				isError, _ = record["isError"].(bool)
				result, _ := record["result"].(map[string]any)
				content, _ := result["content"].([]any)
				if len(content) == 1 {
					block, _ := content[0].(map[string]any)
					text, _ = block["text"].(string)
				}
			case "agent_settled":
				settled = true
			}
			if isSuccessResponse(record, "abort-"+id) {
				if !ended || !settled {
					t.Fatalf("abort answered before %s ended (%v) and the run settled (%v)", tool, ended, settled)
				}
				return true
			}
			return false
		})
		return text, isError, elapsed
	}

	text, isError, elapsed := abortRunningTool("hang", "hang_tool")
	if text != "Operation aborted" || !isError {
		t.Fatalf("hang_tool result = %q (isError %v), want exactly %q as an error", text, isError, "Operation aborted")
	}
	if elapsed < subprocess.ToolAbortGrace {
		t.Fatalf("hang_tool ended %v after the abort, before the %v grace period", elapsed, subprocess.ToolAbortGrace)
	}

	// The Session is free: the next prompt runs a tool, and that tool's own rejection is its result.
	text, isError, elapsed = abortRunningTool("coop", "coop_tool")
	if text != "coop_tool stopped by its signal" || !isError {
		t.Fatalf("coop_tool result = %q (isError %v), want its own thrown message as an error", text, isError)
	}
	if elapsed >= subprocess.ToolAbortGrace {
		t.Fatalf("coop_tool ended %v after the abort; a tool that honours its signal must not wait out the %v grace period", elapsed, subprocess.ToolAbortGrace)
	}
	p.closeAndWait("after aborting both extension tools")
}
