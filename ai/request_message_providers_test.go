//go:build !pig_strip_mistral_conversations

package ai

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// messageCacheHistory builds the transcript of a coding session for the model the shape streams for, with each message shape a session produces and each way a history is poisoned: aborted and errored turns (which the conversion drops), empty text, a tool call nobody answered (which the conversion answers), an error result, an image result, thinking with its signature, and text that JSON escapes or that is not UTF-8.
func messageCacheHistory(random *rand.Rand, shape streamFrameShape, turns int) []Message {
	atoms := []string{"plain", "<b>&amp;</b>", "\u2028\u2029", "\x00\x01\x1f\x7f", "😀 café 漢字", "e\u0301", "\\\"", "\t\r\n", "", "\ufeff", "</script>", "\xff\xfe", "\xed\xa0\x80", "\U0010ffff"}
	text := func() string {
		var builder strings.Builder
		for range random.IntN(5) {
			builder.WriteString(atoms[random.IntN(len(atoms))])
		}
		return builder.String()
	}
	assistant := func(reason StopReason, blocks ...AssistantContentBlock) AssistantMessage {
		message := AssistantMessage{API: shape.api, Provider: shape.providerID, Model: shape.model, StopReason: reason, Content: blocks}
		if reason == StopReasonError || reason == StopReasonAborted {
			message.ErrorMessage = text()
		}
		return message
	}
	var messages []Message
	for turn := range turns {
		id := fmt.Sprintf("call_%d_%d", turn, random.IntN(1000))
		if shape.api == APIAnthropicMessages {
			id = "toolu_" + id
		}
		switch random.IntN(9) {
		case 0:
			messages = append(messages, UserMessage{Content: UserText(text())})
		case 1:
			messages = append(messages, UserMessage{Content: UserContentBlocks{TextContent{Text: text()}, ImageContent{Data: "iVBORw0KGgo" + text(), MimeType: "image/png"}}})
		case 2:
			// A turn that aborted or errored: its partial content never reaches the provider.
			messages = append(messages, UserMessage{Content: UserText(text())},
				assistant([]StopReason{StopReasonAborted, StopReasonError}[random.IntN(2)], TextContent{Text: text()}, ToolCall{ID: id, Name: "write", Arguments: JsonObject{"path": text()}}))
		case 3:
			// A tool call that no result answers, then the next user message.
			messages = append(messages, UserMessage{Content: UserText(text())},
				assistant(StopReasonToolUse, TextContent{Text: text()}, ToolCall{ID: id, Name: "bash", Arguments: JsonObject{"command": text()}}),
				UserMessage{Content: UserText(text())})
		case 4:
			messages = append(messages, UserMessage{Content: UserText(text())},
				assistant(StopReasonStop, TextContent{Text: ""}),
				UserMessage{Content: UserText("")})
		case 5:
			messages = append(messages, UserMessage{Content: UserText(text())},
				assistant(StopReasonToolUse, ThinkingContent{Thinking: text(), ThinkingSignature: "sig" + text()}, TextContent{Text: text()},
					ToolCall{ID: id, Name: "read", Arguments: JsonObject{"path": text(), "n": 1.5, "deep": map[string]JsonValue{"k": []JsonValue{text(), nil, true}}}}),
				ToolResultMessage{ToolCallID: id, ToolName: "read", Content: []ToolResultMessageContent{TextContent{Text: text()}, ImageContent{Data: "iVBORw0KGgo", MimeType: "image/png"}}})
		case 6:
			messages = append(messages, UserMessage{Content: UserText(text())},
				assistant(StopReasonToolUse, ToolCall{ID: id, Name: "write", Arguments: JsonObject{}}),
				ToolResultMessage{ToolCallID: id, ToolName: "write", Content: []ToolResultMessageContent{TextContent{Text: text()}}, IsError: true})
		case 7:
			messages = append(messages, UserMessage{Content: UserText(text())},
				assistant(StopReasonToolUse, ThinkingContent{Thinking: "", Redacted: true, ThinkingSignature: "opaque" + text()}, ToolCall{ID: id, Name: "ls", Arguments: JsonObject{"path": "."}}),
				ToolResultMessage{ToolCallID: id, ToolName: "ls", Content: []ToolResultMessageContent{TextContent{Text: ""}}})
		default:
			messages = append(messages, UserMessage{Content: UserText(text())},
				assistant(StopReasonStop, TextContent{Text: text()}))
		}
	}
	return append(messages, UserMessage{Content: UserText(text())})
}

// A provider sends the body it sends without the message cache, for a session that grows turn by turn (each request holds the messages of the one before), is edited, compacted and branched, and is poisoned in each way a history is; and the cache serves the messages a later request repeats.
func TestProvidersSendTheSameBodyWithTheMessageCache(t *testing.T) {
	saved := requestStrings
	defer func() { requestStrings = saved }()
	tools := []ToolSchema{{Name: "write", Description: "writes <a> file", Parameters: map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}}}}}
	for _, shape := range streamFrameShapes {
		if shape.name != "anthropic-text" && shape.name != "openai-completions-text" {
			continue
		}
		for _, hooked := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/hooked=%v", shape.name, hooked), func(t *testing.T) {
				var body []byte
				response := shape.body(1)
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					body, _ = io.ReadAll(r.Body)
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = io.WriteString(w, response)
				}))
				defer server.Close()
				send := func(messages []Message, retention CacheRetention) []byte {
					provider := shape.provider(server.URL)
					options := StreamOptions{CacheRetention: retention, SessionID: "session"}
					if hooked {
						options.OnPayload = func(payload any, _ *Model) (any, error) { return payload, nil }
					}
					// A history with empty user text is a request Anthropic rejects (non-empty-content); the body is the thing compared, so the request is sent on purpose.
					stream, err := provider.Stream(invalidRequest(t), NormalizeContext(Context{SystemPrompt: "system <&>", Tools: tools, Messages: messages, Frozen: true}), options)
					if err != nil {
						t.Fatal(err)
					}
					for range stream.Events(context.WithoutCancel(t.Context())) {
					}
					_ = provider.Close()
					return bytes.Clone(body)
				}
				random := rand.New(rand.NewPCG(11, 12))
				cached := newQuotedStringCache(1 << 30)
				for trial := range 12 {
					history := messageCacheHistory(random, shape, 40)
					retention := []CacheRetention{"", CacheRetentionNone, CacheRetentionShort, CacheRetentionLong}[trial%4]
					for step := range 8 {
						requestStrings = nil
						plain := send(history, retention)
						requestStrings = cached
						for pass := range 2 {
							if got := send(history, retention); !bytes.Equal(got, plain) {
								at := 0
								for at < len(plain) && at < len(got) && plain[at] == got[at] {
									at++
								}
								t.Fatalf("trial %d step %d pass %d: the body differs from the uncached body at byte %d of %d/%d\n  cached %.120q\n   plain %.120q", trial, step, pass, at, len(got), len(plain), got[max(at-20, 0):], plain[max(at-20, 0):])
							}
						}
						// An edit, a compaction and a branch cut a session at a user message, as a session does: a tool result is never left without its call.
						users := func() (at []int) {
							for i, message := range history {
								if _, ok := message.(UserMessage); ok {
									at = append(at, i)
								}
							}
							return at
						}()
						switch step % 4 {
						case 0: // the session goes on
							history = append(history[:len(history):len(history)], messageCacheHistory(random, shape, 2)...)
						case 1: // a user message is edited in place of its copy
							history = append([]Message(nil), history...)
							history[users[random.IntN(len(users))]] = UserMessage{Content: UserText("edited \u2028 <b>")}
						case 2: // a compaction replaces the head
							history = append(messageCacheHistory(random, shape, 1), history[users[len(users)/2]:]...)
						default: // a branch drops the tail
							history = history[:users[len(users)*3/4]+1]
						}
					}
				}
				if cached.messages.hits == 0 || cached.messages.misses == 0 {
					t.Fatalf("the message cache was not exercised: %d hits, %d misses", cached.messages.hits, cached.messages.misses)
				}
			})
		}
	}
}

// A session that sends the same clean history again encodes none of its messages again, whichever API carries it: what the cache is for.
func TestProvidersEncodeAMessageOfAResentHistoryOnce(t *testing.T) {
	saved := requestStrings
	defer func() { requestStrings = saved }()
	for _, shape := range streamFrameShapes {
		if shape.name != "anthropic-text" && shape.name != "openai-completions-text" {
			continue
		}
		t.Run(shape.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, shape.body(1))
			}))
			defer server.Close()
			var messages []Message
			for turn := range 30 {
				id := fmt.Sprintf("call_%d", turn)
				if shape.api == APIAnthropicMessages {
					id = "toolu_" + id
				}
				messages = append(messages,
					UserMessage{Content: UserText(fmt.Sprintf("do %d", turn))},
					AssistantMessage{API: shape.api, Provider: shape.providerID, Model: shape.model, StopReason: StopReasonToolUse, Content: []AssistantContentBlock{TextContent{Text: "ok"}, ToolCall{ID: id, Name: "read", Arguments: JsonObject{"path": fmt.Sprint("/tmp/", turn)}}}},
					ToolResultMessage{ToolCallID: id, ToolName: "read", Content: []ToolResultMessageContent{TextContent{Text: "contents\n"}}})
			}
			messages = append(messages, UserMessage{Content: UserText("next")})
			requestStrings = newQuotedStringCache(1 << 30)
			send := func() {
				provider := shape.provider(server.URL)
				stream, err := provider.Stream(t.Context(), NormalizeContext(Context{SystemPrompt: "s", Messages: messages, Frozen: true}), StreamOptions{})
				if err != nil {
					t.Fatal(err)
				}
				for range stream.Events(context.WithoutCancel(t.Context())) {
				}
				_ = provider.Close()
			}
			send()
			first := requestStrings.messages.misses
			if first == 0 {
				t.Fatal("the first request cached no message")
			}
			send()
			if requestStrings.messages.misses != first {
				t.Fatalf("a resent history encoded %d messages again", requestStrings.messages.misses-first)
			}
			if requestStrings.messages.hits < first {
				t.Fatalf("a resent history took %d messages from the cache, want %d", requestStrings.messages.hits, first)
			}
		})
	}
}

// A payload hook that edits the messages of the request in place, after the cache has them, is sent as it edited them: the cache serves a message by what it holds, not by where it was.
func TestProvidersSendAMessageAHookEditedInPlace(t *testing.T) {
	saved := requestStrings
	defer func() { requestStrings = saved }()
	edit := map[string]func(any){
		"anthropic-text": func(payload any) {
			blocks := payload.(anthRequest).Messages[1].Content.([]anthContentBlock)
			blocks[0].Text = "edited by a hook"
			blocks[0].CacheControl = &anthCacheControl{Type: "ephemeral"}
		},
		"openai-completions-text": func(payload any) {
			payload.(oaiRequest).Messages[1].Content = "edited by a hook"
		},
	}
	for _, shape := range streamFrameShapes {
		hook, ok := edit[shape.name]
		if !ok {
			continue
		}
		t.Run(shape.name, func(t *testing.T) {
			var body []byte
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ = io.ReadAll(r.Body)
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, shape.body(1))
			}))
			defer server.Close()
			messages := []Message{UserMessage{Content: UserText("first")}, AssistantMessage{API: shape.api, Provider: shape.providerID, Model: shape.model, StopReason: StopReasonStop, Content: []AssistantContentBlock{TextContent{Text: "reply"}}}, UserMessage{Content: UserText("next")}}
			send := func(hooked bool) []byte {
				provider := shape.provider(server.URL)
				options := StreamOptions{}
				if hooked {
					options.OnPayload = func(payload any, _ *Model) (any, error) { hook(payload); return nil, nil }
				}
				stream, err := provider.Stream(t.Context(), NormalizeContext(Context{Messages: messages, Frozen: true}), options)
				if err != nil {
					t.Fatal(err)
				}
				for range stream.Events(context.WithoutCancel(t.Context())) {
				}
				_ = provider.Close()
				return bytes.Clone(body)
			}
			requestStrings = newQuotedStringCache(1 << 30)
			plain := send(false)
			if again := send(false); !bytes.Equal(again, plain) {
				t.Fatalf("a resent request changed:\n%.200q\n%.200q", again, plain)
			}
			if bytes.Contains(plain, []byte("edited by a hook")) {
				t.Fatal("the request holds the hook's edit before the hook ran")
			}
			edited := send(true)
			if !bytes.Contains(edited, []byte("edited by a hook")) {
				t.Fatalf("the cached message was sent instead of the hook's edit: %.300q", edited)
			}
			// The hook's edit does not stay in the cache: the next request without it is the original again.
			if after := send(false); !bytes.Equal(after, plain) {
				t.Fatalf("a request after the hook differs from the one before it:\n%.200q\n%.200q", after, plain)
			}
		})
	}
}
