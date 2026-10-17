// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// The goldens below were computed by durable/contract/lib/workload.mjs (node), the reference of this workload.

func TestFingerprintMatchesTheReferenceWorkload(t *testing.T) {
	ctx := []seen{
		{role: "user", text: "turn h0 tools=1"},
		{role: "assistant", calls: []int{1}},
		{role: "tool", text: payload(1, 0)},
		{role: "assistant", text: "done after 1 lookups"},
	}
	if got := fingerprint(ctx); got != "586a9a216b169fe2" {
		t.Fatalf("fingerprint = %s", got)
	}
	if got := next(ctx[:2]); got.call != 1 {
		t.Fatalf("next after a call = %+v", got)
	}
	if got := next(ctx[:3]); got.answer != "done after 1 lookups" {
		t.Fatalf("next after the result = %+v", got)
	}
}

func TestFingerprintEncodesTextAsJSONStringifyDoes(t *testing.T) {
	// JSON.stringify leaves < > & and U+2028 unescaped; Go's encoder escapes the last even without HTML escaping.
	if got := fingerprint([]seen{{role: "user", text: "a<b&c> \u2028é"}}); got != "562d29727889e8c6" {
		t.Fatalf("fingerprint = %s", got)
	}
}

func TestFingerprintEscapesQuotesBackslashesAndControlCharacters(t *testing.T) {
	if got := fingerprint([]seen{{role: "user", text: "q\"\\\n\t\x1fx\u2029"}}); got != "34568909009ef7c6" {
		t.Fatalf("fingerprint = %s", got)
	}
}

func TestPayloadSizesAndHistory(t *testing.T) {
	for _, c := range []struct{ n, kb, want int }{{97, 0, 8192}, {5, 0, 256}, {5, 2, 2048}} {
		if got := len(payload(c.n, c.kb)); got != c.want {
			t.Errorf("payload(%d,%d) has %d bytes, want %d", c.n, c.kb, got, c.want)
		}
	}
	var got []string
	for _, h := range historyTurns(0, 4) {
		got = append(got, h.id+"|"+h.text)
	}
	want := "h0|turn h0 tools=1 h1|turn h1 tools=1 h2|turn h2 tools=0 h3|turn h3 tools=1"
	if strings.Join(got, " ") != want {
		t.Fatalf("history = %v", got)
	}
}

func TestIncrementalDecidesLikeTheFullScan(t *testing.T) {
	var conversation []seen
	inc := incremental{}
	for turn := range 6 {
		planned := turn % 4
		conversation = append(conversation, seen{role: "user", text: fmt.Sprintf("turn x%d tools=%d", turn, planned)})
		for range planned + 1 {
			full, fast := next(conversation), inc.step(conversation)
			if full != fast {
				t.Fatalf("turn %d with %d messages: full %+v, incremental %+v", turn, len(conversation), full, fast)
			}
			if full.call == 0 {
				conversation = append(conversation, seen{role: "assistant", text: full.answer})
				break
			}
			conversation = append(conversation,
				seen{role: "assistant", calls: []int{full.call}},
				seen{role: "tool", text: payload(full.call, 0)})
		}
	}
}

func TestAnswerStreamsTheRequestedLength(t *testing.T) {
	if got := answerOf("turn a tools=0 stream=100", 0); len(got) != 100 || !strings.HasPrefix(got, "the quick brown fox") {
		t.Fatalf("stream answer = %q", got)
	}
	if got := answerOf("turn a tools=2", 2); got != "done after 2 lookups" {
		t.Fatalf("answer = %q", got)
	}
}

// respondTo runs the script's factory over a transcript the way the faux provider does.
func respondTo(t *testing.T, s *script, messages ...ai.Message) ai.AssistantMessage {
	t.Helper()
	s.rearm = func() {}
	r, err := s.respond(ai.NormalizeContext(ai.Context{Messages: messages}), ai.StreamOptions{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func callsOf(r ai.AssistantMessage) (names []string) {
	for _, b := range r.Content {
		if call, ok := b.(ai.ToolCall); ok {
			names = append(names, fmt.Sprint(call.Arguments["n"]))
		}
	}
	return names
}

// textOf is the text of block, and whether it is a text block.
func textOf(block ai.AssistantContentBlock) (string, bool) {
	text, ok := block.(ai.TextContent)
	return text.Text, ok
}

func TestRespondPlansOneLookupAtATimeOrAParallelRound(t *testing.T) {
	user := ai.UserMessage{Content: ai.UserText("turn m0 tools=3")}
	if got := callsOf(respondTo(t, &script{variant: "full"}, user)); strings.Join(got, ",") != "1" {
		t.Fatalf("sequential round announces %v", got)
	}
	par := ai.UserMessage{Content: ai.UserText("turn m0 tools=3 parallel=1")}
	first := respondTo(t, &script{variant: "full"}, par)
	if got := callsOf(first); strings.Join(got, ",") != "1,2,3" || first.StopReason != "toolUse" {
		t.Fatalf("parallel round announces %v (%s)", got, first.StopReason)
	}
}

func TestParallelRoundContinuesOneLookupAtATimeOnceResultsArrive(t *testing.T) {
	par := ai.UserMessage{Content: ai.UserText("turn m0 tools=3 parallel=1")}
	round := ai.AssistantMessage{Content: []ai.AssistantContentBlock{
		ai.ToolCall{ID: "call-1", Name: "lookup", Arguments: map[string]any{"n": float64(1)}},
	}}
	result := ai.ToolResultMessage{ToolCallID: "call-1", ToolName: "lookup", Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "record 1: x"}}}
	if got := callsOf(respondTo(t, &script{variant: "full"}, par, round, result)); strings.Join(got, ",") != "2" {
		t.Fatalf("after one result the round announces %v", got)
	}
}

func TestRespondFailsTheSecondCallOnceWithARetryableError(t *testing.T) {
	s := &script{variant: "full", fault: "retryable-error"}
	user := ai.UserMessage{Content: ai.UserText("turn m0 tools=0")}
	if r := respondTo(t, s, user); r.StopReason == "error" {
		t.Fatal("the first call must succeed")
	}
	r := respondTo(t, s, user)
	if r.StopReason != "error" || !strings.Contains(r.ErrorMessage, "503") {
		t.Fatalf("second call = %+v", r)
	}
	// workload.mjs answers fauxAssistantMessage("", ...): one empty text block, which Pi stores as content.
	if text, ok := textOf(r.Content[0]); len(r.Content) != 1 || !ok || text != "" {
		t.Fatalf("the error response's content = %+v, want one empty text block", r.Content)
	}
	if r := respondTo(t, s, user); r.StopReason == "error" {
		t.Fatal("the third call must succeed")
	}
}

func TestSummarizationRequestsDoNotAdvanceTheScript(t *testing.T) {
	s := &script{variant: "full"}
	r := respondTo(t, s, ai.SystemMessage{Content: ai.SystemText(summarizeStart + " ...")}, ai.UserMessage{Content: ai.UserText("x")})
	if text, _ := textOf(r.Content[0]); s.calls != 0 || len(r.Content) != 1 || text != "Summary of the earlier turns." {
		t.Fatalf("calls=%d response=%+v", s.calls, r)
	}
}

// Every model call writes its context line, as tape.mjs does for the reference ({call, bench} of that call's messages):
// the gate compares them call by call. One line per turn compared the reference's call 1 with this host's call 9.
func TestEveryModelCallRecordsItsContext(t *testing.T) {
	type line struct {
		call  int
		bench string
	}
	var got []line
	s := &script{variant: "full", record: func(call int, bench string) error { got = append(got, line{call, bench}); return nil }}
	first := ai.UserMessage{Content: ai.UserText("turn m0 tools=1")}
	respondTo(t, s, first)
	respondTo(t, s, first, ai.UserMessage{Content: ai.UserText("again")})
	want := []line{{1, fingerprint(seenOf([]ai.Message{first}))}, {2, fingerprint(seenOf([]ai.Message{first, ai.UserMessage{Content: ai.UserText("again")}}))}}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("records = %v, want %v", got, want)
	}
}
