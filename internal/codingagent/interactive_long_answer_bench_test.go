package codingagent

import (
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// interactiveLongAnswerSection is one section of the streamed long answer: the section of the Pi-Bolt long-answer stream
// (bench/fake_model_stress.py md:N: headings, a nested list, a TypeScript block, a table and a quote) followed by a Python
// block and a list.
const interactiveLongAnswerSection = "## Section {n}: what the function does\n\nThe `parse()` function reads **one token at a time** and keeps a *small* stack; see [the grammar](https://example.com/g/{n}).\nIt handles these cases:\n\n- plain text, with `inline code` and a long line that has to be wrapped by the terminal because it does not fit in its width at all\n- nested lists:\n  1. first, with a number {n}\n  2. second, with ~~strikethrough~~ and unicode: naïve café 日本語 😀\n\n```ts\nexport function parse{n}(input: string): Node[] {\n\tconst out: Node[] = [];\n\tfor (let i = 0; i < input.length; i++) {\n\t\tif (input.charCodeAt(i) === 0x5b) out.push({ kind: \"open\", at: i });\n\t}\n\treturn out;\n}\n```\n\n| column | value | note |\n|---|---|---|\n| alpha | {n} | first row |\n| beta | 2 | a longer note that wraps |\n\n> A quote with **bold** text and a [link](https://example.com).\n\nThe same parser in Python, with a helper:\n\n```python\ndef parse_{n}(text: str) -> list[dict]:\n    out = []\n    for i, ch in enumerate(text):\n        if ch == \"[\":\n            out.append({\"kind\": \"open\", \"at\": i})  # bracket {n}\n    return out\n```\n\n1. Read the input.\n2. Push each bracket.\n3. Return the stack.\n\n"

// interactiveLongAnswer is size bytes of consecutive sections, numbered for run: the highlight memo is process-wide, so a run
// that streamed the same code blocks as an earlier one would find their lines memoized.
func interactiveLongAnswer(size, run int) string {
	var body strings.Builder
	for n := 1; body.Len() < size; n++ {
		body.WriteString(strings.ReplaceAll(interactiveLongAnswerSection, "{n}", strconv.Itoa(run)+"_"+strconv.Itoa(n)))
	}
	return body.String()[:size]
}

// streamInteractiveAnswer streams answer into the interactive mode in chunks of 24 bytes during an agent turn, drawing a
// frame after each, as the main loop does for a fast model; then the turn ends and a last frame is drawn. It returns the
// frames drawn while the answer streamed.
func streamInteractiveAnswer(m *InteractiveMode, answer string) int {
	m.turnActive.Store(true)
	m.handleAgentEvent(agent.MessageStartEvent{Message: agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: agent.RoleAssistant, StopReason: ai.StopReasonStop}}})
	m.tuiInst.Render()
	frames := 0
	for start := 0; start < len(answer); start += 24 {
		end := min(start+24, len(answer))
		partial := &agent.AssistantMessage{Role: agent.RoleAssistant, StopReason: ai.StopReasonStop, Content: []ai.AssistantContentBlock{ai.TextContent{Text: answer[:end]}}}
		m.handleAgentEvent(agent.MessageUpdateEvent{Message: agent.AgentMessage{Assistant: partial}, AssistantMessageEvent: ai.TextDeltaEvent{Delta: answer[start:end]}})
		m.tuiInst.Render()
		frames++
	}
	final := &agent.AssistantMessage{Role: agent.RoleAssistant, StopReason: ai.StopReasonStop, Content: []ai.AssistantContentBlock{ai.TextContent{Text: answer}}}
	m.handleAgentEvent(agent.MessageEndEvent{Message: agent.AgentMessage{Assistant: final}})
	m.tuiInst.Render()
	m.turnActive.Store(false)
	m.tuiInst.Render()
	return frames
}

// interactiveLongAnswerRuns numbers the answers streamed in this process, so each has code blocks of its own.
var interactiveLongAnswerRuns int

// BenchmarkInteractiveLongAnswerStream streams a 60,000-character Markdown answer through the fullscreen interactive mode
// (the assistant block, its Mermaid transform, and the frame) and reports the allocations of a chunk.
func BenchmarkInteractiveLongAnswerStream(b *testing.B) {
	m, _ := newTickRenderProbe(b, "fullscreen")
	var before, after runtime.MemStats
	var frames int
	var mallocs, bytes uint64
	for b.Loop() {
		interactiveLongAnswerRuns++
		answer := interactiveLongAnswer(60000, interactiveLongAnswerRuns)
		runtime.ReadMemStats(&before)
		frames += streamInteractiveAnswer(m, answer)
		runtime.ReadMemStats(&after)
		mallocs += after.Mallocs - before.Mallocs
		bytes += after.TotalAlloc - before.TotalAlloc
	}
	b.ReportMetric(float64(mallocs)/float64(frames), "allocs/chunk")
	b.ReportMetric(float64(bytes)/float64(frames), "B/chunk")
}
