package main

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// rpc-mode.ts awaits session.steer/followUp; AgentSession synchronously emits
// queue_update before those calls return and before the RPC response is written.
func TestRPCQueueUpdatePrecedesResponse(t *testing.T) {
	home := t.TempDir()
	p := startRPCProcess(t, []string{"HOME=" + home, "PIG_HOME=" + home, "PIG_CODING_AGENT_DIR=" + filepath.Join(home, "agent"), "PIG_TEST_FAUX=1"}, "--no-session")
	for _, command := range []string{"steer", "follow_up"} {
		p.sendJSON(map[string]any{"id": command, "type": command, "message": "queued " + command})
		updates := 0
		p.await(command+" ordered response", func(record rpcRecord) bool {
			if record["type"] == "queue_update" {
				updates++
				field := "steering"
				if command == "follow_up" {
					field = "followUp"
				}
				messages, ok := record[field].([]any)
				if !ok || len(messages) != 1 || messages[0] != "queued "+command {
					t.Fatalf("queue payload %#v", record)
				}
			}
			if record["type"] == "response" && record["id"] == command {
				if record["success"] != true || updates != 1 {
					t.Fatalf("response arrived after %d updates: %#v", updates, record)
				}
				return true
			}
			return false
		})
	}
}

// agent-session.ts clearQueue() synchronously emits an empty queue_update
// before returning the removed steering/followUp arrays, and rpc-mode's
// success() runs after that synchronous call returns; the emptying
// queue_update wire event therefore always precedes the clear_queue
// response. Parity scenario 25-rpc-clear-queue observed pig writing the
// clear_queue response before its queue_update, because ClearQueue's event
// travels through the Session's async forwarder while the response is
// written directly by the RPC command loop.
func TestRPCClearQueueUpdatePrecedesResponse(t *testing.T) {
	home := t.TempDir()
	p := startRPCProcess(t, []string{"HOME=" + home, "PIG_HOME=" + home, "PIG_CODING_AGENT_DIR=" + filepath.Join(home, "agent"), "PIG_TEST_FAUX=1"}, "--no-session")
	p.sendJSON(map[string]any{"id": "steer", "type": "steer", "message": "steer me"})
	p.await("steer response", func(record rpcRecord) bool {
		return record["type"] == "response" && record["id"] == "steer"
	})
	p.sendJSON(map[string]any{"id": "follow", "type": "follow_up", "message": "follow me"})
	p.await("follow_up response", func(record rpcRecord) bool {
		return record["type"] == "response" && record["id"] == "follow"
	})

	p.sendJSON(map[string]any{"id": "clear", "type": "clear_queue"})
	sawEmptyUpdate := false
	p.await("clear_queue ordered response", func(record rpcRecord) bool {
		if record["type"] == "queue_update" {
			steering, _ := record["steering"].([]any)
			followUp, _ := record["followUp"].([]any)
			if len(steering) == 0 && len(followUp) == 0 {
				sawEmptyUpdate = true
			}
		}
		if record["type"] == "response" && record["id"] == "clear" {
			if record["success"] != true {
				t.Fatalf("clear_queue response failed: %#v", record)
			}
			if !sawEmptyUpdate {
				t.Fatalf("clear_queue response arrived before the emptying queue_update: %#v", record)
			}
			data, ok := record["data"].(map[string]any)
			if !ok {
				t.Fatalf("clear_queue response missing data: %#v", record)
			}
			steering, _ := data["steering"].([]any)
			followUp, _ := data["followUp"].([]any)
			if len(steering) != 1 || steering[0] != "steer me" || len(followUp) != 1 || followUp[0] != "follow me" {
				t.Fatalf("clear_queue response data %#v", data)
			}
			return true
		}
		return false
	})
}

// A prompt with streamingBehavior during a stream queues the message, which
// emits queue_update, and only then calls preflightResult(true), which writes
// the success response (agent-session.ts prompt, rpc-mode.ts "prompt").
func TestRPCStreamingPromptQueueUpdatePrecedesResponse(t *testing.T) {
	home := t.TempDir()
	p := startRPCProcess(t, []string{"HOME=" + home, "PIG_HOME=" + home, "PIG_CODING_AGENT_DIR=" + filepath.Join(home, "agent"), "PIG_TEST_FAUX=1"}, "--no-session", "--model", "test-faux/faux-1")
	p.sendJSON(map[string]any{"id": "stream", "type": "prompt", "message": "TUI_LIVE_STREAM_PAUSE"})
	p.await("paused stream", func(record rpcRecord) bool {
		return record["type"] == "message_update" && strings.Contains(fmt.Sprint(record), "LIVE-STREAM-08")
	})
	for _, behavior := range []string{"steer", "followUp"} {
		p.sendJSON(map[string]any{"id": behavior, "type": "prompt", "message": "queued " + behavior, "streamingBehavior": behavior})
		updates := 0
		p.await(behavior+" ordered prompt response", func(record rpcRecord) bool {
			if record["type"] == "queue_update" {
				updates++
			}
			if record["type"] == "response" && record["id"] == behavior {
				if record["success"] != true || updates != 1 {
					t.Fatalf("response arrived after %d updates: %#v", updates, record)
				}
				return true
			}
			return false
		})
	}
}
