// Ports packages/durable/test/harness-compaction.test.ts.

package harness

import (
	"context"
	"errors"
	"math"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/storage"
)

// scriptRequest is one recorded model request.
type scriptRequest struct {
	messages []ai.Message
	options  ai.StreamOptions
	model    string
}

// scriptStep answers one request.
type scriptStep func(request scriptRequest) (ai.FauxResponse, error)

// script is a scripted faux model that answers agent requests and summarization requests from separate queues.
type script struct {
	mu              sync.Mutex
	agent           []scriptStep
	summaries       []scriptStep
	agentRequests   []scriptRequest
	summaryRequests []scriptRequest
}

func isSummaryRequest(messages []ai.Message) bool {
	if len(messages) == 0 {
		return false
	}
	system, ok := messages[0].(ai.SystemMessage)
	if !ok {
		return false
	}
	content, ok := system.Content.(ai.SystemText)
	return ok && strings.HasPrefix(string(content), "You are a context summarization assistant")
}

func newScript(setup *chatState) *script {
	result := &script{}
	steps := make([]ai.FauxResponseStep, 500)
	for index := range steps {
		steps[index] = ai.FauxFactoryStep(func(transcript ai.TranscriptContext, options ai.StreamOptions, _ *ai.FauxProviderState, model *ai.Model) (ai.FauxResponse, error) {
			request := scriptRequest{messages: slices.Clone(transcript.Messages()), options: options, model: model.ID}
			summary := isSummaryRequest(request.messages)
			result.mu.Lock()
			queue := &result.agent
			if summary {
				result.summaryRequests = append(result.summaryRequests, request)
				queue = &result.summaries
			} else {
				result.agentRequests = append(result.agentRequests, request)
			}
			var step scriptStep
			if len(*queue) > 0 {
				step = (*queue)[0]
				*queue = (*queue)[1:]
			}
			result.mu.Unlock()
			if step == nil {
				kind := "agent"
				if summary {
					kind = "summary"
				}
				return ai.FauxResponse{}, errors.New("No scripted " + kind + " response")
			}
			return step(request)
		})
	}
	setup.Faux.SetResponses(steps)
	return result
}

func (faux *script) pushAgent(steps ...scriptStep) {
	faux.mu.Lock()
	faux.agent = append(faux.agent, steps...)
	faux.mu.Unlock()
}

func (faux *script) pushSummary(steps ...scriptStep) {
	faux.mu.Lock()
	faux.summaries = append(faux.summaries, steps...)
	faux.mu.Unlock()
}

func (faux *script) lastAgentRequest() scriptRequest {
	faux.mu.Lock()
	defer faux.mu.Unlock()
	return faux.agentRequests[len(faux.agentRequests)-1]
}

func (faux *script) summaryRequestsCopy() []scriptRequest {
	faux.mu.Lock()
	defer faux.mu.Unlock()
	return slices.Clone(faux.summaryRequests)
}

func (faux *script) agentRequestsCopy() []scriptRequest {
	faux.mu.Lock()
	defer faux.mu.Unlock()
	return slices.Clone(faux.agentRequests)
}

func fixed(response ai.FauxResponse) scriptStep {
	return func(scriptRequest) (ai.FauxResponse, error) { return response, nil }
}

func scriptAnswer(content string) scriptStep {
	return fixed(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText(content)}})
}

func scriptFailure(errorMessage string) scriptStep {
	return fixed(ai.FauxResponse{StopReason: "error", ErrorMessage: errorMessage})
}

func scriptSummary(content ...string) scriptStep {
	if len(content) > 0 {
		return scriptAnswer(content[0])
	}
	return scriptAnswer("SUMMARY")
}

// scriptGated is a step that waits for gate, or fails when the request is cancelled.
func scriptGated(gate *deferredGate, step scriptStep, reached *deferredGate) scriptStep {
	return func(request scriptRequest) (ai.FauxResponse, error) {
		if reached != nil {
			reached.resolve()
		}
		if err := gate.wait(request.options.Signal); err != nil {
			return ai.FauxResponse{}, err
		}
		return step(request)
	}
}

const overflowText = "prompt is too long: 250000 tokens > 200000 maximum"

// manualPolicy has small thresholds: no automatic compaction unless a test enables it.
func manualPolicy() *CompactionPolicyPatch {
	return &CompactionPolicyPatch{Enabled: new(false), ReserveTokens: new(1000), KeepRecentTokens: new(150), BackgroundTokens: new(0)}
}

type compactionChat struct {
	harness Harness
	root    Conversation
	setup   *chatState
	faux    *script
}

type compactionOptions struct {
	policy        *CompactionPolicyPatch
	contextWindow int
	storage       durable.Storage
	setup         *chatState
	faux          *script
}

func openCompaction(t *testing.T, options ...compactionOptions) *compactionChat {
	t.Helper()
	var option compactionOptions
	if len(options) > 0 {
		option = options[0]
	}
	if option.contextWindow == 0 {
		option.contextWindow = 100_000
	}
	setup := option.setup
	if setup == nil {
		setup = chatSetup(t, ai.FauxConfig{Models: []ai.FauxModelDefinition{{ID: "faux-1", ContextWindow: option.contextWindow, MaxTokens: 900}}})
	}
	faux := option.faux
	if faux == nil {
		faux = newScript(setup)
	}
	addSection(t, setup.Registry, "preamble", text("You are helpful."), SectionOptions{Tag: new(false)})
	store := option.storage
	if store == nil {
		store = storage.NewMemoryStorage()
	}
	harness, root := openChat(t, store, setup)
	policy := option.policy
	if policy == nil {
		policy = manualPolicy()
	}
	setup.SetSettings(func(settings *HarnessSettings) {
		settings.Compaction = policy
		settings.Retry = &RetryPolicyPatch{Enabled: new(true), MaxRetries: new(2), BaseDelayMs: new(1)}
	})
	harness.Resume()
	return &compactionChat{harness: harness, root: root, setup: setup, faux: faux}
}

// turn runs one turn: user answered by reply.
func (chat *compactionChat) turn(t *testing.T, user, reply string) {
	t.Helper()
	chat.faux.pushAgent(scriptAnswer(reply))
	if settled := must(submitInput(t, chat.root, user).Wait(testContext)); settled.Status != durable.SubmissionDone {
		t.Fatalf("turn %q = %s", user, jsonText(t, settled))
	}
}

// history runs three turns of about 100-token messages.
func (chat *compactionChat) history(t *testing.T) {
	t.Helper()
	chat.turn(t, sized("u1", 100), sized("a1", 100))
	chat.turn(t, sized("u2", 100), sized("a2", 100))
	chat.turn(t, sized("u3", 100), sized("a3", 100))
}

func (chat *compactionChat) compact(t *testing.T, instructions *string) durable.TaskId {
	t.Helper()
	return must(chat.root.Compact(testContext, instructions))
}

func (chat *compactionChat) outcome(t *testing.T, id durable.TaskId) durable.TaskOutcome[CompactionResult] {
	t.Helper()
	record := must(chat.harness.WaitForTask(testContext, id))
	return must(durable.FromJsonValue[durable.TaskOutcome[CompactionResult]](must(durable.ToJsonValue(record.State.Outcome))))
}

// summarySubmission returns the submission of a completed conversation-owned compaction's summary.
func (chat *compactionChat) summarySubmission(t *testing.T, outcome durable.TaskOutcome[CompactionResult]) durable.Submission {
	t.Helper()
	if outcome.Status != durable.OutcomeCompleted || outcome.Result == nil || outcome.Result.SubmissionId == nil {
		t.Fatalf("outcome = %s, want a completed summary submission", jsonText(t, outcome))
	}
	return submissionOfHarness(t, chat.harness, *outcome.Result.SubmissionId)
}

func (chat *compactionChat) kinds(t *testing.T) []string {
	t.Helper()
	return entryKinds(allEntries(t, chat.root))
}

func (chat *compactionChat) live(t *testing.T) *LiveState {
	t.Helper()
	return liveOf(t, chat.harness, chat.root.Id())
}

func (chat *compactionChat) close(t *testing.T) { closeHarness(t, chat.harness) }

func userText(message ai.Message) string {
	if message == nil {
		return ""
	}
	text, _ := textOf(message)
	return text
}

func lastN[T any](values []T, n int) []T { return values[max(0, len(values)-n):] }

var _ = context.Background

// sized is text of about tokens estimated tokens, starting with label (text() in the upstream test).
func sized(label string, tokens int) string {
	return label + " " + strings.Repeat("x", max(0, tokens*4-len(label)-1))
}

// rangeEntries builds entry records with consecutive IDs, as entry() in the upstream test.
type rangeEntries struct{ next durable.EntryId }

func (ids *rangeEntries) entry(kind string, model []ai.Message, head ...durable.EntryId) durable.EntryRecord {
	ids.next++
	record := durable.EntryRecord{Id: ids.next, ConversationId: 1, Kind: kind, Model: model}
	if len(head) > 0 {
		marker := head[0]
		record.Head = &marker
	}
	return record
}

func rangeUser(content string) ai.Message {
	return ai.UserMessage{Content: ai.UserText(content), Timestamp: 0}
}

func rangeAssistant(content string, calls []string, stopReason ai.StopReason) ai.Message {
	blocks := []ai.AssistantContentBlock{ai.TextContent{Text: content}}
	for _, id := range calls {
		blocks = append(blocks, ai.ToolCall{ID: id, Name: "read", Arguments: ai.JsonObject{}})
	}
	if stopReason == "" {
		stopReason = ai.StopReasonStop
	}
	return ai.AssistantMessage{Content: blocks, API: "faux", Provider: "faux", Model: "faux-1", StopReason: stopReason}
}

func rangeToolResult(callId, content string) ai.Message {
	return ai.ToolResultMessage{ToolCallID: callId, ToolName: "read", Content: []ai.ToolResultMessageContent{ai.TextContent{Text: content}}}
}

// rangeView is a view over entries whose contributions are their models, with excluded assistants removed.
func rangeView(entries []durable.EntryRecord, head *durable.EntryRecord) durable.ContextView {
	all := entries
	if head != nil {
		all = append([]durable.EntryRecord{*head}, entries...)
	}
	contributions := make([][]ai.Message, len(all))
	var flat []ai.Message
	for index, record := range all {
		kept := []ai.Message{}
		for _, message := range record.Model {
			if assistant, ok := message.(ai.AssistantMessage); ok && slices.Contains([]ai.StopReason{ai.StopReasonError, ai.StopReasonAborted, ai.StopReasonDeferred}, assistant.StopReason) {
				continue
			}
			kept = append(kept, message)
		}
		contributions[index] = kept
		flat = append(flat, kept...)
	}
	return durable.ContextView{Head: head, Entries: all, Contributions: contributions, Messages: OrderToolResults(flat)}
}

func expectCut(t *testing.T, view durable.ContextView, keepRecentTokens int, want int) {
	t.Helper()
	cut, ok := SelectCut(view, keepRecentTokens)
	if want < 0 {
		if ok {
			t.Fatalf("cut = %d, want undefined", cut)
		}
		return
	}
	if !ok || cut != want {
		t.Fatalf("cut = %d (%v), want %d", cut, ok, want)
	}
}

func TestRangeSelection(t *testing.T) {
	t.Run("keeps about keepRecentTokens and cuts at the first candidate at or after the budget (spec §8.7 example)", func(t *testing.T) {
		ids := &rangeEntries{}
		entries := []durable.EntryRecord{
			ids.entry("pi.user", []ai.Message{rangeUser(sized("1", 10))}),
			ids.entry("pi.assistant", []ai.Message{rangeAssistant("2", []string{"c1"}, "")}),
			ids.entry("pi.tool-result", []ai.Message{rangeToolResult("c1", sized("3", 3000))}),
			ids.entry("pi.assistant", []ai.Message{rangeAssistant(sized("4", 10), nil, "")}),
			ids.entry("pi.user", []ai.Message{rangeUser(sized("5", 10))}),
			ids.entry("pi.assistant", []ai.Message{rangeAssistant(sized("6", 10), nil, "")}),
		}
		expectCut(t, rangeView(entries, nil), 2000, 3)
	})

	t.Run("cuts at a user entry", func(t *testing.T) {
		ids := &rangeEntries{}
		entries := []durable.EntryRecord{
			ids.entry("pi.user", []ai.Message{rangeUser(sized("u1", 100))}),
			ids.entry("pi.assistant", []ai.Message{rangeAssistant(sized("a1", 100), nil, "")}),
			ids.entry("pi.user", []ai.Message{rangeUser(sized("u2", 100))}),
			ids.entry("pi.assistant", []ai.Message{rangeAssistant(sized("a2", 100), nil, "")}),
		}
		expectCut(t, rangeView(entries, nil), 150, 2)
	})

	t.Run("cuts at an assistant in the middle of one long run and never at a tool result", func(t *testing.T) {
		ids := &rangeEntries{}
		entries := []durable.EntryRecord{ids.entry("pi.user", []ai.Message{rangeUser("do it")})}
		for index := range 5 {
			call := "c" + itoa(index)
			entries = append(entries, ids.entry("pi.assistant", []ai.Message{rangeAssistant("step "+itoa(index), []string{call}, "")}))
			entries = append(entries, ids.entry("pi.tool-result", []ai.Message{rangeToolResult(call, sized("r"+itoa(index), 100))}))
		}
		// The budget is reached at the fourth result; the cut is the last call, whose result it keeps.
		cut, _ := SelectCut(rangeView(entries, nil), 150)
		if entries[cut].Kind != "pi.assistant" || cut != 9 {
			t.Fatalf("cut = %d (%s), want 9", cut, entries[cut].Kind)
		}
	})

	t.Run("keeps a huge last tool result together with its assistant", func(t *testing.T) {
		ids := &rangeEntries{}
		entries := []durable.EntryRecord{
			ids.entry("pi.user", []ai.Message{rangeUser("u")}),
			ids.entry("pi.assistant", []ai.Message{rangeAssistant("a", []string{"c"}, "")}),
			ids.entry("pi.tool-result", []ai.Message{rangeToolResult("c", sized("big", 5000))}),
		}
		expectCut(t, rangeView(entries, nil), 100, 1)
	})

	t.Run("never cuts at a system entry or an excluded error or aborted answer", func(t *testing.T) {
		ids := &rangeEntries{}
		entries := []durable.EntryRecord{
			ids.entry("pi.user", []ai.Message{rangeUser(sized("u1", 100))}),
			ids.entry("pi.assistant", []ai.Message{rangeAssistant(sized("a1", 100), nil, "")}),
			ids.entry("pi.system", []ai.Message{ai.SystemMessage{Content: ai.SystemText(""), Sections: ai.OrderedSections{{Name: "s", Value: new(sized("s", 100))}}}}),
			ids.entry("pi.assistant", []ai.Message{rangeAssistant(sized("err", 100), nil, ai.StopReasonError)}),
			ids.entry("pi.assistant", []ai.Message{rangeAssistant(sized("stopped", 100), nil, ai.StopReasonAborted)}),
			ids.entry("pi.assistant", []ai.Message{rangeAssistant(sized("a2", 100), nil, "")}),
		}
		// The walk reaches 150 at the system entry; the excluded answers after it contribute nothing.
		expectCut(t, rangeView(entries, nil), 150, 5)
	})

	t.Run("follows edited contributions: an omitted entry adds nothing and is no candidate", func(t *testing.T) {
		ids := &rangeEntries{}
		entries := []durable.EntryRecord{
			ids.entry("pi.user", []ai.Message{rangeUser(sized("u1", 100))}),
			ids.entry("pi.assistant", []ai.Message{rangeAssistant(sized("a1", 100), nil, "")}),
			ids.entry("pi.user", []ai.Message{rangeUser(sized("u2", 100))}),
			ids.entry("pi.assistant", []ai.Message{rangeAssistant(sized("a2", 100), nil, "")}),
		}
		plain := rangeView(entries, nil)
		expectCut(t, plain, 150, 2)
		omitted := plain
		omitted.Contributions = slices.Clone(plain.Contributions)
		omitted.Contributions[2] = []ai.Message{}
		var flat []ai.Message
		for _, messages := range omitted.Contributions {
			flat = append(flat, messages...)
		}
		omitted.Messages = OrderToolResults(flat)
		expectCut(t, omitted, 150, 1)
	})

	t.Run("does not cut at a user entry that a result of the preceding call still follows", func(t *testing.T) {
		ids := &rangeEntries{}
		entries := []durable.EntryRecord{
			ids.entry("pi.user", []ai.Message{rangeUser(sized("u1", 100))}),
			ids.entry("pi.assistant", []ai.Message{rangeAssistant("a", []string{"c"}, "")}),
			ids.entry("pi.user", []ai.Message{rangeUser(sized("steer", 100))}),
			ids.entry("pi.tool-result", []ai.Message{rangeToolResult("c", sized("r", 100))}),
			ids.entry("pi.assistant", []ai.Message{rangeAssistant(sized("a2", 100), nil, "")}),
		}
		// The budget is reached at the steer; its result follows it, so the cut moves to the next assistant.
		expectCut(t, rangeView(entries, nil), 250, 4)
	})

	t.Run("finds nothing when the budget is never reached or only the marker precedes the cut", func(t *testing.T) {
		ids := &rangeEntries{}
		small := []durable.EntryRecord{ids.entry("pi.user", []ai.Message{rangeUser("hi")}), ids.entry("pi.assistant", []ai.Message{rangeAssistant("hello", nil, "")})}
		expectCut(t, rangeView(small, nil), 150, -1)
		marker := ids.entry("pi.compaction", []ai.Message{rangeUser("summary")}, 0)
		// The budget is reached at the only entry after the marker, so the marker alone would be summarized.
		expectCut(t, rangeView([]durable.EntryRecord{ids.entry("pi.user", []ai.Message{rangeUser(sized("u", 200))})}, &marker), 150, -1)
	})

	t.Run("summarizes an earlier summary marker first", func(t *testing.T) {
		ids := &rangeEntries{}
		marker := ids.entry("pi.compaction", []ai.Message{rangeUser("EARLIER")}, 0)
		kept := []durable.EntryRecord{
			ids.entry("pi.user", []ai.Message{rangeUser(sized("u1", 100))}),
			ids.entry("pi.assistant", []ai.Message{rangeAssistant(sized("a1", 100), nil, "")}),
			ids.entry("pi.user", []ai.Message{rangeUser(sized("u2", 100))}),
			ids.entry("pi.assistant", []ai.Message{rangeAssistant(sized("a2", 100), nil, "")}),
		}
		selected := rangeView(kept, &marker)
		expectCut(t, selected, 150, 3)
		var flat []ai.Message
		for _, messages := range selected.Contributions[:3] {
			flat = append(flat, messages...)
		}
		if serialized := SerializeConversation(flat); !regexp.MustCompile(`^\[User\]: EARLIER`).MatchString(serialized) {
			t.Fatalf("serialized = %q", serialized)
		}
	})
}

func TestSerialization(t *testing.T) {
	t.Run("writes a transcript, truncates tool results, and omits system messages", func(t *testing.T) {
		call := ai.ToolCall{ID: "c", Name: "read", Arguments: ai.JsonObject{"path": "a.ts"}}
		messages := []ai.Message{
			ai.SystemMessage{Content: ai.SystemText(""), Sections: ai.OrderedSections{{Name: "s", Value: new("hidden")}}},
			rangeUser("hello"),
			ai.AssistantMessage{Content: []ai.AssistantContentBlock{ai.ThinkingContent{Thinking: "hmm"}, ai.TextContent{Text: "sure"}, call}, API: "faux", Provider: "faux", Model: "faux-1", StopReason: ai.StopReasonToolUse},
			rangeToolResult("c", strings.Repeat("y", 2500)),
		}
		serialized := SerializeConversation(messages)
		if strings.Contains(serialized, "hidden") {
			t.Fatalf("serialized %q contains the system message", serialized)
		}
		for _, want := range []string{"[User]: hello", "[Assistant thinking]: hmm", "[Assistant]: sure", `[Assistant tool calls]: read(path="a.ts")`, "[Tool result]: " + strings.Repeat("y", 2000) + "\n\n[... 500 more characters truncated]"} {
			if !strings.Contains(serialized, want) {
				t.Fatalf("serialized %q lacks %q", serialized, want)
			}
		}
	})
}

func TestManualCompaction(t *testing.T) {
	t.Run("places the summary at once when idle and keeps raw history", func(t *testing.T) {
		chat := openCompaction(t)
		chat.history(t)
		before := allEntries(t, chat.root)
		usageBefore := must(durable.Snapshot(testContext, chat.harness, UsageDoc, chat.root.Id())).Models["faux/faux-1"]
		chat.faux.pushSummary(scriptSummary())

		outcome := chat.outcome(t, chat.compact(t, new("focus on files")))
		placed := must(chat.summarySubmission(t, outcome).Wait(testContext))
		if placed.Status != durable.SubmissionDone {
			t.Fatalf("placed = %s", jsonText(t, placed))
		}

		// Raw history is unchanged; one summary entry heads the first kept entry, u3.
		after := allEntries(t, chat.root)
		expectEqualJSON(t, after[:len(before)], jsonText(t, before))
		marker := after[len(after)-1]
		var u3 durable.EntryRecord
		for _, record := range before {
			if len(record.Model) > 0 && strings.HasPrefix(userText(record.Model[0]), "u3") {
				u3 = record
			}
		}
		expectMatch(t, marker, jsonText(t, map[string]any{"kind": "pi.compaction", "head": u3.Id, "data": map[string]any{"reason": "manual"}}))
		if placed.Entry == nil || *placed.Entry != marker.Id {
			t.Fatalf("placed entry = %v, want %d", placed.Entry, marker.Id)
		}
		if got := userText(marker.Model[0]); got != "The conversation history before this point was compacted into the following summary:\n\n<summary>\nSUMMARY\n</summary>" {
			t.Fatalf("marker text = %q", got)
		}

		// The model context is the summary followed by the kept entries.
		view := must(chat.root.Context(testContext))
		texts := []string{}
		for _, message := range view.Messages {
			texts = append(texts, userText(message))
		}
		expectEqualJSON(t, texts, jsonText(t, []string{userText(marker.Model[0]), sized("u3", 100), sized("a3", 100)}))

		// The summarizer saw the serialized prefix, the prompt, and the instructions, without tools or caching.
		request := chat.faux.summaryRequestsCopy()[0]
		if len(request.messages) != 2 {
			t.Fatalf("summary request has %d messages", len(request.messages))
		}
		prompt := userText(request.messages[1])
		if !regexp.MustCompile(`^<conversation>\n\[User\]: u1 `).MatchString(prompt) || !strings.Contains(prompt, "[Assistant]: a2 ") || strings.Contains(prompt, "u3 ") || !strings.Contains(prompt, "## Goal") || !strings.HasSuffix(prompt, "\n\nAdditional focus: focus on files") {
			t.Fatalf("prompt = %q", prompt)
		}
		if request.options.CacheRetention != ai.CacheRetentionNone || request.options.MaxTokens != 800 || request.options.Deferred != nil {
			t.Fatalf("options = %+v", request.options)
		}

		// The summarizer's spend is in the ledger, and nothing counts it again later.
		usage := must(durable.Snapshot(testContext, chat.harness, UsageDoc, chat.root.Id())).Models["faux/faux-1"]
		if usage.Input <= usageBefore.Input {
			t.Fatalf("input = %d, want more than %d", usage.Input, usageBefore.Input)
		}
		chat.turn(t, "next", "done")
		agent := chat.faux.lastAgentRequest().messages
		// The next request: the summary, the kept turn, the new input, then one complete system baseline.
		expectEqualJSON(t, roles(agent), `["user","user","assistant","user","system"]`)
		expectMatch(t, agent[4], `{"sections":{"preamble":"You are helpful."}}`)
		chat.close(t)
	})

	t.Run("keeps working while busy and places the summary at the next final boundary", func(t *testing.T) {
		chat := openCompaction(t)
		chat.history(t)
		gate, reached := deferred(), deferred()
		chat.faux.pushAgent(scriptGated(gate, scriptAnswer("late answer"), reached))
		input := submitInput(t, chat.root, "busy")
		if err := reached.wait(testContext); err != nil {
			t.Fatal(err)
		}
		chat.faux.pushSummary(scriptSummary())
		submission := chat.summarySubmission(t, chat.outcome(t, chat.compact(t, nil)))
		if record := must(submission.Status(testContext)); record.Status != durable.SubmissionQueued {
			t.Fatalf("summary = %s, want queued", record.Status)
		}
		gate.resolve()
		expectSettled(t, must(input.Wait(testContext)), durable.SubmissionDone, "")
		expectSettled(t, must(submission.Wait(testContext)), durable.SubmissionDone, "")
		// The summary follows the answer; the kept range still includes the busy turn.
		expectEqualJSON(t, lastN(chat.kinds(t), 3), `["pi.user","pi.assistant","pi.compaction"]`)
		chat.close(t)
	})

	t.Run("places a queued summary at postTools and the run continues in the compacted context", func(t *testing.T) {
		chat := openCompaction(t)
		addTool(t, chat.setup.Registry, new(durable.ToolRegistration{ToolSchema: ai.ToolSchema{Name: "wait", Description: "wait", Parameters: map[string]any{"type": "object", "properties": map[string]any{}}}, Execute: func(context.Context, any, durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			return durable.ToolExecutionResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "waited"}}}, nil
		}}))
		if err := chat.root.Configure(testContext, AgentChange{Tools: SetTo(ToolChange{Exact: true, List: toolsNamed(t, chat.setup, "wait")})}); err != nil {
			t.Fatal(err)
		}
		chat.history(t)
		gate, reached := deferred(), deferred()
		chat.faux.pushAgent(scriptGated(gate, fixed(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxToolCall("wait", map[string]any{}, "")}, StopReason: "toolUse"}), reached), scriptAnswer("after tools"))
		input := submitInput(t, chat.root, "use a tool")
		if err := reached.wait(testContext); err != nil {
			t.Fatal(err)
		}
		chat.faux.pushSummary(scriptSummary())
		chat.outcome(t, chat.compact(t, nil))
		gate.resolve()
		expectSettled(t, must(input.Wait(testContext)), durable.SubmissionDone, "")
		// The continuation request starts with the summary.
		if first := userText(chat.faux.lastAgentRequest().messages[0]); !strings.Contains(first, "<summary>\nSUMMARY\n</summary>") {
			t.Fatalf("continuation starts with %q", first)
		}
		expectEqualJSON(t, lastN(chat.kinds(t), 4), `["pi.tool-result","pi.compaction","pi.system","pi.assistant"]`)
		chat.close(t)
	})

	t.Run("runs follow-ups left by a failed run after placing the summary", func(t *testing.T) {
		chat := openCompaction(t)
		chat.history(t)
		gate, reached := deferred(), deferred()
		chat.faux.pushAgent(scriptGated(gate, scriptFailure("bad request"), reached))
		failed := submitInput(t, chat.root, "fails")
		if err := reached.wait(testContext); err != nil {
			t.Fatal(err)
		}
		followUp := submitInput(t, chat.root, "follow-up")
		gate.resolve()
		expectSettled(t, must(failed.Wait(testContext)), durable.SubmissionUnanswered, "")
		if record := must(followUp.Status(testContext)); record.Status != durable.SubmissionQueued {
			t.Fatalf("follow-up = %s, want queued", record.Status)
		}

		chat.faux.pushSummary(scriptSummary())
		chat.faux.pushAgent(scriptAnswer("followed"))
		chat.outcome(t, chat.compact(t, nil))
		expectSettled(t, must(followUp.Wait(testContext)), durable.SubmissionDone, "")
		request := chat.faux.lastAgentRequest().messages
		if !strings.Contains(userText(request[0]), "SUMMARY") || !slices.ContainsFunc(request, func(message ai.Message) bool { return userText(message) == "follow-up" }) {
			t.Fatalf("request = %v", roles(request))
		}
		chat.close(t)
	})

	t.Run("settles stale when a reset lands while it summarizes", func(t *testing.T) {
		chat := openCompaction(t)
		chat.history(t)
		gate, reached := deferred(), deferred()
		chat.faux.pushSummary(scriptGated(gate, scriptSummary(), reached))
		id := chat.compact(t, nil)
		if err := reached.wait(testContext); err != nil {
			t.Fatal(err)
		}
		resetTo(t, chat.root, nil)
		gate.resolve()
		expectSettled(t, must(chat.summarySubmission(t, chat.outcome(t, id)).Status(testContext)), durable.SubmissionUnanswered, "stale")
		if kinds := chat.kinds(t); kinds[len(kinds)-1] != "pi.reset" {
			t.Fatalf("kinds = %v", kinds)
		}
		chat.close(t)
	})
}

func (chat *compactionChat) usageInput(t *testing.T) int {
	t.Helper()
	return must(durable.Snapshot(testContext, chat.harness, UsageDoc, chat.root.Id())).Models["faux/faux-1"].Input
}

func (chat *compactionChat) firstContextText(t *testing.T) string {
	t.Helper()
	return userText(must(chat.root.Context(testContext)).Messages[0])
}

func (chat *compactionChat) setKeep(keep int) {
	chat.setup.SetSettings(func(settings *HarnessSettings) {
		policy := manualPolicy()
		policy.KeepRecentTokens = new(keep)
		settings.Compaction = policy
	})
}

func awaitGate(t *testing.T, gate *deferredGate) {
	t.Helper()
	if err := gate.wait(testContext); err != nil {
		t.Fatal(err)
	}
}

func TestManualCompactionConcurrency(t *testing.T) {
	t.Run("does not make the conversation busy: a submission during summarization starts its run at once", func(t *testing.T) {
		chat := openCompaction(t)
		chat.history(t)
		summaryGate, summaryReached := deferred(), deferred()
		chat.faux.pushSummary(scriptGated(summaryGate, scriptSummary(), summaryReached))
		id := chat.compact(t, nil)
		awaitGate(t, summaryReached)
		answerGate, answerReached := deferred(), deferred()
		chat.faux.pushAgent(scriptGated(answerGate, scriptAnswer("a4"), answerReached))
		input := submitInput(t, chat.root, "u4")
		// Placed and answered immediately with the uncompacted context, not queued behind the compaction.
		if record := must(input.Status(testContext)); record.Status != durable.SubmissionPlaced {
			t.Fatalf("input = %s, want placed", record.Status)
		}
		awaitGate(t, answerReached)
		if first := userText(chat.faux.lastAgentRequest().messages[0]); first != sized("u1", 100) {
			t.Fatalf("first message = %q", first)
		}
		// The summary is ready while the run is busy, so it queues and lands after the answer.
		summaryGate.resolve()
		submission := chat.summarySubmission(t, chat.outcome(t, id))
		if record := must(submission.Status(testContext)); record.Status != durable.SubmissionQueued {
			t.Fatalf("summary = %s, want queued", record.Status)
		}
		answerGate.resolve()
		expectSettled(t, must(input.Wait(testContext)), durable.SubmissionDone, "")
		expectSettled(t, must(submission.Wait(testContext)), durable.SubmissionDone, "")
		expectEqualJSON(t, lastN(chat.kinds(t), 3), `["pi.user","pi.assistant","pi.compaction"]`)
		chat.close(t)
	})

	t.Run("counts the spend of a summary that ends stale, and writes no entry for it", func(t *testing.T) {
		chat := openCompaction(t)
		chat.history(t)
		before := chat.usageInput(t)
		gate, reached := deferred(), deferred()
		chat.faux.pushSummary(scriptGated(gate, scriptSummary(), reached))
		id := chat.compact(t, nil)
		awaitGate(t, reached)
		resetTo(t, chat.root, nil)
		gate.resolve()
		expectSettled(t, must(chat.summarySubmission(t, chat.outcome(t, id)).Status(testContext)), durable.SubmissionUnanswered, "stale")
		if after := chat.usageInput(t); after <= before {
			t.Fatalf("input = %d, want more than %d", after, before)
		}
		if slices.Contains(chat.kinds(t), "pi.compaction") {
			t.Fatal("a stale summary wrote an entry")
		}
		chat.close(t)
	})

	t.Run("lets the compaction that cuts furthest win, whatever finishes first", func(t *testing.T) {
		chat := openCompaction(t)
		chat.history(t)
		first, firstReached := deferred(), deferred()
		chat.faux.pushSummary(scriptGated(first, scriptSummary("FIRST"), firstReached))
		early := chat.compact(t, nil)
		awaitGate(t, firstReached)
		chat.turn(t, sized("u4", 100), sized("a4", 100))
		chat.faux.pushSummary(scriptSummary("SECOND"))
		if later := chat.outcome(t, chat.compact(t, nil)); later.Status != durable.OutcomeCompleted {
			t.Fatalf("later = %s", later.Status)
		}
		first.resolve()
		// The early compaction cut at u3, before the later cut at u4.
		expectSettled(t, must(chat.summarySubmission(t, chat.outcome(t, early)).Status(testContext)), durable.SubmissionUnanswered, "stale")
		if text := chat.firstContextText(t); !strings.Contains(text, "SECOND") {
			t.Fatalf("context starts with %q", text)
		}
		chat.close(t)
	})

	t.Run("places an older-selected summary that cuts later than the newer one", func(t *testing.T) {
		chat := openCompaction(t)
		chat.history(t)
		// A: small budget, late cut; selected first, finishes last.
		gate, reached := deferred(), deferred()
		chat.faux.pushSummary(scriptGated(gate, scriptSummary("A"), reached))
		a := chat.compact(t, nil)
		awaitGate(t, reached)
		// B: larger budget, earlier cut; placed first.
		chat.setKeep(350)
		chat.faux.pushSummary(scriptSummary("B"))
		chat.outcome(t, chat.compact(t, nil))
		if text := chat.firstContextText(t); !strings.Contains(text, "B") {
			t.Fatalf("context starts with %q", text)
		}
		gate.resolve()
		if record := must(chat.summarySubmission(t, chat.outcome(t, a)).Status(testContext)); record.Status != durable.SubmissionDone {
			t.Fatalf("A = %s, want done", record.Status)
		}
		messages := must(chat.root.Context(testContext)).Messages
		if !strings.Contains(userText(messages[0]), "<summary>\nA\n</summary>") {
			t.Fatalf("context starts with %q", userText(messages[0]))
		}
		rest := []string{}
		for _, message := range messages[1:] {
			rest = append(rest, userText(message))
		}
		expectEqualJSON(t, rest, jsonText(t, []string{sized("u3", 100), sized("a3", 100)}))
		chat.close(t)
	})

	for _, variant := range []struct {
		order string
		keeps []int
		stale bool
	}{{"before", []int{150, 350}, true}, {"at", []int{150, 150}, false}, {"after", []int{350, 150}, false}} {
		t.Run("places two queued summaries in one boundary when the second cuts "+variant.order+" the first", func(t *testing.T) {
			chat := openCompaction(t)
			chat.history(t)
			gate, reached := deferred(), deferred()
			chat.faux.pushAgent(scriptGated(gate, scriptAnswer("done"), reached))
			input := submitInput(t, chat.root, "busy")
			awaitGate(t, reached)
			submissions := []durable.Submission{}
			for index, keep := range variant.keeps {
				chat.setKeep(keep)
				chat.faux.pushSummary(scriptSummary("S" + itoa(index)))
				submissions = append(submissions, chat.summarySubmission(t, chat.outcome(t, chat.compact(t, nil))))
			}
			gate.resolve()
			must(input.Wait(testContext))
			statuses := []string{}
			for _, submission := range submissions {
				statuses = append(statuses, string(must(submission.Wait(testContext)).Status))
			}
			want := []string{"done", "done"}
			if variant.stale {
				want[1] = "unanswered"
			}
			expectEqualJSON(t, statuses, jsonText(t, want))
			chat.close(t)
		})
	}

	t.Run("is aborted by Conversation.abort(); an already queued summary survives it", func(t *testing.T) {
		chat := openCompaction(t)
		chat.history(t)
		reached := deferred()
		chat.faux.pushSummary(scriptGated(deferred(), scriptSummary(), reached))
		id := chat.compact(t, nil)
		awaitGate(t, reached)
		if compactions := chat.live(t).Compactions; len(compactions) != 1 {
			t.Fatalf("compactions = %v", compactions)
		}
		if err := chat.root.Abort(testContext, nil); err != nil {
			t.Fatal(err)
		}
		if outcome := chat.outcome(t, id); outcome.Status != durable.OutcomeAborted {
			t.Fatalf("outcome = %s, want aborted", outcome.Status)
		}
		if compactions := chat.live(t).Compactions; compactions != nil {
			t.Fatalf("compactions = %v, want undefined", compactions)
		}
		if slices.Contains(chat.kinds(t), "pi.compaction") {
			t.Fatal("an aborted compaction wrote a summary")
		}

		// Queued while busy, then Esc: the queued write stays and lands with the next run's boundary.
		gate, busy := deferred(), deferred()
		chat.faux.pushAgent(scriptGated(gate, scriptAnswer("never"), busy))
		submitInput(t, chat.root, "busy")
		awaitGate(t, busy)
		chat.faux.pushSummary(scriptSummary())
		submission := chat.summarySubmission(t, chat.outcome(t, chat.compact(t, nil)))
		if err := chat.root.Abort(testContext, nil); err != nil {
			t.Fatal(err)
		}
		if record := must(submission.Status(testContext)); record.Status != durable.SubmissionQueued {
			t.Fatalf("summary = %s, want queued", record.Status)
		}
		if result := must(submission.Abort(testContext)); result != durable.SubmissionAborted {
			t.Fatalf("abort = %s, want aborted", result)
		}
		chat.turn(t, "next", "ok")
		expectSettled(t, must(submission.Status(testContext)), durable.SubmissionUnanswered, "aborted")
		if slices.Contains(chat.kinds(t), "pi.compaction") {
			t.Fatal("a withdrawn summary was placed")
		}
		chat.close(t)
	})

	t.Run("is ordinary work: idle waits include it", func(t *testing.T) {
		chat := openCompaction(t)
		chat.history(t)
		gate, reached := deferred(), deferred()
		chat.faux.pushSummary(scriptGated(gate, scriptSummary(), reached))
		id := chat.compact(t, nil)
		awaitGate(t, reached)
		idle := make(chan error, 1)
		go func() { idle <- chat.root.WaitForIdle(testContext) }()
		select {
		case <-idle:
			t.Fatal("idle while the compaction runs")
		case <-time.After(20 * time.Millisecond):
		}
		gate.resolve()
		if err := <-idle; err != nil {
			t.Fatal(err)
		}
		if task := must(chat.harness.GetTask(testContext, id)); task.State.Status != durable.TaskTerminal {
			t.Fatalf("status = %s, want terminal", task.State.Status)
		}
		chat.close(t)
	})

	t.Run("enables scheduling right after open", func(t *testing.T) {
		setup := chatSetup(t)
		faux := newScript(setup)
		harness, root := openChat(t, storage.NewMemoryStorage(), setup)
		setup.SetSettings(func(settings *HarnessSettings) {
			policy := manualPolicy()
			policy.KeepRecentTokens = new(10)
			settings.Compaction = policy
		})
		id := must(root.Compact(testContext, nil))
		// GetTask only reads, so progress here comes from Compact itself.
		waitFor(t, func() bool { return must(harness.GetTask(testContext, id)).State.Status == durable.TaskTerminal })
		expectMatch(t, must(harness.GetTask(testContext, id)).State, `{"outcome":{"status":"completed","result":{}}}`)
		if requests := faux.summaryRequestsCopy(); len(requests) != 0 {
			t.Fatalf("summary requests = %d, want 0", len(requests))
		}
		closeHarness(t, harness)
	})
}

func beforeCompact(decide func(CompactionRequest) (*CompactionDecision, error)) *CompactionHooks {
	return &CompactionHooks{BeforeCompact: func(_ context.Context, compaction CompactionRequest, _ HookApi) (*CompactionDecision, error) {
		return decide(compaction)
	}}
}

// compactionInput returns the input tokens a compaction added to the ledger.
func (chat *compactionChat) compactionInput(t *testing.T, run func()) int {
	t.Helper()
	before := chat.usageInput(t)
	run()
	return chat.usageInput(t) - before
}

func TestCompactionOutcomes(t *testing.T) {
	t.Run("completes without a summary when there is nothing to compact", func(t *testing.T) {
		chat := openCompaction(t)
		chat.turn(t, "hi", "hello")
		expectEqualJSON(t, chat.outcome(t, chat.compact(t, nil)), `{"status":"completed","result":{}}`)
		if len(chat.faux.summaryRequestsCopy()) != 0 || chat.live(t).Compactions != nil {
			t.Fatal("a compaction with nothing to compact summarized or stayed listed")
		}
		chat.close(t)
	})

	t.Run("asks beforeCompact: the first decision wins, a throw is reported and skipped", func(t *testing.T) {
		chat := openCompaction(t)
		var seen []CompactionRequest
		addHooks(t, chat.setup.Registry, CompactionTask, beforeCompact(func(CompactionRequest) (*CompactionDecision, error) {
			return nil, errors.New("hook broke")
		}))
		addHooks(t, chat.setup.Registry, CompactionTask, beforeCompact(func(compaction CompactionRequest) (*CompactionDecision, error) {
			seen = append(seen, compaction)
			return &CompactionDecision{Summary: new("FROM HOOK")}, nil
		}))
		addHooks(t, chat.setup.Registry, CompactionTask, beforeCompact(func(CompactionRequest) (*CompactionDecision, error) {
			return &CompactionDecision{Decline: true}, nil
		}))
		chat.history(t)
		if outcome := chat.outcome(t, chat.compact(t, new("why"))); outcome.Status != durable.OutcomeCompleted {
			t.Fatalf("outcome = %s", outcome.Status)
		}
		if len(chat.faux.summaryRequestsCopy()) != 0 {
			t.Fatal("the model summarized despite the hook's summary")
		}
		if text := chat.firstContextText(t); !strings.Contains(text, "<summary>\nFROM HOOK\n</summary>") {
			t.Fatalf("context starts with %q", text)
		}
		reports := chat.setup.Reports.all()
		if len(reports) != 1 || reports[0].Error() != "hook broke" {
			t.Fatalf("reports = %v", reports)
		}
		compaction := seen[0]
		if compaction.Reason != "manual" || compaction.Instructions == nil || *compaction.Instructions != "why" {
			t.Fatalf("compaction = %+v", compaction)
		}
		expectEqualJSON(t, entryKinds(compaction.Entries), `["pi.user","pi.system","pi.assistant","pi.user","pi.assistant"]`)
		texts := []string{}
		for _, message := range compaction.Messages {
			if _, isSystem := message.(ai.SystemMessage); !isSystem {
				texts = append(texts, userText(message))
			}
		}
		expectEqualJSON(t, texts, jsonText(t, []string{sized("u1", 100), sized("a1", 100), sized("u2", 100), sized("a2", 100)}))
		chat.close(t)
	})

	t.Run("completes without a summary when a hook declines", func(t *testing.T) {
		chat := openCompaction(t)
		addHooks(t, chat.setup.Registry, CompactionTask, beforeCompact(func(CompactionRequest) (*CompactionDecision, error) {
			return &CompactionDecision{Decline: true}, nil
		}))
		chat.history(t)
		expectEqualJSON(t, chat.outcome(t, chat.compact(t, nil)), `{"status":"completed","result":{}}`)
		if len(chat.faux.summaryRequestsCopy()) != 0 {
			t.Fatal("a declined compaction summarized")
		}
		chat.close(t)
	})

	t.Run("fails with no_model without a configured model", func(t *testing.T) {
		chat := openCompaction(t)
		chat.history(t)
		if err := chat.root.Configure(testContext, AgentChange{Model: Cleared[durable.ModelRef]()}); err != nil {
			t.Fatal(err)
		}
		expectMatch(t, chat.outcome(t, chat.compact(t, nil)), `{"status":"failed","error":{"detail":{"reason":"no_model"}}}`)
		if chat.live(t).Compactions != nil {
			t.Fatal("a failed compaction stayed listed")
		}
		chat.close(t)
	})

	t.Run("retries a retryable error with the pinned request and counts every attempt once", func(t *testing.T) {
		single := openCompaction(t)
		single.history(t)
		if err := single.root.Configure(testContext, AgentChange{ThinkingLevel: SetTo(ai.ModelThinkingLevel("high"))}); err != nil {
			t.Fatal(err)
		}
		single.faux.pushSummary(scriptSummary())
		once := single.compactionInput(t, func() { single.outcome(t, single.compact(t, nil)) })
		if once <= 0 {
			t.Fatalf("once = %d", once)
		}
		single.close(t)

		chat := openCompaction(t)
		chat.history(t)
		if err := chat.root.Configure(testContext, AgentChange{ThinkingLevel: SetTo(ai.ModelThinkingLevel("high"))}); err != nil {
			t.Fatal(err)
		}
		chat.setup.SetSettings(func(settings *HarnessSettings) {
			settings.Stream = &durable.ConversationStreamOptions{TimeoutMs: new(1234), Deferred: &ai.DeferredOption{Enabled: true}}
		})
		chat.faux.pushSummary(func(scriptRequest) (ai.FauxResponse, error) {
			// Changed during the attempt: the retry still uses the pinned request.
			if err := chat.root.Configure(testContext, AgentChange{ThinkingLevel: SetTo(ai.ModelThinkingLevel("low"))}); err != nil {
				return ai.FauxResponse{}, err
			}
			chat.setup.SetSettings(func(settings *HarnessSettings) {
				settings.Stream = &durable.ConversationStreamOptions{TimeoutMs: new(1)}
			})
			return ai.FauxResponse{StopReason: "error", ErrorMessage: "overloaded"}, nil
		}, scriptSummary())
		twice := chat.compactionInput(t, func() {
			if outcome := chat.outcome(t, chat.compact(t, nil)); outcome.Status != durable.OutcomeCompleted {
				t.Fatalf("outcome = %s", outcome.Status)
			}
		})
		requests := chat.faux.summaryRequestsCopy()
		if len(requests) != 2 || twice != 2*once {
			t.Fatalf("requests = %d, twice = %d, once = %d", len(requests), twice, once)
		}
		for _, request := range requests {
			options := request.options
			if options.Thinking != "high" || options.TimeoutMs == nil || *options.TimeoutMs != 1234 || options.CacheRetention != ai.CacheRetentionNone || options.Deferred != nil {
				t.Fatalf("options = %+v", options)
			}
		}
		chat.close(t)
	})

	t.Run("adds no usage when a hook declines or supplies the summary", func(t *testing.T) {
		for _, decision := range []CompactionDecision{{Decline: true}, {Summary: new("HOOK")}} {
			chat := openCompaction(t)
			addHooks(t, chat.setup.Registry, CompactionTask, beforeCompact(func(CompactionRequest) (*CompactionDecision, error) {
				return &decision, nil
			}))
			chat.history(t)
			if added := chat.compactionInput(t, func() { chat.outcome(t, chat.compact(t, nil)) }); added != 0 {
				t.Fatalf("added = %d, want 0", added)
			}
			chat.close(t)
		}
	})

	t.Run("caps maxTokens at the model's output limit and sends no tools", func(t *testing.T) {
		chat := openCompaction(t)
		addTool(t, chat.setup.Registry, noopTool("read"))
		if err := chat.root.Configure(testContext, AgentChange{Tools: SetTo(ToolChange{Exact: true, List: toolsNamed(t, chat.setup, "read")})}); err != nil {
			t.Fatal(err)
		}
		chat.history(t)
		chat.setup.SetSettings(func(settings *HarnessSettings) {
			policy := manualPolicy()
			policy.ReserveTokens = new(2000)
			settings.Compaction = policy
		})
		chat.faux.pushSummary(scriptSummary())
		chat.outcome(t, chat.compact(t, nil))
		request := chat.faux.summaryRequestsCopy()[0]
		// 0.8 * 2000 = 1600, above the model's 900.
		if request.options.MaxTokens != 900 || len(request.messages) != 2 {
			t.Fatalf("maxTokens = %d, messages = %d", request.options.MaxTokens, len(request.messages))
		}
		for _, message := range request.messages {
			if system, ok := message.(ai.SystemMessage); ok && system.ToolsAdded != nil {
				t.Fatal("the summary request offers tools")
			}
		}
		chat.close(t)
	})

	for _, variant := range []struct {
		name     string
		response ai.FauxResponse
		message  string
	}{
		{"retries run out", ai.FauxResponse{StopReason: "error", ErrorMessage: "overloaded"}, "Summarization failed: overloaded"},
		{"a non-retryable error", ai.FauxResponse{StopReason: "error", ErrorMessage: "bad request"}, "Summarization failed: bad request"},
		{"a length stop", ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("partial")}, StopReason: "length"}, "Summarization hit the token limit; the summary is incomplete"},
		{"a tool call", ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxToolCall("read", map[string]any{}, "")}, StopReason: "stop"}, "Summarization attempted to call a tool"},
		{"empty text", ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("  ")}}, "Summarization produced no text"},
	} {
		t.Run("fails with model_error on "+variant.name, func(t *testing.T) {
			chat := openCompaction(t)
			chat.history(t)
			for range 3 {
				chat.faux.pushSummary(fixed(variant.response))
			}
			expectMatch(t, chat.outcome(t, chat.compact(t, nil)), jsonText(t, map[string]any{"status": "failed", "error": map[string]any{"message": variant.message, "detail": map[string]any{"reason": "model_error"}}}))
			// The retry policy allows two retries after the first attempt.
			want := 1
			if variant.name == "retries run out" {
				want = 3
			}
			if requests := len(chat.faux.summaryRequestsCopy()); requests != want {
				t.Fatalf("requests = %d, want %d", requests, want)
			}
			if chat.live(t).Compactions != nil || slices.Contains(chat.kinds(t), "pi.compaction") {
				t.Fatal("a failed compaction left a status or a summary")
			}
			chat.close(t)
		})
	}
}

// backgroundPolicy has its background threshold at 500 and blocking threshold at 1500 tokens of a 2000-token window.
func backgroundPolicy() *CompactionPolicyPatch {
	return &CompactionPolicyPatch{Enabled: new(true), ReserveTokens: new(500), KeepRecentTokens: new(150), BackgroundTokens: new(1000)}
}

// blockingPolicy has its blocking threshold at 700 tokens of a 1000-token window, no background compaction.
func blockingPolicy() *CompactionPolicyPatch {
	return &CompactionPolicyPatch{Enabled: new(true), ReserveTokens: new(300), KeepRecentTokens: new(150), BackgroundTokens: new(0)}
}

func (chat *compactionChat) setPolicy(policy *CompactionPolicyPatch) {
	chat.setup.SetSettings(func(settings *HarnessSettings) { settings.Compaction = policy })
}

func (chat *compactionChat) compactionTasks(t *testing.T) []durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue] {
	t.Helper()
	tasks := []durable.TaskRecord[durable.JsonValue, durable.JsonValue, durable.JsonValue]{}
	for _, task := range must(chat.harness.Inspect(testContext)).Tasks {
		if task.Record.Kind == "pi.compaction" {
			tasks = append(tasks, task.Record)
		}
	}
	return tasks
}

func TestBackgroundThresholdCompaction(t *testing.T) {
	t.Run("starts above the background threshold without blocking the run; idle waits and Esc ignore it", func(t *testing.T) {
		chat := openCompaction(t, compactionOptions{contextWindow: 2000})
		chat.history(t)
		chat.setPolicy(backgroundPolicy())
		gate, reached := deferred(), deferred()
		chat.faux.pushSummary(scriptGated(gate, scriptSummary(), reached))
		chat.turn(t, sized("u4", 100), sized("a4", 100))
		awaitGate(t, reached)
		task := chat.compactionTasks(t)[0]
		expectMatch(t, task, `{"background":true,"input":{"reason":"threshold"}}`)
		if task.Owner != nil {
			t.Fatalf("owner = %v, want undefined", *task.Owner)
		}
		expectEqualJSON(t, chat.live(t).Compactions, jsonText(t, []any{map[string]any{"taskId": task.Id, "reason": "threshold", "blocking": false, "attempt": 1}}))
		// Background work: neither conversation idle nor Esc waits for or stops it.
		if err := chat.root.WaitForIdle(testContext); err != nil {
			t.Fatal(err)
		}
		if err := chat.root.Abort(testContext, nil); err != nil {
			t.Fatal(err)
		}
		if status := must(chat.harness.GetTask(testContext, task.Id)).State.Status; status != durable.TaskRunning {
			t.Fatalf("status = %s, want running", status)
		}
		gate.resolve()
		if outcome := chat.outcome(t, task.Id); outcome.Status != durable.OutcomeCompleted {
			t.Fatalf("outcome = %s", outcome.Status)
		}
		if kinds := chat.kinds(t); kinds[len(kinds)-1] != "pi.compaction" || !strings.Contains(chat.firstContextText(t), "SUMMARY") {
			t.Fatalf("kinds = %v", kinds)
		}
		chat.close(t)
	})

	for _, variant := range []struct {
		name   string
		policy func() *CompactionPolicyPatch
	}{
		{"disabled", func() *CompactionPolicyPatch {
			policy := backgroundPolicy()
			policy.Enabled = new(false)
			return policy
		}},
		{"backgroundTokens is 0", func() *CompactionPolicyPatch {
			policy := backgroundPolicy()
			policy.BackgroundTokens = new(0)
			return policy
		}},
		{"there is no cut", func() *CompactionPolicyPatch {
			policy := backgroundPolicy()
			policy.KeepRecentTokens = new(100_000)
			return policy
		}},
	} {
		t.Run("does not start when "+variant.name, func(t *testing.T) {
			chat := openCompaction(t, compactionOptions{contextWindow: 2000})
			chat.history(t)
			chat.setPolicy(variant.policy())
			chat.turn(t, sized("u4", 100), sized("a4", 100))
			if tasks := chat.compactionTasks(t); len(tasks) != 0 || len(chat.faux.summaryRequestsCopy()) != 0 {
				t.Fatalf("tasks = %d", len(tasks))
			}
			chat.close(t)
		})
	}

	t.Run("does not start while another compaction is listed", func(t *testing.T) {
		chat := openCompaction(t, compactionOptions{contextWindow: 2000})
		chat.history(t)
		reached := deferred()
		chat.faux.pushSummary(scriptGated(deferred(), scriptSummary(), reached))
		manual := chat.compact(t, nil)
		awaitGate(t, reached)
		chat.setPolicy(backgroundPolicy())
		chat.turn(t, sized("u4", 100), sized("a4", 100))
		tasks := chat.compactionTasks(t)
		if len(tasks) != 1 || tasks[0].Id != manual {
			t.Fatalf("tasks = %s", jsonText(t, tasks))
		}
		must(chat.harness.AbortTask(testContext, manual))
		chat.close(t)
	})

	t.Run("stops through abortTask() and Conversation.abort() with background", func(t *testing.T) {
		for _, stop := range []string{"task", "conversation"} {
			chat := openCompaction(t, compactionOptions{contextWindow: 2000})
			chat.history(t)
			chat.setPolicy(backgroundPolicy())
			reached := deferred()
			chat.faux.pushSummary(scriptGated(deferred(), scriptSummary(), reached))
			chat.turn(t, sized("u4", 100), sized("a4", 100))
			awaitGate(t, reached)
			task := chat.compactionTasks(t)[0]
			if stop == "task" {
				must(chat.harness.AbortTask(testContext, task.Id))
			} else if err := chat.root.Abort(testContext, &durable.ConversationAbortOptions{Background: true}); err != nil {
				t.Fatal(err)
			}
			if outcome := chat.outcome(t, task.Id); outcome.Status != durable.OutcomeAborted || chat.live(t).Compactions != nil {
				t.Fatalf("%s: outcome = %s", stop, outcome.Status)
			}
			chat.close(t)
		}
	})
}

func TestBlockingThresholdCompaction(t *testing.T) {
	t.Run("waits for its compaction, which appends the summary before the request", func(t *testing.T) {
		chat := openCompaction(t, compactionOptions{contextWindow: 1000})
		chat.history(t)
		chat.setPolicy(blockingPolicy())
		// A new section: preparation has a system entry to append, but must not append it before the wait.
		addSection(t, chat.setup.Registry, "extra", text("EXTRA"))
		gate, reached := deferred(), deferred()
		chat.faux.pushSummary(scriptGated(gate, scriptSummary(), reached))
		chat.faux.pushAgent(scriptAnswer("a4"))
		input := submitInput(t, chat.root, sized("u4", 200))
		awaitGate(t, reached)
		child := chat.compactionTasks(t)[0]
		generation := chat.live(t).Run.TaskId
		expectMatch(t, child, jsonText(t, map[string]any{"owner": generation, "background": false, "input": map[string]any{"reason": "threshold"}}))
		expectMatch(t, must(chat.harness.GetTask(testContext, generation)).State, jsonText(t, map[string]any{"status": "waiting", "on": []any{child.Id}, "checkpoint": map[string]any{"phase": "prepare", "attempt": 1, "compacted": child.Id}}))
		expectEqualJSON(t, chat.live(t).Compactions, jsonText(t, []any{map[string]any{"taskId": child.Id, "reason": "threshold", "blocking": true, "attempt": 1}}))
		// Nothing was appended before the wait.
		if kinds := chat.kinds(t); kinds[len(kinds)-1] != "pi.user" {
			t.Fatalf("kinds = %v", kinds)
		}
		gate.resolve()
		expectSettled(t, must(input.Wait(testContext)), durable.SubmissionDone, "")
		if outcome := chat.outcome(t, child.Id); outcome.Status != durable.OutcomeCompleted {
			t.Fatalf("outcome = %s", outcome.Status)
		}
		expectEqualJSON(t, lastN(chat.kinds(t), 3), `["pi.compaction","pi.system","pi.assistant"]`)
		request := chat.faux.lastAgentRequest().messages
		if !strings.Contains(userText(request[0]), "SUMMARY") {
			t.Fatalf("request starts with %q", userText(request[0]))
		}
		systems := []ai.Message{}
		for _, message := range request {
			if _, ok := message.(ai.SystemMessage); ok {
				systems = append(systems, message)
			}
		}
		if len(systems) != 1 {
			t.Fatalf("systems = %d, want 1", len(systems))
		}
		expectMatch(t, systems[0], `{"sections":{"preamble":"You are helpful.","extra":"<extra>\nEXTRA\n</extra>"}}`)
		chat.close(t)
	})

	t.Run("sends the request once, without a second compaction, when the kept part is still above the threshold", func(t *testing.T) {
		chat := openCompaction(t, compactionOptions{contextWindow: 1000})
		chat.history(t)
		policy := blockingPolicy()
		policy.KeepRecentTokens = new(700)
		chat.setPolicy(policy)
		chat.faux.pushSummary(scriptSummary())
		chat.turn(t, sized("u4", 400), "a4")
		compactions := 0
		for _, kind := range chat.kinds(t) {
			if kind == "pi.compaction" {
				compactions++
			}
		}
		if requests := len(chat.faux.summaryRequestsCopy()); requests != 1 || compactions != 1 {
			t.Fatalf("summary requests = %d, compactions = %d", requests, compactions)
		}
		chat.close(t)
	})

	for _, variant := range []struct {
		name    string
		prepare func(t *testing.T, chat *compactionChat)
	}{
		{"declines", func(t *testing.T, chat *compactionChat) {
			addHooks(t, chat.setup.Registry, CompactionTask, beforeCompact(func(CompactionRequest) (*CompactionDecision, error) {
				return &CompactionDecision{Decline: true}, nil
			}))
		}},
		{"fails", func(_ *testing.T, chat *compactionChat) { chat.faux.pushSummary(scriptFailure("bad request")) }},
	} {
		t.Run("sends the request anyway when its compaction "+variant.name, func(t *testing.T) {
			chat := openCompaction(t, compactionOptions{contextWindow: 1000})
			chat.history(t)
			chat.setPolicy(blockingPolicy())
			variant.prepare(t, chat)
			chat.turn(t, sized("u4", 200), "a4")
			if slices.Contains(chat.kinds(t), "pi.compaction") {
				t.Fatal("a summary was placed")
			}
			if first := userText(chat.faux.lastAgentRequest().messages[0]); first != sized("u1", 100) {
				t.Fatalf("request starts with %q", first)
			}
			chat.close(t)
		})
	}

	t.Run("sends the request anyway when its compaction is aborted directly", func(t *testing.T) {
		chat := openCompaction(t, compactionOptions{contextWindow: 1000})
		chat.history(t)
		chat.setPolicy(blockingPolicy())
		reached := deferred()
		chat.faux.pushSummary(scriptGated(deferred(), scriptSummary(), reached))
		chat.faux.pushAgent(scriptAnswer("a4"))
		input := submitInput(t, chat.root, sized("u4", 200))
		awaitGate(t, reached)
		must(chat.harness.AbortTask(testContext, chat.compactionTasks(t)[0].Id))
		expectSettled(t, must(input.Wait(testContext)), durable.SubmissionDone, "")
		if slices.Contains(chat.kinds(t), "pi.compaction") {
			t.Fatal("a summary was placed")
		}
		chat.close(t)
	})

	t.Run("is aborted with its generation by Esc, before the generation's abort handler", func(t *testing.T) {
		chat := openCompaction(t, compactionOptions{contextWindow: 1000})
		chat.history(t)
		chat.setPolicy(blockingPolicy())
		reached := deferred()
		chat.faux.pushSummary(scriptGated(deferred(), scriptSummary(), reached))
		var mu sync.Mutex
		types := []string{}
		stream := must(WatchEvents(testContext, chat.harness, chat.root.Id()))
		stream.Start(func(_ context.Context, batch []AgentEvent) error {
			mu.Lock()
			for _, event := range batch {
				types = append(types, event.EventType())
			}
			mu.Unlock()
			return nil
		})
		input := submitInput(t, chat.root, sized("u4", 200))
		awaitGate(t, reached)
		child := chat.compactionTasks(t)[0]
		if err := chat.root.Abort(testContext, nil); err != nil {
			t.Fatal(err)
		}
		expectSettled(t, must(input.Wait(testContext)), durable.SubmissionUnanswered, "aborted")
		expectMatch(t, must(chat.harness.GetTask(testContext, child.Id)).State, `{"outcome":{"status":"aborted"}}`)
		waitFor(t, func() bool {
			mu.Lock()
			defer mu.Unlock()
			return slices.Contains(types, "run_end")
		})
		mu.Lock()
		end, run := slices.Index(types, "compaction_end"), slices.Index(types, "run_end")
		mu.Unlock()
		if end < 0 || end >= run {
			t.Fatalf("events = %v", types)
		}
		must(stream.Stop())
		chat.close(t)
	})

	t.Run("wins over a background compaction still in flight, which then settles stale", func(t *testing.T) {
		chat := openCompaction(t, compactionOptions{contextWindow: 2000})
		chat.history(t)
		chat.setPolicy(backgroundPolicy())
		gate, reached := deferred(), deferred()
		chat.faux.pushSummary(scriptGated(gate, scriptSummary("BACKGROUND"), reached))
		chat.turn(t, sized("u4", 100), sized("a4", 100))
		awaitGate(t, reached)
		background := chat.compactionTasks(t)[0]
		chat.faux.pushSummary(scriptSummary("BLOCKING"))
		chat.turn(t, sized("u5", 1000), "a5")
		if text := chat.firstContextText(t); !strings.Contains(text, "BLOCKING") {
			t.Fatalf("context starts with %q", text)
		}
		gate.resolve()
		expectSettled(t, must(chat.summarySubmission(t, chat.outcome(t, background.Id)).Status(testContext)), durable.SubmissionUnanswered, "stale")
		chat.close(t)
	})
}

func enabledPolicy() *CompactionPolicyPatch {
	policy := manualPolicy()
	policy.Enabled = new(true)
	return policy
}

func countKind(kinds []string, kind string) int {
	count := 0
	for _, each := range kinds {
		if each == kind {
			count++
		}
	}
	return count
}

func toolCallScript(name string) scriptStep {
	return fixed(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxToolCall(name, map[string]any{}, "")}, StopReason: "toolUse"})
}

func TestOverflowCompaction(t *testing.T) {
	t.Run("compacts and retries with the same attempt, leaving the error out of the retry", func(t *testing.T) {
		chat := openCompaction(t)
		chat.history(t)
		chat.setPolicy(enabledPolicy())
		chat.faux.pushSummary(scriptSummary())
		attempt := -1
		chat.faux.pushAgent(scriptFailure(overflowText), func(scriptRequest) (ai.FauxResponse, error) {
			if generation := chat.live(t).Generation; generation != nil {
				attempt = generation.Attempt
			}
			return ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("fits")}}, nil
		})
		expectSettled(t, must(submitInput(t, chat.root, sized("u4", 100)).Wait(testContext)), durable.SubmissionDone, "")
		if attempt != 1 {
			t.Fatalf("attempt = %d, want 1", attempt)
		}
		expectEqualJSON(t, lastN(chat.kinds(t), 5), `["pi.user","pi.assistant","pi.compaction","pi.system","pi.assistant"]`)
		for _, record := range allEntries(t, chat.root) {
			if record.Kind == "pi.compaction" {
				expectMatch(t, record, `{"data":{"reason":"overflow"}}`)
			}
		}
		retry := chat.faux.lastAgentRequest().messages
		if !strings.Contains(userText(retry[0]), "SUMMARY") || slices.ContainsFunc(retry, func(message ai.Message) bool {
			assistant, ok := message.(ai.AssistantMessage)
			return ok && assistant.StopReason == ai.StopReasonError
		}) {
			t.Fatalf("retry = %v", roles(retry))
		}
		chat.close(t)
	})

	t.Run("fails a second overflow with its error entry", func(t *testing.T) {
		chat := openCompaction(t)
		chat.history(t)
		chat.setPolicy(enabledPolicy())
		chat.faux.pushSummary(scriptSummary())
		chat.faux.pushAgent(scriptFailure(overflowText), scriptFailure(overflowText))
		settled := must(submitInput(t, chat.root, sized("u4", 100)).Wait(testContext))
		expectSettled(t, settled, durable.SubmissionUnanswered, "model_error")
		expectEqualJSON(t, settled.Detail, jsonText(t, overflowText))
		kinds := chat.kinds(t)
		if countKind(kinds, "pi.compaction") != 1 || kinds[len(kinds)-1] != "pi.assistant" {
			t.Fatalf("kinds = %v", kinds)
		}
		chat.close(t)
	})

	t.Run("fails without compacting when compaction is disabled, even for a retryable-looking overflow", func(t *testing.T) {
		chat := openCompaction(t)
		chat.history(t)
		chat.faux.pushAgent(scriptFailure("overloaded: " + overflowText))
		expectSettled(t, must(submitInput(t, chat.root, sized("u4", 100)).Wait(testContext)), durable.SubmissionUnanswered, "model_error")
		if agent, summaries := len(chat.faux.agentRequestsCopy()), len(chat.faux.summaryRequestsCopy()); agent != 4 || summaries != 0 {
			t.Fatalf("agent requests = %d, summary requests = %d", agent, summaries)
		}
		chat.close(t)
	})

	t.Run("fails after a blocking threshold compaction in the same generation", func(t *testing.T) {
		chat := openCompaction(t, compactionOptions{contextWindow: 1000})
		chat.history(t)
		chat.setPolicy(blockingPolicy())
		chat.faux.pushSummary(scriptSummary())
		chat.faux.pushAgent(scriptFailure(overflowText))
		expectSettled(t, must(submitInput(t, chat.root, sized("u4", 200)).Wait(testContext)), durable.SubmissionUnanswered, "model_error")
		if summaries := len(chat.faux.summaryRequestsCopy()); summaries != 1 {
			t.Fatalf("summary requests = %d, want 1", summaries)
		}
		chat.close(t)
	})

	for _, variant := range []struct {
		name     string
		prepare  func(t *testing.T, chat *compactionChat)
		requests int
	}{
		{"declines", func(t *testing.T, chat *compactionChat) {
			addHooks(t, chat.setup.Registry, CompactionTask, beforeCompact(func(CompactionRequest) (*CompactionDecision, error) {
				return &CompactionDecision{Decline: true}, nil
			}))
		}, 0},
		{"fails", func(_ *testing.T, chat *compactionChat) { chat.faux.pushSummary(scriptFailure("bad request")) }, 1},
		// Classification finds no cut, so no compaction starts and the ordinary failure carries the text.
		{"cannot cut", func(_ *testing.T, chat *compactionChat) {
			policy := enabledPolicy()
			policy.KeepRecentTokens = new(100_000)
			chat.setPolicy(policy)
		}, 0},
	} {
		t.Run("fails with the overflow text when compaction "+variant.name, func(t *testing.T) {
			chat := openCompaction(t)
			chat.turn(t, sized("u1", 100), sized("a1", 100))
			chat.setPolicy(enabledPolicy())
			variant.prepare(t, chat)
			chat.faux.pushAgent(scriptFailure(overflowText))
			settled := must(submitInput(t, chat.root, sized("u4", 100)).Wait(testContext))
			expectSettled(t, settled, durable.SubmissionUnanswered, "model_error")
			expectEqualJSON(t, settled.Detail, jsonText(t, overflowText))
			if summaries := len(chat.faux.summaryRequestsCopy()); summaries != variant.requests {
				t.Fatalf("summary requests = %d, want %d", summaries, variant.requests)
			}
			chat.close(t)
		})
	}
}

func TestCompactionEstimates(t *testing.T) {
	for _, clock := range []string{"real", "fixed"} {
		t.Run("ignores usage measured before a summary placed mid-run ("+clock+" clock)", func(t *testing.T) {
			setup := chatSetup(t, ai.FauxConfig{Models: []ai.FauxModelDefinition{{ID: "faux-1", ContextWindow: 2000, MaxTokens: 900}}})
			if clock == "fixed" {
				setup.SetNow(func() float64 { return 1_000 })
			}
			chat := openCompaction(t, compactionOptions{contextWindow: 2000, setup: setup})
			toolGate, toolReached := deferred(), deferred()
			addTool(t, chat.setup.Registry, new(durable.ToolRegistration{ToolSchema: ai.ToolSchema{Name: "slow", Description: "slow", Parameters: map[string]any{"type": "object", "properties": map[string]any{}}}, Execute: func(ctx context.Context, _ any, _ durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
				toolReached.resolve()
				if err := toolGate.wait(ctx); err != nil {
					return durable.ToolExecutionResult{}, err
				}
				return durable.ToolExecutionResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: sized("result", 200)}}}, nil
			}}))
			if err := chat.root.Configure(testContext, AgentChange{Tools: SetTo(ToolChange{Exact: true, List: toolsNamed(t, chat.setup, "slow")})}); err != nil {
				t.Fatal(err)
			}
			chat.turn(t, sized("u1", 220), sized("a1", 220))
			chat.turn(t, sized("u2", 220), sized("a2", 220))
			chat.turn(t, sized("u3", 220), sized("a3", 220))
			// Background at 900, blocking at 1500: the tool call's usage plus its result would cross 1500.
			policy := backgroundPolicy()
			policy.BackgroundTokens = new(600)
			chat.setPolicy(policy)
			// The request starts a background compaction; its tool call's usage measures the whole context.
			chat.faux.pushAgent(toolCallScript("slow"), scriptAnswer("done"))
			summaryGate, summaryReached := deferred(), deferred()
			chat.faux.pushSummary(scriptGated(summaryGate, scriptSummary(), summaryReached))
			input := submitInput(t, chat.root, sized("u4", 50))
			awaitGate(t, toolReached)
			awaitGate(t, summaryReached)
			background := chat.compactionTasks(t)[0]
			summaryGate.resolve()
			// Queued: the run is busy in its tool round.
			if record := must(chat.summarySubmission(t, chat.outcome(t, background.Id)).Status(testContext)); record.Status != durable.SubmissionQueued {
				t.Fatalf("summary = %s, want queued", record.Status)
			}
			toolGate.resolve()
			expectSettled(t, must(input.Wait(testContext)), durable.SubmissionDone, "")
			// The summary landed at postTools; the successor saw a small context and did not compact again.
			if summaries, tasks := len(chat.faux.summaryRequestsCopy()), len(chat.compactionTasks(t)); summaries != 1 || tasks != 0 {
				t.Fatalf("summary requests = %d, compaction tasks = %d", summaries, tasks)
			}
			if first := userText(chat.faux.lastAgentRequest().messages[0]); !strings.Contains(first, "SUMMARY") {
				t.Fatalf("request starts with %q", first)
			}
			chat.close(t)
		})
	}

	t.Run("rebaselines the system prompt over kept system deltas", func(t *testing.T) {
		chat := openCompaction(t)
		var mu sync.Mutex
		mood := "cheerful"
		addSection(t, chat.setup.Registry, "mood", func(context.Context, durable.PromptInput) (*string, error) {
			mu.Lock()
			defer mu.Unlock()
			return new(mood), nil
		}, SectionOptions{Tag: new(false)})
		chat.turn(t, sized("u1", 100), sized("a1", 100))
		chat.turn(t, sized("u2", 100), sized("a2", 100))
		mu.Lock()
		mood = "terse"
		mu.Unlock()
		chat.turn(t, sized("u3", 100), sized("a3", 100))
		chat.setKeep(250)
		chat.faux.pushSummary(scriptSummary())
		chat.outcome(t, chat.compact(t, nil))
		// The kept range holds the delta for the terse mood; the next request has one complete baseline.
		if !slices.ContainsFunc(must(chat.root.Context(testContext)).Entries, func(record durable.EntryRecord) bool { return record.Kind == "pi.system" }) {
			t.Fatal("the kept range has no system entry")
		}
		chat.turn(t, "next", "ok")
		systems := []ai.Message{}
		for _, message := range chat.faux.lastAgentRequest().messages {
			if _, ok := message.(ai.SystemMessage); ok {
				systems = append(systems, message)
			}
		}
		if len(systems) != 1 {
			t.Fatalf("systems = %d, want 1", len(systems))
		}
		expectMatch(t, systems[0], `{"sections":{"preamble":"You are helpful.","mood":"terse"}}`)
		chat.close(t)
	})
}

type childState struct {
	Phase string `json:"phase"`
}

// defineChildTask is a task that completes once gate resolves.
func defineChildTask(gate *deferredGate) durable.Task[struct{}, childState, durable.JsonValue, any] {
	return durable.DefineTask(durable.TaskDefinition[struct{}, childState, durable.JsonValue, any]{
		Name:    "test.child",
		Version: 1,
		Initial: func(struct{}) childState { return childState{Phase: "run"} },
		Phases: map[string]durable.PhaseHandler[struct{}, childState, durable.JsonValue, any]{
			"run": func(ctx context.Context, _ durable.RunningTask[struct{}, childState, durable.JsonValue], runtime durable.TaskRuntime[struct{}, childState, durable.JsonValue, any]) error {
				if err := gate.wait(ctx); err != nil {
					return err
				}
				return runtime.Commit(ctx, func(durable.Tx, durable.RunningTask[struct{}, childState, durable.JsonValue]) (*durable.NextTaskState[childState, durable.JsonValue], error) {
					var null durable.JsonValue
					return &durable.NextTaskState[childState, durable.JsonValue]{Status: durable.TaskTerminal, Outcome: &durable.TaskOutcome[durable.JsonValue]{Status: durable.OutcomeCompleted, Result: &null}}, nil
				})
			},
		},
		Abort: func(ctx context.Context, _ durable.RunningTask[struct{}, childState, durable.JsonValue], runtime durable.TaskRuntime[struct{}, childState, durable.JsonValue, any]) error {
			return runtime.Commit(ctx, func(durable.Tx, durable.RunningTask[struct{}, childState, durable.JsonValue]) (*durable.NextTaskState[childState, durable.JsonValue], error) {
				return &durable.NextTaskState[childState, durable.JsonValue]{Status: durable.TaskTerminal, Outcome: &durable.TaskOutcome[durable.JsonValue]{Status: durable.OutcomeAborted}}, nil
			})
		},
	})
}

func TestCompactionInteractions(t *testing.T) {
	t.Run("summarizes a replaced entry's replacement and shows it to the hook", func(t *testing.T) {
		chat := openCompaction(t)
		chat.history(t)
		u1 := allEntries(t, chat.root)[0]
		submit(t, chat.root, writeDraft(durable.EntryDraft{Kind: "app.redact", Edits: []durable.ContextEdit{{Target: u1.Id, Action: durable.EditReplace, Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("REDACTED")}}}}}))
		var messages []ai.Message
		addHooks(t, chat.setup.Registry, CompactionTask, beforeCompact(func(compaction CompactionRequest) (*CompactionDecision, error) {
			messages = compaction.Messages
			return nil, nil
		}))
		chat.faux.pushSummary(scriptSummary())
		chat.outcome(t, chat.compact(t, nil))
		if userText(messages[0]) != "REDACTED" || !strings.Contains(userText(chat.faux.summaryRequestsCopy()[0].messages[1]), "[User]: REDACTED") {
			t.Fatalf("hook saw %q", userText(messages[0]))
		}
		chat.close(t)
	})

	t.Run("compacts a fork whose cut falls on a parent entry", func(t *testing.T) {
		chat := openCompaction(t)
		chat.history(t)
		entries := allEntries(t, chat.root)
		fork := must(chat.root.Fork(testContext, entries[len(entries)-1].Id, ConversationCreateOptions{Ownership: ownerless}))
		chat.faux.pushSummary(scriptSummary())
		outcome := chat.outcome(t, must(fork.Compact(testContext, nil)))
		expectSettled(t, must(chat.summarySubmission(t, outcome).Wait(testContext)), durable.SubmissionDone, "")
		var u3 durable.EntryRecord
		for _, record := range entries {
			if len(record.Model) > 0 && strings.HasPrefix(userText(record.Model[0]), "u3") {
				u3 = record
			}
		}
		view := must(fork.Context(testContext))
		expectMatch(t, view.Head, jsonText(t, map[string]any{"kind": "pi.compaction", "head": u3.Id, "conversationId": fork.Id()}))
		rest := []string{}
		for _, message := range view.Messages[1:] {
			rest = append(rest, userText(message))
		}
		expectEqualJSON(t, rest, jsonText(t, []string{sized("u3", 100), sized("a3", 100)}))
		// The parent is untouched.
		if slices.Contains(chat.kinds(t), "pi.compaction") {
			t.Fatal("the parent was compacted")
		}
		// The fork's view keeps the parent entries the summary kept.
		state := must(fork.ViewState(testContext))
		expectEqualJSON(t, state.Value().Entries, jsonText(t, view.Entries))
		state.Dispose()
		chat.close(t)
	})

	t.Run("settles stale in a fork reset while it summarizes", func(t *testing.T) {
		chat := openCompaction(t)
		chat.history(t)
		entries := allEntries(t, chat.root)
		fork := must(chat.root.Fork(testContext, entries[len(entries)-1].Id, ConversationCreateOptions{Ownership: ownerless}))
		gate, reached := deferred(), deferred()
		chat.faux.pushSummary(scriptGated(gate, scriptSummary(), reached))
		id := must(fork.Compact(testContext, nil))
		awaitGate(t, reached)
		resetTo(t, fork, nil)
		gate.resolve()
		expectSettled(t, must(chat.summarySubmission(t, chat.outcome(t, id)).Status(testContext)), durable.SubmissionUnanswered, "stale")
		if head := must(fork.Context(testContext)).Head; head == nil || head.Kind != "pi.reset" {
			t.Fatalf("head = %s", jsonText(t, head))
		}
		chat.close(t)
	})

	t.Run("places an older queued summary and the current one together when idle admission drains the inbox", func(t *testing.T) {
		chat := openCompaction(t)
		chat.history(t)
		gate, reached := deferred(), deferred()
		chat.faux.pushAgent(scriptGated(gate, scriptFailure("bad request"), reached))
		failed := submitInput(t, chat.root, "fails")
		awaitGate(t, reached)
		chat.faux.pushSummary(scriptSummary("OLDER"))
		older := chat.outcome(t, chat.compact(t, nil))
		gate.resolve()
		must(failed.Wait(testContext))
		// Idle now, with the older summary still queued; the current one queues behind it and a final boundary runs.
		chat.faux.pushSummary(scriptSummary("CURRENT"))
		current := chat.outcome(t, chat.compact(t, nil))
		statuses := []string{}
		for _, outcome := range []durable.TaskOutcome[CompactionResult]{older, current} {
			statuses = append(statuses, string(must(chat.summarySubmission(t, outcome).Status(testContext)).Status))
		}
		expectEqualJSON(t, statuses, `["done","done"]`)
		if text := chat.firstContextText(t); !strings.Contains(text, "CURRENT") {
			t.Fatalf("context starts with %q", text)
		}
		chat.close(t)
	})

	t.Run("places a hook's summary and holds while work the hook created runs", func(t *testing.T) {
		chat := openCompaction(t)
		childGate := deferred()
		child := defineChildTask(childGate)
		addTask(t, chat.setup.Registry, child)
		rootId := chat.root.Id()
		addHooks(t, chat.setup.Registry, CompactionTask, &CompactionHooks{BeforeCompact: func(ctx context.Context, _ CompactionRequest, api HookApi) (*CompactionDecision, error) {
			_, err := durable.Commit(ctx, chat.harness, func(tx durable.Tx) (durable.TaskId, error) {
				return durable.CreateTask(tx, child, struct{}{}, durable.TaskOptions{Ownership: durable.TaskOwnership{Kind: durable.TaskOwnedByTask, TaskId: api.TaskId()}, ConversationId: &rootId})
			})
			if err != nil {
				return nil, err
			}
			return &CompactionDecision{Summary: new("HOOK")}, nil
		}})
		chat.history(t)
		id := chat.compact(t, nil)
		waitFor(t, func() bool { return must(chat.harness.GetTask(testContext, id)).State.Status == durable.TaskCompleting })
		// The summary and the status removal landed at the hold.
		if kinds := chat.kinds(t); kinds[len(kinds)-1] != "pi.compaction" || chat.live(t).Compactions != nil {
			t.Fatalf("kinds = %v, compactions = %v", kinds, chat.live(t).Compactions)
		}
		childGate.resolve()
		if outcome := chat.outcome(t, id); outcome.Status != durable.OutcomeCompleted {
			t.Fatalf("outcome = %s", outcome.Status)
		}
		chat.close(t)
	})

	t.Run("removes the status of a faulted compaction", func(t *testing.T) {
		chat := openCompaction(t)
		chat.history(t)
		// The Go form of replacing Models.getModel with a throwing function: the provider's model listing panics.
		original := *chat.setup.Faux.Provider()
		withProvider(chat.setup, func(provider *ai.ModelsProvider) {
			provider.GetModels = func() ([]*ai.Model, error) { panic(errors.New("models broke")) }
		})
		outcome := chat.outcome(t, chat.compact(t, nil))
		chat.setup.Models.SetProvider(&original)
		expectMatch(t, outcome, `{"status":"faulted","error":{"message":"models broke"}}`)
		if chat.live(t).Compactions != nil {
			t.Fatal("a faulted compaction stayed listed")
		}
		chat.close(t)
	})
}

// eventValue is an event as upstream's plain object: its fields and its type.
func eventValue(t *testing.T, event AgentEvent) map[string]any {
	t.Helper()
	value := must(jsonValue(jsonText(t, event))).(map[string]any)
	value["type"] = event.EventType()
	return value
}

func TestCompactionEventsAndLiveStatus(t *testing.T) {
	t.Run("reports start and end, and the retry backoff in a late joiner's snapshot", func(t *testing.T) {
		chat := openCompaction(t)
		chat.history(t)
		chat.setup.SetSettings(func(settings *HarnessSettings) {
			settings.Retry = &RetryPolicyPatch{Enabled: new(true), MaxRetries: new(2), BaseDelayMs: new(60_000)}
		})
		var mu sync.Mutex
		events := []AgentEvent{}
		stream := must(WatchEvents(testContext, chat.harness, chat.root.Id()))
		stream.Start(func(_ context.Context, batch []AgentEvent) error {
			mu.Lock()
			events = append(events, batch...)
			mu.Unlock()
			return nil
		})
		chat.faux.pushSummary(scriptFailure("overloaded"))
		id := chat.compact(t, nil)
		waitFor(t, func() bool {
			compactions := chat.live(t).Compactions
			return len(compactions) > 0 && compactions[0].Retry != nil
		})
		late := must(WatchEvents(testContext, chat.harness, chat.root.Id()))
		expectLike(t, late.Snapshot.Compactions, jsonText(t, []any{map[string]any{"taskId": id, "reason": "manual", "blocking": false, "attempt": 1, "retry": map[string]any{"at": "$number", "error": "overloaded"}}}))
		must(late.Stop())
		must(chat.harness.AbortTask(testContext, id))
		compactionEvents := func() []map[string]any {
			mu.Lock()
			defer mu.Unlock()
			values := []map[string]any{}
			for _, event := range events {
				if strings.HasPrefix(event.EventType(), "compaction_") {
					values = append(values, eventValue(t, event))
				}
			}
			return values
		}
		waitFor(t, func() bool {
			return slices.ContainsFunc(compactionEvents(), func(value map[string]any) bool { return value["type"] == "compaction_end" })
		})
		expectEqualJSON(t, compactionEvents(), jsonText(t, []any{
			map[string]any{"type": "compaction_start", "taskId": id, "reason": "manual", "blocking": false},
			map[string]any{"type": "compaction_end", "taskId": id, "reason": "manual"},
		}))
		must(stream.Stop())
		chat.close(t)
	})

	t.Run("lists concurrent compactions in task ID order", func(t *testing.T) {
		chat := openCompaction(t)
		chat.history(t)
		first, second := deferred(), deferred()
		chat.faux.pushSummary(scriptGated(deferred(), scriptSummary(), first), scriptGated(deferred(), scriptSummary(), second))
		a := chat.compact(t, nil)
		b := chat.compact(t, nil)
		awaitGate(t, first)
		awaitGate(t, second)
		ids := []durable.TaskId{}
		for _, status := range chat.live(t).Compactions {
			ids = append(ids, status.TaskId)
		}
		expectEqualJSON(t, ids, jsonText(t, []durable.TaskId{a, b}))
		if err := chat.root.Abort(testContext, nil); err != nil {
			t.Fatal(err)
		}
		if chat.live(t).Compactions != nil {
			t.Fatal("aborted compactions stayed listed")
		}
		chat.close(t)
	})
}

// reopenCompaction reopens the Harness over path with the same setup and script.
func reopenCompaction(t *testing.T, path string, chat *compactionChat) *compactionChat {
	t.Helper()
	harness, root := openChat(t, openSqlite(t, path), chat.setup)
	harness.Resume()
	return &compactionChat{harness: harness, root: root, setup: chat.setup, faux: chat.faux}
}

// firstCompactionChat opens a fresh Harness over path with history, as first() in the upstream test.
func firstCompactionChat(t *testing.T, path string) *compactionChat {
	t.Helper()
	setup := chatSetup(t)
	addSection(t, setup.Registry, "preamble", text("You are helpful."), SectionOptions{Tag: new(false)})
	faux := newScript(setup)
	harness, root := openChat(t, openSqlite(t, path), setup)
	setup.SetSettings(func(settings *HarnessSettings) {
		settings.Compaction = manualPolicy()
		settings.Retry = &RetryPolicyPatch{Enabled: new(true), MaxRetries: new(2), BaseDelayMs: new(1)}
	})
	harness.Resume()
	chat := &compactionChat{harness: harness, root: root, setup: setup, faux: faux}
	chat.history(t)
	return chat
}

func TestCompactionRecovery(t *testing.T) {
	t.Run("repeats nothing after reopen once the summary is placed", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "session.sqlite")
		chat := firstCompactionChat(t, path)
		chat.faux.pushSummary(scriptSummary())
		chat.outcome(t, chat.compact(t, nil))
		entries := allEntries(t, chat.root)
		usage := must(durable.Snapshot(testContext, chat.harness, UsageDoc, chat.root.Id()))
		chat.close(t)
		chat = reopenCompaction(t, path, chat)
		if err := chat.harness.WaitForIdle(testContext); err != nil {
			t.Fatal(err)
		}
		if requests := len(chat.faux.summaryRequestsCopy()); requests != 1 {
			t.Fatalf("summary requests = %d, want 1", requests)
		}
		expectEqualJSON(t, allEntries(t, chat.root), jsonText(t, entries))
		expectEqualJSON(t, must(durable.Snapshot(testContext, chat.harness, UsageDoc, chat.root.Id())), jsonText(t, usage))
		inspection := must(chat.harness.Inspect(testContext))
		if len(inspection.Tasks) != 0 || len(inspection.Submissions) != 0 {
			t.Fatalf("inspection = %s", jsonText(t, inspection))
		}
		chat.close(t)
	})

	t.Run("fails an overflow run with its text when its compaction fails after reopen", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "session.sqlite")
		chat := firstCompactionChat(t, path)
		chat.setPolicy(enabledPolicy())
		reached := deferred()
		chat.faux.pushSummary(scriptGated(deferred(), scriptSummary(), reached))
		chat.faux.pushAgent(scriptFailure(overflowText))
		input := submitInput(t, chat.root, sized("u4", 100))
		awaitGate(t, reached)
		generation := chat.live(t).Run.TaskId
		expectMatch(t, must(chat.harness.GetTask(testContext, generation)).State, jsonText(t, map[string]any{"status": "waiting", "checkpoint": map[string]any{"phase": "prepare", "overflow": overflowText}}))
		chat.close(t)
		chat = reopenCompaction(t, path, chat)
		chat.faux.pushSummary(scriptFailure("bad request"))
		settled := must(submissionOfHarness(t, chat.harness, input.Id()).Wait(testContext))
		expectSettled(t, settled, durable.SubmissionUnanswered, "model_error")
		expectEqualJSON(t, settled.Detail, jsonText(t, overflowText))
		chat.close(t)
	})

	t.Run("reruns select and its hook after a crash in select", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "session.sqlite")
		chat := firstCompactionChat(t, path)
		var mu sync.Mutex
		calls := 0
		reached := deferred()
		addHooks(t, chat.setup.Registry, CompactionTask, &CompactionHooks{BeforeCompact: func(ctx context.Context, _ CompactionRequest, _ HookApi) (*CompactionDecision, error) {
			mu.Lock()
			calls++
			call := calls
			mu.Unlock()
			if call == 1 {
				reached.resolve()
				return nil, abortedBy(ctx)
			}
			return nil, nil
		}})
		id := chat.compact(t, nil)
		awaitGate(t, reached)
		chat.close(t)
		chat = reopenCompaction(t, path, chat)
		chat.faux.pushSummary(scriptSummary())
		if outcome := chat.outcome(t, id); outcome.Status != durable.OutcomeCompleted {
			t.Fatalf("outcome = %s", outcome.Status)
		}
		mu.Lock()
		defer mu.Unlock()
		if calls != 2 {
			t.Fatalf("calls = %d, want 2", calls)
		}
		chat.close(t)
	})

	t.Run("resends an interrupted summarization once and counts only the answered attempt", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "session.sqlite")
		chat := firstCompactionChat(t, path)
		reached := deferred()
		chat.faux.pushSummary(scriptGated(deferred(), scriptSummary(), reached))
		id := chat.compact(t, nil)
		awaitGate(t, reached)
		usageBefore := must(durable.Snapshot(testContext, chat.harness, UsageDoc, chat.root.Id())).Models["faux/faux-1"]
		expectMatch(t, must(chat.harness.GetTask(testContext, id)).State, `{"checkpoint":{"phase":"summarize"}}`)
		chat.close(t)
		chat = reopenCompaction(t, path, chat)
		chat.faux.pushSummary(scriptSummary())
		if outcome := chat.outcome(t, id); outcome.Status != durable.OutcomeCompleted {
			t.Fatalf("outcome = %s", outcome.Status)
		}
		requests := chat.faux.summaryRequestsCopy()
		if len(requests) != 2 {
			t.Fatalf("summary requests = %d, want 2", len(requests))
		}
		withoutTimestamps := func(messages []ai.Message) []any {
			values := []any{}
			for _, message := range messages {
				value := must(jsonValue(jsonText(t, message))).(map[string]any)
				delete(value, "timestamp")
				values = append(values, value)
			}
			return values
		}
		expectEqualJSON(t, withoutTimestamps(requests[1].messages), jsonText(t, withoutTimestamps(requests[0].messages)))
		usage := must(durable.Snapshot(testContext, chat.harness, UsageDoc, chat.root.Id())).Models["faux/faux-1"]
		if usage.Input <= usageBefore.Input || usage.Output-usageBefore.Output != 2 {
			t.Fatalf("usage = %+v, before = %+v", usage, usageBefore)
		}
		chat.close(t)
	})

	t.Run("resumes a retry backoff after reopen", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "session.sqlite")
		chat := firstCompactionChat(t, path)
		var mu sync.Mutex
		now := float64(time.Now().UnixMilli())
		chat.setup.SetNow(func() float64 {
			mu.Lock()
			defer mu.Unlock()
			return now
		})
		chat.setup.SetSettings(func(settings *HarnessSettings) {
			settings.Retry = &RetryPolicyPatch{Enabled: new(true), MaxRetries: new(2), BaseDelayMs: new(60_000)}
		})
		chat.faux.pushSummary(scriptFailure("overloaded"))
		id := chat.compact(t, nil)
		waitFor(t, func() bool {
			compactions := chat.live(t).Compactions
			return len(compactions) > 0 && compactions[0].Retry != nil
		})
		expectMatch(t, must(chat.harness.GetTask(testContext, id)).State, `{"checkpoint":{"phase":"retry","attempt":1}}`)
		chat.close(t)
		mu.Lock()
		now += 120_000
		mu.Unlock()
		chat = reopenCompaction(t, path, chat)
		chat.faux.pushSummary(scriptSummary())
		if outcome := chat.outcome(t, id); outcome.Status != durable.OutcomeCompleted {
			t.Fatalf("outcome = %s", outcome.Status)
		}
		if requests := len(chat.faux.summaryRequestsCopy()); requests != 2 {
			t.Fatalf("summary requests = %d, want 2", requests)
		}
		chat.close(t)
	})

	t.Run("keeps a generation waiting on its blocking compaction across reopen", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "session.sqlite")
		chat := firstCompactionChat(t, path)
		reached := deferred()
		chat.faux.pushSummary(scriptGated(deferred(), scriptSummary(), reached))
		// The faux model window is 128k; this blocking threshold needs a small context window.
		policy := blockingPolicy()
		policy.ReserveTokens = new(128_000 - 700)
		chat.setPolicy(policy)
		input := submitInput(t, chat.root, sized("u4", 200))
		awaitGate(t, reached)
		chat.close(t)
		chat = reopenCompaction(t, path, chat)
		chat.faux.pushSummary(scriptSummary())
		chat.faux.pushAgent(scriptAnswer("a4"))
		expectSettled(t, must(submissionOfHarness(t, chat.harness, input.Id()).Wait(testContext)), durable.SubmissionDone, "")
		expectEqualJSON(t, lastN(chat.kinds(t), 3), `["pi.compaction","pi.system","pi.assistant"]`)
		chat.close(t)
	})

	t.Run("keeps a queued summary across reopen and places it at the next boundary", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "session.sqlite")
		chat := firstCompactionChat(t, path)
		reached := deferred()
		chat.faux.pushAgent(scriptGated(deferred(), scriptAnswer("never"), reached))
		input := submitInput(t, chat.root, "busy")
		awaitGate(t, reached)
		chat.faux.pushSummary(scriptSummary())
		outcome := chat.outcome(t, chat.compact(t, nil))
		chat.close(t)
		chat = reopenCompaction(t, path, chat)
		chat.faux.pushAgent(scriptAnswer("answered"))
		expectSettled(t, must(submissionOfHarness(t, chat.harness, input.Id()).Wait(testContext)), durable.SubmissionDone, "")
		expectSettled(t, must(chat.summarySubmission(t, outcome).Wait(testContext)), durable.SubmissionDone, "")
		chat.close(t)
	})
}

func (chat *compactionChat) queuedSummary(t *testing.T, content ...string) durable.Submission {
	t.Helper()
	chat.faux.pushSummary(scriptSummary(content...))
	return chat.summarySubmission(t, chat.outcome(t, chat.compact(t, nil)))
}

type busyRun struct {
	input   durable.Submission
	release func()
}

func (chat *compactionChat) busy(t *testing.T, reply ...scriptStep) busyRun {
	t.Helper()
	step := scriptAnswer("done")
	if len(reply) > 0 {
		step = reply[0]
	}
	gate, reached := deferred(), deferred()
	chat.faux.pushAgent(scriptGated(gate, step, reached))
	input := submitInput(t, chat.root, "busy")
	awaitGate(t, reached)
	return busyRun{input: input, release: gate.resolve}
}

func TestCompactionAndTheInbox(t *testing.T) {
	t.Run("places a reset queued after the summary last, and makes a summary queued after a reset stale", func(t *testing.T) {
		for _, order := range []string{"summary first", "reset first"} {
			chat := openCompaction(t)
			chat.history(t)
			run := chat.busy(t)
			var submission durable.Submission
			if order == "summary first" {
				submission = chat.queuedSummary(t)
				resetTo(t, chat.root, nil)
			} else {
				resetTo(t, chat.root, nil)
				submission = chat.queuedSummary(t)
			}
			run.release()
			must(run.input.Wait(testContext))
			settled := must(submission.Wait(testContext))
			want := durable.SubmissionDone
			if order == "reset first" {
				want = durable.SubmissionUnanswered
			}
			kinds := chat.kinds(t)
			if settled.Status != want || kinds[len(kinds)-1] != "pi.reset" || must(chat.root.Context(testContext)).Head.Kind != "pi.reset" {
				t.Fatalf("%s: settled = %s, kinds = %v", order, settled.Status, kinds)
			}
			chat.close(t)
		}
	})

	t.Run("places a summary left queued by a failed run at the next submission, before its input", func(t *testing.T) {
		chat := openCompaction(t)
		chat.history(t)
		run := chat.busy(t, scriptFailure("bad request"))
		submission := chat.queuedSummary(t)
		run.release()
		expectSettled(t, must(run.input.Wait(testContext)), durable.SubmissionUnanswered, "")
		if record := must(submission.Status(testContext)); record.Status != durable.SubmissionQueued {
			t.Fatalf("summary = %s, want queued", record.Status)
		}
		chat.turn(t, "again", "ok")
		if record := must(submission.Status(testContext)); record.Status != durable.SubmissionDone {
			t.Fatalf("summary = %s, want done", record.Status)
		}
		request := chat.faux.lastAgentRequest().messages
		if !strings.Contains(userText(request[0]), "SUMMARY") || !slices.ContainsFunc(request, func(message ai.Message) bool { return userText(message) == "again" }) {
			t.Fatalf("request = %v", roles(request))
		}
		chat.close(t)
	})

	t.Run("keeps the full retry budget after an overflow compaction", func(t *testing.T) {
		chat := openCompaction(t)
		chat.history(t)
		chat.setPolicy(enabledPolicy())
		chat.faux.pushSummary(scriptSummary())
		chat.faux.pushAgent(scriptFailure("prompt is too long"), scriptFailure("overloaded"), scriptFailure("overloaded"), scriptAnswer("finally"))
		expectSettled(t, must(submitInput(t, chat.root, sized("u4", 100)).Wait(testContext)), durable.SubmissionDone, "")
		chat.close(t)
	})

	t.Run("loses an application edit placed while a compaction summarizes (spec §12)", func(t *testing.T) {
		chat := openCompaction(t)
		chat.history(t)
		u1 := allEntries(t, chat.root)[0]
		gate, reached := deferred(), deferred()
		chat.faux.pushSummary(scriptGated(gate, scriptSummary(), reached))
		id := chat.compact(t, nil)
		awaitGate(t, reached)
		submit(t, chat.root, writeDraft(durable.EntryDraft{Kind: "app.redact", Edits: []durable.ContextEdit{{Target: u1.Id, Action: durable.EditReplace, Messages: []ai.Message{user("REDACTED")}}}}))
		gate.resolve()
		chat.outcome(t, id)
		// The summary was made from the unredacted entry, and the edit's target left the range.
		if !strings.Contains(userText(chat.faux.summaryRequestsCopy()[0].messages[1]), "[User]: u1 ") {
			t.Fatal("the summary request lacks the unredacted entry")
		}
		for _, message := range must(chat.root.Context(testContext)).Messages {
			if userText(message) == "REDACTED" {
				t.Fatal("the redaction survived compaction")
			}
		}
		chat.close(t)
	})

	t.Run("keeps the kept entries mounted in the conversation view", func(t *testing.T) {
		chat := openCompaction(t)
		chat.history(t)
		state := must(chat.root.ViewState(testContext))
		chat.faux.pushSummary(scriptSummary())
		chat.outcome(t, chat.compact(t, nil))
		waitFor(t, func() bool {
			entries := state.Value().Entries
			return len(entries) > 0 && entries[0].Kind == "pi.compaction"
		})
		expectEqualJSON(t, state.Value().Entries, jsonText(t, must(chat.root.Context(testContext)).Entries))
		state.Dispose()
		chat.close(t)
	})
}

func TestCompactionEdgeCases(t *testing.T) {
	t.Run("keeps the one-compaction limit through a retry backoff", func(t *testing.T) {
		chat := openCompaction(t, compactionOptions{contextWindow: 1000})
		chat.history(t)
		chat.setPolicy(blockingPolicy())
		var mu sync.Mutex
		asked := 0
		addHooks(t, chat.setup.Registry, CompactionTask, beforeCompact(func(CompactionRequest) (*CompactionDecision, error) {
			mu.Lock()
			asked++
			mu.Unlock()
			return &CompactionDecision{Decline: true}, nil
		}))
		chat.faux.pushAgent(scriptFailure("overloaded"))
		chat.turn(t, sized("u4", 200), "a4")
		// One blocking compaction, declined; the retried preparation, still above the threshold, started no other.
		mu.Lock()
		defer mu.Unlock()
		if requests := len(chat.faux.agentRequestsCopy()); asked != 1 || requests != 5 {
			t.Fatalf("asked = %d, agent requests = %d", asked, requests)
		}
		chat.close(t)
	})

	t.Run("does not start a background compaction when a manual one was admitted during preparation", func(t *testing.T) {
		chat := openCompaction(t, compactionOptions{contextWindow: 2000})
		chat.history(t)
		chat.setPolicy(backgroundPolicy())
		rendering, release := deferred(), deferred()
		var mu sync.Mutex
		hold := true
		addSection(t, chat.setup.Registry, "slow", func(ctx context.Context, _ durable.PromptInput) (*string, error) {
			mu.Lock()
			holding := hold
			hold = false
			mu.Unlock()
			if holding {
				rendering.resolve()
				if err := release.wait(ctx); err != nil {
					return nil, err
				}
			}
			return new("slow"), nil
		})
		reached := deferred()
		chat.faux.pushSummary(scriptGated(deferred(), scriptSummary(), reached))
		chat.faux.pushAgent(scriptAnswer("a4"))
		input := submitInput(t, chat.root, sized("u4", 100))
		awaitGate(t, rendering)
		manual := chat.compact(t, nil)
		awaitGate(t, reached)
		release.resolve()
		expectSettled(t, must(input.Wait(testContext)), durable.SubmissionDone, "")
		tasks := chat.compactionTasks(t)
		// A background compaction would have sent its own summarization request.
		if len(tasks) != 1 || tasks[0].Id != manual || len(chat.faux.summaryRequestsCopy()) != 1 {
			t.Fatalf("tasks = %s", jsonText(t, tasks))
		}
		must(chat.harness.AbortTask(testContext, manual))
		chat.close(t)
	})

	t.Run("starts no background compaction after a blocking one in the same generation", func(t *testing.T) {
		chat := openCompaction(t, compactionOptions{contextWindow: 2000})
		chat.history(t)
		// Blocking at 1500, background at 200: the kept part stays above the background threshold.
		policy := backgroundPolicy()
		policy.BackgroundTokens = new(1300)
		policy.KeepRecentTokens = new(400)
		chat.setPolicy(policy)
		chat.faux.pushSummary(scriptSummary())
		chat.turn(t, sized("u4", 1000), "a4")
		if requests, tasks := len(chat.faux.summaryRequestsCopy()), len(chat.compactionTasks(t)); requests != 1 || tasks != 0 {
			t.Fatalf("summary requests = %d, tasks = %d", requests, tasks)
		}
		chat.close(t)
	})

	t.Run("sends the request anyway when its blocking compaction faults", func(t *testing.T) {
		chat := openCompaction(t, compactionOptions{contextWindow: 1000})
		chat.history(t)
		chat.setPolicy(blockingPolicy())
		// Upstream replaces Models.completeSimple with a throwing function. Go's CompleteSimple has no throw path, so the
		// summarization answers a value that is not strict JSON and the compaction's commit faults the task instead.
		original := *chat.setup.Faux.Provider()
		withProvider(chat.setup, func(provider *ai.ModelsProvider) {
			provider.StreamSimple = func(ctx context.Context, model *ai.Model, request ai.TranscriptContext, options ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
				if isSummaryRequest(request.Messages()) {
					final := fauxMessage("SUMMARY", ai.StopReasonStop)
					final.Usage.Cost.Total = math.NaN()
					return streamOf(fauxMessage("", ai.StopReasonPending), 0, final), nil
				}
				return original.StreamSimple(ctx, model, request, options)
			}
		})
		chat.turn(t, sized("u4", 200), "a4")
		chat.setup.Models.SetProvider(&original)
		if slices.Contains(chat.kinds(t), "pi.compaction") || chat.live(t).Compactions != nil {
			t.Fatal("a faulted compaction left a summary or a status")
		}
		chat.close(t)
	})

	t.Run("fails an overflow run with its text when its compaction is aborted directly or itself overflows", func(t *testing.T) {
		for _, how := range []string{"abort", "overflow"} {
			chat := openCompaction(t)
			chat.history(t)
			chat.setPolicy(enabledPolicy())
			reached := deferred()
			if how == "abort" {
				chat.faux.pushSummary(scriptGated(deferred(), scriptSummary(), reached))
			} else {
				chat.faux.pushSummary(scriptFailure("prompt is too long for the summary"))
			}
			chat.faux.pushAgent(scriptFailure(overflowText))
			input := submitInput(t, chat.root, sized("u4", 100))
			if how == "abort" {
				awaitGate(t, reached)
				must(chat.harness.AbortTask(testContext, chat.compactionTasks(t)[0].Id))
			}
			settled := must(input.Wait(testContext))
			expectSettled(t, settled, durable.SubmissionUnanswered, "model_error")
			expectEqualJSON(t, settled.Detail, jsonText(t, overflowText))
			if chat.live(t).Compactions != nil {
				t.Fatalf("%s: a compaction stayed listed", how)
			}
			chat.close(t)
		}
	})

	t.Run("treats a length stop as an ordinary answer, not an overflow", func(t *testing.T) {
		chat := openCompaction(t)
		chat.history(t)
		chat.setPolicy(enabledPolicy())
		chat.faux.pushAgent(fixed(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("cut short")}, StopReason: "length", ErrorMessage: overflowText}))
		expectSettled(t, must(submitInput(t, chat.root, "go").Wait(testContext)), durable.SubmissionDone, "")
		if requests := len(chat.faux.summaryRequestsCopy()); requests != 0 {
			t.Fatalf("summary requests = %d, want 0", requests)
		}
		chat.close(t)
	})
}

// eventLog collects event types, or batches of them, from a stream.
type eventLog struct {
	mu      sync.Mutex
	batches [][]string
}

func watchEventLog(t *testing.T, chat *compactionChat) (*eventLog, *AgentEventStream) {
	t.Helper()
	log := &eventLog{}
	stream := must(WatchEvents(testContext, chat.harness, chat.root.Id()))
	stream.Start(func(_ context.Context, batch []AgentEvent) error {
		types := []string{}
		for _, event := range batch {
			types = append(types, event.EventType())
		}
		log.mu.Lock()
		log.batches = append(log.batches, types)
		log.mu.Unlock()
		return nil
	})
	return log, stream
}

func (log *eventLog) flat() []string {
	log.mu.Lock()
	defer log.mu.Unlock()
	types := []string{}
	for _, batch := range log.batches {
		types = append(types, batch...)
	}
	return types
}

func (log *eventLog) batchWith(eventType string) []string {
	log.mu.Lock()
	defer log.mu.Unlock()
	for _, batch := range log.batches {
		if slices.Contains(batch, eventType) {
			return slices.Clone(batch)
		}
	}
	return nil
}

func TestCompactionEdgeCasesLater(t *testing.T) {
	t.Run("orders the events of a summary placed at once", func(t *testing.T) {
		chat := openCompaction(t)
		chat.history(t)
		log, stream := watchEventLog(t, chat)
		chat.faux.pushSummary(scriptSummary())
		chat.outcome(t, chat.compact(t, nil))
		waitFor(t, func() bool { return slices.Contains(log.flat(), "compaction_end") })
		events := log.flat()
		end := slices.Index(events, "compaction_end")
		expectEqualJSON(t, events[end-2:end+3], `["message_start","message_end","compaction_end","submission","usage_changed"]`)
		must(stream.Stop())
		chat.close(t)
	})

	t.Run("puts compaction_start last in the batch of the preparation commit", func(t *testing.T) {
		chat := openCompaction(t, compactionOptions{contextWindow: 2000})
		chat.history(t)
		chat.setPolicy(backgroundPolicy())
		log, stream := watchEventLog(t, chat)
		// A new section makes the preparation commit append a system entry next to the compaction's status.
		addSection(t, chat.setup.Registry, "extra", text("extra"))
		chat.faux.pushSummary(scriptGated(deferred(), scriptSummary(), nil))
		chat.turn(t, sized("u4", 100), "a4")
		waitFor(t, func() bool { return log.batchWith("compaction_start") != nil })
		expectEqualJSON(t, log.batchWith("compaction_start"), `["message_start","message_end","compaction_start"]`)
		must(stream.Stop())
		must(chat.harness.AbortTask(testContext, chat.compactionTasks(t)[0].Id))
		chat.close(t)
	})

	t.Run("keeps a background summary queued through a retry backoff while a blocking compaction wins", func(t *testing.T) {
		chat := openCompaction(t, compactionOptions{contextWindow: 2000})
		chat.history(t)
		chat.setPolicy(backgroundPolicy())
		chat.setup.SetSettings(func(settings *HarnessSettings) {
			settings.Retry = &RetryPolicyPatch{Enabled: new(true), MaxRetries: new(2), BaseDelayMs: new(300)}
		})
		summaryGate, summaryReached := deferred(), deferred()
		chat.faux.pushSummary(scriptGated(summaryGate, scriptSummary("BACKGROUND"), summaryReached), scriptSummary("BLOCKING"))
		failed := deferred()
		chat.faux.pushAgent(func(scriptRequest) (ai.FauxResponse, error) {
			failed.resolve()
			return ai.FauxResponse{StopReason: "error", ErrorMessage: "overloaded"}, nil
		}, scriptAnswer("a4"))
		input := submitInput(t, chat.root, sized("u4", 100))
		awaitGate(t, failed)
		awaitGate(t, summaryReached)
		// During the backoff: the background summary queues, and the thresholds drop so the retry blocks.
		waitFor(t, func() bool {
			generation := chat.live(t).Generation
			return generation != nil && generation.Retry != nil
		})
		background := chat.compactionTasks(t)[0]
		summaryGate.resolve()
		submission := chat.summarySubmission(t, chat.outcome(t, background.Id))
		if record := must(submission.Status(testContext)); record.Status != durable.SubmissionQueued {
			t.Fatalf("summary = %s, want queued", record.Status)
		}
		policy := backgroundPolicy()
		policy.ReserveTokens = new(1500)
		policy.KeepRecentTokens = new(50)
		chat.setPolicy(policy)
		expectSettled(t, must(input.Wait(testContext)), durable.SubmissionDone, "")
		if first := userText(chat.faux.lastAgentRequest().messages[0]); !strings.Contains(first, "BLOCKING") {
			t.Fatalf("request starts with %q", first)
		}
		expectSettled(t, must(submission.Wait(testContext)), durable.SubmissionUnanswered, "stale")
		chat.close(t)
	})

	t.Run("summarizes the previous summary in a second compaction", func(t *testing.T) {
		chat := openCompaction(t)
		chat.history(t)
		chat.faux.pushSummary(scriptSummary("FIRST"))
		chat.outcome(t, chat.compact(t, nil))
		chat.turn(t, sized("u4", 100), sized("a4", 100))
		chat.turn(t, sized("u5", 100), sized("a5", 100))
		chat.faux.pushSummary(scriptSummary("SECOND"))
		chat.outcome(t, chat.compact(t, nil))
		prompt := userText(chat.faux.summaryRequestsCopy()[1].messages[1])
		if !regexp.MustCompile(`^<conversation>\n\[User\]: The conversation history before this point was compacted`).MatchString(prompt) || !strings.Contains(prompt, "FIRST") {
			t.Fatalf("prompt = %q", prompt)
		}
		if text := chat.firstContextText(t); !strings.Contains(text, "SECOND") {
			t.Fatalf("context starts with %q", text)
		}
		chat.close(t)
	})

	t.Run("leaves no usage, submission, summary, or outcome when the classifying commit is rejected", func(t *testing.T) {
		store := newControlledStorage()
		chat := openCompaction(t, compactionOptions{storage: store})
		chat.history(t)
		usage := func() string {
			return jsonText(t, must(durable.Snapshot(testContext, chat.harness, UsageDoc, chat.root.Id())).Models["faux/faux-1"])
		}
		before := usage()
		chat.faux.pushSummary(func(request scriptRequest) (ai.FauxResponse, error) {
			store.failNextCommit(durable.NewStorageRejected("rejected", nil))
			return scriptSummary()(request)
		})
		id := chat.compact(t, nil)
		if outcome := chat.outcome(t, id); outcome.Status != durable.OutcomeFaulted {
			t.Fatalf("outcome = %s, want faulted", outcome.Status)
		}
		if usage() != before || slices.Contains(chat.kinds(t), "pi.compaction") || len(must(chat.harness.Inspect(testContext)).Submissions) != 0 {
			t.Fatal("a rejected classifying commit left usage, a summary, or a submission")
		}
		if record := must(store.SubmissionByRequest(testContext, chat.root.Id(), "compaction:"+itoa(int(id)))); record != nil {
			t.Fatalf("submission = %s", jsonText(t, record))
		}
		if chat.live(t).Compactions != nil {
			t.Fatal("a faulted compaction stayed listed")
		}
		chat.close(t)
	})
}

type blockingRunState struct {
	input    durable.Submission
	gate     *deferredGate
	blocking durable.TaskId
}

// blockingRun starts a run whose generation waits on a blocking compaction whose summary is held.
func (chat *compactionChat) blockingRun(t *testing.T) blockingRunState {
	t.Helper()
	chat.history(t)
	chat.setPolicy(blockingPolicy())
	gate, reached := deferred(), deferred()
	chat.faux.pushSummary(scriptGated(gate, scriptSummary("BLOCKING"), reached))
	input := submitInput(t, chat.root, sized("u4", 200))
	awaitGate(t, reached)
	return blockingRunState{input: input, gate: gate, blocking: chat.compactionTasks(t)[0].Id}
}

func TestBlockingAndManualCompaction(t *testing.T) {
	t.Run("places a manual summary selected before the blocking one landed; its equal cut replaces it", func(t *testing.T) {
		chat := openCompaction(t, compactionOptions{contextWindow: 1000})
		run := chat.blockingRun(t)
		// Selected from the same context as the blocking compaction, so it cuts at the same entry.
		submission := chat.queuedSummary(t, "MANUAL")
		if record := must(submission.Status(testContext)); record.Status != durable.SubmissionQueued {
			t.Fatalf("manual = %s, want queued", record.Status)
		}
		chat.faux.pushAgent(scriptAnswer("a4"))
		run.gate.resolve()
		expectSettled(t, must(run.input.Wait(testContext)), durable.SubmissionDone, "")
		// The request after the blocking compaction used its summary; the manual one landed at the final boundary.
		if first := userText(chat.faux.lastAgentRequest().messages[0]); !strings.Contains(first, "BLOCKING") {
			t.Fatalf("request starts with %q", first)
		}
		expectSettled(t, must(submission.Wait(testContext)), durable.SubmissionDone, "")
		markers := []durable.EntryRecord{}
		for _, record := range allEntries(t, chat.root) {
			if record.Kind == "pi.compaction" {
				markers = append(markers, record)
			}
		}
		if len(markers) != 2 || *markers[1].Head != *markers[0].Head {
			t.Fatalf("markers = %s", jsonText(t, markers))
		}
		messages := must(chat.root.Context(testContext)).Messages
		if !strings.Contains(userText(messages[0]), "MANUAL") || slices.ContainsFunc(messages, func(message ai.Message) bool { return strings.Contains(userText(message), "BLOCKING") }) {
			t.Fatalf("context starts with %q", userText(messages[0]))
		}
		chat.close(t)
	})

	t.Run("finds nothing to compact for a manual compaction selected after the blocking summary landed", func(t *testing.T) {
		chat := openCompaction(t, compactionOptions{contextWindow: 1000})
		run := chat.blockingRun(t)
		answerGate, answerReached := deferred(), deferred()
		chat.faux.pushAgent(scriptGated(answerGate, scriptAnswer("a4"), answerReached))
		run.gate.resolve()
		awaitGate(t, answerReached)
		expectEqualJSON(t, chat.outcome(t, chat.compact(t, nil)), `{"status":"completed","result":{}}`)
		if requests := len(chat.faux.summaryRequestsCopy()); requests != 1 {
			t.Fatalf("summary requests = %d, want 1", requests)
		}
		answerGate.resolve()
		expectSettled(t, must(run.input.Wait(testContext)), durable.SubmissionDone, "")
		chat.close(t)
	})

	t.Run("aborts the run and both compactions on Esc and appends nothing", func(t *testing.T) {
		chat := openCompaction(t, compactionOptions{contextWindow: 1000})
		run := chat.blockingRun(t)
		manualReached := deferred()
		chat.faux.pushSummary(scriptGated(deferred(), scriptSummary("MANUAL"), manualReached))
		manual := chat.compact(t, nil)
		awaitGate(t, manualReached)
		blocking := []bool{}
		for _, status := range chat.live(t).Compactions {
			blocking = append(blocking, status.Blocking)
		}
		expectEqualJSON(t, blocking, `[true,false]`)
		if err := chat.root.Abort(testContext, nil); err != nil {
			t.Fatal(err)
		}
		expectSettled(t, must(run.input.Wait(testContext)), durable.SubmissionUnanswered, "aborted")
		if chat.outcome(t, run.blocking).Status != durable.OutcomeAborted || chat.outcome(t, manual).Status != durable.OutcomeAborted {
			t.Fatal("a compaction survived Esc")
		}
		state := chat.live(t)
		if state.Compactions != nil || state.Run != nil || slices.Contains(chat.kinds(t), "pi.compaction") || len(must(chat.harness.Inspect(testContext)).Submissions) != 0 {
			t.Fatalf("live = %s", jsonText(t, state))
		}
		chat.close(t)
	})

	t.Run("places a summary that survived Esc at the next submission, before its input", func(t *testing.T) {
		chat := openCompaction(t)
		chat.history(t)
		reached := deferred()
		chat.faux.pushAgent(scriptGated(deferred(), scriptAnswer("never"), reached))
		busy := submitInput(t, chat.root, "busy")
		awaitGate(t, reached)
		submission := chat.queuedSummary(t)
		if err := chat.root.Abort(testContext, nil); err != nil {
			t.Fatal(err)
		}
		expectSettled(t, must(busy.Wait(testContext)), durable.SubmissionUnanswered, "aborted")
		if record := must(submission.Status(testContext)); record.Status != durable.SubmissionQueued {
			t.Fatalf("summary = %s, want queued", record.Status)
		}
		chat.turn(t, "u5", "a5")
		if record := must(submission.Status(testContext)); record.Status != durable.SubmissionDone {
			t.Fatalf("summary = %s, want done", record.Status)
		}
		request := chat.faux.lastAgentRequest().messages
		if !strings.Contains(userText(request[0]), "SUMMARY") || !slices.ContainsFunc(request, func(message ai.Message) bool { return userText(message) == "u5" }) {
			t.Fatalf("request = %v", roles(request))
		}
		expectEqualJSON(t, lastN(chat.kinds(t), 4), `["pi.compaction","pi.user","pi.system","pi.assistant"]`)
		chat.close(t)
	})
}

func TestCompactionPinning(t *testing.T) {
	t.Run("keeps the pinned model through a retry and advances the live attempt", func(t *testing.T) {
		setup := chatSetup(t, ai.FauxConfig{Models: []ai.FauxModelDefinition{{ID: "faux-1", ContextWindow: 100_000, MaxTokens: 900}, {ID: "faux-2", ContextWindow: 100_000, MaxTokens: 900}}})
		chat := openCompaction(t, compactionOptions{setup: setup})
		chat.history(t)
		attempt := -1
		chat.faux.pushSummary(func(scriptRequest) (ai.FauxResponse, error) {
			// Switched during the attempt: the retry still uses the pinned model.
			if err := chat.root.Configure(testContext, AgentChange{Model: SetTo(durable.ModelRef{Provider: "faux", ModelId: "faux-2"})}); err != nil {
				return ai.FauxResponse{}, err
			}
			return ai.FauxResponse{StopReason: "error", ErrorMessage: "overloaded"}, nil
		}, func(request scriptRequest) (ai.FauxResponse, error) {
			if compactions := chat.live(t).Compactions; len(compactions) > 0 {
				attempt = compactions[0].Attempt
			}
			return scriptSummary()(request)
		})
		if outcome := chat.outcome(t, chat.compact(t, nil)); outcome.Status != durable.OutcomeCompleted {
			t.Fatalf("outcome = %s", outcome.Status)
		}
		models := []string{}
		for _, request := range chat.faux.summaryRequestsCopy() {
			models = append(models, request.model)
		}
		expectEqualJSON(t, models, `["faux-1","faux-1"]`)
		if attempt != 2 {
			t.Fatalf("attempt = %d, want 2", attempt)
		}
		usage := must(durable.Snapshot(testContext, chat.harness, UsageDoc, chat.root.Id())).Models
		if _, ok := usage["faux/faux-1"]; len(usage) != 1 || !ok {
			t.Fatalf("usage models = %s", jsonText(t, usage))
		}
		chat.close(t)
	})

	t.Run("shows a late joiner a compaction that is summarizing", func(t *testing.T) {
		chat := openCompaction(t)
		chat.history(t)
		reached := deferred()
		chat.faux.pushSummary(scriptGated(deferred(), scriptSummary(), reached))
		id := chat.compact(t, nil)
		awaitGate(t, reached)
		stream := must(WatchEvents(testContext, chat.harness, chat.root.Id()))
		expectEqualJSON(t, stream.Snapshot.Compactions, jsonText(t, []any{map[string]any{"taskId": id, "reason": "manual", "blocking": false, "attempt": 1}}))
		must(stream.Stop())
		must(chat.harness.AbortTask(testContext, id))
		chat.close(t)
	})

	for _, variant := range []struct {
		name     string
		response ai.FauxResponse
	}{
		{"a stop whose input exceeds the window", ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("fine")}}},
		{"a length stop that fills the window without output", ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("")}, StopReason: "length"}},
	} {
		t.Run("treats silent overflow as an ordinary answer: "+variant.name, func(t *testing.T) {
			chat := openCompaction(t, compactionOptions{contextWindow: 300})
			chat.history(t)
			// Thresholds out of reach, so only overflow classification could compact.
			chat.setPolicy(&CompactionPolicyPatch{Enabled: new(true), ReserveTokens: new(-100_000), KeepRecentTokens: new(150), BackgroundTokens: new(0)})
			chat.faux.pushAgent(fixed(variant.response))
			expectSettled(t, must(submitInput(t, chat.root, "go").Wait(testContext)), durable.SubmissionDone, "")
			entries := allEntries(t, chat.root)
			message := entries[len(entries)-1].Model[0].(ai.AssistantMessage)
			if message.Usage.Input < 300 || len(chat.faux.summaryRequestsCopy()) != 0 {
				t.Fatalf("input = %d, summary requests = %d", message.Usage.Input, len(chat.faux.summaryRequestsCopy()))
			}
			chat.close(t)
		})
	}

	t.Run("sends the request when a blocking compaction finds nothing under a policy changed after preparation", func(t *testing.T) {
		chat := openCompaction(t, compactionOptions{contextWindow: 1000})
		chat.history(t)
		chat.setPolicy(blockingPolicy())
		var mu sync.Mutex
		changed := false
		addSection(t, chat.setup.Registry, "policy", func(context.Context, durable.PromptInput) (*string, error) {
			// Rendering runs after preparation read the policy; the compaction reads this one.
			mu.Lock()
			defer mu.Unlock()
			if !changed {
				changed = true
				policy := blockingPolicy()
				policy.KeepRecentTokens = new(100_000)
				chat.setPolicy(policy)
			}
			return new("p"), nil
		})
		chat.turn(t, sized("u4", 200), "a4")
		if tasks, requests := len(chat.compactionTasks(t)), len(chat.faux.summaryRequestsCopy()); tasks != 0 || requests != 0 || slices.Contains(chat.kinds(t), "pi.compaction") {
			t.Fatalf("tasks = %d, summary requests = %d", tasks, requests)
		}
		chat.close(t)
	})
}

func TestBlockedCompaction(t *testing.T) {
	t.Run("survives reopen blocked and is orphaned on abort with its status removed", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "session.sqlite")
		setup := chatSetup(t)
		harness, root := openChat(t, openSqlite(t, path), setup)
		// A compaction stored by a newer version than this process registers, for example after a downgrade.
		newerDefinition := *CompactionTask.Definition
		newerDefinition.Version = 2
		newer := durable.DefineTask(newerDefinition)
		rootId := root.Id()
		id := commitValue(t, root, func(tx durable.Tx) (durable.TaskId, error) {
			taskId, err := durable.CreateTask(tx, newer, CompactionInput{Reason: "manual"}, durable.TaskOptions{Ownership: durable.TaskOwnership{Kind: durable.TaskOwnedByConversation}})
			if err != nil {
				return 0, err
			}
			live, err := docDraft(tx, LiveDoc, rootId)
			if err != nil {
				return 0, err
			}
			return taskId, setJSON(live, "compactions", []CompactionStatus{{TaskId: taskId, Reason: "manual", Blocking: false, Attempt: 1}})
		})
		closeHarness(t, harness)
		harness, root = openChat(t, openSqlite(t, path), setup)
		harness.Resume()
		chat := &compactionChat{harness: harness, root: root, setup: setup, faux: newScript(setup)}
		log, stream := watchEventLog(t, chat)
		inspection := must(chat.harness.Inspect(testContext))
		index := slices.IndexFunc(inspection.Tasks, func(task TaskInspection) bool { return task.Record.Id == id })
		if state := inspection.Tasks[index].State; state.Kind != TaskInspectionBlocked || state.Reason != "task_too_old" || state.Error != nil {
			t.Fatalf("state = %+v", state)
		}
		must(chat.harness.AbortTask(testContext, id))
		expectEqualJSON(t, must(chat.harness.WaitForTask(testContext, id)).State.Outcome, `{"status":"orphaned","reason":"task_too_old"}`)
		if chat.live(t).Compactions != nil {
			t.Fatal("an orphaned compaction stayed listed")
		}
		waitFor(t, func() bool { return slices.Contains(log.flat(), "compaction_end") })
		must(stream.Stop())
		chat.close(t)
	})
}

func TestContextContributions(t *testing.T) {
	t.Run("apply edits carried by an older head marker in the range", func(t *testing.T) {
		chat := openCompaction(t)
		rootId := chat.root.Id()
		type noteIds struct{ a, b, c durable.EntryId }
		ids := commitValue(t, chat.root, func(tx durable.Tx) (noteIds, error) {
			a, err := tx.AppendEntry(rootId, durable.EntryDraft{Kind: "app.note", Model: []ai.Message{user("a")}})
			if err != nil {
				return noteIds{}, err
			}
			b, err := tx.AppendEntry(rootId, durable.EntryDraft{Kind: "app.note", Model: []ai.Message{user("b")}})
			if err != nil {
				return noteIds{}, err
			}
			// An older marker that omits b, then a newer one whose range still contains the older marker.
			if _, err := tx.AppendEntry(rootId, durable.EntryDraft{Kind: "app.head", Head: &a.Id, Edits: []durable.ContextEdit{{Target: b.Id, Action: durable.EditOmit}}}); err != nil {
				return noteIds{}, err
			}
			c, err := tx.AppendEntry(rootId, durable.EntryDraft{Kind: "app.note", Model: []ai.Message{user("c")}})
			if err != nil {
				return noteIds{}, err
			}
			if _, err := tx.AppendEntry(rootId, durable.EntryDraft{Kind: "app.head", Head: &a.Id, Model: []ai.Message{user("H")}}); err != nil {
				return noteIds{}, err
			}
			return noteIds{a.Id, b.Id, c.Id}, nil
		})
		view := must(chat.root.Context(testContext))
		entryIds := []durable.EntryId{}
		for _, record := range view.Entries[1:] {
			entryIds = append(entryIds, record.Id)
		}
		expectEqualJSON(t, entryIds, jsonText(t, []durable.EntryId{ids.a, ids.b, ids.c}))
		contributions := [][]string{}
		for _, messages := range view.Contributions {
			texts := []string{}
			for _, message := range messages {
				texts = append(texts, userText(message))
			}
			contributions = append(contributions, texts)
		}
		expectEqualJSON(t, contributions, `[["H"],["a"],[],["c"]]`)
		texts := []string{}
		for _, message := range view.Messages {
			texts = append(texts, userText(message))
		}
		expectEqualJSON(t, texts, `["H","a","c"]`)
		// The summarizer sees the same contributions: the omitted entry stays out.
		chat.setKeep(1)
		chat.faux.pushSummary(scriptSummary())
		chat.outcome(t, chat.compact(t, nil))
		if prompt := userText(chat.faux.summaryRequestsCopy()[0].messages[1]); !strings.Contains(prompt, "<conversation>\n[User]: H\n\n[User]: a\n</conversation>") {
			t.Fatalf("prompt = %q", prompt)
		}
		chat.close(t)
	})
}
