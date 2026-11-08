package tools

// Copied from coding-agent core/tools/render-utils.ts without `node:os` homedir, `pathToFileURL` hyperlinks or local path resolution (section 11):
// paths display relative to the session cwd. Output text is sanitized server text.
//
// pi: apps/p2/src/tui/tools/render-utils.ts

import (
	"fmt"
	"strings"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/jsvalue"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/render"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/theme"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
	pitui "github.com/MichaelKinsy/PiG/tui"
)

// ShortenPath displays paths under the session cwd relative to it. A non-string path displays as "".
func ShortenPath(path any, cwd string) string {
	text, ok := path.(string)
	if !ok {
		return ""
	}
	root := strings.TrimRight(cwd, "/")
	if root != "" && strings.HasPrefix(text, root+"/") {
		return text[len(root)+1:]
	}
	return text
}

// Str is str: a string value as itself, a null or undefined value as "", anything else as not ok.
func Str(value any) (string, bool) {
	switch typed := value.(type) {
	case string:
		return typed, true
	case nil:
		return "", true
	}
	return "", false
}

// Num is num: a finite number; ok is false otherwise.
func Num(value any) (float64, bool) {
	n, ok := value.(float64)
	if !ok || n != n || n > 1.7976931348623157e308 || n < -1.7976931348623157e308 {
		return 0, false
	}
	return n, true
}

// ReplaceTabs replaces tabs with three spaces.
func ReplaceTabs(text string) string { return strings.ReplaceAll(text, "\t", "   ") }

// NormalizeDisplayText drops carriage returns.
func NormalizeDisplayText(text string) string { return strings.ReplaceAll(text, "\r", "") }

// GetTextOutput is getTextOutput: the text of a result with image placeholders (the worker never draws images).
func GetTextOutput(result *ToolResultView) string {
	if result == nil {
		return ""
	}
	var texts []string
	var images []ai.ImageContent
	for _, part := range result.Content {
		switch typed := part.(type) {
		case ai.TextContent:
			texts = append(texts, render.SafeText(typed.Text))
		case *ai.TextContent:
			texts = append(texts, render.SafeText(typed.Text))
		case ai.ImageContent:
			images = append(images, typed)
		case *ai.ImageContent:
			images = append(images, *typed)
		}
	}
	output := strings.ReplaceAll(strings.Join(texts, "\n"), "\r", "")
	if len(images) > 0 {
		indicators := make([]string, len(images))
		for i, image := range images {
			mime := image.MimeType
			if mime == "" {
				mime = "image/unknown"
			}
			var dimensions *pitui.ImageDimensions
			if image.Data != "" {
				dimensions = pitui.GetImageDimensions(image.Data, image.MimeType)
			}
			indicators[i] = pitui.ImageFallback(render.SafeText(mime), dimensions, "")
		}
		joined := strings.Join(indicators, "\n")
		if output != "" {
			output += "\n" + joined
		} else {
			output = joined
		}
	}
	return output
}

const collapsedArgsChars = 100

// FormatToolCallWithArgs is the generic tool call header: the title followed by the arguments (collapsed `key=value`, expanded `key: value` lines).
func FormatToolCallWithArgs(title string, args any, t *theme.Theme, expanded bool) string {
	header := t.Fg("toolTitle", t.Bold(render.SafeText(title)))
	if args == nil {
		return header
	}
	type entry struct {
		key   string
		value any
	}
	var entries []entry
	if object, ok := args.(*jsvalue.Object); ok {
		for _, key := range object.Keys {
			entries = append(entries, entry{key, object.Values[key]})
		}
	} else if _, isList := args.([]any); isList {
		entries = []entry{{"args", args}}
	} else {
		entries = []entry{{"args", args}}
	}
	if len(entries) == 0 {
		return header
	}
	if expanded {
		lines := make([]string, len(entries))
		for i, e := range entries {
			var text string
			if s, ok := e.value.(string); ok {
				text = s
			} else {
				text, _ = jsvalue.Stringify(e.value, 2)
			}
			lines[i] = "  " + e.key + ": " + strings.ReplaceAll(ReplaceTabs(render.SafeText(text)), "\n", "\n    ")
		}
		return header + "\n" + t.Fg("muted", strings.Join(lines, "\n"))
	}
	pairs := make([]string, len(entries))
	for i, e := range entries {
		text, _ := jsvalue.Stringify(e.value, 0)
		pairs[i] = e.key + "=" + text
	}
	joined := strings.ReplaceAll(render.SafeText(strings.Join(pairs, " ")), "\n", " ")
	preview := joined
	if jsstring.Length(joined) > collapsedArgsChars {
		preview = jsSlice(joined, collapsedArgsChars-3) + "..."
	}
	return header + " " + t.Fg("muted", preview)
}

// jsSlice is text.slice(0, n) over UTF-16 code units.
func jsSlice(text string, n int) string {
	units := 0
	for i, r := range text {
		width := 1
		if r > 0xffff {
			width = 2
		}
		if units+width > n {
			return text[:i]
		}
		units += width
	}
	return text
}

// InvalidArgText is invalidArgText.
func InvalidArgText(t *theme.Theme) string { return t.Fg("error", "[invalid arg]") }

// RenderToolPathOptions are renderToolPath's options.
type RenderToolPathOptions struct{ EmptyFallback string }

// RenderToolPath is renderToolPath; ok is false for the `null` (invalid) raw path.
func RenderToolPath(rawPath string, valid bool, t *theme.Theme, cwd string, options RenderToolPathOptions) string {
	if !valid {
		return InvalidArgText(t)
	}
	value := rawPath
	if value == "" {
		value = options.EmptyFallback
	}
	if value == "" {
		return t.Fg("toolOutput", "...")
	}
	return t.Fg("accent", render.SafeText(ShortenPath(value, cwd)))
}

// MoreLinesHint is `... (N more lines, ctrl+o to expand)`; noun is "more lines" by default.
func MoreLinesHint(ui render.RenderContext, t *theme.Theme, remaining int, noun string) string {
	if noun == "" {
		noun = "more lines"
	}
	return t.Fg("muted", fmt.Sprintf("... (%d %s,", remaining, noun)) + " " + render.KeyHint(ui, "app.tools.expand", "to expand") + t.Fg("muted", ")")
}

// FormatSize is Durable's `formatSize` (truncate.ts).
func FormatSize(bytes int) string {
	switch {
	case bytes < 1024:
		return fmt.Sprintf("%dB", bytes)
	case bytes < 1024*1024:
		return jsstring.ToFixed(float64(bytes)/1024, 1) + "KB"
	}
	return jsstring.ToFixed(float64(bytes)/(1024*1024), 1) + "MB"
}

// Durable's DEFAULT_MAX_BYTES and DEFAULT_MAX_LINES.
const (
	DefaultMaxBytes = 50 * 1024
	DefaultMaxLines = 2000
)

// TrimTrailingEmptyLines drops the empty lines at the end.
func TrimTrailingEmptyLines(lines []string) []string {
	end := len(lines)
	for end > 0 && lines[end-1] == "" {
		end--
	}
	return lines[:end]
}
