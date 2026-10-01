package main

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// promptResponses returns the `prompt` responses for id, as upstream's getPromptResponses (rpc-prompt-response-semantics.test.ts:84-88).
func promptResponses(records []rpcRecord, id string) []rpcRecord {
	var responses []rpcRecord
	for _, r := range records {
		if r["id"] == id && r["type"] == "response" && r["command"] == "prompt" {
			responses = append(responses, r)
		}
	}
	return responses
}

func assertPromptFailure(t *testing.T, records []rpcRecord, id string) rpcRecord {
	t.Helper()
	responses := promptResponses(records, id)
	if len(responses) != 1 || responses[0]["success"] != false {
		t.Fatalf("prompt %s responses=%#v", id, responses)
	}
	return responses[0]
}

// assertPromptDisposition asserts one successful prompt response whose data is exactly {disposition}, the shape rpc-mode.ts:403-407 writes.
func assertPromptDisposition(t *testing.T, records []rpcRecord, id, disposition string) {
	t.Helper()
	responses := promptResponses(records, id)
	want := rpcRecord{"id": id, "type": "response", "command": "prompt", "success": true, "data": map[string]any{"disposition": disposition}}
	if len(responses) != 1 || !reflect.DeepEqual(responses[0], want) {
		t.Fatalf("prompt %s responses=%#v want one %#v", id, responses, want)
	}
}

// rpcExtension writes an extension the RPC process loads with -e, the analogue of createTestExtensionsResult([factory]) (rpc-prompt-response-semantics.test.ts:99-104,149).
func rpcExtension(t *testing.T, source string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "extension.mjs")
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func queueUpdate(steering, followUp []any) rpcRecord {
	return rpcRecord{"type": "queue_update", "steering": steering, "followUp": followUp}
}

// awaitRecord waits until want has been read from the RPC output, whether it arrived before the call or after it, and fails on the first response to id that is not want.
func (f *upstreamRPC) awaitRecord(want rpcRecord) {
	f.p.t.Helper()
	if slices.ContainsFunc(f.seen, func(r rpcRecord) bool { return reflect.DeepEqual(r, want) }) {
		return
	}
	f.await(fmt.Sprintf("%v", want), func(r rpcRecord) bool { return reflect.DeepEqual(r, want) })
}

// awaitDisposition reads the response to a steer or follow_up and asserts its data is exactly {disposition}, the shape rpc-mode.ts:419-425 writes.
func (f *upstreamRPC) awaitDisposition(id, command, disposition string) {
	f.p.t.Helper()
	want := rpcRecord{"id": id, "type": "response", "command": command, "success": true, "data": map[string]any{"disposition": disposition}}
	if got := f.response(id); !reflect.DeepEqual(got, want) {
		f.p.t.Fatalf("response to %s = %#v, want %#v", id, got, want)
	}
}

func TestRPCPromptResponseSemanticsUpstream(t *testing.T) {
	t.Parallel()
	// .upstream/v0.99.1/packages/coding-agent/test/rpc-prompt-response-semantics.test.ts:197
	t.Run("emits one failure response when prompt preflight rejects", func(t *testing.T) {
		home := t.TempDir()
		dir := filepath.Join(home, "agent")
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		models := `{"providers":{"fake-provider":{"baseUrl":"https://example.invalid","api":"openai-completions","models":[{"id":"fake-model","name":"Fake Model","reasoning":false,"input":[],"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0},"contextWindow":0,"maxTokens":0}]}}}`
		if err := os.WriteFile(filepath.Join(dir, "models.json"), []byte(models), 0600); err != nil {
			t.Fatal(err)
		}
		f := &upstreamRPC{p: startRPCProcessAt(t, t.TempDir(), []string{"HOME=" + home, "PIG_HOME=" + home, "PIG_CODING_AGENT_DIR=" + dir}, "--offline", "--no-extensions", "--model", "fake-provider/fake-model", "--no-session")}
		f.p.send(`{"id":"b1","type":"prompt","message":"Hello"}`)
		f.response("b1")
		f.finish()
		r := assertPromptFailure(t, f.seen, "b1")
		if !strings.Contains(r["error"].(string), "No API key found for fake-provider.\n\nUse /login to log into a provider via OAuth or API key. See:") {
			t.Fatal(r)
		}
	})
	// .upstream/v0.99.1/packages/coding-agent/test/rpc-prompt-response-semantics.test.ts:237
	// #9098: a successful prompt may start an agent run or be consumed by an extension.
	t.Run("emits one started response when prompt preflight succeeds", func(t *testing.T) {
		f := newUpstreamRPC(t, false)
		f.p.send(`{"id":"b2","type":"prompt","message":"Hello"}`)
		f.response("b2")
		f.await("settled", func(r rpcRecord) bool { return r["type"] == "agent_settled" })
		f.finish()
		assertPromptDisposition(t, f.seen, "b2", "started")
	})
	// .upstream/v0.99.1/packages/coding-agent/test/rpc-prompt-response-semantics.test.ts:291
	t.Run("emits one success response when prompt is queued during streaming", func(t *testing.T) {
		f := newUpstreamRPC(t, true)
		f.p.send(`{"id":"b3-start","type":"prompt","message":"Start"}`)
		f.response("b3-start")
		f.waitForProviderRequest()
		f.p.send(`{"id":"b3","type":"prompt","message":"Queue this","streamingBehavior":"followUp"}`)
		f.response("b3")
		f.unblock()
		f.await("settled", func(r rpcRecord) bool { return r["type"] == "agent_settled" })
		f.finish()
		assertPromptDisposition(t, f.seen, "b3-start", "started")
		assertPromptDisposition(t, f.seen, "b3", "queued")
	})
	// .upstream/v0.99.1/packages/coding-agent/test/rpc-prompt-response-semantics.test.ts:259
	t.Run("reports extension commands and intercepted input as handled without starting a run", func(t *testing.T) {
		extension := rpcExtension(t, `export default function (pi) {
  pi.registerCommand("handled", { handler: async () => {} });
  pi.on("input", (event) => {
    if (event.text === "handled input") return { action: "handled" };
  });
}
`)
		f := newUpstreamRPC(t, false, "-e", extension)
		for _, input := range [][2]string{{"command", "/handled"}, {"input", "handled input"}} {
			id, message := input[0], input[1]
			f.p.sendJSON(rpcRecord{"id": id, "type": "prompt", "message": message})
			f.response(id)
			assertPromptDisposition(t, f.seen, id, "handled")
		}
		f.finish()
		for _, r := range f.seen {
			if r["type"] == "agent_start" {
				t.Fatalf("a handled prompt started a run: %#v", r)
			}
		}
	})
	// .upstream/v0.99.1/packages/coding-agent/test/rpc-prompt-response-semantics.test.ts:329 (#9803: a handler can consume A while independently queueing B; report A's outcome)
	for _, command := range []string{"steer", "follow_up"} {
		t.Run("reports "+command+" as handled even when an extension queues another message", func(t *testing.T) {
			deliverAs := map[string]string{"steer": "steer", "follow_up": "followUp"}[command]
			extension := rpcExtension(t, `export default function (pi) {
  pi.on("input", (event) => {
    if (event.text === "A" && event.source === "rpc") {
      pi.sendUserMessage("B", { deliverAs: "`+deliverAs+`" });
      return { action: "handled" };
    }
  });
}
`)
			f := newUpstreamRPC(t, true, "-e", extension)
			f.p.send(`{"id":"start","type":"prompt","message":"Start"}`)
			f.response("start")
			f.waitForProviderRequest()
			f.p.sendJSON(rpcRecord{"id": "A", "type": command, "message": "A"})
			steering, followUp := []any{}, []any{}
			if command == "steer" {
				steering = []any{"B"}
			} else {
				followUp = []any{"B"}
			}
			f.awaitDisposition("A", command, "handled")
			f.awaitRecord(queueUpdate(steering, followUp))
			f.unblock()
			f.await("settled", func(r rpcRecord) bool { return r["type"] == "agent_settled" })
			f.finish()
			responses := 0
			for _, r := range f.seen {
				if r["type"] == "response" && r["id"] == "A" {
					responses++
				}
			}
			if responses != 1 {
				t.Fatalf("responses to A = %d, want 1", responses)
			}
		})
	}
	// .upstream/v0.99.1/packages/coding-agent/test/rpc-prompt-response-semantics.test.ts:377
	for _, command := range []string{"steer", "follow_up"} {
		t.Run("reports "+command+" as queued after input transformation", func(t *testing.T) {
			extension := rpcExtension(t, `export default function (pi) {
  pi.on("input", (event) => {
    if (event.text === "A") return { action: "transform", text: "B" };
  });
}
`)
			f := newUpstreamRPC(t, false, "-e", extension)
			f.p.sendJSON(rpcRecord{"id": "A", "type": command, "message": "A"})
			steering, followUp := []any{}, []any{}
			if command == "steer" {
				steering = []any{"B"}
			} else {
				followUp = []any{"B"}
			}
			f.awaitDisposition("A", command, "queued")
			f.awaitRecord(queueUpdate(steering, followUp))
			f.finish()
		})
	}
	// .upstream/v0.99.1/packages/coding-agent/test/rpc-prompt-response-semantics.test.ts:411
	t.Run("returns and clears queued steering and follow-up messages", func(t *testing.T) {
		f := newUpstreamRPC(t, true)
		for _, cmd := range []rpcRecord{{"id": "clear-start", "type": "prompt", "message": "Start"}, {"id": "clear-steering", "type": "prompt", "message": "Change direction", "streamingBehavior": "steer"}, {"id": "clear-follow-up", "type": "prompt", "message": "Summarize when finished", "streamingBehavior": "followUp"}} {
			f.p.sendJSON(cmd)
			f.response(cmd["id"].(string))
			if cmd["id"] == "clear-start" {
				// A preflight acknowledgement precedes the initial steering poll. Hold the provider request, as upstream's delayed assistant stream does, before queueing mid-stream messages.
				f.waitForProviderRequest()
			}
		}
		f.p.send(`{"id":"clear","type":"clear_queue"}`)
		r := f.response("clear")
		want := rpcRecord{"id": "clear", "type": "response", "command": "clear_queue", "success": true, "data": map[string]any{"steering": []any{"Change direction"}, "followUp": []any{"Summarize when finished"}}}
		if !reflect.DeepEqual(r, want) {
			t.Fatalf("clear=%#v want %#v", r, want)
		}
		f.unblock()
		f.await("settled", func(r rpcRecord) bool { return r["type"] == "agent_settled" })
		f.finish()
		starts := 0
		for _, r := range f.seen {
			if r["type"] == "agent_start" {
				starts++
			}
		}
		if starts != 1 {
			t.Fatalf("agent_start count=%d", starts)
		}
		assertPromptDisposition(t, f.seen, "clear-start", "started")
		// The steering prompt was queued (rpc-prompt-response-semantics.test.ts:428-432); the follow-up response is only counted (:445-447).
		assertPromptDisposition(t, f.seen, "clear-steering", "queued")
		assertPromptDisposition(t, f.seen, "clear-follow-up", "queued")
	})
}
