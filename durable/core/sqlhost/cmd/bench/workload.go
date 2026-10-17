// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/MichaelKinsy/PiG/ai"
)

// The durable-bench workload (durable/contract/lib/workload.mjs, CONTRACT section 6, W1-W4): one conversation, a scripted
// model on the faux provider and a `lookup` tool.

const (
	system         = "You are a benchmark agent. Call lookup as instructed, then answer briefly."
	measuredTools  = 8
	unit           = "the quick brown fox jumps over the lazy dog. "
	summarizeStart = "You are a context summarization assistant"
)

var cycle = [...]int{1, 1, 0}

// The realistic-summary workload (bench.mjs BENCH_WORKLOAD=summary): compaction on with the policy defaults, a 200,000-token
// window and 8 KiB lookup results (W4a's shape). The scripted model answers a summarization request with summaryText, plain
// text of the median length of a Pi-template summary, never with a tool call; a tool call there is the stress case.
// workloadSummaryBlocking (summary-blocking) is the same with compaction only ever blocking (backgroundTokens 0): summary's
// compaction may start in the background beside the next request, so which later model call first sees the summary depends
// on scheduling, and blocking compaction makes the transcript the same on every host.
const (
	workloadStandard        = "standard"
	workloadSummary         = "summary"
	workloadSummaryBlocking = "summary-blocking"
	summaryWindow           = 200_000
	summaryPayloadKB        = 8
	summaryChars            = 23_511
)

// summaryText is bench.mjs's SUMMARY, byte for byte: the same text on every target keeps the transcripts equal.
var summaryText = func() string {
	var b strings.Builder
	b.WriteString("## Goal\nRun the scripted benchmark conversation: call lookup as instructed, then answer briefly.\n\n## Constraints & Preferences\n- Use the lookup tool exactly as each turn asks.\n\n## Progress\n### Done\n")
	for i := 1; b.Len() < summaryChars; i++ {
		fmt.Fprintf(&b, "- [x] Turn %d: called lookup, read record %d, and answered with the count of lookups done.\n", i, i)
	}
	return b.String()[:summaryChars]
}()

// workloadOf is bench.mjs's BENCH_WORKLOAD: unset or standard is durable-bench's workload, summary the realistic-summary one.
func workloadOf(v string) (string, error) {
	switch v {
	case "", workloadStandard:
		return workloadStandard, nil
	case workloadSummary, workloadSummaryBlocking:
		return v, nil
	}
	return "", fmt.Errorf("BENCH_WORKLOAD %q: want standard, summary or summary-blocking", v)
}

// summarizes reports whether a workload compacts with the realistic summary.
func summarizes(workload string) bool {
	return workload == workloadSummary || workload == workloadSummaryBlocking
}

var (
	toolsRE    = regexp.MustCompile(`tools=(\d+)`)
	streamRE   = regexp.MustCompile(`stream=(\d+)`)
	parallelRE = regexp.MustCompile(`parallel=1`)
)

type turnSpec struct{ id, text string }

// historyTurns is workload.mjs history(from, to): one tool, one tool, no tool.
func historyTurns(from, to int) []turnSpec {
	var out []turnSpec
	for i := from; i < to; i++ {
		id := "h" + strconv.Itoa(i)
		out = append(out, turnSpec{id, fmt.Sprintf("turn %s tools=%d", id, cycle[i%len(cycle)])})
	}
	return out
}

// payload is the lookup tool's result text; kb makes every result that many KiB instead of the 256 byte / 8 KiB mix.
func payload(n, kb int) string {
	head := fmt.Sprintf("record %d: ", n)
	size := 256
	switch {
	case kb > 0:
		size = kb * 1024
	case n%97 == 0:
		size = 8192
	}
	return head + strings.Repeat("x", size-len(head))
}

// absentCall marks a tool call whose arguments have no numeric n in a seen message's calls.
const absentCall = -1 << 62

// seen is one message as the bench fingerprint sees it: role, text and the lookup numbers of its tool calls.
type seen struct {
	role  string
	text  string
	calls []int
}

func textOfBlocks(blocks []ai.UserContentBlock) string {
	var b strings.Builder
	for _, block := range blocks {
		if t, ok := block.(ai.TextContent); ok {
			b.WriteString(t.Text)
		}
	}
	return b.String()
}

// seenOf is workload.mjs seenOf: the user, assistant and tool-result messages of a transcript.
func seenOf(messages []ai.Message) []seen {
	// A transcript has at most one seen entry per message; the scripted model builds this for the whole history on every model call.
	out := make([]seen, 0, len(messages))
	for _, m := range messages {
		switch m := m.(type) {
		case ai.UserMessage:
			switch c := m.Content.(type) {
			case ai.UserText:
				out = append(out, seen{role: "user", text: string(c)})
			case ai.UserContentBlocks:
				out = append(out, seen{role: "user", text: textOfBlocks(c)})
			}
		case ai.AssistantMessage:
			s := seen{role: "assistant"}
			for _, block := range m.Content {
				switch b := block.(type) {
				case ai.TextContent:
					s.text += b.Text
				case ai.ToolCall:
					if n, ok := b.Arguments["n"].(float64); ok {
						s.calls = append(s.calls, int(n))
					} else {
						// JSON.stringify([undefined]) is [null]: a call without a numeric n (the subagent tool's) is a null.
						s.calls = append(s.calls, absentCall)
					}
				}
			}
			out = append(out, s)
		case ai.ToolResultMessage:
			s := seen{role: "tool"}
			for _, block := range m.Content {
				if t, ok := block.(ai.TextContent); ok {
					s.text += t.Text
				}
			}
			out = append(out, s)
		}
	}
	return out
}

// fingerprint is the bench fingerprint: 8 bytes of sha256 over JSON [role, text, calls] of every message (CONTRACT 5.4).
func fingerprint(messages []seen) string {
	var b strings.Builder
	b.WriteByte('[')
	for i, m := range messages {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteByte('[')
		writeJSONString(&b, m.role)
		b.WriteByte(',')
		writeJSONString(&b, m.text)
		b.WriteString(",[")
		for j, n := range m.calls {
			if j > 0 {
				b.WriteByte(',')
			}
			if n == absentCall {
				b.WriteString("null")
				continue
			}
			b.WriteString(strconv.Itoa(n))
		}
		b.WriteString("]]")
	}
	b.WriteByte(']')
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])[:16]
}

// writeJSONString writes s as JSON.stringify does: only the quote, the backslash and control characters are escaped.
// (encoding/json also escapes U+2028 and U+2029, so it cannot produce the reference's bytes.)
func writeJSONString(b *strings.Builder, s string) {
	const hex = "0123456789abcdef"
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"':
			b.WriteString(`\"`)
		case r == '\\':
			b.WriteString(`\\`)
		case r == '\b':
			b.WriteString(`\b`)
		case r == '\f':
			b.WriteString(`\f`)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\r':
			b.WriteString(`\r`)
		case r == '\t':
			b.WriteString(`\t`)
		case r < 0x20:
			b.WriteString(`\u00`)
			b.WriteByte(hex[r>>4])
			b.WriteByte(hex[r&15])
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
}

type step struct {
	call   int // the next lookup number; zero with answer set
	answer string
	done   int
}

// next is workload.mjs next(): the decision from the whole conversation so far.
func next(messages []seen) step {
	last := -1
	for i, m := range messages {
		if m.role == "user" {
			last = i
		}
	}
	planned := 0
	if last >= 0 {
		if m := toolsRE.FindStringSubmatch(messages[last].text); m != nil {
			planned, _ = strconv.Atoi(m[1])
		}
	}
	total, done := 0, 0
	for i, m := range messages {
		if m.role == "tool" {
			total++
			if i > last {
				done++
			}
		}
	}
	if done < planned {
		return step{call: total + 1, done: done}
	}
	return step{answer: fmt.Sprintf("done after %d lookups", done), done: done}
}

// incremental is workload.mjs Incremental: the same decision from counters over only the new messages (W2).
type incremental struct{ scanned, planned, total, done int }

func (c *incremental) step(messages []seen) step {
	if len(messages) < c.scanned {
		*c = incremental{}
	}
	for i := c.scanned; i < len(messages); i++ {
		switch messages[i].role {
		case "user":
			c.planned = 0
			if m := toolsRE.FindStringSubmatch(messages[i].text); m != nil {
				c.planned, _ = strconv.Atoi(m[1])
			}
			c.done = 0
		case "tool":
			c.total++
			c.done++
		}
	}
	c.scanned = len(messages)
	if c.done < c.planned {
		return step{call: c.total + 1, done: c.done}
	}
	return step{answer: fmt.Sprintf("done after %d lookups", c.done), done: c.done}
}

// answerOf is W3: `stream=C` answers with C characters of text, so a response streams in many chunks.
func answerOf(user string, done int) string {
	if m := streamRE.FindStringSubmatch(user); m != nil {
		if chars, _ := strconv.Atoi(m[1]); chars > 0 {
			return strings.Repeat(unit, (chars+len(unit)-1)/len(unit))[:chars]
		}
	}
	return fmt.Sprintf("done after %d lookups", done)
}

func lastUserText(messages []ai.Message) string {
	for _, message := range slices.Backward(messages) {
		if u, ok := message.(ai.UserMessage); ok {
			for _, s := range seenOf([]ai.Message{u}) {
				return s.text
			}
		}
	}
	return ""
}

// script is the scripted model's state across one process.
type script struct {
	variant string
	fault   string
	// summary answers a summarization request (the realistic-summary workload); empty keeps the contract's short answer.
	summary string
	inc     incremental
	calls   int
	last    []ai.Message
	rearm   func()
	// record writes the model call's context line, as tape.mjs does for every model call: {call, bench}.
	record    func(call int, bench string) error
	recordErr error
	// markCall is the call whose context firstSeen keeps (the first measured call of a timing run); 0 keeps none.
	markCall  int
	firstSeen []seen
}

func isSummarization(messages []ai.Message) bool {
	for _, m := range messages {
		if s, ok := m.(ai.SystemMessage); ok {
			if t, ok := s.Content.(ai.SystemText); ok && strings.HasPrefix(string(t), summarizeStart) {
				return true
			}
		}
	}
	return false
}

// respond is the faux response factory of scriptedModel in workload.mjs.
func (s *script) respond(request ai.TranscriptContext, _ ai.StreamOptions, _ *ai.FauxProviderState, _ *ai.Model) (ai.AssistantMessage, error) {
	s.rearm()
	// Pi's scripted model reads transcript.messages in place (bench.mjs); Messages() would deep-copy the whole transcript
	// on every call, a cost the reference never pays.
	messages := make([]ai.Message, request.Len())
	for i := range messages {
		messages[i] = request.At(i)
	}
	if isSummarization(messages) {
		answer := "Summary of the earlier turns."
		if s.summary != "" {
			answer = s.summary
		}
		return clockedMessage([]ai.FauxContentBlock{ai.FauxText(answer)}, ai.FauxAssistantMessageOptions{}), nil
	}
	s.last = messages
	s.calls++
	if s.calls == s.markCall {
		s.firstSeen = seenOf(messages)
	}
	if s.record != nil {
		if err := s.record(s.calls, fingerprint(seenOf(messages))); err != nil && s.recordErr == nil {
			s.recordErr = err
		}
	}
	if s.fault == "retryable-error" && s.calls == 2 {
		return clockedMessage([]ai.FauxContentBlock{ai.FauxText("")}, ai.FauxAssistantMessageOptions{StopReason: "error", ErrorMessage: "503 service unavailable"}), nil
	}
	seenMessages := seenOf(messages)
	user := lastUserText(messages)
	var st step
	if s.variant == "incr" {
		st = s.inc.step(seenMessages)
	} else {
		st = next(seenMessages)
	}
	if st.call == 0 {
		return clockedMessage([]ai.FauxContentBlock{ai.FauxText(answerOf(user, st.done))}, ai.FauxAssistantMessageOptions{}), nil
	}
	// W4e: `subagent=1` turns delegate to the `subagent` tool, which runs the task in a child conversation the tool call owns.
	if subagentRE.MatchString(user) {
		label := ""
		if m := turnLabelRE.FindStringSubmatch(user); m != nil {
			label = m[1]
		}
		return clockedMessage([]ai.FauxContentBlock{ai.FauxToolCall("subagent", map[string]any{"task": "task for " + label}, &ai.FauxToolCallOptions{ID: fmt.Sprintf("call-sub-%d", s.calls)})}, ai.FauxAssistantMessageOptions{StopReason: "toolUse"}), nil
	}
	planned := 1
	if parallelRE.MatchString(user) {
		if m := toolsRE.FindStringSubmatch(user); m != nil {
			planned, _ = strconv.Atoi(m[1])
		}
	}
	doneAfterUser := 0
	for _, message := range slices.Backward(messages) {
		if _, ok := message.(ai.UserMessage); ok {
			break
		}
		if _, ok := message.(ai.ToolResultMessage); ok {
			doneAfterUser++
		}
	}
	calls := []int{st.call}
	if planned > 1 && doneAfterUser == 0 {
		calls = calls[:0]
		for i := range planned {
			calls = append(calls, st.call+i)
		}
	}
	blocks := make([]ai.FauxContentBlock, len(calls))
	for i, n := range calls {
		blocks[i] = ai.FauxToolCall("lookup", map[string]any{"n": float64(n)}, &ai.FauxToolCallOptions{ID: fmt.Sprintf("call-%d", n)})
	}
	return clockedMessage(blocks, ai.FauxAssistantMessageOptions{StopReason: "toolUse"}), nil
}

// benchFingerprint is the fingerprint of the last scripted model call's context.
func (s *script) benchFingerprint() string { return fingerprint(seenOf(s.last)) }

// epochMs is the run's fixed clock (the conformance gate's DETENV_EPOCH): the reference runs under a virtual clock that
// moves only when a timer is due, so a scripted message made without a timestamp carries the epoch.
var (
	subagentRE  = regexp.MustCompile(`subagent=1`)
	turnLabelRE = regexp.MustCompile(`turn (\S+)`)
)

var epochMs int64 = 1_800_000_000_000

// The reference's faux provider draws Math.random() once per chunk it streams (faux.ts splitStringByTokenSize), and detenv's
// Math.random, getRandomValues and uuidv7 share one stream, so a uuid made after a model call is a different id from one made
// after none. drawRNG is that stream and fauxChunkChars the provider's chunk size in characters (tokenSize * 4).
var (
	drawRNG        *detenvRNG
	fauxChunkChars = 4_000_000_000
)

func consumeFauxDraws(content []ai.FauxContentBlock) {
	if drawRNG == nil {
		return
	}
	for _, block := range content {
		var n int
		switch block.Type {
		case "text":
			n = len(utf16.Encode([]rune(block.Text)))
		case "thinking":
			n = len(utf16.Encode([]rune(block.Thinking)))
		case "toolCall":
			// faux.ts streams JSON.stringify(block.arguments); only its length counts, which member order does not change.
			n = ai.JSONStringifyLength(block.Arguments)
		}
		for range (n + fauxChunkChars - 1) / fauxChunkChars {
			drawRNG.next()
		}
	}
}

func clockedMessage(content []ai.FauxContentBlock, options ai.FauxAssistantMessageOptions) ai.AssistantMessage {
	if options.Timestamp == nil {
		options.Timestamp = new(int64(math.Floor(virtualNow())))
	}
	consumeFauxDraws(content)
	return ai.FauxAssistantMessage(ai.FauxContentBlocks(content), options)
}
