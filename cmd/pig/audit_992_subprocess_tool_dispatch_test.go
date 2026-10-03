//go:build !pig_strip_mcp

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Real-binary probes of how a model-issued call reaches a Node extension tool, found while probing the renderShell
// case of tool-renderer-examples.test.ts (minimal-mode.ts spreads createEditToolDefinition, which carries
// prepareArguments, into its edit override). Each expectation was measured on real Pi 0.99.2 with the same extension
// and an OpenAI-compatible endpoint that streams the same tool calls. The argument member order the same probe found
// (audit-992-codemode-mcp CM3) belongs to another lane and is not asserted here:
//
//   - agent-loop.ts:707-716 (prepareToolCall) runs tool.prepareArguments before validateToolArguments, so a legacy
//     argument shape is rewritten before the schema check. Pi answers "prepared:hello".
//   - agent-loop.ts:586-647 (executeToolCallsParallel) starts every prepared call through Promise.all(map) in source
//     order, so each execute's synchronous prefix runs in call order. Pi answers start#1..start#N in source order.
const auditDispatchExtension = `let started = 0;
export default function (pi) {
  pi.registerTool({
    name: "audit_prepared", label: "audit_prepared", description: "Echo text",
    parameters: { type: "object", required: ["text"], properties: { text: { type: "string" } } },
    prepareArguments: (args) => (args && typeof args.legacy === "string" ? { text: args.legacy } : args),
    async execute(_id, params) { return { content: [{ type: "text", text: "prepared:" + params.text }], details: {} }; },
  });
  pi.registerTool({
    name: "audit_order", label: "audit_order", description: "Report the start order and the arguments",
    parameters: { type: "object", properties: { zeta: { type: "string" }, alpha: { type: "string" } } },
    async execute(_id, params) { started += 1; return { content: [{ type: "text", text: "start#" + started + " " + JSON.stringify(params) }], details: {} }; },
  });
}
`

// runAuditDispatchPrint runs one print-mode prompt whose first model answer is calls, and returns the tool results
// the binary sent back with the second request.
func runAuditDispatchPrint(t *testing.T, calls [][2]string) []string {
	t.Helper()
	binary := buildPigBinaryForSignalTest(t)
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
	extension := filepath.Join(t.TempDir(), "audit-tools.mjs")
	if err := os.WriteFile(extension, []byte(auditDispatchExtension), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), binary, "--no-extensions", "-e", extension, "--model", "fixture/test", "--no-skills", "--no-prompt-templates", "--no-context-files", "--no-session", "--print", "go")
	cmd.Dir = cwd
	cmd.Env = append(os.Environ(), "HOME="+home, "PIG_HOME="+filepath.Join(home, ".pig"), "PIG_CODING_AGENT_DIR="+agentDir, "PIG_OFFLINE=1", "PI_SKIP_VERSION_CHECK=1")
	cmd.Stdin = strings.NewReader("")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("print mode: %v\n%s", err, out)
	}
	requests := chat.recorded()
	if len(requests) < 2 {
		t.Fatalf("the model saw %d requests, want the tool results in a second one", len(requests))
	}
	return requests[1].Results
}

func TestAuditNodeExtensionToolCallsReachTheToolLikePi(t *testing.T) {
	t.Run("prepareArguments_runs_before_validation", func(t *testing.T) {
		results := runAuditDispatchPrint(t, [][2]string{{"audit_prepared", `{"legacy":"hello"}`}})
		if len(results) != 1 || results[0] != "prepared:hello" {
			t.Fatalf("tool results = %q, want [\"prepared:hello\"]: the host validates the raw arguments before the extension's prepareArguments can rewrite them", results)
		}
	})

	t.Run("parallel_batch_starts_in_source_order", func(t *testing.T) {
		const n = 8
		calls := make([][2]string, n)
		for i := range n {
			calls[i] = [2]string{"audit_order", fmt.Sprintf(`{"zeta":"%d"}`, i+1)}
		}
		results := runAuditDispatchPrint(t, calls)
		if len(results) != n {
			t.Fatalf("tool results = %q, want %d", results, n)
		}
		for i, result := range results {
			if want := fmt.Sprintf(`start#%d {"zeta":"%d"}`, i+1, i+1); result != want {
				t.Fatalf("tool results = %q: call %d started as %q, want %q (Pi starts a parallel batch in source order)", results, i+1, result, want)
			}
		}
	})
}
