package ai

import (
	"encoding/json"
	"testing"
)

// openai-codex-responses.ts:1542 sends JSON.stringify({ type: "response.create", ...requestBody }): type first, then the members of the body in the order it has them, with
// the cached continuation `{ ...body, previous_response_id, input: delta }` replacing input in place and adding previous_response_id last. A Go map wrote every key sorted.
func TestCodexWebSocketMessageKeepsTheRequestBodyMemberOrder(t *testing.T) {
	body := []byte(`{"model":"m","store":false,"stream":true,"instructions":"i","input":[{"role":"user","content":"a","type":"message"},{"type":"message","role":"assistant","content":"<b> & b"}],"tools":[],"tool_choice":"auto","zeta":{"y":1,"x":2}}`)
	request, err := readCodexRequest(body)
	if err != nil {
		t.Fatal(err)
	}
	got, err := codexWebSocketMessage(request, nil)
	if err != nil {
		t.Fatal(err)
	}
	const whole = `{"type":"response.create","model":"m","store":false,"stream":true,"instructions":"i","input":[{"role":"user","content":"a","type":"message"},{"type":"message","role":"assistant","content":"<b> & b"}],"tools":[],"tool_choice":"auto","zeta":{"y":1,"x":2}}`
	if string(got) != whole {
		t.Fatalf("whole body:\n got %s\nwant %s", got, whole)
	}

	got, err = codexWebSocketMessage(request, &codexDelta{previousResponseID: "resp_1", count: 1})
	if err != nil {
		t.Fatal(err)
	}
	const continued = `{"type":"response.create","model":"m","store":false,"stream":true,"instructions":"i","input":[{"type":"message","role":"assistant","content":"<b> & b"}],"tools":[],"tool_choice":"auto","zeta":{"y":1,"x":2},"previous_response_id":"resp_1"}`
	if string(got) != continued {
		t.Fatalf("continuation:\n got %s\nwant %s", got, continued)
	}
}

// anthropic-messages.ts hands the SDK the object a payload hook returned; the SDK moves params.betas into the anthropic-beta header and serializes the rest in the
// object's own member order. PiG forces stream on a replacement body (anthropic-messages.ts:652) but does not sort the members.
func TestAnthropicReplacementBodyKeepsItsMemberOrder(t *testing.T) {
	payload := json.RawMessage(`{"zeta":1,"model":"m","betas":["b1","b2"],"messages":[{"role":"user","content":"x"}],"alpha":{"y":2,"x":1},"stream":false,"max_tokens":5}`)
	body, betas, err := anthropicWireBody(payload)
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"zeta":1,"model":"m","messages":[{"role":"user","content":"x"}],"alpha":{"y":2,"x":1},"stream":true,"max_tokens":5}`
	if string(body) != want {
		t.Fatalf("body:\n got %s\nwant %s", body, want)
	}
	if betas == nil || *betas != "b1,b2" {
		t.Fatalf("betas = %v, want b1,b2", betas)
	}
	body, _, err = anthropicWireBody(json.RawMessage(`{"model":"m","zeta":1}`))
	if err != nil || string(body) != `{"model":"m","zeta":1,"stream":true}` {
		t.Fatalf("a body without stream gets it last: %s, %v", body, err)
	}
}

// The bodies a payload hook can leave that Pi's own stream does not reach in TestReplacementAndWebSocketBodiesMatchPiOnTheWire. Expected text is node's
// JSON.stringify({ type: "response.create", ...requestBody }) for the same requestBody: a continuation of a body without input appends previous_response_id and then
// `input: []`, a body type keeps the first position, an array-index key goes first, and a null body (a hook that returned null) spreads nothing.
func TestCodexWebSocketMessageSpreadsBodiesWithoutInputAndNull(t *testing.T) {
	body := []byte(`{"model":"m","0":1,"type":"x"}`)
	request, err := readCodexRequest(body)
	if err != nil {
		t.Fatal(err)
	}
	entry := &codexWebSocketEntry{continuation: &codexWebSocketContinuation{lastRest: request.rest(), lastResponseID: "resp_1"}}
	got, err := codexWebSocketMessage(request, cachedCodexDelta(entry, request))
	if want := `{"0":1,"type":"x","model":"m","previous_response_id":"resp_1","input":[]}`; err != nil || string(got) != want {
		t.Fatalf("continuation without input:\n got %s, %v\nwant %s", got, err, want)
	}

	null, err := readCodexRequest([]byte("null"))
	if err != nil {
		t.Fatal(err)
	}
	got, err = codexWebSocketMessage(null, nil)
	if want := `{"type":"response.create"}`; err != nil || string(got) != want {
		t.Fatalf("null body: got %s, %v, want %s", got, err, want)
	}
	entry = &codexWebSocketEntry{continuation: &codexWebSocketContinuation{lastResponseID: "resp_1"}}
	got, err = codexWebSocketMessage(null, cachedCodexDelta(entry, null))
	if want := `{"type":"response.create","previous_response_id":"resp_1","input":[]}`; err != nil || string(got) != want {
		t.Fatalf("null body continuation: got %s, %v, want %s", got, err, want)
	}
}
