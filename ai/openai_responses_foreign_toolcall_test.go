package ai

import (
	"regexp"
	"strings"
	"testing"
)

// Ports openai-responses-foreign-toolcall-id.test.ts. A github-copilot tool call
// carries a raw id of the form call_xxx|<400-char base64 blob>. When replayed on
// an openai-codex (openai-responses) model, the emitted function_call item id
// must be a bounded, Codex-safe fc_<hash> shape (<=64, ^fc_[A-Za-z0-9]+$) or the
// Responses API rejects the request with "item id must start with fc". Upstream
// hashes the foreign item part with shortHash; pig's shortHash32 is byte-identical.
func TestResponsesConvertMessages_ForeignCopilotToolCallIDHashedToFcShape(t *testing.T) {
	const copilotRawID = "call_4VnzVawQXPB9MgYib7CiQFEY|I9b95oN1wD/cHXKTw3PpRkL6KkCtzTJhUxMouMWYwHeTo2j3htzfSk7YPx2vifiIM4g3A8XXyOj8q4Bt6SLUG7gqY1E3ELkrkVQNHglRfUmWj84lqxJY+Puieb3VKyX0FB+83TUzn91cDMF/4gzt990IzqVrc+nIb9RRscRD070Du16q1glydVjWR0SBJsE6TbY/esOjFpqplogQqrajm1eI++f3eLi73R6q7hVusY0QbeFySVxABCjhN0lXB04caBe1rzHjYzul6MAXj7uq+0r17VLq+yrtyYhN12wkmFqHeqTyEei6EFPbMy24Nc+IbJlkP0OCg02W+gOnyBFcbi2ctvJFSOhSjt1CqBdqCnnhwUqXjbWiT0wh3DmLScRgTHmGkaI+oAcQQjfic65nxj+TnEkReA=="

	p := &openAIResponsesProvider{cfg: OpenAIResponsesConfig{ProviderID: "openai-codex"}}
	messages := []Message{
		UserMessage{Content: UserText("Use the tool.")},
		AssistantMessage{Content: []AssistantContentBlock{
			ToolCall{ID: copilotRawID, Name: "edit", Arguments: JsonObject{"path": "src/styles/app.css"}},
		}},
		ToolResultMessage{ToolCallID: copilotRawID, Content: []ToolResultMessageContent{TextContent{Text: "ok"}}},
	}

	items, _ := p.convertMessages(messages, nil)

	var fc, fco *respInputItem
	for i := range items {
		switch items[i].Type {
		case "function_call":
			fc = &items[i]
		case "function_call_output":
			fco = &items[i]
		}
	}
	if fc == nil {
		t.Fatal("no function_call item produced")
	}

	// Independent oracle: upstream shortHash of the item part (computed via the
	// upstream JS hash), not pig's own output.
	const wantItemID = "fc_ifd2c719fz6a9"
	if fc.ID != wantItemID {
		t.Errorf("function_call item id = %q, want %q", fc.ID, wantItemID)
	}
	if len(fc.ID) > 64 {
		t.Errorf("item id length %d exceeds 64", len(fc.ID))
	}
	if !regexp.MustCompile(`^fc_[A-Za-z0-9]+$`).MatchString(fc.ID) {
		t.Errorf("item id %q is not a Codex-safe fc_ shape", fc.ID)
	}

	// The call part sanitizes to itself (already [A-Za-z0-9_]); the paired
	// function_call_output must carry the same normalized call_id.
	const wantCallID = "call_4VnzVawQXPB9MgYib7CiQFEY"
	if fc.CallID != wantCallID {
		t.Errorf("function_call call_id = %q, want %q", fc.CallID, wantCallID)
	}
	if fco == nil {
		t.Fatal("no function_call_output item produced")
	}
	if fco.CallID != wantCallID {
		t.Errorf("function_call_output call_id = %q, want %q (must pair with the call)", fco.CallID, wantCallID)
	}
	if strings.Contains(fco.CallID, "|") {
		t.Errorf("function_call_output call_id %q still contains the raw item part", fco.CallID)
	}
}

// Characterizes and locks pig's fc_-prefix proxy for isForeignToolCall. pig's
// Message model carries no source.provider, so normalizeResponsesToolCallID keys
// foreign-vs-same on whether the stored item id is already fc_-prefixed. On
// poisoned/migrated same-origin history (a stored item id that is not
// fc_-prefixed), pig hashes it where upstream: which knows
// source.provider === model.provider: would sanitize-and-preserve it. Both are
// valid, deterministically paired, non-persisted ids, so there is no observable
// or interop difference (not a divergence). This test pins the boundary: if pig
// ever preserves here (fc_toolu_abc123), the proxy has silently become structural
// without threading source.provider, and this expectation must be revisited.
func TestResponsesToolCallID_SameOriginNonFcItemIsHashed(t *testing.T) {
	p := &openAIResponsesProvider{cfg: OpenAIResponsesConfig{ProviderID: "openai-codex", Model: "gpt-5-codex"}}
	messages := []Message{AssistantMessage{Content: []AssistantContentBlock{
		ToolCall{ID: "call_1|toolu_abc123", Name: "bash", Arguments: JsonObject{}},
	}}}

	items, _ := p.convertMessages(messages, nil)
	var fc *respInputItem
	for i := range items {
		if items[i].Type == "function_call" {
			fc = &items[i]
		}
	}
	if fc == nil {
		t.Fatal("no function_call item produced")
	}

	// Independent oracle: upstream JS shortHash("toolu_abc123") -> fc_1j9q39ppfzy2f.
	const wantHashed = "fc_1j9q39ppfzy2f"
	const upstreamPreserved = "fc_toolu_abc123"
	if fc.ID == upstreamPreserved {
		t.Fatalf("item id = %q: pig now preserves a same-origin non-fc_ id like upstream: the fc_ proxy became structural without threading source.provider; revisit the boundary comment", fc.ID)
	}
	if fc.ID != wantHashed {
		t.Errorf("item id = %q, want hashed %q", fc.ID, wantHashed)
	}
}
