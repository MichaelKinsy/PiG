package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func startCustomMessageRPC(t *testing.T) *rpcProcess {
	t.Helper()
	root := t.TempDir()
	fixture := filepath.Join(root, "custom-message.mjs")
	if err := os.WriteFile(fixture, []byte(`export default function(pi) {
 pi.registerCommand("custom-message", {handler: (args) => {
  const options = args ? JSON.parse(args) : {triggerTurn:false};
  pi.sendMessage({customType:"probe",content:[{type:"text",text:"idle-message"}],display:false,details:{marker:42}}, options);
 }});
}`), 0o600); err != nil {
		t.Fatal(err)
	}
	p := startRPCProcessAt(t, root, []string{
		"PIG_HOME=" + filepath.Join(root, "pig"),
		"PIG_CODING_AGENT_DIR=" + filepath.Join(root, "pig", "agent"),
		"PI_CODING_AGENT_DIR=" + filepath.Join(root, "pi"),
		"PIG_TEST_FAUX=1",
		"PIG_TEST_FAUX_SCENARIO=parity-basic",
	}, "--model", "test-faux/faux-1", "--no-session", "--no-extensions", "-e", fixture)
	p.send(`{"id":"commands","type":"get_commands"}`)
	p.await("custom command registration", func(record rpcRecord) bool {
		if !isSuccessResponse(record, "commands") {
			return false
		}
		data, _ := record["data"].(map[string]any)
		commands, _ := data["commands"].([]any)
		for _, value := range commands {
			command, _ := value.(map[string]any)
			if command["name"] == "custom-message" {
				return true
			}
		}
		t.Fatalf("command was not registered: %v\n%s", record, p.stderr.String())
		return false
	})
	return p
}

// Pi 0.87.1 AgentSession.sendCustomMessage appends an idle custom message and emits message_start/message_end before an immediately handled prompt acknowledges success. No model turn is required.
func TestRPCExtensionCustomMessageBeforeFirstModelPrompt(t *testing.T) {
	p := startCustomMessageRPC(t)
	p.send(`{"id":"custom","type":"prompt","message":"/custom-message"}`)
	var events []string
	p.await("custom message before command completion", func(record rpcRecord) bool {
		if record["type"] == "agent_start" {
			t.Fatal("triggerTurn:false started a model turn")
		}
		message, _ := record["message"].(map[string]any)
		if message["customType"] == "probe" {
			events = append(events, record["type"].(string))
			if message["role"] != "custom" || message["display"] != false {
				t.Fatalf("custom message fields: %v", message)
			}
			if !reflect.DeepEqual(message["content"], []any{map[string]any{"type": "text", "text": "idle-message"}}) || !reflect.DeepEqual(message["details"], map[string]any{"marker": float64(42)}) {
				t.Fatalf("custom message content/details: %v", message)
			}
		}
		if !isSuccessResponse(record, "custom") {
			return false
		}
		if !reflect.DeepEqual(events, []string{"message_start", "message_end"}) {
			t.Fatalf("events before command completion = %v\n%s", events, p.stderr.String())
		}
		return true
	})
	p.closeAndWait("after idle custom message")
}

// Pi handles nextTurn before triggerTurn and does not publish the custom message until a later prompt delivers it.
func TestRPCExtensionCustomMessageNextTurnWaitsForPrompt(t *testing.T) {
	p := startCustomMessageRPC(t)
	p.sendJSON(map[string]any{"id": "queue", "type": "prompt", "message": `/custom-message {"deliverAs":"nextTurn","triggerTurn":true}`})
	p.await("queued custom message", func(record rpcRecord) bool {
		if record["type"] == "agent_start" || record["type"] == "message_start" || record["type"] == "message_end" {
			t.Fatalf("nextTurn published before the next prompt: %v", record)
		}
		return isSuccessResponse(record, "queue")
	})
	p.send(`{"id":"run","type":"prompt","message":"reply with exactly: delivered"}`)
	var custom []string
	p.await("next-turn custom message delivery", func(record rpcRecord) bool {
		message, _ := record["message"].(map[string]any)
		if message["customType"] == "probe" {
			custom = append(custom, record["type"].(string))
		}
		if record["type"] != "agent_settled" {
			return false
		}
		if !reflect.DeepEqual(custom, []string{"message_start", "message_end"}) {
			t.Fatalf("nextTurn custom events = %v", custom)
		}
		return true
	})
	p.closeAndWait("after next-turn delivery")
}

func TestRPCExtensionCustomMessageTriggerStartsOwnedTurn(t *testing.T) {
	p := startCustomMessageRPC(t)
	p.sendJSON(map[string]any{"id": "trigger", "type": "prompt", "message": `/custom-message {"triggerTurn":true}`})
	var events []string
	p.await("custom-triggered turn settlement", func(record rpcRecord) bool {
		if record["type"] == "agent_start" {
			events = append(events, "agent_start")
		}
		message, _ := record["message"].(map[string]any)
		if message["customType"] == "probe" {
			events = append(events, record["type"].(string))
		}
		if record["type"] != "agent_settled" {
			return false
		}
		if !reflect.DeepEqual(events, []string{"agent_start", "message_start", "message_end"}) {
			t.Fatalf("custom-triggered events = %v", events)
		}
		return true
	})
	p.closeAndWait("after custom-triggered turn")
}
