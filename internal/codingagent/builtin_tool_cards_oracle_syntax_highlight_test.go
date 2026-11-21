//go:build !pig_strip_syntax_highlight

package codingagent

// pi: packages/coding-agent/src/core/tools/renderers/read.ts

// pi: packages/coding-agent/src/core/tools/renderers/write.ts

// pi: packages/coding-agent/src/core/tools/renderers/edit.ts

// pi: packages/coding-agent/src/core/tools/renderers/bash.ts

// pi: packages/coding-agent/src/core/tools/renderers/grep.ts

// pi: packages/coding-agent/src/core/tools/renderers/find.ts

// pi: packages/coding-agent/src/core/tools/renderers/ls.ts

// pi: packages/coding-agent/src/core/tools/renderers/index.ts

import (
	"fmt"
	"math/rand"
	"os"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

// tool-execution.ts with the built-in tool definitions' renderCall and renderResult (core/tools/{read,write,bash,grep,find,ls}.ts)
// against pinned Pi: each card pending and with a result, collapsed and expanded, in both themes at two widths, as the interactive mode builds it from tool events.
// Rows are compared byte for byte: colours, backgrounds and padding included.
func TestBuiltinToolCardsMatchPi(t *testing.T) {
	probes, names := builtinToolCardProbes(t)
	compareToolCards(t, probes, names)
}

// Cards for files in a language highlight.js registers only in its full load (README.md is markdown) and in one it never registers (schema.graphql: the mdCodeBlock color for an unsupported language, which the card
// draws on either side of the load). Interactive mode's init runs that load within moments of a session starting, and the rest of a test process inherits it once any test starts a mode. The load is a one-way latch
// in Pi and PiG alike, so this test loads PiG's before the oracle runs, as a test ahead of it in the process would: it fails on any ordering where the Pi side drew without the languages the PiG side had.
func TestBuiltinToolCardsMatchPiAfterTheHighlightLanguageLoad(t *testing.T) {
	tui.LoadAllHighlightLanguages()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	body := "# heading\n  indented\n\ttabbed\n\x1b[31mred\x1b[0m\n{ \"k\": [1, 2] }\n```go\npackage main\n```"
	var probes []toolCardProbe
	var names []string
	for _, path := range []string{"README.md", "schema.graphql"} {
		for _, tool := range []string{"read", "write"} {
			args := map[string]any{"path": path}
			result := &toolCardResult{Content: cardText(body)}
			if tool == "write" {
				args["content"] = body
				result = &toolCardResult{Content: cardText("Successfully wrote 80 bytes to " + path)}
			}
			for _, theme := range []string{"dark", "light"} {
				for _, expanded := range []bool{false, true} {
					probes = append(probes, toolCardProbe{Tool: tool, Args: args, Result: result, Expanded: expanded, Width: 80, Theme: theme, CWD: cwd})
					names = append(names, fmt.Sprintf("%s %s theme=%s expanded=%v", tool, path, theme, expanded))
				}
			}
		}
	}
	compareToolCards(t, probes, names)
}

// TestBuiltinToolCardsRandomMatchPi drives random tool calls and results through the same cards: arguments of every type, result text with wide, combining, ANSI, tab, CRLF and very long lines, from none to 70 rows, error and truncation details, widths from 3 to 120 (Pi pads the children of a Box to a content width of at least one column, which only shows below the two padding columns), both themes, collapsed and expanded.
func TestBuiltinToolCardsRandomMatchPi(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	rng := rand.New(rand.NewSource(20261017))
	// The card clock's span draws from its own source so the rest of the corpus stays the one rng gives.
	clockRng := rand.New(rand.NewSource(20261013))
	pieces := []string{"", "x", "alpha beta", "日本語のテキスト", "e\u0301", "😀", "\ttabbed", "  indented", "\x1b[31mred\x1b[0m", "line with CR\r", "src/a.go:12: needle", "a/very/long/path/that/keeps/going/and/going/and/going/until/it/wraps/around/the/card.txt", strings.Repeat("w", 150), "{ \"k\": [1, 2] }", "package main", "# heading", "\x00bin\x07"}
	paths := []string{"notes.txt", "main.go", "README.md", "data.json", "日本語.txt", "a b/c.ts", "/abs/path/file.py", "~/home.txt", "", ".", "../up.sh", "dir/", "x.unknownext"}
	text := func(rows int) string {
		out := make([]string, rows)
		for i := range out {
			out[i] = pieces[rng.Intn(len(pieces))]
		}
		return strings.Join(out, "\n")
	}
	tools := []string{"read", "write", "edit", "bash", "powershell", "grep", "find", "ls"}
	var probes []toolCardProbe
	var names []string
	for i := range 500 {
		tool := tools[rng.Intn(len(tools))]
		args := map[string]any{}
		maybe := func(key string, value any) {
			if rng.Intn(2) == 0 {
				args[key] = value
			}
		}
		maybe("path", paths[rng.Intn(len(paths))])
		maybe("pattern", pieces[rng.Intn(len(pieces))])
		maybe("command", pieces[rng.Intn(len(pieces))]+"\n"+pieces[rng.Intn(len(pieces))])
		maybe("content", text(rng.Intn(30)))
		maybe("glob", []string{"*.go", "**/*.ts", ""}[rng.Intn(3)])
		maybe("offset", []any{1, 10, 0, -1, "x"}[rng.Intn(5)])
		maybe("limit", []any{1, 100, 0, "y"}[rng.Intn(4)])
		maybe("timeout", []any{1, 30, 0.5, "z"}[rng.Intn(4)])
		maybe("edits", []any{map[string]any{"oldText": pieces[rng.Intn(len(pieces))], "newText": pieces[rng.Intn(len(pieces))]}})
		var result *toolCardResult
		if rng.Intn(5) != 0 {
			result = &toolCardResult{Content: cardText(text(rng.Intn(70))), IsError: rng.Intn(6) == 0}
			details := map[string]any{}
			if rng.Intn(4) == 0 {
				details["truncation"] = map[string]any{"truncated": true, "truncatedBy": []string{"lines", "bytes"}[rng.Intn(2)], "totalLines": 5000, "totalBytes": 90000, "outputLines": 2000, "outputBytes": 50000, "lastLinePartial": rng.Intn(2) == 0, "firstLineExceedsLimit": rng.Intn(4) == 0, "maxLines": 2000, "maxBytes": 51200}
				if rng.Intn(2) == 0 {
					details["fullOutputPath"] = "/tmp/full.log"
				}
			}
			if tool == "edit" && rng.Intn(2) == 0 {
				details["diff"] = "  1 context\n-  2 " + pieces[rng.Intn(len(pieces))] + "\n+  2 " + pieces[rng.Intn(len(pieces))] + "\n  3 tail"
				details["firstChangedLine"] = 2
			}
			for _, key := range []string{"matchLimitReached", "resultLimitReached", "entryLimitReached"} {
				if rng.Intn(8) == 0 {
					details[key] = 100
				}
			}
			if rng.Intn(8) == 0 {
				details["linesTruncated"] = true
			}
			if len(details) > 0 {
				result.Details = details
			}
		}
		partial := result == nil && (tool == "bash" || tool == "powershell")
		if partial {
			result = &toolCardResult{Content: cardText("")[:0]}
		}
		probes = append(probes, toolCardProbe{Tool: tool, Args: args, Result: result, Partial: partial, Expanded: rng.Intn(2) == 0, Width: 3 + rng.Intn(118), Theme: []string{"dark", "light"}[rng.Intn(2)], CWD: cwd, ElapsedMs: clockRng.Int63n([]int64{60_000, 2 * 3_600_000}[clockRng.Intn(2)])})
		names = append(names, fmt.Sprintf("random %d %s", i, tool))
	}
	compareToolCards(t, probes, names)
}
