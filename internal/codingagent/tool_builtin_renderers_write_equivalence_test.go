package codingagent

import (
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/tui"
)

// eagerWriteCache is the write highlight cache as Pig ported it before frames read lines lazily: every line highlighted as
// it arrives and the leading block highlighted again after every update.
type eagerWriteCache struct {
	rawPath, lang, rawContent string
	normalizedLines           []string
	highlightedLines          []string
}

func eagerRebuild(rawPath, content string) *eagerWriteCache {
	lang := ""
	if rawPath != "" {
		lang = tui.LanguageFromPath(rawPath)
	}
	if lang == "" {
		return nil
	}
	normalized := replaceTabs(strings.ReplaceAll(content, "\r", ""))
	return &eagerWriteCache{rawPath: rawPath, lang: lang, rawContent: content, normalizedLines: strings.Split(normalized, "\n"), highlightedLines: tui.HighlightCode(normalized, lang)}
}

func eagerUpdate(cache *eagerWriteCache, rawPath, content string) *eagerWriteCache {
	lang := ""
	if rawPath != "" {
		lang = tui.LanguageFromPath(rawPath)
	}
	if lang == "" {
		return nil
	}
	if cache == nil || cache.lang != lang || cache.rawPath != rawPath || !strings.HasPrefix(content, cache.rawContent) {
		return eagerRebuild(rawPath, content)
	}
	if len(content) == len(cache.rawContent) {
		return cache
	}
	delta := replaceTabs(strings.ReplaceAll(content[len(cache.rawContent):], "\r", ""))
	cache.rawContent = content
	if len(cache.normalizedLines) == 0 {
		cache.normalizedLines, cache.highlightedLines = append(cache.normalizedLines, ""), append(cache.highlightedLines, "")
	}
	segments := strings.Split(delta, "\n")
	last := len(cache.normalizedLines) - 1
	cache.normalizedLines[last] += segments[0]
	for len(cache.highlightedLines) <= last {
		cache.highlightedLines = append(cache.highlightedLines, "")
	}
	cache.highlightedLines[last] = highlightSingleLine(cache.normalizedLines[last], cache.lang)
	for _, segment := range segments[1:] {
		cache.normalizedLines = append(cache.normalizedLines, segment)
		cache.highlightedLines = append(cache.highlightedLines, highlightSingleLine(segment, cache.lang))
	}
	prefix := min(writePartialFullHighlightLines, len(cache.normalizedLines))
	block := tui.HighlightCode(strings.Join(cache.normalizedLines[:prefix], "\n"), cache.lang)
	for i := range prefix {
		if i < len(block) {
			cache.highlightedLines[i] = block[i]
		} else {
			cache.highlightedLines[i] = highlightSingleLine(cache.normalizedLines[i], cache.lang)
		}
	}
	return cache
}

// eagerWriteText is the text writeRenderCall drew before frames read lines lazily: it decoded the arguments' JSON text,
// split the whole content into lines and trimmed the empty ones at the end.
func eagerWriteText(args json.RawMessage, cache **eagerWriteCache, complete, expanded bool, cwd string) string {
	theme := tui.ActiveTheme()
	fields := decodeRenderArgs(args)
	text := tui.FormatWriteHeader(pathRenderArgs(fields), cwd)
	rawPath, _ := stringField(fields, "file_path", "path")
	content, ok := stringField(fields, "content")
	switch {
	case !ok:
		*cache = nil
	case complete:
		*cache = eagerRebuild(rawPath, content)
	default:
		*cache = eagerUpdate(*cache, rawPath, content)
	}
	switch {
	case !ok:
		text += "\n\n" + themeFg(theme.Error, "[invalid content arg - expected string]")
	case content != "":
		normalized := strings.ReplaceAll(content, "\r", "")
		lang := ""
		if rawPath != "" {
			lang = tui.LanguageFromPath(rawPath)
		}
		var rendered []string
		switch {
		case lang != "" && *cache != nil:
			rendered = (*cache).highlightedLines
		case lang != "":
			rendered = tui.HighlightCode(replaceTabs(normalized), lang)
		default:
			rendered = strings.Split(normalized, "\n")
		}
		lines := trimTrailingEmptyLines(rendered)
		display := lines
		if !expanded && len(lines) > builtInPreviewLines {
			display = lines[:builtInPreviewLines]
		}
		styled := make([]string, len(display))
		for i, line := range display {
			if lang != "" {
				styled[i] = line
			} else {
				styled[i] = themeFg(theme.ToolOutput, replaceTabs(line))
			}
		}
		text += "\n\n" + strings.Join(styled, "\n")
		if remaining := len(lines) - len(display); remaining > 0 {
			text += moreLinesHint("\n... (" + strconv.Itoa(remaining) + " more lines, " + strconv.Itoa(len(lines)) + " total,")
		}
	}
	return text
}

// writeEquivalenceContent is a file of n lines with what the line structure and the highlighter react to: carriage
// returns, tabs, a block comment and a string that span lines, blank lines inside and at the end, non-ASCII text and, when
// surrogate is set, lone surrogate escapes.
func writeEquivalenceContent(random *rand.Rand, n int, surrogate bool) string {
	var content strings.Builder
	for i := range n {
		switch random.IntN(9) {
		case 0:
			content.WriteString("\r\n")
		case 1:
			fmt.Fprintf(&content, "\tconst s%d = `multi\nline %d`;\n", i, i)
		case 2:
			fmt.Fprintf(&content, "/* comment %d\n * continues é\n */\n", i)
		case 3:
			fmt.Fprintf(&content, "  \t\r\n")
		case 4:
			if surrogate {
				fmt.Fprintf(&content, "const lone%d = \"@HI@ then @LO@, @HI@@HI@ and @PAIR@\"; // 한국어\n", i)
				continue
			}
			fallthrough
		default:
			fmt.Fprintf(&content, "export function f%d(x: number): number { return x * %d; } // note\r\n", i, i)
		}
	}
	for range random.IntN(4) {
		content.WriteString([]string{"\n", "\r\n", "\r"}[random.IntN(3)])
	}
	return content.String()
}

// The write card draws, at every prefix of streamed arguments, what it drew when it decoded their JSON text and kept every
// line highlighted: in every chunking of the stream, collapsed and expanded, for a highlighted path, a path without a
// language and a path that changes, and after the arguments complete. The streamed arguments reach the renderer as the
// streaming parser's object (writeRenderCall with no text, as a streaming card calls it); the reference reads their JSON
// text, as the card did before.
func TestWriteRenderCallMatchesTheEagerRendererAtEveryPrefix(t *testing.T) {
	for seed := range uint64(48) {
		random := rand.New(rand.NewPCG(seed, 0))
		lines := []int{5, 40, 70, 130}[seed%4]
		surrogate := seed%5 < 2
		content := writeEquivalenceContent(random, lines, surrogate)
		path := []string{"src/a.ts", "notes/a.txt", "src/a.go"}[seed%3]
		expanded := seed%2 == 1
		raw, err := json.Marshal(content)
		if err != nil {
			t.Fatal(err)
		}
		text := `{"path":"` + path + `","content":` + string(raw) + `}`
		if surrogate {
			// A model writes surrogate escapes, lone ones included; a Go string cannot hold them.
			text = strings.NewReplacer("@HI@", `\ud800`, "@LO@", `\udc01`, "@PAIR@", `\ud83d\ude00`).Replace(text)
		}
		state := map[string]any{}
		var eager *eagerWriteCache
		check := func(end int, complete bool) {
			var call ai.ToolCall
			call.SetStreamingArguments(text[:end])
			args := streamedToolCallArgs{call: call}
			context := extension.ToolRenderContext{State: state, Cwd: "/work", ArgsComplete: complete, IsPartial: true, Expanded: expanded}
			var got string
			if fields := args.Object(); fields != nil {
				context.Args = fields
				got = strings.Join(writeRenderCall(nil, nil, context).Render(160), "\n")
			} else {
				encoded := args.JSON()
				context.Args = encoded
				got = strings.Join(writeRenderCall(encoded, nil, context).Render(160), "\n")
			}
			want := strings.Join(tui.NewPaddedText(eagerWriteText(args.JSON(), &eager, complete, expanded, "/work"), 0, 0, nil).Render(160), "\n")
			if got != want {
				t.Fatalf("seed %d (%s, expanded %v) at %d of %d bytes (complete %v):\n got %q\nwant %q", seed, path, expanded, end, len(text), complete, got, want)
			}
		}
		for end := 0; end < len(text); {
			end = min(len(text), end+1+random.IntN([]int{24, 200, 1500}[random.IntN(3)]))
			for end < len(text) && text[end]&0xc0 == 0x80 {
				end++
			}
			check(end, false)
			if random.IntN(40) == 0 {
				// The model rewrites the path: a new path rebuilds.
				text = strings.Replace(text, `"path":"`+path+`"`, `"path":"`+path+`x.ts"`, 1)
				path += "x.ts"
			}
		}
		check(len(text), true)
		check(len(text), true)
	}
}
