package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"math/rand/v2"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/rpcclient"
)

// The RPC event encoder writes each event member into the output line. These tests hold its text to the text of the encoder it replaced: rpcObject.MarshalJSON, which encodes every member apart and has the line encoder compact the result.

// legacyRPCObject hides rpcObject's AppendStringify, so the line encoder takes the route that encodes every member with json.Marshal and compacts the text.
type legacyRPCObject rpcObject

func (object legacyRPCObject) MarshalJSON() ([]byte, error) { return rpcObject(object).MarshalJSON() }

func legacyRPCLine(frame any) ([]byte, error) {
	if object, ok := frame.(rpcObject); ok {
		frame = legacyRPCObject(object)
	}
	return rpcclient.SerializeJsonLine(frame)
}

// legacyRPCMessageUpdate is rpcMessageUpdate before it kept the members of a plain event as they are: every event went through its own encoding and back.
func legacyRPCMessageUpdate(event agent.MessageUpdateEvent) (any, error) {
	if event.Message.Assistant == nil {
		return nil, fmt.Errorf("message_update message is not an assistant message")
	}
	if event.AssistantMessageEvent == nil {
		return nil, fmt.Errorf("message_update assistant event is nil")
	}
	encoded, err := json.Marshal(rpcAssistantEventFields(event.AssistantMessageEvent))
	if err != nil {
		return nil, fmt.Errorf("marshal message_update assistant event: %w", err)
	}
	var wire rpcObject
	if err := json.Unmarshal(encoded, &wire); err != nil {
		return nil, fmt.Errorf("decode message_update assistant event: %w", err)
	}
	wire = slices.DeleteFunc(wire, func(field rpcField) bool { return field.name == "partial" })
	if start, ok := event.AssistantMessageEvent.(ai.ToolCallStartEvent); ok {
		id, name, toolCall, indexValid := start.Partial.ObserveToolCallIdentity(start.ContentIndex)
		if !indexValid {
			return nil, fmt.Errorf("toolcall_start content index %d is invalid", start.ContentIndex)
		}
		if !toolCall {
			return nil, fmt.Errorf("toolcall_start content at index %d is not a tool call", start.ContentIndex)
		}
		wire = append(wire, rpcField{"id", id}, rpcField{"toolName", name})
	}
	return rpcObject{{"type", "message_update"}, {"usage", rpcUsage(event.Message.Assistant.ObserveUsage())}, {"assistantMessageEvent", wire}}, nil
}

func legacyRPCFrames(event agent.AgentEvent) ([]any, error) {
	if update, ok := event.(agent.MessageUpdateEvent); ok {
		frame, err := legacyRPCMessageUpdate(update)
		if err != nil {
			return nil, err
		}
		return []any{frame}, nil
	}
	return rpcAgentEvent(event)
}

// requireLegacyRPCLines checks that one event's frames encode to the lines, or the error, the replaced encoder gives, through both the line writer and rpcclient.
func requireLegacyRPCLines(t testing.TB, label string, event agent.AgentEvent) {
	t.Helper()
	got, gotErr := rpcAgentEvent(event)
	want, wantErr := legacyRPCFrames(event)
	if fmt.Sprint(gotErr) != fmt.Sprint(wantErr) {
		t.Fatalf("%s: conversion error %v, legacy %v", label, gotErr, wantErr)
	}
	if len(got) != len(want) {
		t.Fatalf("%s: %d frames, legacy %d", label, len(got), len(want))
	}
	for i := range got {
		wantLine, wantLineErr := legacyRPCLine(want[i])
		gotLine, gotLineErr := rpcclient.SerializeJsonLine(got[i])
		// The replaced route's top-level type is the test's wrapper of rpcObject.
		if fmt.Sprint(gotLineErr) != strings.ReplaceAll(fmt.Sprint(wantLineErr), "cli.legacyRPCObject", "cli.rpcObject") {
			t.Fatalf("%s frame %d: encode error %v, legacy %v", label, i, gotLineErr, wantLineErr)
		}
		if !bytes.Equal(gotLine, wantLine) {
			t.Fatalf("%s frame %d:\n got %q\nwant %q", label, i, gotLine, wantLine)
		}
		var written bytes.Buffer
		writeJSONLine(&written, got[i])
		if wantLineErr == nil && !bytes.Equal(written.Bytes(), wantLine) {
			t.Fatalf("%s frame %d written:\n got %q\nwant %q", label, i, written.Bytes(), wantLine)
		}
		if wantLineErr != nil && written.Len() != 0 {
			t.Fatalf("%s frame %d: writeJSONLine wrote %q for an encode error", label, i, written.Bytes())
		}
	}
}

// edgeStrings are the strings whose escaping differs between encoders: JSON.stringify's escapes, UTF-16 units that pair or stand alone, ill-formed UTF-8, the characters HTML escaping and the line separators touch, and runs long enough for the word-at-a-time scan.
func edgeStrings() []string {
	return []string{
		"", "plain ascii text that is long enough to take eight bytes at a time", "quote \" backslash \\ slash / done",
		"\x00\x01\x07\b\t\n\v\f\r\x1b\x1f\x7f", "tab\tnewline\ncarriage\rreturn", "<script>alert(1 && 2 > 1)</script>",
		"line\u2028separator\u2029paragraph", "replacement \ufffd char", "emoji \U0001F600 \U0001F468\u200D\U0001F469\u200D\U0001F467 flags \U0001F1FA\U0001F1F8",
		"\xff", "\xfe\xff", "\xc3", "\xe2\x82", "\xf0\x9f\x98", "ok\xc3(ok", "\xc0\x80", "\xed\xa0\x80", "\xed\xbf\xbf",
		"\xed\xa0\xbd\xed\xb8\x80", "lone high \xed\xa0\xbd then text", "lone low \xed\xb8\x80 then text", "\xed\xb8\x80\xed\xa0\xbd",
		"\\u003c \\u003e \\u0026 \\u2028 \\ufffd literal escapes", "日本語 مرحبا Привет \u00e9\u0301",
		strings.Repeat("abcdefgh", 9) + "\"" + strings.Repeat("x", 17) + "\n" + strings.Repeat("y", 100),
		strings.Repeat("ab\u2028", 300), strings.Repeat("é", 700), strings.Repeat("\xff", 33),
	}
}

// jsonQuote is text as a JSON string; the Go %q verb writes \x escapes JSON does not have.
func jsonQuote(text string) string {
	quoted, err := json.Marshal(text)
	if err != nil {
		panic(err)
	}
	return string(quoted)
}

func randomText(random *rand.Rand) string {
	palette := edgeStrings()
	var text strings.Builder
	for range random.IntN(6) {
		switch random.IntN(4) {
		case 0:
			text.WriteString(palette[random.IntN(len(palette))])
		case 1:
			for range random.IntN(40) {
				text.WriteByte(byte(random.IntN(256)))
			}
		case 2:
			for range random.IntN(20) {
				text.WriteRune(rune(random.IntN(0x110000)))
			}
		default:
			text.WriteString(strings.Repeat("word ", random.IntN(30)))
		}
	}
	return text.String()
}

func randomNumber(random *rand.Rand) float64 {
	return []float64{0, math.Copysign(0, -1), 1, -1, 1.5, 0.1 + 0.2, 1e20, 1e21, 1e-6, 1e-7, 123456789.123456789, math.MaxFloat64, math.SmallestNonzeroFloat64, 4.35, 0.000003, 12.5e-9}[random.IntN(16)]
}

func randomJSONValue(random *rand.Rand, depth int) any {
	switch random.IntN(7) {
	case 0:
		return randomText(random)
	case 1:
		return randomNumber(random)
	case 2:
		return random.IntN(1000) - 500
	case 3:
		return random.IntN(2) == 0
	case 4:
		return nil
	case 5:
		if depth > 0 {
			return []any{randomJSONValue(random, depth-1), randomJSONValue(random, depth-1)}
		}
	}
	if depth > 0 {
		object := ai.JsonObject{}
		for range random.IntN(4) {
			object[randomText(random)] = randomJSONValue(random, depth-1)
		}
		return object
	}
	return "leaf"
}

func randomUsage(random *rand.Rand) *ai.Usage {
	usage := &ai.Usage{Input: random.IntN(100000), Output: random.IntN(5000), CacheRead: random.IntN(100000), CacheWrite: random.IntN(5000), TotalTokens: random.IntN(200000),
		Cost: ai.UsageCost{Input: randomNumber(random), Output: randomNumber(random), CacheRead: randomNumber(random), CacheWrite: randomNumber(random), Total: randomNumber(random)}}
	if random.IntN(2) == 0 {
		usage.CacheWrite1h = new(random.IntN(100))
	}
	if random.IntN(2) == 0 {
		usage.Reasoning = new(random.IntN(100))
	}
	return usage
}

func randomAssistantBlocks(random *rand.Rand) []ai.AssistantContentBlock {
	var blocks []ai.AssistantContentBlock
	for range 1 + random.IntN(4) {
		switch random.IntN(3) {
		case 0:
			blocks = append(blocks, ai.TextContent{Text: randomText(random), TextSignature: []string{"", randomText(random)}[random.IntN(2)]})
		case 1:
			blocks = append(blocks, ai.ThinkingContent{Thinking: randomText(random), ThinkingSignature: []string{"", randomText(random)}[random.IntN(2)], Redacted: random.IntN(5) == 0})
		default:
			arguments := ai.JsonObject{}
			for range random.IntN(4) {
				arguments[randomText(random)] = randomJSONValue(random, 3)
			}
			blocks = append(blocks, ai.ToolCall{ID: randomText(random), Name: randomText(random), Arguments: arguments})
		}
	}
	return blocks
}

func randomAssistant(random *rand.Rand) *agent.AssistantMessage {
	message := &agent.AssistantMessage{Role: "assistant", Content: randomAssistantBlocks(random), API: ai.APIAnthropicMessages, Provider: randomText(random), ModelID: randomText(random),
		Timestamp: random.Int64N(1 << 60), Usage: randomUsage(random), StopReason: []ai.StopReason{"", ai.StopReasonStop, ai.StopReasonToolUse, ai.StopReasonError}[random.IntN(4)]}
	if random.IntN(3) == 0 {
		message.ErrorMessage, message.ErrorBeforeTimestamp = randomText(random), random.IntN(2) == 0
	}
	if random.IntN(3) == 0 {
		message.ResponseID, message.ResponseModel, message.RawStopReason = randomText(random), randomText(random), randomText(random)
	}
	if random.IntN(3) == 0 {
		message.EndTurn, message.DurationMs = new(random.IntN(2) == 0), new(random.Int64N(100000))
		message.ThinkingLevel, message.ProviderThinkingLevel = "high", "high"
	}
	return message
}

func randomToolResult(random *rand.Rand) agent.ToolResultMessage {
	message := agent.ToolResultMessage{Role: "toolResult", ToolCallID: randomText(random), ToolName: randomText(random), IsError: random.IntN(2) == 0, Timestamp: random.Int64N(1 << 60),
		Content: []ai.ToolResultMessageContent{ai.TextContent{Text: randomText(random)}, ai.ImageContent{Data: randomText(random), MimeType: "image/png"}}}
	switch random.IntN(3) {
	case 0:
		message.Details = map[string]any{"path": randomText(random), "bytes": random.IntN(1 << 20), "ratio": randomNumber(random), "nested": []any{randomText(random), nil}}
	case 1:
		message.DetailsNull = true
	}
	if random.IntN(2) == 0 {
		message.DurationMs = new(random.Int64N(5000))
	}
	if random.IntN(3) == 0 {
		message.Usage = randomUsage(random)
	}
	return message
}

func randomToolResultValue(random *rand.Rand) agent.AgentToolResult {
	result := agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: randomText(random)}}, IsError: random.IntN(2) == 0}
	if random.IntN(2) == 0 {
		result.Details = map[string]any{"text": randomText(random), "n": random.IntN(10)}
	}
	if random.IntN(3) == 0 {
		result.StructuredContent = json.RawMessage(fmt.Sprintf(`{"b": %s, "a": [1, 2.50, -0, 1e21]}`, jsonQuote(randomText(random))))
	}
	if random.IntN(3) == 0 {
		result.Usage = randomUsage(random)
	}
	return result
}

func randomAgentMessage(random *rand.Rand) agent.AgentMessage {
	switch random.IntN(3) {
	case 0:
		var content ai.UserContent = ai.UserText(randomText(random))
		if random.IntN(2) == 0 {
			content = ai.UserContentBlocks{ai.TextContent{Text: randomText(random)}, ai.ImageContent{Data: randomText(random), MimeType: "image/jpeg"}}
		}
		return agent.AgentMessage{User: &agent.UserMessage{Role: "user", Content: content, Timestamp: random.Int64N(1 << 60)}}
	case 1:
		return agent.AgentMessage{Assistant: randomAssistant(random)}
	default:
		result := randomToolResult(random)
		return agent.AgentMessage{ToolResult: &result}
	}
}

// randomEvents is one of every event the RPC writer converts, filled from random.
func randomEvents(random *rand.Rand) []agent.AgentEvent {
	assistant := randomAssistant(random)
	partial := &ai.AssistantMessage{Content: []ai.AssistantContentBlock{ai.TextContent{Text: randomText(random)}, ai.ToolCall{ID: randomText(random), Name: randomText(random), Arguments: ai.JsonObject{"k": randomJSONValue(random, 2)}}}}
	assistantMessage := agent.AgentMessage{Assistant: assistant}
	toolCall := ai.ToolCall{ID: randomText(random), Name: randomText(random), Arguments: ai.JsonObject{randomText(random): randomJSONValue(random, 3)}}
	args := json.RawMessage(fmt.Sprintf(`{ "a" : %s , "b":[1, 2.0, -0] }`, jsonQuote(randomText(random))))
	events := []agent.AgentEvent{
		agent.AgentStartEvent{}, agent.TurnStartEvent{}, agent.SessionInfoChangedEvent{Name: randomText(random)},
		agent.AgentEndEvent{Messages: []agent.AgentMessage{randomAgentMessage(random), assistantMessage, randomAgentMessage(random)}, WillRetry: random.IntN(2) == 0},
		agent.AgentSettledEvent{Aborted: random.IntN(2) == 0},
		agent.TurnEndEvent{Message: assistantMessage, ToolResults: []agent.ToolResultMessage{randomToolResult(random), randomToolResult(random)}},
		agent.MessageStartEvent{Message: randomAgentMessage(random)}, agent.MessageEndEvent{Message: randomAgentMessage(random)},
		agent.MessageStartEvent{Message: assistantMessage}, agent.MessageEndEvent{Message: assistantMessage},
		agent.ToolExecutionStartEvent{ToolCallID: randomText(random), ToolName: randomText(random), Args: args},
		agent.ToolExecutionUpdateEvent{ToolCallID: randomText(random), ToolName: randomText(random), Args: args, PartialResult: randomToolResultValue(random)},
		agent.ToolExecutionEndEvent{ToolCallID: randomText(random), ToolName: randomText(random), Result: randomToolResultValue(random), IsError: random.IntN(2) == 0, DurationMs: new(random.Int64N(1000))},
		agent.ToolExecutionEndEvent{ToolCallID: randomText(random), ToolName: randomText(random), Result: randomToolResultValue(random)},
		agent.CompactionStartEvent{Reason: randomText(random)},
		agent.CompactionEndEvent{Reason: "manual", Summary: randomText(random), FirstKeptEntryID: randomText(random), TokensBefore: random.IntN(1000), EstimatedTokensAfter: random.IntN(1000), Usage: randomUsage(random), ErrorMessage: randomText(random)},
		agent.CompactionEndEvent{Reason: "overflow", Aborted: true, ErrorMessage: randomText(random)},
		agent.AutoRetryStartEvent{Attempt: 1, MaxAttempts: 3, DelayMs: 2000, ErrorMessage: randomText(random)}, agent.AutoRetryEndEvent{Attempt: 2, FinalError: randomText(random)},
		agent.SummarizationRetryScheduledEvent{Attempt: 1, MaxAttempts: 2, DelayMs: 5, ErrorMessage: randomText(random)},
		agent.SummarizationRetryAttemptStartEvent{Source: "compaction", Reason: "manual"}, agent.SummarizationRetryFinishedEvent{},
		agent.QueueUpdateEvent{Steering: []string{randomText(random)}, FollowUp: []string{randomText(random), randomText(random)}},
		agent.BashExecutionUpdateEvent{ID: new(randomText(random)), Delta: randomText(random)}, agent.ThinkingLevelChangedEvent{Level: "high"},
		agent.EntryAppendedEvent{Entry: json.RawMessage(fmt.Sprintf(`{"type": "custom", "data": %s}`, jsonQuote(randomText(random))))},
	}
	for _, update := range []ai.AssistantMessageEvent{
		ai.StartEvent{Partial: partial}, ai.TextStartEvent{ContentIndex: 0, Partial: partial}, ai.TextDeltaEvent{ContentIndex: 1, Delta: randomText(random), Partial: partial},
		ai.TextEndEvent{ContentIndex: 0, Content: randomText(random), Partial: partial}, ai.ThinkingStartEvent{ContentIndex: 0, Partial: partial},
		ai.ThinkingDeltaEvent{ContentIndex: 2, Delta: randomText(random), Partial: partial}, ai.ThinkingEndEvent{ContentIndex: 0, Content: randomText(random), Partial: partial},
		ai.ToolCallStartEvent{ContentIndex: 1, Partial: partial}, ai.ToolCallDeltaEvent{ContentIndex: 1, Delta: randomText(random), Partial: partial},
		ai.ToolCallEndEvent{ContentIndex: 1, ToolCall: toolCall, Partial: partial},
		ai.DoneEvent{Reason: ai.StopReasonStop, Message: &ai.AssistantMessage{Content: partial.Content}}, ai.ErrorEvent{Reason: ai.StopReasonError, Error: &ai.AssistantMessage{Content: partial.Content, ErrorMessage: randomText(random)}},
	} {
		events = append(events, agent.MessageUpdateEvent{Message: assistantMessage, AssistantMessageEvent: update})
	}
	return events
}

// The event lines equal the replaced encoder's over random events built from the strings and numbers that separate encoders.
func TestRPCEventLinesMatchTheReplacedEncoder(t *testing.T) {
	rounds := 150
	if testing.Short() {
		rounds = 15
	}
	for seed := range uint64(rounds) {
		random := rand.New(rand.NewPCG(seed, 0x5eed))
		for i, event := range randomEvents(random) {
			requireLegacyRPCLines(t, fmt.Sprintf("seed %d event %d %T", seed, i, event), event)
		}
	}
}

// Each edge string alone, in every member position an event gives a string: message text, thinking, tool names, tool arguments and results, deltas and names.
func TestRPCEventLinesMatchTheReplacedEncoderForEdgeStrings(t *testing.T) {
	for _, text := range edgeStrings() {
		partial := &ai.AssistantMessage{Content: []ai.AssistantContentBlock{ai.ToolCall{ID: text, Name: text}}}
		assistant := agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: "assistant", Provider: text, ModelID: text, ErrorMessage: text, ResponseID: text, Usage: &ai.Usage{},
			Content: []ai.AssistantContentBlock{ai.TextContent{Text: text}, ai.ThinkingContent{Thinking: text, ThinkingSignature: text}, ai.ToolCall{ID: text, Name: text, Arguments: ai.JsonObject{text: text}}}}}
		for i, event := range []agent.AgentEvent{
			agent.MessageEndEvent{Message: assistant}, agent.MessageStartEvent{Message: agent.AgentMessage{User: &agent.UserMessage{Role: "user", Content: ai.UserText(text)}}},
			agent.MessageUpdateEvent{Message: assistant, AssistantMessageEvent: ai.TextDeltaEvent{Delta: text, Partial: partial}},
			agent.MessageUpdateEvent{Message: assistant, AssistantMessageEvent: ai.TextEndEvent{Content: text, Partial: partial}},
			agent.MessageUpdateEvent{Message: assistant, AssistantMessageEvent: ai.ToolCallDeltaEvent{Delta: text, Partial: partial}},
			agent.MessageUpdateEvent{Message: assistant, AssistantMessageEvent: ai.ToolCallStartEvent{Partial: partial}},
			agent.ToolExecutionEndEvent{ToolCallID: text, ToolName: text, Result: agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: text}}, Details: map[string]any{text: text}}},
			agent.CompactionEndEvent{Reason: text, Summary: "s", ErrorMessage: text},
		} {
			requireLegacyRPCLines(t, fmt.Sprintf("%q event %d", text, i), event)
		}
	}
}

// A number that no JSON text holds, or a value that cannot be encoded, fails as it did: the same error, and nothing written.
func TestRPCEventLinesFailAsTheReplacedEncoderDid(t *testing.T) {
	assistant := func(usage *ai.Usage) agent.AgentMessage {
		return agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: "assistant", Usage: usage, Content: []ai.AssistantContentBlock{ai.TextContent{Text: "x"}}}}
	}
	for _, cost := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		for field := range 5 {
			usage := &ai.Usage{}
			*[]*float64{&usage.Cost.Input, &usage.Cost.Output, &usage.Cost.CacheRead, &usage.Cost.CacheWrite, &usage.Cost.Total}[field] = cost
			for _, split := range []bool{false, true} {
				if split {
					usage.CacheWrite1h = new(1)
				}
				for i, event := range []agent.AgentEvent{agent.MessageEndEvent{Message: assistant(usage)}, agent.TurnEndEvent{Message: assistant(usage)},
					agent.CompactionEndEvent{Reason: "manual", Summary: "s", Usage: usage},
				} {
					requireLegacyRPCLines(t, fmt.Sprintf("cost %v field %d split %v event %d", cost, field, split, i), event)
					if _, err := rpcclient.SerializeJsonLine(mustFrames(t, event)[0]); err == nil {
						t.Fatalf("cost %v field %d event %d encoded", cost, field, i)
					}
				}
			}
		}
	}
	// Invalid raw JSON in a nested member: the error text carries one wrapper per nesting level.
	for i, event := range []agent.AgentEvent{
		agent.ToolExecutionStartEvent{ToolCallID: "c", ToolName: "t", Args: json.RawMessage(`{"a":`)},
		agent.ToolExecutionUpdateEvent{ToolCallID: "c", ToolName: "t", Args: json.RawMessage(`nope`), PartialResult: agent.AgentToolResult{}},
		agent.ToolExecutionEndEvent{ToolCallID: "c", ToolName: "t", Result: agent.AgentToolResult{Details: json.RawMessage(`[1,`)}},
		agent.TurnEndEvent{Message: assistant(&ai.Usage{}), ToolResults: []agent.ToolResultMessage{{Role: "toolResult", Details: json.RawMessage(`{`)}}},
	} {
		requireLegacyRPCLines(t, fmt.Sprintf("invalid raw %d", i), event)
		if _, err := rpcclient.SerializeJsonLine(mustFrames(t, event)[0]); err == nil {
			t.Fatalf("invalid raw %d encoded", i)
		}
	}
}

func mustFrames(t testing.TB, event agent.AgentEvent) []any {
	t.Helper()
	frames, err := rpcAgentEvent(event)
	if err != nil {
		t.Fatal(err)
	}
	return frames
}

// The frames of every faux fixture's session, the events a real run converts, encode as the replaced encoder did.
func TestRPCEventLinesMatchTheReplacedEncoderForFauxSessions(t *testing.T) {
	oracle := readFauxRPCOracle(t)
	var frames atomic.Int64
	for _, result := range oracle.Results {
		t.Run(result.Fixture.Name, func(t *testing.T) {
			fauxRPCRun(t, result.Fixture, func(frame any) {
				frames.Add(1)
				want, wantErr := legacyRPCLine(frame)
				var got bytes.Buffer
				writeJSONLine(&got, frame)
				if (wantErr != nil) != (got.Len() == 0) || (wantErr == nil && !bytes.Equal(got.Bytes(), want)) {
					t.Errorf("frame %T:\n got %q\nwant %q (%v)", frame, got.Bytes(), want, wantErr)
				}
			})
		})
	}
	if frames.Load() == 0 {
		t.Fatal("no frames recorded")
	}
}

// A tool call that cannot be encoded fails when the event is converted, which ends the RPC output, not when the line is written, where a failed line is dropped.
func TestRPCEventLinesFailAtConversionForAnUnencodableToolCall(t *testing.T) {
	partial := &ai.AssistantMessage{Content: []ai.AssistantContentBlock{ai.ToolCall{ID: "c", Name: "t"}}}
	assistant := agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: "assistant", Usage: &ai.Usage{}, Content: []ai.AssistantContentBlock{ai.TextContent{Text: "x"}}}}
	for _, value := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		event := agent.MessageUpdateEvent{Message: assistant, AssistantMessageEvent: ai.ToolCallEndEvent{ContentIndex: 0, Partial: partial,
			ToolCall: ai.ToolCall{ID: "c", Name: "t", Arguments: ai.JsonObject{"nested": []any{ai.JsonObject{"k": value}}}}}}
		requireLegacyRPCLines(t, fmt.Sprintf("toolcall_end %v", value), event)
		if _, err := rpcAgentEvent(event); err == nil {
			t.Fatalf("toolcall_end with %v converted; want the conversion to fail", value)
		}
	}
}

// Usage that a provider created with cacheWrite1h and reasoning after cost keeps that member order in every event that carries it, and an RPCUsage built from its members takes the order it was given.
func TestRPCEventLinesKeepTheMemberOrderOfUsageTheProviderBuilt(t *testing.T) {
	for _, text := range []string{
		`{"input":1,"output":2,"cacheRead":3,"cacheWrite":4,"totalTokens":10,"cost":{"input":0.5,"output":1e21,"cacheRead":0,"cacheWrite":-0,"total":1.5},"cacheWrite1h":7,"reasoning":8}`,
		`{"input":1,"output":2,"cacheRead":3,"cacheWrite":4,"cacheWrite1h":7,"reasoning":8,"totalTokens":10,"cost":{"input":0.5,"output":1e21,"cacheRead":0,"cacheWrite":-0,"total":1.5}}`,
		`{"input":1,"output":2,"cacheRead":3,"cacheWrite":4,"reasoning":8,"totalTokens":10,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}}`,
	} {
		var usage ai.Usage
		if err := json.Unmarshal([]byte(text), &usage); err != nil {
			t.Fatal(err)
		}
		assistant := agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: "assistant", Usage: &usage, Content: []ai.AssistantContentBlock{ai.TextContent{Text: "x"}}}}
		for i, event := range []agent.AgentEvent{
			agent.MessageEndEvent{Message: assistant}, agent.TurnEndEvent{Message: assistant},
			agent.MessageUpdateEvent{Message: assistant, AssistantMessageEvent: ai.TextDeltaEvent{Delta: "d", Partial: &ai.AssistantMessage{}}},
			agent.CompactionEndEvent{Reason: "manual", Summary: "s", Usage: &usage},
			agent.ToolExecutionEndEvent{ToolCallID: "c", ToolName: "t", Result: agent.AgentToolResult{Usage: &usage}},
		} {
			requireLegacyRPCLines(t, fmt.Sprintf("usage %s event %d", text, i), event)
		}
		line, err := rpcclient.SerializeJsonLine(mustFrames(t, agent.MessageEndEvent{Message: assistant})[0])
		if err != nil {
			t.Fatal(err)
		}
		costFirst := func(encoded []byte) bool {
			return bytes.Index(encoded, []byte(`"cost"`)) < bytes.Index(encoded, []byte(`"cacheWrite1h"`))
		}
		if bytes.Contains(line, []byte(`"cacheWrite1h"`)) && costFirst(line) != costFirst([]byte(text)) {
			t.Fatalf("member order of %s changed: %s", text, line)
		}
	}
	reasoning := 3
	for _, usage := range []RPCUsage{{Input: 1, Reasoning: &reasoning}, {Input: 1, CacheWrite1h: &reasoning}, {Input: 1, CacheWrite1h: &reasoning, Reasoning: &reasoning}, {}} {
		want, err := rpcclient.SerializeJsonLine(legacyUsage{usage})
		if err != nil {
			t.Fatal(err)
		}
		got, err := rpcclient.SerializeJsonLine(usage)
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("usage %+v:\n got %q (%v)\nwant %q", usage, got, err, want)
		}
	}
}

// legacyUsage hides RPCUsage's AppendStringify, so the line encoder compacts what MarshalJSON returns.
type legacyUsage struct{ usage RPCUsage }

func (u legacyUsage) MarshalJSON() ([]byte, error) { return u.usage.MarshalJSON() }

// Every member type an rpcObject holds is written as its json.Marshal text is, in a nested object and in a slice, whichever route writes it.
func TestRPCObjectAppendStringifyMatchesMarshalJSON(t *testing.T) {
	number := 3
	members := []any{
		nil, "", "a<b>&\u2028\xff", ai.EventTextDelta, ai.StopReasonToolUse, ai.API("anthropic-messages"), ai.ModelThinkingLevel("high"),
		0, -7, int64(1) << 60, true, false, math.Copysign(0, -1), 1e21, 1.5, float32(0.1), uint8(200), &number, (*int)(nil),
		json.RawMessage(`{ "a" : [1, 2.0] }`), []string{"a", "b\n"}, []string(nil), map[string]any{"z": 1, "a": nil},
		[]any(nil), []any{}, []any{nil, "x", 1, []any{2, rpcObject{{"k", "v"}}}}, rpcObject{}, rpcObject{{"a\xff", rpcObject{{"b", []any(nil)}}}},
	}
	for i, member := range members {
		object := rpcObject{{"first", "x"}, {"member", member}, {"last", 1}}
		want, wantErr := rpcclient.SerializeJsonLine(legacyRPCObject(object))
		got, err := rpcclient.SerializeJsonLine(object)
		if fmt.Sprint(err) != strings.ReplaceAll(fmt.Sprint(wantErr), "cli.legacyRPCObject", "cli.rpcObject") || !bytes.Equal(got, want) {
			t.Fatalf("member %d %#v:\n got %q (%v)\nwant %q (%v)", i, member, got, err, want, wantErr)
		}
	}
	for i, member := range []any{make(chan int), func() {}, json.RawMessage(`{`), rpcObject{{"x", make(chan int)}}, []any{1, make(chan int)}} {
		object := rpcObject{{"first", "x"}, {"member", member}}
		want, wantErr := rpcclient.SerializeJsonLine(legacyRPCObject(object))
		got, err := rpcclient.SerializeJsonLine(object)
		if wantErr == nil || err == nil || fmt.Sprint(err) != strings.ReplaceAll(fmt.Sprint(wantErr), "cli.legacyRPCObject", "cli.rpcObject") || got != nil || want != nil {
			t.Fatalf("unencodable member %d: got %q (%v) want %q (%v)", i, got, err, want, wantErr)
		}
	}
}
