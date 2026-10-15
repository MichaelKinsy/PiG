package ai

import (
	"errors"
	"reflect"
	"testing"
)

// The resolve step shares the transcript's messages, and a Frozen transcript shares the caller's (D104). Each provider
// therefore holds the caller's messages until its own Messages copy, so building a request on any API, with later system
// messages kept in place or collapsed, must leave the caller's messages as they were.
func TestProviderRequestsLeaveAFrozenTranscriptUnchanged(t *testing.T) {
	fixture := resolveFixture(3)
	tools := []ToolSchema{{Name: "read", Description: "read a file", Parameters: JsonObject{"type": "object"}}}
	apis := []struct {
		api     API
		baseURL string
	}{
		{APIOpenAICompletions, "http://127.0.0.1:9"},
		{APIOpenAIResponses, "http://127.0.0.1:9"},
		{APIAzureOpenAIResponses, "https://example.openai.azure.com/openai/v1"},
		{APIAnthropicMessages, "http://127.0.0.1:9"},
		{APIGoogleGenerativeAI, "http://127.0.0.1:9"},
		{APIGoogleVertex, "http://127.0.0.1:9"},
		{APIMistralConversations, "http://127.0.0.1:9"},
		{APIBedrockConverseStream, "http://127.0.0.1:9"},
	}
	for _, target := range apis {
		for _, mid := range []bool{false, true} {
			// The leading system message comes from SystemPrompt and Tools, so the caller's slice holds only the conversation.
			messages := cloneMessages(fixture.messages[1:])
			before := cloneMessages(messages)
			transcript := NormalizeContext(Context{SystemPrompt: "system", Tools: tools, Messages: messages, Frozen: true})
			model := &Model{ID: "m", DisplayName: "m", Input: []string{"text"}, Capabilities: ModelCapabilities{ContextWindow: 128000, MaxOutputTokens: 16384}, ProviderMeta: ProviderMetadata{API: target.api, ProviderID: "p", BaseURL: target.baseURL, Compat: &ModelCompat{SupportsMidConvoSystemMessages: new(mid)}}}
			captured := false
			sentinel := errors.New("payload captured")
			options := StreamOptions{APIKey: "fake-key", Region: "us-east-1", OnPayload: func(any, *Model) (any, error) {
				captured = true
				return nil, sentinel
			}}
			stream, err := apiStreams(target.api).Stream(invalidRequest(t), model, transcript, options)
			if err == nil {
				stream.Result()
			}
			if !captured {
				t.Fatalf("%s mid=%v: no request was built (err %v)", target.api, mid, err)
			}
			if !reflect.DeepEqual(messages, before) {
				t.Fatalf("%s mid=%v: building the request changed the caller's messages", target.api, mid)
			}
		}
	}
}
