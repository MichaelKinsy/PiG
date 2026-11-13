package codingagent

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// theme.ts getLanguageFromPath reads a plain object, so for a path whose lowercased extension is constructor or __proto__ it returns a member inherited from
// Object.prototype, and highlightCode throws a TypeError for that value. read.ts formatReadResult (an expanded result that is no error) and write.ts renderCall
// (content that still streams in, or a complete call's content unless it is empty) throw with it, and tool-execution.ts draws the fallback: for the write call the
// tool name and its arguments as key=value pairs, for the read result the output's rows directly under the header, where the read card has a blank row first.
// Every other path draws the cards themselves. TestReadWriteCardsMatchPi compares the same cards with Pi byte for byte.
func TestReadAndWriteCardsDrawTheFallbackForAPathNamedLikeAnObjectPrototypeMember(t *testing.T) {
	for _, tc := range []struct {
		path     string
		fallback bool
	}{
		{"out.constructor", true},
		{"x.__proto__", true},
		{"a/b/c.constructor", true},
		{"Constructor.CONSTRUCTOR", true},
		{".constructor", true},
		{"constructor", true},
		{"out.toString", false},
		{"out.valueOf", false},
		{"out.constructor.ts", false},
		{"out.unknownext", false},
		{"out.ts", false},
	} {
		quoted, err := json.Marshal(tc.path)
		if err != nil {
			t.Fatal(err)
		}
		withContent := func(content string) json.RawMessage {
			return json.RawMessage(`{"path":` + string(quoted) + `,"content":` + content + `}`)
		}
		argument := "path=" + string(quoted)

		write := toolComponentRaw(t, "write", "w-"+tc.path, withContent(`"const x = 1;\n"`), nil)
		write.card.SetArgsComplete()
		if rows := write.plain(120); strings.Contains(rows, argument) != tc.fallback {
			t.Errorf("write %q, complete: call fallback = %v, want %v\n%s", tc.path, !tc.fallback, tc.fallback, rows)
		}

		// A complete call with empty content highlights nothing: the header draws for every path.
		empty := toolComponentRaw(t, "write", "e-"+tc.path, withContent(`""`), nil)
		empty.card.SetArgsComplete()
		if rows := empty.plain(120); strings.Contains(rows, argument) {
			t.Errorf("write %q, complete, empty content: drew the call fallback\n%s", tc.path, rows)
		}

		// While the content streams in, the highlight cache is built from it even when it is still empty.
		streaming := toolComponentRaw(t, "write", "s-"+tc.path, withContent(`""`), nil)
		if rows := streaming.plain(120); strings.Contains(rows, argument) != tc.fallback {
			t.Errorf("write %q, streaming: call fallback = %v, want %v\n%s", tc.path, !tc.fallback, tc.fallback, rows)
		}

		// Content that is no string is the invalid-argument text, with no lookup.
		invalid := toolComponentRaw(t, "write", "i-"+tc.path, withContent(`5`), nil)
		invalid.card.SetArgsComplete()
		if rows := invalid.plain(120); strings.Contains(rows, argument) || !strings.Contains(rows, "[invalid content arg - expected string]") {
			t.Errorf("write %q, number content: want the invalid-argument text\n%s", tc.path, rows)
		}

		read := toolComponentRaw(t, "read", "r-"+tc.path, json.RawMessage(`{"path":`+string(quoted)+`}`), nil)
		read.update(agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "alpha\n\tbeta\n"}}}, false)
		read.card.SetExpanded(true)
		rows := strings.Split(read.plain(120), "\n")
		header := slices.IndexFunc(rows, func(row string) bool { return strings.Contains(row, "read ") })
		if header < 0 || header+1 >= len(rows) {
			t.Fatalf("read %q: no header row followed by output\n%s", tc.path, strings.Join(rows, "\n"))
		}
		if direct := strings.TrimSpace(rows[header+1]) == "alpha"; direct != tc.fallback {
			t.Errorf("read %q, expanded: output directly under the header = %v, want %v\n%s", tc.path, direct, tc.fallback, strings.Join(rows, "\n"))
		}

		// An error result is drawn without a lookup: the read card's blank row and the error text, for every path.
		failed := toolComponentRaw(t, "read", "f-"+tc.path, json.RawMessage(`{"path":`+string(quoted)+`}`), nil)
		failed.update(agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "ENOENT: no such file"}}, IsError: true}, false)
		failed.card.SetExpanded(true)
		rows = strings.Split(failed.plain(120), "\n")
		header = slices.IndexFunc(rows, func(row string) bool { return strings.Contains(row, "read ") })
		if header < 0 || header+2 >= len(rows) || strings.TrimSpace(rows[header+1]) != "" || !strings.Contains(rows[header+2], "ENOENT") {
			t.Errorf("read %q, error: want a blank row then the error text\n%s", tc.path, strings.Join(rows, "\n"))
		}
	}
}
