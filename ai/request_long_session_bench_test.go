//go:build !pig_strip_mistral_conversations

package ai

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// longSessionHistory is the context the long-session row (Pi-Bolt bench/long_session.py) sends at its last prompt: prompts
// prompts, each a user message, four turns that stream about 120 words and read a 48 KB source file, and a closing answer.
func longSessionHistory(prompts int) []Message {
	words := strings.Repeat("lorem ipsum dolor sit amet consectetur adipiscing elit ", 15)
	line := "\tconst value = this.session.get(\"key\") ?? `fallback ${index}`; // <tag> & \\path\n"
	file := strings.Repeat(line, 48<<10/len(line))
	messages := make([]Message, 0, prompts*10)
	for prompt := range prompts {
		messages = append(messages, UserMessage{Content: UserText("Read the four fixture files")})
		for turn := range 4 {
			id := fmt.Sprintf("call_%d", turn)
			call := ToolCall{ID: id, Name: "read"}
			call.SetStreamingArguments(fmt.Sprintf(`{"path": "fixture/file-%d.ts"}`, turn))
			messages = append(messages,
				AssistantMessage{API: APIOpenAICompletions, Provider: "fake", Model: "fake-model", StopReason: StopReasonToolUse, Content: []AssistantContentBlock{TextContent{Text: words}, call}},
				ToolResultMessage{ToolCallID: id, ToolName: "read", Content: []ToolResultMessageContent{TextContent{Text: file + fmt.Sprint(prompt, turn)}}},
			)
		}
		messages = append(messages, AssistantMessage{API: APIOpenAICompletions, Provider: "fake", Model: "fake-model", StopReason: StopReasonStop, Content: []AssistantContentBlock{TextContent{Text: words + fmt.Sprintf("\n\nDone: read all four files (prompt %d).", prompt+1)}}})
	}
	return append(messages, UserMessage{Content: UserText("Read the four fixture files")})
}

// BenchmarkCompletionsRequestLongSession converts and encodes the completions request of the long-session row's 75th prompt
// (about 750 messages and 14 MB), the per-request work that grows with the history.
func BenchmarkCompletionsRequestLongSession(b *testing.B) {
	transcript := NormalizeContext(Context{SystemPrompt: "You are a coding agent.", Messages: longSessionHistory(75), Frozen: true})
	provider := NewOpenAIProvider(OpenAIConfig{ProviderID: "fake", Model: "fake-model", BaseURL: "http://127.0.0.1:1/v1", APIKey: "fake"}).(*openAIProvider)
	build := func() int {
		conversion, err := provider.convertTranscript(transcript, nil, false)
		if err != nil {
			b.Fatal(err)
		}
		body, err := marshalSpliced(oaiRequest{Model: "fake-model", Messages: conversion.chat, Stream: true}, requestStrings, marshalRequestBody)
		if err != nil {
			b.Fatal(err)
		}
		return body.size
	}
	b.ReportMetric(float64(build()), "request-bytes")
	b.ReportAllocs()
	for b.Loop() {
		build()
	}
}

// largeWriteHistory is the large-write row's follow-up request: a write call carrying a 200 KB file, its result and the next prompt.
func largeWriteHistory() []Message {
	content := strings.Repeat("\tconst value = \"<tag>\" & `x`;\n", 200<<10/31)
	call := ToolCall{ID: "call_1", Name: "write"}
	call.SetStreamingArguments(`{"path":"big.ts","content":` + strconv.Quote(content) + `}`)
	// The agent encodes the arguments once when the call ends (agent.newPendingToolCall).
	if _, err := call.ArgumentsJSON(); err != nil {
		panic(err)
	}
	return []Message{
		UserMessage{Content: UserText("Write big.ts")},
		AssistantMessage{API: APIOpenAICompletions, Provider: "fake", Model: "fake-model", StopReason: StopReasonToolUse, Content: []AssistantContentBlock{call}},
		ToolResultMessage{ToolCallID: "call_1", ToolName: "write", Content: []ToolResultMessageContent{TextContent{Text: "Successfully wrote 204800 bytes to big.ts"}}},
		UserMessage{Content: UserText("next")},
	}
}

// BenchmarkRequestLargeWriteFollowUp converts and encodes the request after a 200 KB write call, on completions and Responses.
func BenchmarkRequestLargeWriteFollowUp(b *testing.B) {
	messages := largeWriteHistory()
	b.Run("completions", func(b *testing.B) {
		transcript := NormalizeContext(Context{Messages: messages, Frozen: true})
		provider := NewOpenAIProvider(OpenAIConfig{ProviderID: "fake", Model: "fake-model", BaseURL: "http://127.0.0.1:1/v1", APIKey: "fake"}).(*openAIProvider)
		b.ReportAllocs()
		for b.Loop() {
			conversion, err := provider.convertTranscript(transcript, nil, false)
			if err != nil {
				b.Fatal(err)
			}
			if _, err := marshalSpliced(oaiRequest{Model: "fake-model", Messages: conversion.chat, Stream: true}, requestStrings, marshalRequestBody); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("responses", func(b *testing.B) {
		provider := &openAIResponsesProvider{}
		b.ReportAllocs()
		for b.Loop() {
			items, err := provider.convertMessages(messages, nil)
			if err != nil {
				b.Fatal(err)
			}
			if _, err := marshalSpliced(items, requestStrings, marshalRequestBody); err != nil {
				b.Fatal(err)
			}
		}
	})
}

// codexContinuationBody is a Codex request body with items input items of about 1 KB each.
func codexContinuationBody(items int) []byte {
	input := make([]any, items)
	for i := range input {
		input[i] = map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": fmt.Sprintf("%d %s", i, strings.Repeat("text <b> & ", 90))}}}
	}
	body, err := marshalRequestBody(map[string]any{"model": "gpt-5", "store": false, "stream": true, "instructions": "i", "input": input, "tool_choice": "auto"})
	if err != nil {
		panic(err)
	}
	return body
}

// BenchmarkCodexWebSocketContinuation is a cached WebSocket turn at 2,000 input items: read the body, compare it with the previous
// request and response, write the delta message and keep the request for the next turn.
func BenchmarkCodexWebSocketContinuation(b *testing.B) {
	previous, body := codexContinuationBody(1998), codexContinuationBody(2000)
	last, err := readCodexRequest(previous)
	if err != nil {
		b.Fatal(err)
	}
	response := []any{map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": fmt.Sprintf("%d %s", 1998, strings.Repeat("text <b> & ", 90))}}}}
	responseItem, _ := marshalRequestBody(response[0])
	continuation := &codexWebSocketContinuation{lastRest: last.rest(), lastResponseID: "resp_1", baseline: append(slices.Clone(last.input), responseItem)}
	b.ReportAllocs()
	for b.Loop() {
		entry := &codexWebSocketEntry{continuation: continuation}
		request, err := readCodexRequest(body)
		if err != nil {
			b.Fatal(err)
		}
		delta := cachedCodexDelta(entry, request)
		if delta == nil || delta.count != 1 {
			b.Fatalf("no continuation: %+v", delta)
		}
		if _, err := codexWebSocketMessage(request, delta); err != nil {
			b.Fatal(err)
		}
		item, _ := marshalRequestBody(response[0])
		_ = &codexWebSocketContinuation{lastRest: request.rest(), baseline: append(slices.Clone(request.input), item)}
	}
}
