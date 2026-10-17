package codingagent

import (
	"bytes"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// A session keeps the record of a message entry it holds as the record's decode for the entry's life, so the record holds no room beyond what a copy of it would: a short message's
// record encoded into a buffer reserved for its text plus sessionLineOverhead kept about a kilobyte of spare capacity per entry, which an RPC
// turn (a user and an assistant message) left on the live heap (TestRPCTurnProcessGate's heap_live_growth_bytes). A message whose text outgrows
// the encoder's pooled buffer keeps at most the room reserved for its escapes and the overhead, whatever escapes its text has: a buffer sized
// for the text alone was outgrown by the escapes of source text (a newline, tab or quote every few dozen bytes) and replaced by one of twice its
// size, which the session kept (1.5 times the record's length spare).
//
// A message the session holds as appended keeps no record at all (TestSessionRetainsNoRecordOfAppendedMessages); the record
// marshalSessionLine encodes is the one an entry held as its record's decode keeps, so its capacity is measured where it is made.
func TestMessageEntryRecordKeepsNoSpareCapacity(t *testing.T) {
	long := strings.Repeat("x", 80<<10)
	source := strings.Repeat("\tvalue := compute(input, \"options\") // the result\n", 80<<10/50)
	longSpare := func(text string) func([]byte) int {
		return func([]byte) int { return len(text)/sessionLineEscapeShare + sessionLineOverhead }
	}
	cases := []struct {
		name     string
		msg      agent.AgentMessage
		maxSpare func(raw []byte) int
	}{
		{"user", agent.AgentMessage{User: &agent.UserMessage{Role: "user", Content: ai.UserText("What is 20+22?"), Timestamp: 1}},
			func(raw []byte) int { return cap(bytes.Clone(raw)) - len(raw) }},
		{"assistant", agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: "assistant", Content: []ai.AssistantContentBlock{ai.TextContent{Text: "42"}},
			API: "faux", Provider: "test-faux", ModelID: "faux-1", Usage: &ai.Usage{Input: 1, Output: 1, TotalTokens: 2}, StopReason: "stop", Timestamp: 2}},
			func(raw []byte) int { return cap(bytes.Clone(raw)) - len(raw) }},
		{"long user", agent.AgentMessage{User: &agent.UserMessage{Role: "user", Content: ai.UserText(long), Timestamp: 3}}, longSpare(long)},
		{"long source text", agent.AgentMessage{User: &agent.UserMessage{Role: "user", Content: ai.UserText(source), Timestamp: 4}}, longSpare(source)},
	}
	parent := "0a1b2c3d"
	for _, tc := range cases {
		raw, err := marshalSessionLine(MessageEntry{SessionEntryBase: SessionEntryBase{Type: "message", ID: "4e5f6a7b", ParentID: &parent,
			Timestamp: "2026-10-13T08:00:00.000Z"}, Message: tc.msg})
		if err != nil {
			t.Fatal(err)
		}
		if spare, limit := cap(raw)-len(raw), tc.maxSpare(raw); spare > limit {
			t.Errorf("%s: record of %d bytes keeps %d spare bytes, want at most %d", tc.name, len(raw), spare, limit)
		}
	}
}
