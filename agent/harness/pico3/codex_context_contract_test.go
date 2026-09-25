package pico3

import "testing"

// Fork-cut context must pair the synthesized error with the original call ID,
// not merely supply an arbitrary error-shaped tool result.
func TestForkMissingToolResultPreservesCallIdentity(t *testing.T) {
	t.Parallel()
	env := openEnv(t, openOptions{tools: []*ToolDeclaration{newTool("a", toolOptions{}).ToolDeclaration}})
	env.wait(env.send(env.root, "tool:a"))
	var assistant Id
	var call JsonObject
	for _, entry := range env.entries() {
		if entry.Kind == "pi.assistant" {
			assistant = entry.Id
			call = toolCallsOf(entry.Model[0])[0]
			break
		}
	}
	fork := must(env.root.Fork(bg, &assistant, ConversationSpec{}))
	var results []JsonObject
	for _, message := range env.context(fork.Id).Messages {
		if message["role"] == "toolResult" {
			results = append(results, message)
		}
	}
	if len(results) != 1 {
		t.Fatalf("fork context contains %d results, want one", len(results))
	}
	equal(t, results[0]["toolCallId"], call["id"], "original call identity")
	equal(t, results[0]["toolName"], call["name"], "original tool name")
	equal(t, results[0]["isError"], true, "missing result is an error")
}
