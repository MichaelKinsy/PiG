package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func assertPromptResponses(t *testing.T, records []rpcRecord, id string, success bool) rpcRecord {
	t.Helper()
	var responses []rpcRecord
	for _, r := range records {
		if r["id"] == id && r["type"] == "response" && r["command"] == "prompt" {
			responses = append(responses, r)
		}
	}
	if len(responses) != 1 || responses[0]["success"] != success {
		t.Fatalf("prompt %s responses=%#v", id, responses)
	}
	return responses[0]
}

func TestRPCPromptResponseSemanticsUpstream(t *testing.T) {
	// .upstream/v0.87.1/packages/coding-agent/test/rpc-prompt-response-semantics.test.ts:190
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
		r := assertPromptResponses(t, f.seen, "b1", false)
		if !strings.Contains(r["error"].(string), "No API key found for fake-provider.\n\nUse /login to log into a provider via OAuth or API key. See:") {
			t.Fatal(r)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/rpc-prompt-response-semantics.test.ts:232
	t.Run("emits one success response when prompt preflight succeeds", func(t *testing.T) {
		f := newUpstreamRPC(t, false)
		f.p.send(`{"id":"b2","type":"prompt","message":"Hello"}`)
		f.response("b2")
		f.await("settled", func(r rpcRecord) bool { return r["type"] == "agent_settled" })
		f.finish()
		assertPromptResponses(t, f.seen, "b2", true)
	})
	// .upstream/v0.87.1/packages/coding-agent/test/rpc-prompt-response-semantics.test.ts:252
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
		assertPromptResponses(t, f.seen, "b3-start", true)
		assertPromptResponses(t, f.seen, "b3", true)
	})
	// .upstream/v0.87.1/packages/coding-agent/test/rpc-prompt-response-semantics.test.ts:286
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
		for _, id := range []string{"clear-start", "clear-steering", "clear-follow-up"} {
			assertPromptResponses(t, f.seen, id, true)
		}
	})
}
