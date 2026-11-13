//go:build !pig_strip_syntax_highlight

package tui

import (
	"fmt"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

// md20000Section is the Markdown section that the scoreboard's md:20000 cell repeats, with {n} for the section number: prose, lists, a table, a quote and a fenced TypeScript block of eight lines.
const md20000Section = "## Section {n}: what the function does\n\nThe `parse()` function reads **one token at a time** and keeps a *small* stack; see [the grammar](https://example.com/g/{n}).\nIt handles these cases:\n\n- plain text, with `inline code` and a long line that has to be wrapped by the terminal because it does not fit in its width at all\n- nested lists:\n  1. first, with a number {n}\n  2. second, with ~~strikethrough~~ and unicode: naïve café 日本語 😀\n\n```ts\n" + md20000Function + "```\n\n| column | value | note |\n|---|---|---|\n| a{n} | 1 | first |\n| b{n} | 22 | second, longer |\n\n> A quote that closes the section, number {n}.\n\n"

// md20000Function is the code of the section's block.
const md20000Function = "export function parse{n}(input: string): Node[] {\n\tconst out: Node[] = [];\n\tfor (let i = 0; i < input.length; i++) {\n\t\tif (input.charCodeAt(i) === 0x5b) out.push({ kind: \"open\", at: i });\n\t}\n\treturn out;\n}\n"

// md20000FrameChars is the characters one frame of the cell adds: its deltas are 24 characters and a 16 ms frame draws about seven of them.
const md20000FrameChars = 175

// md20000Body is the answer of the cell: sections numbered from 1, cut at chars characters.
func md20000Body(chars int) string {
	var body strings.Builder
	for n, count := 1, 0; count < chars; n++ {
		section := strings.ReplaceAll(md20000Section, "{n}", strconv.Itoa(n))
		body.WriteString(section)
		count += utf8.RuneCountInString(section)
	}
	return string([]rune(body.String())[:chars])
}

// longTypeScriptFence is an answer that is one fenced block of 200 lines: the cell's function 25 times.
func longTypeScriptFence() string {
	var body strings.Builder
	body.WriteString("```ts\n")
	for n := 1; n <= 25; n++ {
		body.WriteString(strings.ReplaceAll(md20000Function, "{n}", strconv.Itoa(n)))
	}
	body.WriteString("```\n")
	return body.String()
}

// longJSONFence is an answer that is one fenced JSON document of 200 lines, which highlight.js reads as one object that stays open to its last line.
func longJSONFence() string {
	var body strings.Builder
	body.WriteString("```json\n{\n  \"items\": [\n")
	for n := 1; n <= 33; n++ {
		comma := ","
		if n == 33 {
			comma = ""
		}
		fmt.Fprintf(&body, "    {\n      \"id\": %d,\n      \"name\": \"item %d\",\n      \"tags\": [\"a\", \"b\"],\n      \"ok\": true\n    }%s\n", n, n, comma)
	}
	body.WriteString("  ]\n}\n```\n")
	return body.String()
}

// streamedFrameTexts is the text of a streamed answer at each frame: body in steps of frameChars characters.
func streamedFrameTexts(body string, frameChars int) []string {
	runes := []rune(body)
	var frames []string
	for end := frameChars; ; end += frameChars {
		end = min(end, len(runes))
		frames = append(frames, string(runes[:end]))
		if end == len(runes) {
			return frames
		}
	}
}

// highlightCall is one call of a Markdown theme's highlighter.
type highlightCall struct{ code, lang string }

// recordedHighlightCalls is the highlighter calls of a Markdown that draws the frames in order: it highlights a code block when a frame changes the block's code, which every frame that reaches the open block does. Collections are held off while it draws, so no call comes from a block rendered again after one.
func recordedHighlightCalls(frames []string) []highlightCall {
	defer debug.SetGCPercent(debug.SetGCPercent(-1))
	var calls []highlightCall
	theme := GetMarkdownTheme()
	theme.HighlightCode = func(code, lang string) []string {
		calls = append(calls, highlightCall{strings.Clone(code), lang})
		return HighlightCode(code, lang)
	}
	markdown := NewMarkdownWithOptions("", 1, 0, &theme, nil, nil)
	for _, frame := range frames {
		markdown.SetText(frame)
		markdown.Render(100)
	}
	return calls
}

// resetHighlightState empties the highlight memo and the resume states, as a new process has them.
func resetHighlightState() {
	theme := ActiveTheme()
	hlMu.Lock()
	resetHighlightCacheLocked()
	hlTheme = theme
	hlMu.Unlock()
}

// replayHighlightCalls highlights the calls in order as HighlightCode does. engine is the characters of code handed to the highlight engine: for each call the code after the point its run resumes at, the whole code when it resumes from nothing. code is the characters of code of the blocks, each counted once at its last call.
func replayHighlightCalls(calls []highlightCall) (engine, code int) {
	theme := ActiveTheme()
	for i, call := range calls {
		hlMu.Lock()
		_, memoized := hlCache[hlKey{lang: call.lang, code: call.code}]
		hlMu.Unlock()
		if base, _ := hlFindResume(call.code, call.lang, theme, false); !memoized && SupportsLanguage(call.lang) {
			engine += len(call.code) - len(base.prefix)
		}
		highlightCodeLines(call.code, call.lang)
		sameBlock := i+1 < len(calls) && calls[i+1].lang == call.lang && (strings.HasPrefix(calls[i+1].code, call.code) || strings.HasPrefix(call.code, calls[i+1].code))
		if !sameBlock {
			code += len(call.code)
		}
	}
	return engine, code
}

// BenchmarkStreamedCodeBlocks replays the code blocks of a streamed answer through HighlightCode frame by frame, as the Markdown of a streaming message calls it: the scoreboard's md:20000 answer (blocks of eight lines of TypeScript, a frame every 175 characters), one TypeScript block of 200 lines and one JSON document of 200 lines. One op is the whole stream from an empty memo. engine-chars/code-char is the characters handed to the highlight engine for each character of code: 1 is every character scanned once.
func BenchmarkStreamedCodeBlocks(b *testing.B) {
	for _, c := range []struct {
		name string
		body string
	}{
		{"md20000", md20000Body(20000)},
		{"ts-200-lines", longTypeScriptFence()},
		{"json-200-lines", longJSONFence()},
	} {
		b.Run(c.name, func(b *testing.B) {
			calls := recordedHighlightCalls(streamedFrameTexts(c.body, md20000FrameChars))
			resetHighlightState()
			engine, code := replayHighlightCalls(calls)
			if code == 0 {
				b.Fatal("the answer has no code block")
			}
			b.ReportAllocs()
			for b.Loop() {
				resetHighlightState()
				for _, call := range calls {
					highlightCodeLines(call.code, call.lang)
				}
			}
			b.ReportMetric(float64(engine)/float64(code), "engine-chars/code-char")
			b.ReportMetric(float64(len(calls)), "calls")
		})
	}
}

// BenchmarkStreamingMessageAcrossCollections streams the md:20000 answer into an assistant message, a frame every 175 characters, and runs a full collection every 30 frames, as the collections that fall in the cell's window do (two to four). The collections run off the clock. live-heap-KB is the heap in use after those collections, which holds what the message keeps between frames.
func BenchmarkStreamingMessageAcrossCollections(b *testing.B) {
	runes := []rune(md20000Body(20000))
	var deltas []string
	for at := 0; at < len(runes); at += md20000FrameChars {
		deltas = append(deltas, string(runes[at:min(at+md20000FrameChars, len(runes))]))
	}
	var live, collections uint64
	var stats runtime.MemStats
	b.ReportAllocs()
	for b.Loop() {
		resetHighlightState()
		block := NewAssistantMessageComponent(nil, false, nil, "", nil, nil, "", "")
		for frame, delta := range deltas {
			block.SetTextDelta(delta)
			block.Render(100)
			if frame%30 == 29 {
				b.StopTimer()
				runtime.GC()
				runtime.ReadMemStats(&stats)
				live += stats.HeapAlloc
				collections++
				b.StartTimer()
			}
		}
		block.FinishStreaming()
		block.Render(100)
	}
	b.ReportMetric(float64(live)/float64(max(1, collections))/1024, "live-heap-KB")
}
