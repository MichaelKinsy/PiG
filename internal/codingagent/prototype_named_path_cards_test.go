package codingagent

import (
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/tui"
)

// A path whose lowercased extension is constructor or __proto__ is a file with no known language: the write card and the expanded read card are the cards
// themselves, with plain text, as for any other unknown extension. TestCardsOfAPathNamedLikeAnObjectPrototypeMemberAgainstPi compares them with Pi.
func TestReadAndWriteCardsOfAPathNamedLikeAnObjectPrototypeMemberAreTheCardsThemselves(t *testing.T) {
	for _, path := range []string{"out.constructor", "x.__proto__", "a/b/c.constructor", "Constructor.CONSTRUCTOR", ".constructor", "constructor", "out.toString", "out.valueOf", "out.constructor.ts", "out.unknownext", "out.ts"} {
		quoted, err := json.Marshal(path)
		if err != nil {
			t.Fatal(err)
		}
		withContent := func(content string) json.RawMessage {
			return json.RawMessage(`{"path":` + string(quoted) + `,"content":` + content + `}`)
		}
		// The call fallback of tool-execution.ts draws the arguments as key=value pairs.
		argument := "path=" + string(quoted)

		write := toolComponentRaw(t, "write", "w-"+path, withContent(`"const x = 1;\n"`), nil)
		write.card.SetArgsComplete()
		if rows := write.plain(120); strings.Contains(rows, argument) || !strings.Contains(rows, "const x = 1;") {
			t.Errorf("write %q, complete: want the write card with its content\n%s", path, rows)
		}

		empty := toolComponentRaw(t, "write", "e-"+path, withContent(`""`), nil)
		empty.card.SetArgsComplete()
		if rows := empty.plain(120); strings.Contains(rows, argument) {
			t.Errorf("write %q, complete, empty content: drew the call fallback\n%s", path, rows)
		}

		streaming := toolComponentRaw(t, "write", "s-"+path, withContent(`""`), nil)
		if rows := streaming.plain(120); strings.Contains(rows, argument) {
			t.Errorf("write %q, streaming: drew the call fallback\n%s", path, rows)
		}

		// Content that is no string is the invalid-argument text.
		invalid := toolComponentRaw(t, "write", "i-"+path, withContent(`5`), nil)
		invalid.card.SetArgsComplete()
		if rows := invalid.plain(120); strings.Contains(rows, argument) || !strings.Contains(rows, "[invalid content arg - expected string]") {
			t.Errorf("write %q, number content: want the invalid-argument text\n%s", path, rows)
		}

		// The read card has a blank row between its header and the output; the result fallback has none.
		read := toolComponentRaw(t, "read", "r-"+path, json.RawMessage(`{"path":`+string(quoted)+`}`), nil)
		read.update(agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "alpha\n\tbeta\n"}}}, false)
		read.card.SetExpanded(true)
		rows := strings.Split(read.plain(120), "\n")
		header := slices.IndexFunc(rows, func(row string) bool { return strings.Contains(row, "read ") })
		if header < 0 || header+2 >= len(rows) || strings.TrimSpace(rows[header+1]) != "" || strings.TrimSpace(rows[header+2]) != "alpha" {
			t.Errorf("read %q, expanded: want a blank row then the output under the header\n%s", path, strings.Join(rows, "\n"))
		}

		failed := toolComponentRaw(t, "read", "f-"+path, json.RawMessage(`{"path":`+string(quoted)+`}`), nil)
		failed.update(agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "ENOENT: no such file"}}, IsError: true}, false)
		failed.card.SetExpanded(true)
		rows = strings.Split(failed.plain(120), "\n")
		header = slices.IndexFunc(rows, func(row string) bool { return strings.Contains(row, "read ") })
		if header < 0 || header+2 >= len(rows) || strings.TrimSpace(rows[header+1]) != "" || !strings.Contains(rows[header+2], "ENOENT") {
			t.Errorf("read %q, error: want a blank row then the error text\n%s", path, strings.Join(rows, "\n"))
		}
	}
}

// D645, the file extension row, at the read and write cards. theme.ts getLanguageFromPath reads a plain object, so for a path whose lowercased extension is
// constructor or __proto__ it returns a member inherited from Object.prototype and highlightCode throws a TypeError for that value. read.ts formatReadResult (an
// expanded result that is no error) and write.ts renderCall (content that still streams in, or a complete call's content unless it is empty) throw with it, and
// tool-execution.ts draws its fallback: for the write call the tool name and its arguments, for the read result the output's rows directly under the header.
// PiG draws the cards themselves. Each path is drawn beside a twin whose extension differs in its last letter, which neither side knows: PiG's rows for the
// path are Pi's rows for the twin with the path's name in them, the twin's rows are equal on both sides, and Pi's rows for the path are its fallback where the
// shape says so, which the pinned Pi must draw in this run.
func TestCardsOfAPathNamedLikeAnObjectPrototypeMemberAgainstPi(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	const content = "const x = 1;\n\tlet y = 2;\n"
	read := func(string) *toolCardResult {
		return &toolCardResult{Content: cardText("alpha line\n\tbeta tabbed\ngamma line\n")}
	}
	missing := func(string) *toolCardResult {
		return &toolCardResult{Content: cardText("ENOENT: no such file"), IsError: true}
	}
	wrote := func(path string) *toolCardResult {
		return &toolCardResult{Content: cardText("Successfully wrote to " + path)}
	}
	shapes := []struct {
		name        string
		tool        string
		content     any
		result      func(path string) *toolCardResult
		expanded    bool
		streaming   bool
		piFallsBack bool
	}{
		{name: "read, expanded", tool: "read", result: read, expanded: true, piFallsBack: true},
		{name: "read, collapsed", tool: "read", result: read},
		{name: "read error, expanded", tool: "read", result: missing, expanded: true},
		{name: "write call", tool: "write", content: content, piFallsBack: true},
		{name: "write call, expanded", tool: "write", content: content, expanded: true, piFallsBack: true},
		{name: "write result, expanded", tool: "write", content: content, result: wrote, expanded: true, piFallsBack: true},
		{name: "write call, streaming", tool: "write", content: "", streaming: true, piFallsBack: true},
		{name: "write call, empty content", tool: "write", content: ""},
	}
	pairs := [][2]string{
		{"notes.constructor", "notes.constructoq"},
		{"x.__proto__", "x.__proto_q"},
		{"a/b/c.constructor", "a/b/c.constructoq"},
		{"Constructor.CONSTRUCTOR", "Constructor.CONSTRUCTOQ"},
		{"NOTES.__PROTO__", "NOTES.__PROTO_Q"},
		{".constructor", ".constructoq"},
		{"constructor", "constructoq"},
		{"__proto__", "__proto_q"},
		{"a.ts.constructor", "a.ts.constructoq"},
	}
	var probes []toolCardProbe
	for _, pair := range pairs {
		for _, shape := range shapes {
			for _, path := range pair {
				args := map[string]any{"path": path}
				if shape.tool == "write" {
					args["content"] = shape.content
				}
				probe := toolCardProbe{Tool: shape.tool, Args: args, Expanded: shape.expanded, Streaming: shape.streaming, Width: 80, Theme: "dark", CWD: cwd}
				if shape.result != nil {
					probe.Result = shape.result(path)
				}
				probes = append(probes, probe)
			}
		}
	}
	tui.LoadAllHighlightLanguages()
	pi := piToolCardRows(t, probes)
	pig := renderBuiltinToolCards(t, probes)
	if len(pi) != len(probes) || len(pig) != len(probes) {
		t.Fatalf("%d probes: Pi drew %d cards, PiG %d", len(probes), len(pi), len(pig))
	}
	fallbacks := 0
	for i := 0; i < len(probes); i += 2 {
		pair, shape := pairs[i/2/len(shapes)], shapes[i/2%len(shapes)]
		piPath, piTwin := strings.Join(pi[i], "\n"), strings.Join(pi[i+1], "\n")
		pigPath, pigTwin := strings.Join(pig[i], "\n"), strings.Join(pig[i+1], "\n")
		if pigTwin != piTwin {
			t.Errorf("%s of %q (the twin): PiG and Pi differ\n  PiG %q\n  Pi  %q", shape.name, pair[1], pigTwin, piTwin)
		}
		card := strings.ReplaceAll(piTwin, pair[1], pair[0])
		if pigPath != card {
			t.Errorf("%s of %q: PiG does not draw the card of a path without a language\n  PiG  %q\n  want %q", shape.name, pair[0], pigPath, card)
		}
		fellBack := piPath != card
		if fellBack {
			fallbacks++
		}
		if fellBack != shape.piFallsBack {
			t.Errorf("%s of %q: Pi draws its fallback = %v, the record says %v\n  Pi   %q\n  card %q", shape.name, pair[0], fellBack, shape.piFallsBack, piPath, card)
		}
	}
	t.Logf("%d paths in %d shapes: Pi draws its fallback for %d cards, PiG for none", len(pairs), len(shapes), fallbacks)
}
