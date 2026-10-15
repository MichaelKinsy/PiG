package cli

import (
	"testing"
)

// rpc-mode.ts case "bash" passes command.excludeFromContext to recordBashResult/executeBash unchanged, and agent-session.ts recordBashResult
// assigns options?.excludeFromContext, so the recorded bashExecution message carries the member exactly when the client sent a boolean: none for
// an absent member, false for false, true for true. get_messages returns those messages as recorded.
func TestRPCBashKeepsTheClientsExcludeFromContextMember(t *testing.T) {
	if testing.Short() {
		t.Skip("starts the pig binary")
	}
	home := t.TempDir()
	p := startRPCProcess(t, []string{"PIG_TEST_FAUX=1", "PIG_HOME=" + home, "PIG_CODING_AGENT_DIR=" + home + "/agent"}, "--model", "test-faux/faux-1", "--no-session", "--no-extensions")
	for _, command := range []string{
		`{"id":"absent","type":"bash","command":"echo absent"}`,
		`{"id":"false","type":"bash","command":"echo false","excludeFromContext":false}`,
		`{"id":"true","type":"bash","command":"echo true","excludeFromContext":true}`,
	} {
		p.send(command)
		var id string
		p.await("bash response", func(record rpcRecord) bool {
			if record["type"] != "response" || record["command"] != "bash" {
				return false
			}
			if record["success"] != true {
				t.Fatalf("bash failed: %v\n%s", record, p.stderr.String())
			}
			id, _ = record["id"].(string)
			return true
		})
		if id == "" {
			t.Fatalf("bash response without id for %s", command)
		}
	}
	p.send(`{"id":"messages","type":"get_messages"}`)
	messages, _ := rpcResponseData(t, p, "messages")["messages"].([]any)
	want := map[string]any{"echo absent": nil, "echo false": false, "echo true": true}
	seen := 0
	for _, raw := range messages {
		message, _ := raw.(map[string]any)
		if message["role"] != "bashExecution" {
			continue
		}
		command, _ := message["command"].(string)
		expected, known := want[command]
		if !known {
			t.Fatalf("unexpected bash message %v", message)
		}
		seen++
		got, present := message["excludeFromContext"]
		if expected == nil {
			if present {
				t.Errorf("%s: excludeFromContext = %v, want no member", command, got)
			}
			continue
		}
		if !present || got != expected {
			t.Errorf("%s: excludeFromContext = %v (present %v), want %v", command, got, present, expected)
		}
	}
	if seen != len(want) {
		t.Fatalf("get_messages returned %d of %d bash messages: %v", seen, len(want), messages)
	}
}
