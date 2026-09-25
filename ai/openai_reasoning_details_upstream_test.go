package ai

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// Strict ports of upstream packages/ai/test/openai-completions-reasoning-details.test.ts.
// Upstream asserts exact thinkingSignature === JSON.stringify(details) and replay payload equality.

const rfReasoningDetail = `{"type":"reasoning.encrypted","id":"call_1","data":"encrypted-signature"}`
const rfSignedText = `{"type":"reasoning.text","text":"I should call the read tool.","signature":"sha256:signed-text","id":"reasoning-text-1","format":"anthropic-claude-v1","index":0}`
const rfSummary = `{"type":"reasoning.summary","summary":"Decided to inspect the requested file.","id":"reasoning-summary-1","format":"anthropic-claude-v1","index":1}`
const rfToolCallChunk = `data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"read","arguments":"{\"path\":\"README.md\"}"}}]},"finish_reason":null}]}` + "\n\n"

func rfChunk(delta string, finish string) string {
	f := "null"
	if finish != "" {
		f = `"` + finish + `"`
	}
	return `data: {"id":"chatcmpl-test","choices":[{"index":0,"delta":` + delta + `,"finish_reason":` + f + `}]}` + "\n\n"
}

func rfRun(t *testing.T, providerID string, chunks ...string) *AssistantMessage {
	t.Helper()
	return runOpenAICompletionsSSEForProvider(t, providerID, strings.Join(chunks, "")+"data: [DONE]\n")
}

func rfThinking(t *testing.T, m *AssistantMessage) ThinkingContent {
	t.Helper()
	var found []ThinkingContent
	for _, b := range m.Content {
		if th, ok := b.(ThinkingContent); ok {
			found = append(found, th)
		}
	}
	if len(found) != 1 {
		t.Fatalf("thinking blocks = %d in %#v, want 1", len(found), m.Content)
	}
	return found[0]
}

func rfReplay(t *testing.T, providerID string, m Message) oaiMessage {
	t.Helper()
	p := &openAIProvider{cfg: OpenAIConfig{ProviderID: providerID}}
	out, err := p.convertMessagesWithCompat([]Message{m}, nil, "system", false)
	if err != nil {
		t.Fatal(err)
	}
	for _, msg := range out {
		if msg.Role == "assistant" {
			return msg
		}
	}
	t.Fatalf("no assistant message in %#v", out)
	return oaiMessage{}
}

func rfJSONEqual(t *testing.T, label string, got any, wantJSON string) {
	t.Helper()
	gotBytes, _ := json.Marshal(got)
	var g, w any
	_ = json.Unmarshal(gotBytes, &g)
	if err := json.Unmarshal([]byte(wantJSON), &w); err != nil {
		t.Fatalf("bad want json: %v", err)
	}
	if !reflect.DeepEqual(g, w) {
		t.Fatalf("%s = %s, want %s", label, gotBytes, wantJSON)
	}
}

func TestReviewF_Upstream1_PreservesDetailsInThinkingSignature(t *testing.T) {
	m := rfRun(t, "openrouter", rfChunk(`{"reasoning_details":[`+rfReasoningDetail+`]}`, ""), rfToolCallChunk, rfChunk(`{}`, "tool_calls"))
	th := rfThinking(t, m)
	if th.Thinking != "" || th.ThinkingSignature != "["+rfReasoningDetail+"]" {
		t.Fatalf("thinking = %#v", th)
	}
	var tool *ToolCall
	for _, b := range m.Content {
		if tc, ok := b.(ToolCall); ok {
			tool = &tc
		}
	}
	if tool == nil || tool.ID != "call_1" || tool.Name != "read" || tool.Arguments["path"] != "README.md" || tool.ThoughtSignature != "" {
		t.Fatalf("tool = %#v", tool)
	}
	rfJSONEqual(t, "replayed reasoning_details", rfReplay(t, "openrouter", *m).ReasoningDetails, "["+rfReasoningDetail+"]")
}

func TestReviewF_Upstream2_LegacyToolSignatureFallback(t *testing.T) {
	m := rfRun(t, "openrouter", rfChunk(`{"reasoning_details":[`+rfReasoningDetail+`]}`, ""), rfToolCallChunk, rfChunk(`{}`, "tool_calls"))
	var content []AssistantContentBlock
	for _, b := range m.Content {
		switch b := b.(type) {
		case ThinkingContent:
			continue
		case ToolCall:
			b.ThoughtSignature = rfReasoningDetail
			content = append(content, b)
		default:
			content = append(content, b)
		}
	}
	m.Content = content
	rfJSONEqual(t, "replayed reasoning_details", rfReplay(t, "openrouter", *m).ReasoningDetails, "["+rfReasoningDetail+"]")
}

func TestReviewF_Upstream3_SignedTextAndSummaryOrder(t *testing.T) {
	m := rfRun(t, "openrouter",
		rfChunk(`{"reasoning":"I should call the read tool.","reasoning_details":[`+rfSignedText+`]}`, ""),
		rfChunk(`{"reasoning_details":[`+rfReasoningDetail+`,`+rfSummary+`]}`, ""),
		rfToolCallChunk, rfChunk(`{}`, "tool_calls"))
	want := "[" + rfSignedText + "," + rfReasoningDetail + "," + rfSummary + "]"
	th := rfThinking(t, m)
	if th.Thinking != "I should call the read tool." || th.ThinkingSignature != want {
		t.Fatalf("thinking = %#v\nwant sig %s", th, want)
	}
	replayed := rfReplay(t, "openrouter", *m)
	rfJSONEqual(t, "replayed reasoning_details", replayed.ReasoningDetails, want)
	if replayed.Reasoning != "" {
		t.Fatalf("reasoning = %q, want undefined", replayed.Reasoning)
	}
}

func TestReviewF_Upstream4_MergeConsecutiveDeltas(t *testing.T) {
	later := `{"type":"reasoning.summary","summary":"After encrypted block.","format":"openai-responses-v1","index":0}`
	m := rfRun(t, "openrouter",
		rfChunk(`{"reasoning_details":[{"type":"reasoning.text","text":"The","index":0}]}`, ""),
		rfChunk(`{"reasoning_details":[{"type":"reasoning.text","text":" user wants the time.","signature":"sha256:text-signature","format":"openai-responses-v1","index":0}]}`, ""),
		rfChunk(`{"reasoning_details":[{"type":"reasoning.summary","summary":"Looked","index":0}]}`, ""),
		rfChunk(`{"reasoning_details":[{"type":"reasoning.summary","summary":" up time.","format":"openai-responses-v1","index":0}]}`, ""),
		rfChunk(`{"reasoning_details":[`+rfReasoningDetail+`]}`, ""),
		rfChunk(`{"reasoning_details":[`+later+`]}`, ""),
		rfToolCallChunk, rfChunk(`{}`, "tool_calls"))
	// Exact JSON.stringify key order upstream produces.
	want := `[{"type":"reasoning.text","text":"The user wants the time.","index":0,"signature":"sha256:text-signature","format":"openai-responses-v1"},` +
		`{"type":"reasoning.summary","summary":"Looked up time.","index":0,"format":"openai-responses-v1"},` +
		rfReasoningDetail + "," + later + "]"
	th := rfThinking(t, m)
	if th.Thinking != "" || th.ThinkingSignature != want {
		t.Fatalf("thinking sig = %s\nwant           %s", th.ThinkingSignature, want)
	}
	rfJSONEqual(t, "replayed reasoning_details", rfReplay(t, "openrouter", *m).ReasoningDetails, want)
}

// AI-02 red/green: field recording, precedence when several present, opencode-go remap, replay.
func TestReviewF_AI02_ReasoningFieldPrecedenceAndReplay(t *testing.T) {
	cases := []struct {
		name, provider, delta, wantSig, wantField string
	}{
		{"content_only", "deepseek", `{"reasoning_content":"think"}`, "reasoning_content", "reasoning_content"},
		{"reasoning_only", "openrouter", `{"reasoning":"think"}`, "reasoning", "reasoning"},
		{"text_only", "x", `{"reasoning_text":"think"}`, "reasoning_text", "reasoning_text"},
		{"content_wins_over_reasoning", "chutes", `{"reasoning":"think","reasoning_content":"think"}`, "reasoning_content", "reasoning_content"},
		{"reasoning_wins_over_text", "x", `{"reasoning_text":"think","reasoning":"think"}`, "reasoning", "reasoning"},
		{"empty_content_falls_through", "x", `{"reasoning_content":"","reasoning":"think"}`, "reasoning", "reasoning"},
		{"opencode_go_remap", "opencode-go", `{"reasoning":"think"}`, "reasoning_content", "reasoning_content"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := rfRun(t, c.provider, rfChunk(c.delta, ""), rfChunk(`{"content":"ok"}`, "stop"))
			th := rfThinking(t, m)
			if th.ThinkingSignature != c.wantSig || th.Thinking != "think" {
				t.Fatalf("thinking = %#v, want sig %q", th, c.wantSig)
			}
			r := rfReplay(t, c.provider, *m)
			encoded, _ := json.Marshal(r)
			var obj map[string]any
			_ = json.Unmarshal(encoded, &obj)
			if obj[c.wantField] != "think" {
				t.Fatalf("replay = %s, want %s=think", encoded, c.wantField)
			}
			for _, other := range []string{"reasoning_content", "reasoning", "reasoning_text"} {
				if other != c.wantField {
					if _, present := obj[other]; present {
						t.Fatalf("replay = %s, unexpected %s", encoded, other)
					}
				}
			}
		})
	}
}

// Reasoning field deltas interleaved with text/tool calls stay on one thinking block (upstream single thinkingBlock).
func TestReviewF_ReasoningInterleavedStaysOneBlock(t *testing.T) {
	m := rfRun(t, "deepseek",
		rfChunk(`{"reasoning_content":"a"}`, ""),
		rfChunk(`{"content":"x"}`, ""),
		rfChunk(`{"reasoning_content":"b"}`, ""),
		rfChunk(`{"content":"y"}`, "stop"))
	if len(m.Content) != 2 {
		t.Fatalf("content = %#v, want [thinking ab, text xy]", m.Content)
	}
	th := rfThinking(t, m)
	if th.Thinking != "ab" {
		t.Fatalf("thinking = %#v", th)
	}
	if tx, ok := m.Content[1].(TextContent); !ok || tx.Text != "xy" {
		t.Fatalf("text = %#v", m.Content[1])
	}
}

// AI-13: unindexed tool calls, id fallback, and upstream "index set only if block.streamIndex undefined".
func TestReviewF_AI13_UnindexedThenIndexed(t *testing.T) {
	m := rfRun(t, "x",
		rfChunk(`{"tool_calls":[{"id":"a","type":"function","function":{"name":"read","arguments":"{\"p\""}}]}`, ""),
		rfChunk(`{"tool_calls":[{"index":3,"id":"a","function":{"arguments":":1}"}}]}`, ""),
		rfChunk(`{"tool_calls":[{"index":3,"function":{"arguments":""}}]}`, ""),
		rfChunk(`{"tool_calls":[{"id":"b","type":"function","function":{"name":"ls","arguments":"{}"}}]}`, "tool_calls"))
	if len(m.Content) != 2 {
		t.Fatalf("content = %#v", m.Content)
	}
	a := m.Content[0].(ToolCall)
	b := m.Content[1].(ToolCall)
	if a.ID != "a" || a.Arguments["p"] != float64(1) || b.ID != "b" || b.Name != "ls" {
		t.Fatalf("content = %#v", m.Content)
	}
}

// Upstream isOpenAIReasoningDetail: format must be undefined or string (null rejected);
// text/summary/data must be typeof string (null rejected).
func TestReviewF_DetailValidationRejectsNulls(t *testing.T) {
	for _, detail := range []string{
		`{"type":"reasoning.text","text":"t","format":null}`,
		`{"type":"reasoning.text","text":null}`,
		`{"type":"reasoning.summary","summary":null}`,
		`{"type":"reasoning.encrypted","id":"e","data":null}`,
	} {
		t.Run(detail, func(t *testing.T) {
			m := rfRun(t, "openrouter", rfChunk(`{"reasoning_details":[`+detail+`]}`, ""), rfChunk(`{"content":"ok"}`, "stop"))
			for _, b := range m.Content {
				if th, ok := b.(ThinkingContent); ok {
					t.Fatalf("upstream drops invalid detail; got thinking %#v", th)
				}
			}
		})
	}
}

// Upstream replay: signed thinking details win over legacy tool-call encrypted signatures.
func TestReviewF_ReplaySignedDetailsBeatLegacy(t *testing.T) {
	signed := `[{"type":"reasoning.text","text":"t","signature":"s"}]`
	legacy := `{"type":"reasoning.encrypted","id":"call_1","data":"old"}`
	msg := AssistantMessage{Content: []AssistantContentBlock{
		ToolCall{ID: "call_1", Name: "read", Arguments: JsonObject{}, ThoughtSignature: legacy},
		ThinkingContent{Thinking: "t", ThinkingSignature: signed},
	}}
	rfJSONEqual(t, "reasoning_details", rfReplay(t, "openrouter", msg).ReasoningDetails, signed)
}

// Upstream replay: raw reasoning field comes from nonEmptyThinkingBlocks[0].thinkingSignature.
func TestReviewF_ReplayUsesFirstNonEmptyThinkingSignature(t *testing.T) {
	msg := AssistantMessage{Content: []AssistantContentBlock{
		ThinkingContent{Thinking: "", ThinkingSignature: ""},
		ThinkingContent{Thinking: "think", ThinkingSignature: "reasoning_content"},
		TextContent{Text: "ok"},
	}}
	r := rfReplay(t, "deepseek", msg)
	if r.ReasoningContent == nil || *r.ReasoningContent != "think" {
		t.Fatalf("replay = %#v, want reasoning_content=think", r)
	}
}

// Upstream ensureToolCallBlock: a block found by id that already has a streamIndex is NOT
// re-keyed to a different wire index; a later index-only delta for that index starts a new block.
func TestReviewF_AI13_IDMatchDoesNotRekeyIndexedBlock(t *testing.T) {
	m := rfRun(t, "x",
		rfChunk(`{"tool_calls":[{"index":0,"id":"a","type":"function","function":{"name":"read","arguments":"{}"}}]}`, ""),
		rfChunk(`{"tool_calls":[{"index":1,"id":"a","function":{"arguments":""}}]}`, ""),
		rfChunk(`{"tool_calls":[{"index":1,"function":{"name":"ls","arguments":"{}"}}]}`, "tool_calls"))
	if len(m.Content) != 2 {
		t.Fatalf("content = %#v, want two tool calls like upstream", m.Content)
	}
}

// Nit probe: upstream JSON.stringify keeps "<" literal in merged text; Go json.Marshal HTML-escapes.
func TestReviewF_Nit_MergedTextHTMLEscape(t *testing.T) {
	m := rfRun(t, "openrouter",
		rfChunk(`{"reasoning_details":[{"type":"reasoning.text","text":"a <"}]}`, ""),
		rfChunk(`{"reasoning_details":[{"type":"reasoning.text","text":" b"}]}`, "stop"))
	want := `[{"type":"reasoning.text","text":"a < b"}]`
	if got := rfThinking(t, m).ThinkingSignature; got != want {
		t.Fatalf("sig = %s, want %s", got, want)
	}
}
