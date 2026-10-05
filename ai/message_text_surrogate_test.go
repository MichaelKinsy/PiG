package ai

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

// loneHigh is the WTF-8 spelling of String.fromCharCode(0xd83d).
const loneHigh = "\xed\xa0\xbd"

func TestMessageTextSanitizesLoneSurrogatesOnTheWire(t *testing.T) {
	// Pi sanitizes system, user and assistant text in every provider converter: anthropic-messages.ts:1089,1098,1255,1276,1284,1318; openai-completions.ts:1251,1257,1266,1297; google-shared.ts:207,212,242; google-generative-ai.ts:393. The unicode-surrogate.test.ts provider/auth matrix supplies the provider shapes.
	text := "unpaired " + loneHigh + " end"
	want := "unpaired  end"
	seen := map[string]bool{}
	for _, tc := range unicodeUpstreamCases() {
		key := tc.provider + "/" + tc.model + "/" + map[bool]string{false: "key", true: "oauth"}[tc.oauth]
		if tc.kind != 2 || seen[key] {
			continue
		}
		seen[key] = true
		t.Run(key, func(t *testing.T) {
			model, ok := LookupModelExact(tc.provider + "/" + tc.model)
			if !ok {
				t.Fatal("missing upstream model")
			}
			reached := false
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				reached = true
				decoded, err := decodeMatrixRequest(r)
				if err != nil {
					t.Error(err)
				}
				for _, role := range []string{"system", "user-text", "assistant", "user-block"} {
					if !jsonContainsText(decoded, role+": "+want) {
						t.Errorf("%s text was not sanitized: %v", role, decoded)
					}
				}
				writeMatrixUsageResponse(t, w, model.API, "ok", false)
			}))
			defer server.Close()
			provider := newMatrixProvider(t, model, server.URL, tc.oauth)
			defer func() {
				if err := provider.Close(); err != nil {
					t.Error(err)
				}
			}()
			request := Context{SystemPrompt: "system: " + text, Messages: []Message{
				UserMessage{Content: UserText("user-text: " + text), Timestamp: 1},
				AssistantMessage{Content: []AssistantContentBlock{TextContent{Text: "assistant: " + text}}, API: model.API, Provider: model.Provider, Model: model.ID, StopReason: StopReasonStop, Timestamp: 2},
				UserMessage{Content: UserContentBlocks{TextContent{Text: "user-block: " + text}}, Timestamp: 3},
			}}
			options := StreamOptions{ModelCost: model.ToModel().CostRates(), Transport: TransportSSE, Env: ProviderEnv{"AWS_BEDROCK_SKIP_AUTH": "1", "AWS_REGION": "us-east-1"}}
			stream, err := provider.Stream(t.Context(), NormalizeContext(request), options)
			if err != nil {
				t.Fatal(err)
			}
			if response := stream.Result(); response.StopReason == StopReasonError {
				t.Fatalf("error=%s", response.ErrorMessage)
			}
			if !reached {
				t.Fatal("no request reached the server")
			}
		})
	}
}

func TestAnthropicSanitizesBeforeOrAfterBlankChecksLikeUpstream(t *testing.T) {
	// anthropic-messages.ts:1274-1300: a string user message is trimmed before sanitizing, but block text is sanitized and then filtered. 1316-1351: assistant text is trimmed first; thinking is sanitized on both wire shapes.
	converted, err := anthConvertMessagesDetailed([]Message{
		UserMessage{Content: UserText(" " + loneHigh)},
		UserMessage{Content: UserContentBlocks{TextContent{Text: " " + loneHigh}, TextContent{Text: "b" + loneHigh}}},
		AssistantMessage{Content: []AssistantContentBlock{TextContent{Text: loneHigh}, ThinkingContent{Thinking: "t" + loneHigh, ThinkingSignature: "sig"}, ThinkingContent{Thinking: "u" + loneHigh}}},
	}, false, false, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	got := converted.messages
	want := []anthMessage{
		{Role: "user", Content: " "},
		{Role: "user", Content: []anthContentBlock{{Type: "text", Text: "b"}}},
		{Role: "assistant", Content: []anthContentBlock{{Type: "text", Text: ""}, {Type: "thinking", Thinking: "t", Signature: "sig"}, {Type: "text", Text: "u"}}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("messages=%#v\nwant %#v", got, want)
	}
}

func TestOpenAICompletionsSanitizesTextButNotReasoningFields(t *testing.T) {
	// openai-completions.ts:1260-1266 filters user parts by raw length before sanitizing; 1316-1319 sanitizes thinking sent as text, while 1338-1339 copies reasoning fields unchanged.
	messages := []Message{
		UserMessage{Content: UserContentBlocks{TextContent{Text: loneHigh}}},
		AssistantMessage{Content: []AssistantContentBlock{ThinkingContent{Thinking: "r" + loneHigh, ThinkingSignature: "reasoning_content"}, TextContent{Text: "a" + loneHigh}}},
	}
	out, err := convertMessages(messages, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 || !reflect.DeepEqual(out[0].Content, []oaiContentPart{{Type: "text", Text: ""}}) || out[1].Content != "a" || out[1].ReasoningContent == nil || *out[1].ReasoningContent != "r"+loneHigh {
		t.Fatalf("messages=%#v", out)
	}
	asText, err := convertMessagesInternal(messages[1:], false, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if want := []oaiContentPart{{Type: "text", Text: "r"}, {Type: "text", Text: "a"}}; len(asText) != 1 || !reflect.DeepEqual(asText[0].Content, want) {
		t.Fatalf("thinking-as-text=%#v, want %#v", asText, want)
	}
}

func TestGoogleSanitizesMessageText(t *testing.T) {
	// google-shared.ts:207,212,242,255,262.
	got := geminiConvertMessages([]Message{
		UserMessage{Content: UserText("u" + loneHigh)},
		UserMessage{Content: UserContentBlocks{TextContent{Text: "b" + loneHigh}}},
		AssistantMessage{Provider: "google", Model: "gemini-2.5-flash", Content: []AssistantContentBlock{TextContent{Text: "a" + loneHigh}, ThinkingContent{Thinking: "t" + loneHigh}}},
		AssistantMessage{Provider: "openai", Model: "gpt", Content: []AssistantContentBlock{ThinkingContent{Thinking: "x" + loneHigh}}},
	}, "google", "gemini-2.5-flash", true)
	var texts []string
	for _, content := range got {
		for _, part := range content.Parts {
			if part.Text != nil {
				texts = append(texts, *part.Text)
			}
		}
	}
	if want := []string{"u", "b", "a", "t", "x"}; !reflect.DeepEqual(texts, want) {
		t.Fatalf("texts=%q, want %q", texts, want)
	}
}
