package coding

import (
	"context"
	"reflect"
	"runtime"
	"slices"
	"sync"
	"testing"
	"weak"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/sessionentry"
)

// upstream: agent-session.ts _handleAgentEvent and _replaceMessageInPlace; agent-loop.js message_start, message_end, turn_end and agent_end hand the transcript's own message objects to the extensions, and appendMessage(event.message) stores that object.
//
// An extension process cannot hold the host's objects, so the Session numbers each message and sends the number with every payload that holds it, and the entry message_end persists is named by the same number (sessionentry.MessageSeq). A handler after a message_end replacement holds the replacement, which is no object Pi shares, so its request carries no number.
func TestSessionNumbersTheMessagesExtensionEventsShare(t *testing.T) {
	type seen struct {
		event string
		role  string
		seqs  []uint64
	}
	var mu sync.Mutex
	var log []seen
	record := func(event string, roles func(args ...any) string) extension.HandlerFn {
		return func(args ...any) (any, error) {
			ctx, _ := args[1].(context.Context)
			mu.Lock()
			defer mu.Unlock()
			log = append(log, seen{event: event, role: roles(args...), seqs: slices.Clone(extension.MessageSequences(ctx))})
			return nil, nil
		}
	}
	role := func(args ...any) string {
		switch e := args[0].(type) {
		case extension.MessageStartEvent:
			return e.Message.Role()
		case extension.MessageEndEvent:
			return e.Message.Role()
		}
		return ""
	}
	none := func(...any) string { return "" }
	editor := func(args ...any) (any, error) {
		message := args[0].(extension.MessageEndEvent).Message
		if message.User == nil {
			return nil, nil
		}
		replacement := *message.User
		replacement.Content = ai.UserText("edited")
		out := extension.AgentMessage(agent.AgentMessage{User: &replacement})
		return &extension.MessageEndEventResult{Message: &out}, nil
	}
	h := newRecoveryHarness(t, harnessOptions{extension: extension.Extension{Path: "/ext/numbers", Handlers: map[string][]extension.HandlerFn{
		"message_start": {record("message_start", role)},
		"message_end": {
			record("message_end/1", role),
			editor,
			record("message_end/3", role),
		},
		"turn_end":  {record("turn_end", none)},
		"agent_end": {record("agent_end", none)},
	}}}, fauxReply("hello", ai.StopReasonStop, 0))
	if _, err := h.session.Send(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}

	find := func(event, role string) seen {
		t.Helper()
		for _, s := range log {
			if s.event == event && s.role == role {
				return s
			}
		}
		t.Fatalf("no %s for %q in %+v", event, role, log)
		return seen{}
	}
	userStart, userEnd, userAfterEdit := find("message_start", "user"), find("message_end/1", "user"), find("message_end/3", "user")
	assistantStart, assistantEnd, assistantAfter := find("message_start", "assistant"), find("message_end/1", "assistant"), find("message_end/3", "assistant")
	if len(userStart.seqs) != 1 || userStart.seqs[0] == 0 {
		t.Fatalf("user message_start sequences = %v, want one number", userStart.seqs)
	}
	user := userStart.seqs[0]
	if !reflect.DeepEqual(userEnd.seqs, []uint64{user}) {
		t.Errorf("user message_end sequences = %v, want the message_start's %d", userEnd.seqs, user)
	}
	if len(userAfterEdit.seqs) != 0 {
		t.Errorf("a handler after a replacement got sequences %v, want none: it holds the replacement, which Pi shares with nothing", userAfterEdit.seqs)
	}
	if len(assistantStart.seqs) != 0 {
		t.Errorf("assistant message_start sequences = %v, want none: it carries the partial message", assistantStart.seqs)
	}
	if len(assistantEnd.seqs) != 1 || assistantEnd.seqs[0] == 0 || assistantEnd.seqs[0] == user {
		t.Fatalf("assistant message_end sequences = %v, want a number of its own", assistantEnd.seqs)
	}
	assistantSeq := assistantEnd.seqs[0]
	if !reflect.DeepEqual(assistantAfter.seqs, []uint64{assistantSeq}) {
		t.Errorf("a handler after no replacement got %v, want %v", assistantAfter.seqs, []uint64{assistantSeq})
	}
	if turn := find("turn_end", ""); !reflect.DeepEqual(turn.seqs, []uint64{assistantSeq}) {
		t.Errorf("turn_end sequences = %v, want the assistant message's %d and no tool results", turn.seqs, assistantSeq)
	}
	// The run's messages are the system message the session starts with, the prompt and the reply.
	end := find("agent_end", "")
	if len(end.seqs) != 3 || end.seqs[0] == 0 || end.seqs[1] != user || end.seqs[2] != assistantSeq {
		t.Errorf("agent_end sequences = %v, want a system message's, %d and %d", end.seqs, user, assistantSeq)
	}
	system := end.seqs[0]

	// The entries message_end persisted are named by the same numbers, in the order they were written.
	var ids []string
	for _, entry := range h.entries("message") {
		ids = append(ids, entry.Base().ID)
	}
	pairs, next := h.session.Inner().MessageEntriesAfter(0)
	if len(ids) != 3 {
		t.Fatalf("message entries = %v, want the system message, the prompt and the reply", ids)
	}
	want := []sessionentry.MessageSeq{{ID: ids[0], Seq: system}, {ID: ids[1], Seq: user}, {ID: ids[2], Seq: assistantSeq}}
	if !reflect.DeepEqual(pairs, want) || next != 3 {
		t.Errorf("message entries = %+v (cursor %d) for entries %v, want %+v", pairs, next, ids, want)
	}
}

// The numbers belong to a message and to nothing it looks like, and a bounded set is kept.
func TestMessageSequencesNumberMessagesByIdentity(t *testing.T) {
	var sequences messageSequences
	first := agent.AgentMessage{User: &agent.UserMessage{Role: "user", Content: ai.UserText("same")}}
	lookalike := agent.AgentMessage{User: &agent.UserMessage{Role: "user", Content: ai.UserText("same")}}
	a, b := sequences.Sequence(first), sequences.Sequence(lookalike)
	if a == 0 || b == 0 || a == b {
		t.Fatalf("sequences %d and %d: two messages that look alike are two messages", a, b)
	}
	if got := sequences.Sequence(first); got != a {
		t.Errorf("the same message got %d the second time, want %d", got, a)
	}
	if got := sequences.Sequence(agent.AgentMessage{}); got != 0 {
		t.Errorf("an empty message got %d, want 0", got)
	}

	// A copy nobody made through the agent loop is another message.
	envelope := first
	envelopeUser := *first.User
	envelope.User = &envelopeUser
	if got := sequences.Known(envelope); got != 0 {
		t.Errorf("a copy that is no start envelope got %d, want 0", got)
	}

	// A turn_end carries copies of its tool results: they are known by the call id of the newest one.
	result := agent.AgentMessage{ToolResult: &agent.ToolResultMessage{Role: "toolResult", ToolCallID: "call_1"}}
	n := sequences.Sequence(result)
	copied := agent.AgentMessage{ToolResult: &agent.ToolResultMessage{Role: "toolResult", ToolCallID: "call_1"}}
	if got := sequences.Known(copied); got != n {
		t.Errorf("a tool result copy got %d, want %d", got, n)
	}
	again := agent.AgentMessage{ToolResult: &agent.ToolResultMessage{Role: "toolResult", ToolCallID: "call_1"}}
	if got := sequences.Sequence(again); got == n {
		t.Errorf("a later tool result with the same call id got the earlier one's number %d", n)
	}
	if got := sequences.Known(copied); got == n {
		t.Errorf("the call id still names the earlier result %d after a newer one", n)
	}

	for range messageSequenceLimit + 10 {
		sequences.Sequence(agent.AgentMessage{User: &agent.UserMessage{Role: "user"}})
	}
	if len(sequences.byMessage) > messageSequenceLimit {
		t.Errorf("%d messages kept, want at most %d", len(sequences.byMessage), messageSequenceLimit)
	}
	if got := sequences.Known(first); got != 0 {
		t.Errorf("the oldest message is still numbered %d after the bound", got)
	}
}

// upstream: agent-session.ts _entryIdsByMessage is a WeakMap, so the messages of a session that was replaced are collectable.
//
// A Session that numbers messages holds them strongly (the number is looked up by the message), so a session switch (/new, /resume, fork) must release them: the table is cleared when the inner session is replaced, and the counter goes on, so a number is never reused for another message.
func TestReplaceInnerReleasesTheNumberedMessages(t *testing.T) {
	var mu sync.Mutex
	var user weak.Pointer[agent.UserMessage]
	h := newRecoveryHarness(t, harnessOptions{extension: extension.Extension{Path: "/ext/release", Handlers: map[string][]extension.HandlerFn{
		"message_end": {func(args ...any) (any, error) {
			// Asking for the numbers is what numbers the message (a Node runtime does it when it sends them).
			extension.MessageSequences(args[1].(context.Context))
			if message := args[0].(extension.MessageEndEvent).Message; message.User != nil {
				mu.Lock()
				user = weak.Make(message.User)
				mu.Unlock()
			}
			return nil, nil
		}},
	}}}, fauxReply("hello", ai.StopReasonStop, 0))
	if _, err := h.session.Send(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}
	// The harness keeps the events it saw, and they carry the messages: they are not what is under test.
	if err := h.session.FlushEvents(context.Background()); err != nil {
		t.Fatal(err)
	}
	h.mu.Lock()
	h.events = nil
	h.mu.Unlock()
	mu.Lock()
	if user.Value() == nil {
		mu.Unlock()
		t.Fatal("the extension saw no user message")
	}
	mu.Unlock()
	h.session.messageSeqs.Sequence(agent.AgentMessage{ToolResult: &agent.ToolResultMessage{Role: "toolResult", ToolCallID: "call_1"}})
	h.session.messageSeqs.mu.Lock()
	before := h.session.messageSeqs.last
	h.session.messageSeqs.mu.Unlock()
	if before == 0 {
		t.Fatal("the session numbered no message")
	}

	h.session.ReplaceInner(icodingagent.NewSession("replacement", h.session.CWD()))
	for range 5 {
		runtime.GC()
	}
	mu.Lock()
	reachable := user.Value() != nil
	mu.Unlock()
	if reachable {
		t.Error("the user message of the replaced session is still reachable: the Session's message numbering holds it")
	}
	if next := h.session.messageSeqs.Sequence(agent.AgentMessage{User: &agent.UserMessage{Role: "user", Content: ai.UserText("next")}}); next <= before {
		t.Errorf("the first message after the replacement got %d, want a number above the %d numbered before: a number is never reused", next, before)
	}
	if got := h.session.messageSeqs.Known(agent.AgentMessage{ToolResult: &agent.ToolResultMessage{Role: "toolResult", ToolCallID: "call_1"}}); got != 0 {
		t.Errorf("a tool call id of the replaced session still names message %d", got)
	}
}

// A message is numbered for the Node runtime that receives it and for no one else: a Session whose handlers never ask for the numbers (no Node extension is loaded) numbers nothing, notes no entry and holds no message.
func TestSessionNumbersNothingWhenNoExtensionAsksForNumbers(t *testing.T) {
	silent := func(...any) (any, error) { return nil, nil }
	for name, option := range map[string]harnessOptions{
		"no extension":         {},
		"handlers that ignore": {extension: extension.Extension{Path: "/ext/silent", Handlers: map[string][]extension.HandlerFn{"message_start": {silent}, "message_end": {silent}, "turn_end": {silent}, "agent_end": {silent}}}},
	} {
		t.Run(name, func(t *testing.T) {
			h := newRecoveryHarness(t, option, fauxReply("hello", ai.StopReasonStop, 0))
			if _, err := h.session.Send(context.Background(), "go"); err != nil {
				t.Fatal(err)
			}
			sequences := &h.session.messageSeqs
			sequences.mu.Lock()
			last, held := sequences.last, len(sequences.byMessage)
			sequences.mu.Unlock()
			if last != 0 || held != 0 {
				t.Errorf("the session numbered %d messages and holds %d, want none", last, held)
			}
			if pairs, next := h.session.Inner().MessageEntriesAfter(0); len(pairs) != 0 || next != 0 {
				t.Errorf("message entries = %v (cursor %d), want none", pairs, next)
			}
		})
	}
}

// A Node process's table of message objects is keyed by the number alone, and Sessions and graph runners that reach one process number their messages independently: the numbers come from one counter, so no two messages of the process share one.
func TestMessageSequencesNeverRepeatAcrossInstances(t *testing.T) {
	var first, second messageSequences
	seen := map[uint64]bool{}
	for i := range 20 {
		sequences := &first
		if i%2 == 1 {
			sequences = &second
		}
		number := sequences.Sequence(agent.AgentMessage{User: &agent.UserMessage{Role: "user", Content: ai.UserText("m")}})
		if number == 0 || seen[number] {
			t.Fatalf("message %d numbered %d, which another message of the process holds", i, number)
		}
		seen[number] = true
	}
}
