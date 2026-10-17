package agent

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// Every test of the package runs with the memo engaged from the first message, so the agent-level tests exercise it on short histories.
func init() { llmMemoMinMessages = 1 }

func memoUser(text string) AgentMessage {
	return AgentMessage{User: &UserMessage{Role: RoleUser, Content: ai.UserContentBlocks{ai.TextContent{Text: text}}}}
}

func memoAssistant(text, stop string) AgentMessage {
	return AgentMessage{Assistant: &AssistantMessage{Role: RoleAssistant, StopReason: ai.StopReason(stop), Content: []ai.AssistantContentBlock{ai.TextContent{Text: text}}}}
}

func memoResult(id string) AgentMessage {
	return AgentMessage{ToolResult: &ToolResultMessage{Role: RoleToolResult, ToolCallID: id, ToolName: "read", Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "ok"}}}}
}

// poisonedHistory holds every shape a persisted history can: an empty-content user, an errored and an aborted assistant turn, an empty text block, an orphaned tool result, a system message and the custom roles that send nothing or a user message.
func poisonedHistory() []AgentMessage {
	return []AgentMessage{
		{System: &ai.SystemMessage{Content: ai.SystemText("sys")}},
		{User: &UserMessage{Role: RoleUser}},
		memoAssistant("", "error"),
		memoAssistant("partial", "aborted"),
		memoAssistant("", "stop"),
		memoResult("orphan"),
		{Custom: map[string]any{"role": RoleCompactionSummary, "summary": "s", "tokensBefore": 1, "timestamp": 1}},
		{Custom: map[string]any{"role": RoleBashExecution, "command": "ls", "output": "x", "excludeFromContext": true}},
		{Custom: map[string]any{"role": "unknown"}},
		memoUser("q"),
	}
}

// The memo returns what ConvertToLLM returns, on every prefix of a history and on a history it has seen before.
func TestLLMMemoConvertsLikeConvertToLLM(t *testing.T) {
	history := poisonedHistory()
	var memo llmMemo
	for pass := range 2 {
		for n := range len(history) + 1 {
			if got, want := memo.convert(history[:n]), ConvertToLLM(history[:n]); !reflect.DeepEqual(got, want) {
				t.Fatalf("pass %d, %d messages: got %#v want %#v", pass, n, got, want)
			}
		}
	}
}

// A request that extends the previous one converts only the new messages; a history whose middle message is another value, or a shorter one (compaction, /tree, /fork), converts that part again.
func TestLLMMemoReconvertsWhatIsNotAnIdenticalPrefix(t *testing.T) {
	history := []AgentMessage{memoUser("a"), memoAssistant("b", "stop"), memoUser("c"), memoAssistant("d", "stop")}
	var memo llmMemo
	memo.convert(history)

	replaced := append([]AgentMessage(nil), history...)
	replaced[1] = memoAssistant("B", "stop")
	if got, want := memo.convert(replaced), ConvertToLLM(replaced); !reflect.DeepEqual(got, want) {
		t.Fatalf("replaced middle message: got %#v want %#v", got, want)
	}
	// Each variant is compared: a replaced user, system or tool result message converts again.
	for name, variant := range map[string]AgentMessage{
		"user":        memoUser("C"),
		"system":      {System: &ai.SystemMessage{Content: ai.SystemText("other")}},
		"tool result": memoResult("other"),
	} {
		base := []AgentMessage{memoUser("a"), {System: &ai.SystemMessage{Content: ai.SystemText("sys")}}, memoResult("r"), memoAssistant("d", "stop")}
		var variants llmMemo
		variants.convert(base)
		changed := append([]AgentMessage(nil), base...)
		slot := map[string]int{"user": 0, "system": 1, "tool result": 2}[name]
		changed[slot] = variant
		if got, want := variants.convert(changed), ConvertToLLM(changed); !reflect.DeepEqual(got, want) {
			t.Fatalf("%s replaced: got %#v want %#v", name, got, want)
		}
	}
	if got := memo.convert(history[:2]); !reflect.DeepEqual(got, ConvertToLLM(history[:2])) {
		t.Fatalf("shorter history: got %#v", got)
	}
	if got := memo.convert(nil); len(got) != 0 {
		t.Fatalf("empty history converted to %#v", got)
	}
	extended := append(append([]AgentMessage(nil), history...), memoUser("e"))
	if got, want := memo.convert(extended), ConvertToLLM(extended); !reflect.DeepEqual(got, want) {
		t.Fatalf("extended history: got %#v want %#v", got, want)
	}
	if len(memo.ends) != len(extended) {
		t.Fatalf("memo remembers %d messages, want %d", len(memo.ends), len(extended))
	}
}

// A message that was in a request is not written afterwards, so the memo serves its first conversion: a Custom map known by identity keeps its conversion, and a new map converts again.
func TestLLMMemoKnowsCustomMessagesByIdentity(t *testing.T) {
	custom := map[string]any{"role": RoleCompactionSummary, "summary": "one", "tokensBefore": 1, "timestamp": 1}
	var memo llmMemo
	first := memo.convert([]AgentMessage{{Custom: custom}})
	other := map[string]any{"role": RoleCompactionSummary, "summary": "two", "tokensBefore": 1, "timestamp": 1}
	second := memo.convert([]AgentMessage{{Custom: other}})
	if reflect.DeepEqual(first, second) {
		t.Fatal("a different Custom map was served the conversion of the first")
	}
	if want := ConvertToLLM([]AgentMessage{{Custom: other}}); !reflect.DeepEqual(second, want) {
		t.Fatalf("got %#v want %#v", second, want)
	}
}

// An assistant message backed by a live stream converts from the stream's current state, so the memo never remembers it.
func TestLLMMemoDoesNotRememberAStreamBackedAssistant(t *testing.T) {
	partial := &ai.AssistantMessage{Content: []ai.AssistantContentBlock{ai.TextContent{Text: "he"}}}
	live := shallowAssistantMessage(partial)
	history := []AgentMessage{memoUser("q"), {Assistant: live}, memoUser("after")}
	var memo llmMemo
	memo.convert(history)
	if len(memo.ends) != 1 {
		t.Fatalf("memo remembers %d messages, want only the one before the stream-backed message", len(memo.ends))
	}
	if got, want := memo.convert(history), ConvertToLLM(history); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v want %#v", got, want)
	}
}

// The caller owns the returned slice: a hook that rewrites it (TransformLLMMessages, the image limit, the system prompt projection) does not reach the next request.
func TestLLMMemoResultIsCallerOwned(t *testing.T) {
	history := []AgentMessage{memoUser("a"), memoAssistant("b", "stop")}
	var memo llmMemo
	first := memo.convert(history)
	first[0] = ai.UserMessage{Content: ai.UserText("rewritten")}
	if got, want := memo.convert(history), ConvertToLLM(history); !reflect.DeepEqual(got, want) {
		t.Fatalf("a rewritten result leaked into the memo: got %#v want %#v", got, want)
	}
}

// A request that appends one exchange allocates the same number of objects at 10 and at 1,000 messages of history.
func TestLLMMemoAllocationsDoNotGrowWithTheHistory(t *testing.T) {
	measure := func(n int) float64 {
		history := make([]AgentMessage, 0, n+3)
		// A compaction summary leads a compacted history: a custom message that converts again on every request must not end the remembered prefix.
		history = append(history, AgentMessage{Custom: map[string]any{"role": RoleCompactionSummary, "summary": "s", "tokensBefore": 1, "timestamp": 1}})
		for range n / 2 {
			history = append(history, memoUser("q"), memoAssistant("a", "stop"))
		}
		var memo llmMemo
		memo.convert(history)
		next := append(slices.Clone(history), memoUser("next"))
		memo.convert(next)
		// Steady state: each run extends the remembered history by one message.
		return testing.AllocsPerRun(50, func() {
			next = append(next, memoUser("more"))
			memo.convert(next)
		})
	}
	small, large := measure(10), measure(1000)
	if large > small+1 {
		t.Fatalf("allocations grew with the history: %v at 10 messages, %v at 1000", small, large)
	}
	if large > 12 {
		t.Fatalf("a steady request allocates %v times, budget 12", large)
	}
}

// declareToolChanges with nothing pending returns what the general path returns.
func TestDeclareToolChangesWithNothingPending(t *testing.T) {
	tools, committed := steadyToolFixtureWithProperties(3, 6, 2)
	if got := declareToolChanges(tools, committed, nil); len(got) != 0 {
		t.Fatalf("an unchanged loadout declared %d messages", len(got))
	}
	// A loadout the history never declared adds one system message carrying every tool.
	got := declareToolChanges(tools, nil, nil)
	if len(got) != 1 || got[0].System == nil || len(got[0].System.ToolsAdded) != len(tools) {
		t.Fatalf("a new loadout declared %#v", got)
	}
	if want := declareToolChanges(tools, nil, []AgentMessage{}); len(want) != 1 || want[0].System == nil || len(want[0].System.ToolsAdded) != len(tools) {
		t.Fatalf("empty non-nil pending: %#v", want)
	}
}

// Whatever a PrepareRequest hook does to the loop context between requests (keep it, compact it into a summary and a tail,
// rewrite one message in place of its pointer, restore an earlier context, replace the model), the provider request equals the
// one an agent with the standard converter written out builds: the memo only skips work, it never serves a stale message.
func TestAgentRequestsMatchTheStandardConversionAcrossContextChanges(t *testing.T) {
	run := func(explicit bool) [][]ai.Message {
		var requests [][]ai.Message
		var earlier []AgentMessage
		request := 0
		a := mustNewAgent(AgentOptions{
			Model: scriptedModel(&scriptedProvider{respond: replyText("unused")}),
			StreamFn: func(_ context.Context, _ *ai.Model, transcript ai.TranscriptContext, _ ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
				messages := make([]ai.Message, transcript.Len())
				for i := range messages {
					messages[i] = transcript.At(i)
				}
				requests = append(requests, messages)
				return doneStream(textMessage(fmt.Sprintf("reply %d", len(requests)))), nil
			},
			PrepareRequest: func(_ context.Context, req PrepareRequestContext) (*AgentRequestUpdate, error) {
				request++
				ctx := req.Context.Messages
				switch request {
				case 2:
					earlier = ctx
				case 3: // compaction: a summary and the tail
					summary := AgentMessage{Custom: map[string]any{"role": RoleCompactionSummary, "summary": "short", "tokensBefore": 9, "timestamp": 1}}
					return &AgentRequestUpdate{Context: &AgentContext{Messages: append([]AgentMessage{summary}, ctx[len(ctx)-2:]...), Tools: req.Context.Tools}}, nil
				case 4: // a context edit: one message in the middle becomes another value
					edited := append([]AgentMessage(nil), ctx...)
					edited[1] = memoUser("edited")
					return &AgentRequestUpdate{Context: &AgentContext{Messages: edited, Tools: req.Context.Tools}}, nil
				case 5: // /tree or /fork back to an earlier context
					return &AgentRequestUpdate{Context: &AgentContext{Messages: earlier, Tools: req.Context.Tools}}, nil
				}
				return nil, nil
			},
		})
		if explicit {
			a.SetConvertToLlm(func(msgs []AgentMessage) ([]ai.Message, error) { return ConvertToLLM(msgs), nil })
		}
		for turn := range 7 {
			if _, err := a.SendMessages(t.Context(), []AgentMessage{memoUser(fmt.Sprintf("prompt %d", turn))}); err != nil {
				t.Fatal(err)
			}
		}
		return requests
	}
	got, want := run(false), run(true)
	if len(got) != 7 || len(want) != 7 {
		t.Fatalf("requests: %d with the memo, %d without, want 7", len(got), len(want))
	}
	for i := range got {
		if !reflect.DeepEqual(got[i], want[i]) {
			t.Fatalf("request %d differs:\nmemo: %#v\nwant: %#v", i+1, got[i], want[i])
		}
	}
}

// Pi converts the live message objects on every request, so a hook that writes to an older message in place (a pruning hook) changes what the next request sends. The memo serves a message only while its fields still equal what it converted: each write below, made to a message the memo has remembered, shows in the next conversion.
func TestLLMMemoSeesWritesToRememberedMessages(t *testing.T) {
	toolCall := ai.ToolCall{ID: "c1", Name: "read", Arguments: ai.JsonObject{"path": "a"}}
	fresh := func() []AgentMessage {
		return []AgentMessage{
			{System: &ai.SystemMessage{Content: ai.SystemText("sys")}},
			memoUser("first"),
			{User: &UserMessage{Role: RoleUser, Content: ai.UserContentBlocks{ai.TextContent{Text: "blocks"}, ai.TextContent{Text: "more"}}, Timestamp: 1}},
			{Assistant: &AssistantMessage{Role: RoleAssistant, StopReason: ai.StopReasonToolUse, Content: []ai.AssistantContentBlock{ai.TextContent{Text: "thinking out loud"}, toolCall}, Usage: &ai.Usage{Input: 1}}},
			{ToolResult: &ToolResultMessage{Role: RoleToolResult, ToolCallID: "c1", ToolName: "read", Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "file text"}}, Timestamp: 2}},
			memoAssistant("done", "stop"),
		}
	}
	writes := map[string]func(history []AgentMessage){
		"user content replaced":            func(h []AgentMessage) { h[1].User.Content = ai.UserText("pruned") },
		"user timestamp":                   func(h []AgentMessage) { h[1].User.Timestamp = 99 },
		"user block replaced in place":     func(h []AgentMessage) { h[2].User.Content.(ai.UserContentBlocks)[0] = ai.TextContent{Text: "pruned"} },
		"user blocks truncated":            func(h []AgentMessage) { h[2].User.Content = h[2].User.Content.(ai.UserContentBlocks)[:1] },
		"assistant block replaced":         func(h []AgentMessage) { h[3].Assistant.Content[0] = ai.TextContent{Text: "pruned"} },
		"assistant content truncated":      func(h []AgentMessage) { h[3].Assistant.Content = h[3].Assistant.Content[1:] },
		"assistant stop reason":            func(h []AgentMessage) { h[3].Assistant.StopReason = ai.StopReasonStop },
		"assistant usage replaced":         func(h []AgentMessage) { h[3].Assistant.Usage = &ai.Usage{Input: 2} },
		"assistant model":                  func(h []AgentMessage) { h[5].Assistant.ModelID = "other" },
		"tool result block replaced":       func(h []AgentMessage) { h[4].ToolResult.Content[0] = ai.TextContent{Text: "truncated"} },
		"tool result content emptied":      func(h []AgentMessage) { h[4].ToolResult.Content = nil },
		"tool result details":              func(h []AgentMessage) { h[4].ToolResult.Details = map[string]any{"n": 1} },
		"tool result error flag":           func(h []AgentMessage) { h[4].ToolResult.IsError = true },
		"system content":                   func(h []AgentMessage) { h[0].System.Content = ai.SystemText("other") },
		"system tools added":               func(h []AgentMessage) { h[0].System.ToolsAdded = []ai.ToolSchema{{Name: "t"}} },
		"message replaced by another kind": func(h []AgentMessage) { h[1] = memoAssistant("now an assistant", "stop") },
	}
	for name, write := range writes {
		t.Run(name, func(t *testing.T) {
			history := fresh()
			var memo llmMemo
			memo.convert(history)
			write(history)
			if got, want := memo.convert(history), ConvertToLLM(history); !reflect.DeepEqual(got, want) {
				t.Fatalf("the memo served a stale message:\n got %#v\nwant %#v", got, want)
			}
			// And again: the changed conversion is the remembered one now.
			if got, want := memo.convert(history), ConvertToLLM(history); !reflect.DeepEqual(got, want) {
				t.Fatalf("the second request after the write differs:\n got %#v\nwant %#v", got, want)
			}
		})
	}
}

// A custom message is a map the memo cannot compare, so it converts on every request: a write to its map shows, and the messages after it are still served from the memo.
func TestLLMMemoConvertsCustomMessagesAgain(t *testing.T) {
	summary := map[string]any{"role": RoleCompactionSummary, "summary": "one", "tokensBefore": 1, "timestamp": 1}
	history := []AgentMessage{{Custom: summary}, memoUser("a"), memoAssistant("b", "stop")}
	var memo llmMemo
	memo.convert(history)
	summary["summary"] = "two"
	if got, want := memo.convert(history), ConvertToLLM(history); !reflect.DeepEqual(got, want) {
		t.Fatalf("a rewritten custom message: got %#v want %#v", got, want)
	}
	// A custom message that stops sending anything changes the length of the conversion and ends the remembered prefix.
	excluded := map[string]any{"role": RoleBashExecution, "command": "ls", "output": "x", "excludeFromContext": false}
	history = []AgentMessage{{Custom: excluded}, memoUser("a")}
	memo.convert(history)
	excluded["excludeFromContext"] = true
	if got, want := memo.convert(history), ConvertToLLM(history); !reflect.DeepEqual(got, want) {
		t.Fatalf("a custom message that became excluded: got %#v want %#v", got, want)
	}
}

// The reproducer of the review of the first memo: a TransformContext or PrepareRequest hook that rewrites the first message in place on the third request.
func TestAgentHooksThatRewriteAMessageInPlaceReachTheRequest(t *testing.T) {
	for _, hook := range []string{"transform", "prepare"} {
		run := func(explicit bool) [][]ai.Message {
			var requests [][]ai.Message
			request := 0
			mutate := func(messages []AgentMessage) {
				request++
				if request == 3 && messages[0].User != nil {
					messages[0].User.Content = ai.UserText("pruned")
				}
			}
			options := AgentOptions{
				Model: scriptedModel(&scriptedProvider{respond: replyText("unused")}),
				StreamFn: func(_ context.Context, _ *ai.Model, transcript ai.TranscriptContext, _ ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
					messages := make([]ai.Message, transcript.Len())
					for i := range messages {
						messages[i] = transcript.At(i)
					}
					requests = append(requests, messages)
					return doneStream(textMessage(fmt.Sprintf("reply %d", len(requests)))), nil
				},
			}
			if hook == "transform" {
				options.TransformContext = func(_ context.Context, messages []AgentMessage) ([]AgentMessage, error) {
					mutate(messages)
					return messages, nil
				}
			} else {
				options.PrepareRequest = func(_ context.Context, req PrepareRequestContext) (*AgentRequestUpdate, error) {
					mutate(req.Context.Messages)
					return nil, nil
				}
			}
			a := mustNewAgent(options)
			if explicit {
				a.SetConvertToLlm(func(messages []AgentMessage) ([]ai.Message, error) { return ConvertToLLM(messages), nil })
			}
			for turn := range 4 {
				if _, err := a.SendMessages(t.Context(), []AgentMessage{memoUser(fmt.Sprintf("prompt %d", turn))}); err != nil {
					t.Fatal(err)
				}
			}
			return requests
		}
		got, want := run(false), run(true)
		for i := range want {
			if !reflect.DeepEqual(got[i], want[i]) {
				t.Errorf("%s hook: request %d differs:\nmemo: %#v\nwant: %#v", hook, i+1, got[i][0], want[i][0])
			}
		}
	}
}

// A context under the minimum is converted as ConvertToLLM does and nothing is kept; one at the minimum is remembered, and shrinking back under it forgets.
func TestLLMMemoKeepsNothingForAShortContext(t *testing.T) {
	memo := llmMemo{minMessages: defaultLLMMemoMinMessages}
	history := make([]AgentMessage, 0, defaultLLMMemoMinMessages)
	for len(history) < defaultLLMMemoMinMessages-1 {
		history = append(history, memoUser("q"))
	}
	if got, want := memo.convert(history), ConvertToLLM(history); !reflect.DeepEqual(got, want) || len(memo.out) != 0 || len(memo.ends) != 0 {
		t.Fatalf("a context of %d messages: memo holds %d conversions", len(history), len(memo.out))
	}
	history = append(history, memoAssistant("a", "stop"))
	if got, want := memo.convert(history), ConvertToLLM(history); !reflect.DeepEqual(got, want) || len(memo.out) != len(history) {
		t.Fatalf("a context at the minimum: memo holds %d of %d conversions", len(memo.out), len(history))
	}
	if got, want := memo.convert(history[:3]), ConvertToLLM(history[:3]); !reflect.DeepEqual(got, want) || len(memo.out) != 0 || cap(memo.out) != 0 {
		t.Fatalf("a context shrunk under the minimum still holds %d conversions", len(memo.out))
	}
}
