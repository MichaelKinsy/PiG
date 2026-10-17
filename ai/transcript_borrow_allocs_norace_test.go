//go:build !race

package ai

import (
	"errors"
	"fmt"
	"testing"
)

// The race detector adds allocations that vary between runs (pooled values are dropped at random), so the allocation difference is measured without it.
// A provider borrows the transcript, so what it never sends costs nothing: a tool result's details (UI data the request leaves out) must not add an allocation to the request build, however large they are. A deep copy of the transcript anywhere in the build (Messages, ResolveTranscript, CollapseSystemMessages, WithoutInitialSystemMessage) copies every details tree and fails this; a converter's own work is the same for both histories.
func TestRequestBuildDoesNotCopyWhatItDoesNotSend(t *testing.T) {
	const turns = 40
	for _, model := range borrowCanaryModels(t) {
		if model.API == APIBedrockConverseStream {
			// Converse has no payload hook to stop the build before the SDK serializes and signs the request; its conversion is covered by TestRequestBuildLeavesTheTranscriptUnchanged.
			continue
		}
		t.Run(model.Provider+"/"+model.ID, func(t *testing.T) {
			var provider Provider
			if model.API == APIPiMessages {
				provider = NewPiMessagesProvider(PiMessagesConfig{BaseURL: "http://127.0.0.1:1", APIKey: "matrix-key", Model: model.ID, ProviderID: model.Provider})
			} else {
				provider = newMatrixProvider(t, model, "http://127.0.0.1:1", false)
			}
			t.Cleanup(func() { _ = provider.Close() })
			allocs := func(details func(int) any) float64 {
				messages := transformHistory(turns)
				for i := range messages {
					switch message := messages[i].(type) {
					case AssistantMessage:
						message.API, message.Provider, message.Model = model.API, model.Provider, model.ID
						messages[i] = message
					case ToolResultMessage:
						message.Details = details(i)
						messages[i] = message
					}
				}
				transcript := NormalizeContext(Context{Messages: messages})
				options := StreamOptions{APIKey: "matrix-key", Transport: TransportSSE, OnPayload: func(any, *Model) (any, error) { return nil, errors.New("stop") }}
				return testing.AllocsPerRun(3, func() {
					if stream, err := provider.Stream(t.Context(), transcript, options); err == nil && stream != nil {
						_ = stream.Result()
					}
				})
			}
			plain := allocs(func(int) any { return nil })
			heavy := allocs(func(i int) any {
				rows := make([]any, 16)
				for j := range rows {
					rows[j] = map[string]any{"line": float64(j), "text": fmt.Sprintf("row %d of result %d", j, i)}
				}
				return map[string]any{"rows": rows, "path": fmt.Sprintf("file-%d", i)}
			})
			// One details tree is 18 objects; a single deep copy of the history adds 3*turns of them.
			if heavy-plain >= turns {
				t.Fatalf("the request build allocates %.0f times with tool-result details and %.0f times without: it copies data it does not send", heavy, plain)
			}
		})
	}
}

// A request build reads the replayed tools and copies none of them: the replay and the system prompt allocate the same over deep tool schemas as over shallow ones, which a deep copy of every tool per replay would not, and the collapse copies fewer schemas than the copying construction.
func TestToolReplayAllocationsDoNotGrowWithTheSchemas(t *testing.T) {
	build := func(depth int) []Message {
		schema := map[string]any{"type": "string"}
		for i := range depth {
			schema = map[string]any{"type": "object", "properties": map[string]any{fmt.Sprintf("p%d", i): schema, "q": []any{"a", "b"}}}
		}
		tools := make([]ToolSchema, 6)
		for i := range tools {
			tools[i] = ToolSchema{Name: fmt.Sprintf("tool%d", i), Description: "d", Parameters: schema, PromptGuidelines: []string{"g"}}
		}
		return []Message{SystemMessage{Content: SystemText("base"), ToolsAdded: tools}, UserMessage{Content: UserText("hi")}}
	}
	measure := func(depth int, run func(messages []Message)) float64 {
		messages := build(depth)
		return testing.AllocsPerRun(20, func() { run(messages) })
	}
	for name, run := range map[string]func([]Message){
		"borrowedCurrentTools":         func(messages []Message) { _ = borrowedCurrentTools(messages) },
		"GetCurrentSystemPrompt":       func(messages []Message) { _ = GetCurrentSystemPrompt(messages) },
		"borrowedInitialSystemMessage": func(messages []Message) { _ = borrowedInitialSystemMessage(messages) },
	} {
		if small, large := measure(1, run), measure(12, run); large > small {
			t.Errorf("%s allocates %.0f times over deep tool schemas and %.0f over shallow ones: it copies the schemas", name, large, small)
		}
	}
	// The collapse validates the leading message it builds, and the validation's allocations grow with the schemas; the replay and the collapse add none beyond it.
	collapse := func(messages []Message) { _ = CollapseSystemMessages(TranscriptContext{messages: messages}) }
	validate := func(messages []Message) { _ = validateTranscriptContext(TranscriptContext{messages: messages[:1]}) }
	collapseGrowth := measure(12, collapse) - measure(1, collapse)
	if validateGrowth := measure(12, validate) - measure(1, validate); collapseGrowth > validateGrowth {
		t.Errorf("CollapseSystemMessages allocates %.0f more times over deep tool schemas, their validation alone %.0f: it copies the schemas", collapseGrowth, validateGrowth)
	}
}
