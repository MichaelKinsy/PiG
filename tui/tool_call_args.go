package tui

// Ports packages/coding-agent/src/core/tools/render-utils.ts formatToolCallWithArgs.

import (
	"bytes"
	"encoding/json"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/jsonstringify"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// collapsedArgsChars is COLLAPSED_ARGS_CHARS (render-utils.ts:71), counted in UTF-16 units like String.length.
const collapsedArgsChars = 100

// toolCallArg is one `Object.entries` entry of the arguments: the key and the value as JSON.stringify writes it.
type toolCallArg struct {
	key  string
	json []byte
}

// toolCallArgEntries is `Object.entries(args)` for a plain object and `[["args", args]]` for any other value
// (render-utils.ts:82-86). ok is false when args is undefined or null, which upstream shows as the title alone.
func toolCallArgEntries(args json.RawMessage) (entries []toolCallArg, ok bool) {
	trimmed := bytes.TrimSpace(args)
	if len(trimmed) == 0 || !json.Valid(trimmed) {
		return nil, false
	}
	// JSON.parse then JSON.stringify: source order except integer keys first, the last of a repeated key, JS number text.
	canonical, err := jsonstringify.Canonicalize(trimmed)
	if err != nil || string(canonical) == "null" {
		return nil, false
	}
	if canonical[0] != '{' {
		return []toolCallArg{{key: "args", json: canonical}}, true
	}
	decoder := json.NewDecoder(bytes.NewReader(canonical))
	if _, err := decoder.Token(); err != nil {
		return nil, false
	}
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return nil, false
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, false
		}
		entries = append(entries, toolCallArg{key: key.(string), json: value})
	}
	return entries, true
}

// FormatToolCallWithArgs is the generic tool call header: the title followed by the arguments. Collapsed, they are
// `key=value` pairs on the title line, cut to 100 characters. Expanded, each is a `key: value` line below the title, with
// strings shown raw and continuation lines indented.
func FormatToolCallWithArgs(title string, args json.RawMessage, theme *Theme, expanded bool) string {
	if theme == nil {
		theme = ActiveTheme()
	}
	header := theme.FgText("toolTitle", boldText(title))
	entries, ok := toolCallArgEntries(args)
	if !ok || len(entries) == 0 {
		return header
	}
	if expanded {
		lines := make([]string, len(entries))
		for i, entry := range entries {
			text := string(entry.json)
			if text[0] == '"' {
				var raw string
				if json.Unmarshal(entry.json, &raw) == nil {
					text = raw
				}
			} else {
				var indented bytes.Buffer
				if json.Indent(&indented, entry.json, "", "  ") == nil {
					text = indented.String()
				}
			}
			text = strings.ReplaceAll(strings.ReplaceAll(text, "\t", "   "), "\r", "")
			lines[i] = "  " + entry.key + ": " + strings.Join(strings.Split(text, "\n"), "\n    ")
		}
		return header + "\n" + theme.FgText("muted", strings.Join(lines, "\n"))
	}
	pairs := make([]string, len(entries))
	for i, entry := range entries {
		pairs[i] = entry.key + "=" + string(entry.json)
	}
	joined := strings.Join(pairs, " ")
	preview := joined
	if jsstring.Length(joined) > collapsedArgsChars {
		preview = jsstring.Slice(joined, 0, collapsedArgsChars-3) + "..."
	}
	return header + " " + theme.FgText("muted", preview)
}
