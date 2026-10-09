// SPDX-License-Identifier: MIT

package history

import "math"

const toolResultMaxChars = 2000 // TOOL_RESULT_MAX_CHARS

// jsText is a JavaScript string under construction (WTF-8).
type jsText []byte

func (t jsText) add(piece []byte) jsText { return jsText(appendJS(t, piece)) }

func (t jsText) addString(s string) jsText { return jsText(appendJS(t, []byte(s))) }

// stringField returns the decoded value of a JSON string value, or false when v is not a string.
func stringField(v []byte) ([]byte, bool) {
	if valueKind(v) != '"' {
		return nil, false
	}
	return unescape(nil, v[1:len(v)-1]), true
}

// contentText is the local contentText of compaction.ts: a string content as is, else the text of the blocks that
// have text, joined by newlines.
func contentText(content []byte) []byte {
	switch valueKind(content) {
	case '"':
		return unescape(nil, content[1:len(content)-1])
	case '[':
		var out jsText
		first := true
		s := newScanner(content)
		it, err := s.array()
		if err != nil {
			return nil
		}
		for it.next() {
			blk := content[it.vs:it.ve]
			if valueKind(blk) != '{' {
				continue
			}
			bs := newScanner(blk)
			oit, err := bs.object()
			if err != nil {
				continue
			}
			isText := false
			var text []byte
			hasText := false
			for oit.next() {
				switch {
				case oit.keyIs("type"):
					isText = false
					if v := oit.value(); valueKind(v) == '"' {
						isText = keyIs(v[1:len(v)-1], "text")
					}
				case oit.keyIs("text"):
					text, hasText = stringField(oit.value())
				}
			}
			if isText && hasText {
				if !first {
					out = out.addString("\n")
				}
				first = false
				out = out.add(text)
			}
		}
		return out
	}
	return nil
}

// truncateText is compaction.ts truncate: text cut to maxChars units with a count of the rest.
func truncateText(text []byte, maxChars int) []byte {
	n := unitsOf(text)
	if n <= maxChars {
		return text
	}
	out := jsText(append([]byte(nil), sliceUnits(text, maxChars)...))
	out = out.addString("\n\n[... ")
	out = out.add(appendInt(nil, int64(n-maxChars)))
	return out.addString(" more characters truncated]")
}

// SerializeConversation renders messages as plain text so the summarizer reads a transcript instead of continuing
// it (src/harness/compaction.ts serializeConversation). The result is a JavaScript string as WTF-8.
func SerializeConversation(messages [][]byte) []byte {
	var out jsText
	first := true
	push := func(part []byte) {
		if !first {
			out = out.addString("\n\n")
		}
		first = false
		out = out.add(part)
	}
	for _, msg := range messages {
		s := newScanner(msg)
		it, err := s.object()
		if err != nil {
			continue
		}
		var role, content []byte
		for it.next() {
			switch {
			case it.keyIs("role"):
				role = nil
				if v := it.value(); valueKind(v) == '"' {
					role = v[1 : len(v)-1]
				}
			case it.keyIs("content"):
				content = it.value()
			}
		}
		switch {
		case keyIs(role, "user"):
			if text := contentText(content); len(text) > 0 {
				push(jsText("[User]: ").add(text))
			}
		case keyIs(role, "assistant"):
			serializeAssistant(content, push)
		case keyIs(role, "toolResult"):
			if text := contentText(content); len(text) > 0 {
				push(jsText("[Tool result]: ").add(truncateText(text, toolResultMaxChars)))
			}
		}
	}
	return out
}

func serializeAssistant(content []byte, push func([]byte)) {
	if valueKind(content) != '[' {
		return
	}
	var thinking, texts, calls jsText
	nThinking, nTexts, nCalls := 0, 0, 0
	s := newScanner(content)
	ait, err := s.array()
	if err != nil {
		return
	}
	for ait.next() {
		blk := content[ait.vs:ait.ve]
		if valueKind(blk) != '{' {
			continue
		}
		bs := newScanner(blk)
		oit, err := bs.object()
		if err != nil {
			continue
		}
		var typ, text, think, name, args []byte
		hasName, hasArgs := false, false
		for oit.next() {
			switch {
			case oit.keyIs("type"):
				typ = nil
				if v := oit.value(); valueKind(v) == '"' {
					typ = v[1 : len(v)-1]
				}
			case oit.keyIs("text"):
				text, _ = stringField(oit.value())
			case oit.keyIs("thinking"):
				think, _ = stringField(oit.value())
			case oit.keyIs("name"):
				name, hasName = stringField(oit.value())
			case oit.keyIs("arguments"):
				args, hasArgs = oit.value(), true
			}
		}
		switch {
		case keyIs(typ, "thinking"):
			if nThinking > 0 {
				thinking = thinking.addString("\n")
			}
			thinking = thinking.add(think)
			nThinking++
		case keyIs(typ, "text"):
			if nTexts > 0 {
				texts = texts.addString("\n")
			}
			texts = texts.add(text)
			nTexts++
		case keyIs(typ, "toolCall"):
			if nCalls > 0 {
				calls = calls.addString("; ")
			}
			if hasName {
				calls = calls.add(name)
			} else {
				calls = calls.addString("undefined")
			}
			calls = calls.addString("(")
			if hasArgs {
				calls = appendEntriesText(calls, args)
			}
			calls = calls.addString(")")
			nCalls++
		}
	}
	if nThinking > 0 {
		push(jsText("[Assistant thinking]: ").add(thinking))
	}
	if nTexts > 0 {
		push(jsText("[Assistant]: ").add(texts))
	}
	if nCalls > 0 {
		push(jsText("[Assistant tool calls]: ").add(calls))
	}
}

// appendEntriesText appends Object.entries(args).map(([k, v]) => `${k}=${JSON.stringify(v)}`).join(", ").
func appendEntriesText(dst jsText, args []byte) jsText {
	if valueKind(args) != '{' {
		return dst
	}
	canon, err := Canonical(nil, args)
	if err != nil {
		return dst
	}
	s := newScanner(canon)
	it, err := s.object()
	if err != nil {
		return dst
	}
	first := true
	for it.next() {
		if !first {
			dst = dst.addString(", ")
		}
		first = false
		dst = dst.add(unescape(nil, it.key)).addString("=").add(it.value())
	}
	return dst
}

// SummaryPrompt is the summarizer's user text: the serialized conversation, the prompt, and any instructions.
func SummaryPrompt(messages [][]byte, instructions []byte, hasInstructions bool) []byte {
	out := jsText("<conversation>\n")
	out = out.add(SerializeConversation(messages))
	out = out.addString("\n</conversation>\n\n")
	out = out.addString(summarizationPrompt)
	if hasInstructions {
		out = out.addString("\n\nAdditional focus: ").add(instructions)
	}
	return out
}

// AppendSummaryContext appends the pi-ai Context of the summarization request, {messages: [system, user]}.
// instructions is the compaction input's instructions as a JavaScript string, when it has any.
func AppendSummaryContext(dst []byte, messages [][]byte, instructions []byte, hasInstructions bool, now float64) ([]byte, error) {
	dst = append(dst, `{"messages":[{"role":"system","content":`...)
	dst = appendString(dst, []byte(summarizationSystemPrompt))
	dst = append(dst, `,"timestamp":`...)
	dst, err := AppendNumber(dst, now)
	if err != nil {
		return dst, err
	}
	dst = append(dst, `},{"role":"user","content":[{"type":"text","text":`...)
	dst = appendString(dst, SummaryPrompt(messages, instructions, hasInstructions))
	dst = append(dst, `}],"timestamp":`...)
	dst, err = AppendNumber(dst, now)
	if err != nil {
		return dst, err
	}
	return append(dst, "}]}"...), nil
}

// SummaryMaxTokens is the summarization request's output budget: 80% of the reserve, capped by the model's own
// limit when it has one.
func SummaryMaxTokens(reserveTokens, modelMaxTokens float64) float64 {
	limit := math.Inf(1)
	if modelMaxTokens > 0 {
		limit = modelMaxTokens
	}
	return math.Min(math.Floor(0.8*reserveTokens), limit)
}

// SummaryText returns the summary of a clean `stop` message with text and no tool call; anything else is not a
// summary (src/harness/compaction.ts summaryText).
func SummaryText(msg []byte) ([]byte, bool) {
	s := newScanner(msg)
	it, err := s.object()
	if err != nil {
		return nil, false
	}
	var stop, content []byte
	for it.next() {
		switch {
		case it.keyIs("stopReason"):
			stop = nil
			if v := it.value(); valueKind(v) == '"' {
				stop = v[1 : len(v)-1]
			}
		case it.keyIs("content"):
			content = it.value()
		}
	}
	if !keyIs(stop, "stop") || valueKind(content) != '[' {
		return nil, false
	}
	var out jsText
	n := 0
	cs := newScanner(content)
	ait, err := cs.array()
	if err != nil {
		return nil, false
	}
	for ait.next() {
		blk := content[ait.vs:ait.ve]
		if valueKind(blk) != '{' {
			continue
		}
		bs := newScanner(blk)
		oit, err := bs.object()
		if err != nil {
			continue
		}
		var typ, text []byte
		for oit.next() {
			switch {
			case oit.keyIs("type"):
				typ = nil
				if v := oit.value(); valueKind(v) == '"' {
					typ = v[1 : len(v)-1]
				}
			case oit.keyIs("text"):
				text, _ = stringField(oit.value())
			}
		}
		switch {
		case keyIs(typ, "toolCall"):
			return nil, false
		case keyIs(typ, "text"):
			if n > 0 {
				out = out.addString("\n")
			}
			out = out.add(text)
			n++
		}
	}
	trimmed := trimJS(out)
	if len(trimmed) == 0 {
		return nil, false
	}
	return trimmed, true
}

// SummaryFailure is the failure text of a message that is not a summary (src/harness/compaction.ts
// summaryFailure), as a JavaScript string.
func SummaryFailure(msg []byte) []byte {
	s := newScanner(msg)
	it, err := s.object()
	var stop, errMsg, content []byte
	hasErr := false
	if err == nil {
		for it.next() {
			switch {
			case it.keyIs("stopReason"):
				stop, _ = stringField(it.value())
			case it.keyIs("errorMessage"):
				errMsg, hasErr = stringField(it.value())
			case it.keyIs("content"):
				content = it.value()
			}
		}
	}
	switch string(stop) {
	case "error", "aborted":
		out := jsText("Summarization failed: ")
		if hasErr {
			return out.add(errMsg)
		}
		return out.add(stop)
	case "length":
		return []byte("Summarization hit the token limit; the summary is incomplete")
	}
	if hasToolCall(content) {
		return []byte("Summarization attempted to call a tool")
	}
	return []byte("Summarization produced no text")
}

func hasToolCall(content []byte) bool {
	if valueKind(content) != '[' {
		return false
	}
	s := newScanner(content)
	it, err := s.array()
	if err != nil {
		return false
	}
	for it.next() {
		blk := content[it.vs:it.ve]
		if valueKind(blk) != '{' {
			continue
		}
		bs := newScanner(blk)
		oit, err := bs.object()
		if err != nil {
			continue
		}
		for oit.next() {
			if oit.keyIs("type") {
				if v := oit.value(); valueKind(v) == '"' && keyIs(v[1:len(v)-1], "toolCall") {
					return true
				}
			}
		}
	}
	return false
}

// AppendSummaryEntryDraft appends the entry draft a finished summary places (src/harness/compaction.ts
// placeSummary): kind pi.compaction, head the first kept entry, a user message carrying the wrapped summary, and
// the reason as data. summary is a JavaScript string (WTF-8).
func AppendSummaryEntryDraft(dst []byte, firstKept int64, summary []byte, reason string, now float64) ([]byte, error) {
	dst = append(dst, `{"kind":"pi.compaction","head":`...)
	dst = appendInt(dst, firstKept)
	dst = append(dst, `,"model":[{"role":"user","content":[{"type":"text","text":`...)
	text := jsText(summaryPrefix).add(summary).addString(summarySuffix)
	dst = appendString(dst, text)
	dst = append(dst, `}],"timestamp":`...)
	dst, err := AppendNumber(dst, now)
	if err != nil {
		return dst, err
	}
	dst = append(dst, `}],"data":{"reason":`...)
	dst = appendString(dst, []byte(reason))
	return append(dst, "}}"...), nil
}
