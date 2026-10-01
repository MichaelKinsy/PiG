package main

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestRPCEmptyPromptRetainsTextBlock(t *testing.T) {
	f := newUpstreamRPC(t, false)
	events := f.prompt("")
	found := false
	for _, event := range events {
		if event["type"] != "message_end" {
			continue
		}
		message, _ := event["message"].(map[string]any)
		if message["role"] != "user" {
			continue
		}
		found = true
		if !reflect.DeepEqual(message["content"], []any{map[string]any{"type": "text", "text": ""}}) {
			t.Fatalf("empty user=%v", message)
		}
	}
	if !found {
		t.Fatal("missing user message")
	}
	f.finish()
}

// Pi awaits the shell's result, not the active provider's completion (rpc-mode.ts:563-583). A global wait-for-idle would hide the ordering bug and deadlock this test.
func TestRPCBashCompletesWhileProviderIsPending(t *testing.T) {
	f := newUpstreamRPC(t, true)
	f.command("prompt", rpcRecord{"message": "held provider"})
	f.waitForProviderRequest()
	data := f.command("bash", rpcRecord{"command": "printf bash-during-provider"})
	want := rpcRecord{"output": "bash-during-provider", "exitCode": float64(0), "cancelled": false, "truncated": false}
	if !reflect.DeepEqual(data, want) {
		t.Fatalf("bash=%v want=%v", data, want)
	}
	f.unblock()
	f.await("settled after provider release", func(r rpcRecord) bool { return r["type"] == "agent_settled" })
	f.finish()
}

// Rejections from the awaited queue APIs yield too; they cannot escape inside the admitting input callback.
func TestRPCQueueErrorsYieldToInputBatch(t *testing.T) {
	home := t.TempDir()
	p := startRPCUIFixture(t, []string{"PIG_HOME=" + home, "PIG_CODING_AGENT_DIR=" + filepath.Join(home, "agent")}, "--offline", "--no-session", "--model", "test-faux/faux-1")
	p.send("{\"id\":\"steer\",\"type\":\"steer\",\"message\":\"/block-model-select\"}\n{\"id\":\"follow\",\"type\":\"follow_up\",\"message\":\"/block-model-select\"}\n{\"id\":\"sync\",\"type\":\"abort_retry\"}")
	var records []rpcRecord
	p.await("queue rejections", func(r rpcRecord) bool { records = append(records, r); return len(records) == 3 })
	message := `Extension command "/block-model-select" cannot be queued. Use prompt() or execute the command when not streaming.`
	want := []rpcRecord{
		{"id": "sync", "type": "response", "command": "abort_retry", "success": true},
		{"id": "steer", "type": "response", "command": "steer", "success": false, "error": message},
		{"id": "follow", "type": "response", "command": "follow_up", "success": false, "error": message},
	}
	if !reflect.DeepEqual(records, want) {
		t.Fatalf("records=%v want=%v", records, want)
	}
	p.closeAndWait("after queue rejections")
}

// Pi rpc-mode.ts:417-425 awaits both Session queue APIs and answers with their dispositions (agent-session.ts:2093,2130). Their nested fulfilled Promises do not publish a response inside jsonl.ts:onData's input batch.
func TestRPCQueueRepliesYieldToInputBatch(t *testing.T) {
	home := t.TempDir()
	p := startRPCProcessAt(t, t.TempDir(), []string{"PIG_HOME=" + home, "PIG_CODING_AGENT_DIR=" + filepath.Join(home, "agent"), "PIG_TEST_FAUX=1"}, "--offline", "--no-session", "--no-extensions", "--model", "test-faux/faux-1")
	p.send("{\"id\":\"steer\",\"type\":\"steer\",\"message\":\"\"}\n{\"id\":\"follow\",\"type\":\"follow_up\",\"message\":\"\"}\n{\"id\":\"sync\",\"type\":\"abort_retry\"}")
	var records []rpcRecord
	responses := 0
	p.await("both queue continuations", func(r rpcRecord) bool {
		records = append(records, r)
		if r["type"] == "response" {
			responses++
		}
		return responses == 3
	})
	want := []rpcRecord{
		{"type": "queue_update", "steering": []any{""}, "followUp": []any{}},
		{"type": "queue_update", "steering": []any{""}, "followUp": []any{""}},
		{"id": "sync", "type": "response", "command": "abort_retry", "success": true},
		{"id": "steer", "type": "response", "command": "steer", "success": true, "data": map[string]any{"disposition": "queued"}},
		{"id": "follow", "type": "response", "command": "follow_up", "success": true, "data": map[string]any{"disposition": "queued"}},
	}
	if !reflect.DeepEqual(records, want) {
		t.Fatalf("records=%v want=%v", records, want)
	}
	p.closeAndWait("after queue continuations")
}
