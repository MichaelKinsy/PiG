package main

import (
	"os"
	"path/filepath"
	"testing"
)

func preflightRPC(t *testing.T, blocked ...bool) *upstreamRPC {
	t.Helper()
	fixture, err := filepath.Abs("testdata/rpc-preflight.mjs")
	if err != nil {
		t.Fatal(err)
	}
	return newUpstreamRPC(t, len(blocked) > 0 && blocked[0], "-e", fixture)
}

func waitPreflightDialog(f *upstreamRPC, title string) string {
	f.p.t.Helper()
	var id string
	f.await(title, func(r rpcRecord) bool {
		if r["type"] == "response" && r["id"] == "first" {
			f.p.t.Fatalf("premature preflight response: %v", r)
		}
		if isUISelect(r, title) {
			id, _ = r["id"].(string)
		}
		return id != ""
	})
	return id
}

// The rejection observer runs after the prompt's input await and after synchronous replies already queued by the same data callback (rpc-mode.ts:393-415).
func TestRPCPreflightRejectionYieldsToStateResponse(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, "agent")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	models := `{"providers":{"fake-provider":{"baseUrl":"https://example.invalid","api":"openai-completions","models":[{"id":"fake-model","name":"Fake","contextWindow":128000,"maxTokens":4096}]}}}`
	if err := os.WriteFile(filepath.Join(dir, "models.json"), []byte(models), 0600); err != nil {
		t.Fatal(err)
	}
	p := startRPCProcessAt(t, t.TempDir(), []string{"HOME=" + home, "PIG_HOME=" + home, "PIG_CODING_AGENT_DIR=" + dir}, "--offline", "--no-extensions", "--no-session", "--model", "fake-provider/fake-model")
	p.send("{\"id\":\"prompt\",\"type\":\"prompt\",\"message\":\"Hello\"}\n{\"id\":\"state\",\"type\":\"get_state\"}")
	var records []rpcRecord
	p.await("state and preflight failure", func(r rpcRecord) bool { records = append(records, r); return len(records) == 2 })
	if records[0]["id"] != "state" || records[0]["success"] != true || records[1]["id"] != "prompt" || records[1]["success"] != false {
		t.Fatalf("records=%v", records)
	}
	p.closeAndWait("after preflight rejection")
}

// Pi agent-session.ts:1634-1667 samples streaming after the input await, not when the prompt arrives. _runAgentPrompt (1468-1473) alone marks the Session active.
func TestRPCPreflightAdmission(t *testing.T) {
	for _, command := range []string{"steer", "follow_up"} {
		t.Run(command+" input suspension", func(t *testing.T) {
			f := preflightRPC(t)
			f.p.sendJSON(rpcRecord{"id": "first", "type": command, "message": "hold-input"})
			id := waitPreflightDialog(f, "Input suspended")
			state := f.command("get_state", nil)
			if state["isStreaming"] != false || state["pendingMessageCount"] != float64(0) {
				t.Fatal(state)
			}
			f.p.sendJSON(rpcRecord{"type": "extension_ui_response", "id": id, "value": "release"})
			if response := f.response("first"); response["success"] != true {
				t.Fatal(response)
			}
			cleared := f.command("clear_queue", nil)
			key := "steering"
			if command == "follow_up" {
				key = "followUp"
			}
			queued := cleared[key].([]any)
			if len(queued) != 1 || queued[0] != "What is 20+22?" {
				t.Fatalf("queue=%v", cleared)
			}
			f.finish()
		})
	}
	t.Run("preflight is idle", func(t *testing.T) {
		f := preflightRPC(t)
		f.p.send(`{"id":"first","type":"prompt","message":"hold-before"}`)
		id := waitPreflightDialog(f, "Preflight suspended")
		state := f.command("get_state", nil)
		if state["isStreaming"] != false {
			t.Fatalf("preflight state=%v", state)
		}
		f.p.sendJSON(rpcRecord{"type": "extension_ui_response", "id": id, "value": "release"})
		f.response("first")
		f.await("first settled", func(r rpcRecord) bool { return r["type"] == "agent_settled" })
		f.finish()
	})
	t.Run("second prompt and queues do not wait for preflight", func(t *testing.T) {
		f := preflightRPC(t)
		f.p.send(`{"id":"first","type":"prompt","message":"hold-before"}`)
		id := waitPreflightDialog(f, "Preflight suspended")
		f.command("steer", rpcRecord{"message": "steered"})
		f.command("follow_up", rpcRecord{"message": "followed"})
		f.prompt("What is 20+22?")
		for _, r := range f.seen {
			if r["type"] == "response" && r["id"] == "first" {
				t.Fatalf("first escaped suspension: %v", r)
			}
		}
		f.p.sendJSON(rpcRecord{"type": "extension_ui_response", "id": id, "value": "release"})
		f.response("first")
		f.await("resumed first settles", func(r rpcRecord) bool { return r["type"] == "agent_settled" })
		f.finish()
		assertPromptResponses(t, f.seen, "first", true)
	})
	t.Run("streaming is rechecked after input await", func(t *testing.T) {
		f := preflightRPC(t, true)
		f.p.send(`{"id":"first","type":"prompt","message":"hold-input","streamingBehavior":"followUp"}`)
		id := waitPreflightDialog(f, "Input suspended")
		f.command("prompt", rpcRecord{"message": "What is 20+22?"})
		f.waitForProviderRequest()
		if state := f.command("get_state", nil); state["isStreaming"] != true {
			t.Fatal(state)
		}
		f.p.sendJSON(rpcRecord{"type": "extension_ui_response", "id": id, "value": "release"})
		if response := f.response("first"); response["success"] != true {
			t.Fatal(response)
		}
		state := f.command("get_state", nil)
		if state["pendingMessageCount"] != float64(1) {
			t.Fatalf("input was not queued after becoming active: %v", state)
		}
		f.unblock()
		f.await("queued input settles", func(r rpcRecord) bool { return r["type"] == "agent_settled" })
		f.finish()
	})
	t.Run("resumed preflight does not serialize behind active provider", func(t *testing.T) {
		f := preflightRPC(t, true)
		f.p.send(`{"id":"first","type":"prompt","message":"hold-before"}`)
		id := waitPreflightDialog(f, "Preflight suspended")
		f.command("prompt", rpcRecord{"message": "What is 20+22?"})
		f.waitForProviderRequest()
		f.p.sendJSON(rpcRecord{"type": "extension_ui_response", "id": id, "value": "release"})
		response := f.response("first")
		if response["success"] != true {
			t.Fatal(response)
		}
		// Pi acknowledges successful preflight, then Agent.prompt rejects a concurrent run. The rejected Session run still settles.
		f.await("busy prepared prompt settles", func(r rpcRecord) bool { return r["type"] == "agent_settled" })
		f.unblock()
		f.await("provider run settles", func(r rpcRecord) bool { return r["type"] == "agent_settled" })
		f.finish()
		f.mu.Lock()
		defer f.mu.Unlock()
		if len(f.requests) != 1 {
			t.Fatalf("concurrent preflight was incorrectly serialized into another provider call: %d", len(f.requests))
		}
	})
	t.Run("EOF drains suspended preflight", func(t *testing.T) {
		f := preflightRPC(t)
		f.p.send(`{"id":"first","type":"prompt","message":"hold-before"}`)
		waitPreflightDialog(f, "Preflight suspended")
		f.finish()
	})
	t.Run("input suspension leaves command reader live", func(t *testing.T) {
		f := preflightRPC(t)
		f.p.send(`{"id":"first","type":"prompt","message":"hold-input"}`)
		id := waitPreflightDialog(f, "Input suspended")
		f.p.sendJSON(rpcRecord{"type": "extension_ui_response", "id": id, "value": "release"})
		f.response("first")
		f.await("input prompt settles", func(r rpcRecord) bool { return r["type"] == "agent_settled" })
		f.finish()
	})
}
