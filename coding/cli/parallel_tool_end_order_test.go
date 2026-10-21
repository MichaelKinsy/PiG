//go:build !pig_strip_mcp

package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Pi runs the calls of one assistant message through Promise.all(map) (agent-loop.ts:586-647): each call's tool_execution_end
// follows its own execute promise, so calls whose tools settle back to back end in the order they settled. Eight calls wait on
// timers of equal length that the one event loop creates in call order and fires in call order, so Pi ends them 1..8 on every run.
// PiG reads the eight answers of the extension's runtime in that order and woke the goroutines that waited for them in none:
// the ends came out in a different order on most runs.
const settleOrderExtension = `export default function (pi) {
  pi.registerTool({
    name: "settle_order", label: "settle_order", description: "Wait on a timer, then answer with the call's number",
    parameters: { type: "object", required: ["n"], properties: { n: { type: "number" } } },
    async execute(_id, params) {
      await new Promise((resolve) => setTimeout(resolve, 50));
      return { content: [{ type: "text", text: "settled#" + params.n }], details: {} };
    },
  });
}
`

func TestNodeExtensionParallelToolCallsEndInTheOrderTheirToolsSettle(t *testing.T) {
	const n = 8
	binary := buildPigBinaryForSignalTest(t)
	calls := make([][2]string, n)
	for i := range n {
		calls[i] = [2]string{"settle_order", fmt.Sprintf(`{"n":%d}`, i+1)}
	}
	extension := filepath.Join(t.TempDir(), "settle-order.mjs")
	if err := os.WriteFile(extension, []byte(settleOrderExtension), 0o600); err != nil {
		t.Fatal(err)
	}
	// Each run is one process: the ends of one batch are a sample of the race, so several processes bound the chance that a
	// run that races passes by luck.
	for run := range 5 {
		home, cwd := t.TempDir(), t.TempDir()
		agentDir := filepath.Join(home, "pig")
		if err := os.MkdirAll(agentDir, 0o700); err != nil {
			t.Fatal(err)
		}
		chat := startScriptedChatServer(t, func(chatRequest) chatReply { return chatReply{Calls: calls} }, chatText("done"))
		models, _ := json.Marshal(map[string]any{"providers": map[string]any{"fixture": map[string]any{
			"api": "openai-completions", "baseUrl": chat.URL, "apiKey": "fixture",
			"models": []any{map[string]any{"id": "test", "name": "Test", "contextWindow": 100000, "maxTokens": 4096}},
		}}})
		if err := os.WriteFile(filepath.Join(agentDir, "models.json"), models, 0o600); err != nil {
			t.Fatal(err)
		}
		cmd := exec.CommandContext(t.Context(), binary, "--no-extensions", "-e", extension, "--model", "fixture/test", "--no-skills", "--no-prompt-templates", "--no-context-files", "--no-session", "--mode", "json", "go")
		cmd.Dir = cwd
		cmd.Env = append(os.Environ(), "HOME="+home, "PIG_HOME="+filepath.Join(home, ".pig"), "PIG_CODING_AGENT_DIR="+agentDir, "PIG_OFFLINE=1", "PI_SKIP_VERSION_CHECK=1")
		cmd.Stdin = strings.NewReader("")
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("json mode: %v\n%s", err, out)
		}
		var ends []string
		for line := range strings.SplitSeq(string(out), "\n") {
			var event struct {
				Type   string `json:"type"`
				Result struct {
					Content []struct {
						Text string `json:"text"`
					} `json:"content"`
				} `json:"result"`
			}
			if json.Unmarshal([]byte(line), &event) != nil || event.Type != "tool_execution_end" || len(event.Result.Content) == 0 {
				continue
			}
			ends = append(ends, event.Result.Content[0].Text)
		}
		want := make([]string, n)
		for i := range n {
			want[i] = fmt.Sprintf("settled#%d", i+1)
		}
		if strings.Join(ends, " ") != strings.Join(want, " ") {
			t.Fatalf("run %d: tool_execution_end order = %q, want %q (the order the tools settled)", run, ends, want)
		}
	}
}
