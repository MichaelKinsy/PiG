// Ports packages/durable/src/codemode/output.ts.

package codemode

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/MichaelKinsy/PiG/ai"
	cm "github.com/MichaelKinsy/PiG/codemode"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// outputFile writes a file of the result under a fixed name; its path, or why it could not be written.
type outputFile func(name string, data any) (path string, failure string)

// outputLimits are the tool's output limits, which the Harness enforces by cutting without a spill file: results stay
// inside them.
type outputLimits struct{ maxBytes, maxLines int }

// lineMargin is the lines of the result besides the script's text: the header, markers, notes.
const lineMargin = 100

// DefaultMaxOutputTokens is the default token budget for script output.
const DefaultMaxOutputTokens = 10_000

// charsPerTokenOutput is the characters per token when estimating.
const charsPerTokenOutput = 4

// imageExtensions are the file extensions of the image types `image()` accepts.
var imageExtensions = map[string]string{"image/png": ".png", "image/jpeg": ".jpg", "image/gif": ".gif", "image/webp": ".webp"}

// valueText is like the script's `text()`: strings as is, other values as compact JSON.
func valueText(value json.RawMessage) string {
	var text string
	if json.Unmarshal(value, &text) == nil && bytes.HasPrefix(bytes.TrimSpace(value), []byte(`"`)) {
		return text
	}
	return string(compact(value))
}

// formatItems lays out the script's output so the model can tell items apart: with more than one text item, each starts
// with a `==> text N/M <==` line; `console` lines follow all other output in one `<console_output>` block.
func formatItems(output []cm.OutputItem) []ai.ToolResultMessageContent {
	total := 0
	for _, item := range output {
		if item.Type == cm.OutputItemText && !item.Console {
			total++
		}
	}
	var items []ai.ToolResultMessageContent
	var consoleLines []string
	index := 0
	for _, item := range output {
		switch {
		case item.Type == cm.OutputItemImage:
			items = append(items, ai.ImageContent{Data: item.Data, MimeType: item.MimeType})
		case item.Console:
			consoleLines = append(consoleLines, item.Text)
		default:
			index++
			text := item.Text
			if total > 1 {
				text = fmt.Sprintf("==> text %d/%d <==\n%s", index, total, item.Text)
			}
			items = append(items, ai.TextContent{Text: text})
		}
	}
	if len(consoleLines) > 0 {
		items = append(items, ai.TextContent{Text: "<console_output>\n" + strings.Join(consoleLines, "\n") + "\n</console_output>"})
	}
	return items
}

// joinAdjacentText joins adjacent text items into one, each part starting on its own line. The separator check looks at the
// last non-empty part, not at the joined text, and each run is joined once, so the work is linear in the output.
//
// Ports packages/durable/src/codemode/output.ts (joinAdjacentText).
func joinAdjacentText(items []ai.ToolResultMessageContent) []ai.ToolResultMessageContent {
	var joined []ai.ToolResultMessageContent
	var parts []string
	lastPart := ""
	flush := func() {
		if len(parts) > 0 {
			joined = append(joined, ai.TextContent{Text: strings.Join(parts, "")})
		}
		parts, lastPart = nil, ""
	}
	for _, item := range items {
		text, isText := item.(ai.TextContent)
		if !isText {
			flush()
			joined = append(joined, item)
			continue
		}
		if lastPart != "" && !strings.HasSuffix(lastPart, "\n") {
			parts = append(parts, "\n")
			lastPart = "\n"
		}
		parts = append(parts, text.Text)
		if text.Text != "" {
			lastPart = text.Text
		}
	}
	flush()
	return joined
}

// truncate applies the token budget: when the combined text exceeds it, the text items become one item that keeps the
// start and end of the text, and images follow it. The complete text is written to a temp file.
func truncate(items []ai.ToolResultMessageContent, maxTokens int, limits outputLimits, write outputFile) ([]ai.ToolResultMessageContent, string) {
	var texts []string
	for _, item := range items {
		if text, ok := item.(ai.TextContent); ok {
			texts = append(texts, text.Text)
		}
	}
	combined := strings.Join(texts, "\n")
	// Within the limits too: a UTF-16 unit is at most 3 UTF-8 bytes, and the rest of the result is a few short lines.
	//portlint:allow numbers maxBytes is OUTPUT_LIMITS' positive constant, so the truncation is Math.floor(limits.maxBytes / 4) (output.ts:107)
	budget := min(maxTokens*charsPerTokenOutput, limits.maxBytes/4)
	//portlint:allow numbers maxLines (100_000) exceeds LINE_MARGIN, so the truncation is Math.floor of a positive quotient (output.ts:108)
	lineBudget := (limits.maxLines - lineMargin) / 2
	lines := strings.Count(combined, "\n") + 1
	length := jsstring.Length(combined)
	if len(texts) == 0 || (length <= budget && lines <= 2*lineBudget) {
		return slicesCopy(items), ""
	}
	//portlint:allow numbers budget is non-negative (max_output_tokens is a non-negative safe integer), so this is Math.floor(budget / 2)
	headChars := budget / 2
	tailChars := budget - headChars
	head := strings.Join(firstLines(strings.Split(jsstring.Slice(combined, 0, headChars), "\n"), lineBudget), "\n")
	tail := ""
	if tailChars > 0 {
		tail = strings.Join(lastLines(strings.Split(jsstring.Slice(combined, -tailChars), "\n"), lineBudget), "\n")
	}
	removed := length - jsstring.Length(head) - jsstring.Length(tail)
	text := fmt.Sprintf("Warning: truncated output (original token count: %d)\nTotal output lines: %d\n\n%s…%d tokens truncated…%s",
		ceilDiv(length, charsPerTokenOutput), lines, head, ceilDiv(removed, charsPerTokenOutput), tail)
	path, failure := write("output.txt", combined)
	fullOutputPath := ""
	if failure == "" {
		text += "\n\n[Full output: " + path + " (read with offset/limit)]"
		fullOutputPath = path
	} else {
		text += "\n\n[Could not save the full output: " + failure + "]"
	}
	truncated := []ai.ToolResultMessageContent{ai.TextContent{Text: text}}
	for _, item := range items {
		if image, ok := item.(ai.ImageContent); ok {
			truncated = append(truncated, image)
		}
	}
	return truncated, fullOutputPath
}

func slicesCopy(items []ai.ToolResultMessageContent) []ai.ToolResultMessageContent {
	return append([]ai.ToolResultMessageContent(nil), items...)
}

// ceilDiv is Math.ceil(a / b) for a >= 0 and b > 0: the callers pass a text length and the units truncate removed, which
// is never negative because head and tail come from disjoint lines or together stay within a budget below the length.
func ceilDiv(a, b int) int {
	//portlint:allow numbers a >= 0 and b > 0, where (a + b - 1) / b is Math.ceil(a / b)
	return (a + b - 1) / b
}

// firstLines is lines.slice(0, n).
func firstLines(lines []string, n int) []string { return lines[:min(n, len(lines))] }

// lastLines is lines.slice(-n): the last n lines, and all of them for n of 0 (slice(-0) is slice(0)).
func lastLines(lines []string, n int) []string {
	if n <= 0 || n >= len(lines) {
		return lines
	}
	return lines[len(lines)-n:]
}

// decodeBase64 is atob: ASCII whitespace is ignored and the padding may be missing.
func decodeBase64(data string) ([]byte, error) {
	cleaned := strings.Map(func(r rune) rune {
		if r == ' ' || r == '\t' || r == '\n' || r == '\f' || r == '\r' {
			return -1
		}
		return r
	}, data)
	cleaned = strings.TrimRight(cleaned, "=")
	return base64.RawStdEncoding.DecodeString(cleaned)
}

// saveImages saves each image to a file and puts a text item with its path before it; an image shown twice is saved once.
func saveImages(items []ai.ToolResultMessageContent, write outputFile) ([]ai.ToolResultMessageContent, error) {
	labels := map[string]string{}
	var result []ai.ToolResultMessageContent
	for _, item := range items {
		image, ok := item.(ai.ImageContent)
		if !ok {
			result = append(result, item)
			continue
		}
		label, seen := labels[image.Data]
		if !seen {
			bytes, err := decodeBase64(image.Data)
			if err != nil {
				return nil, fmt.Errorf("The string to be decoded is not correctly encoded.")
			}
			kind := fmt.Sprintf("%s, %d bytes", image.MimeType, len(bytes))
			extension, known := imageExtensions[image.MimeType]
			if !known {
				extension = ".bin"
			}
			// A failed write must not discard the result of a script whose calls already ran: it becomes part of the label.
			path, failure := write(fmt.Sprintf("image-%d%s", len(labels)+1, extension), bytes)
			if failure == "" {
				label = "[Image saved to " + path + " (" + kind + ")]"
			} else {
				label = "[Image (" + kind + ") could not be saved: " + failure + "]"
			}
			labels[image.Data] = label
		}
		result = append(result, ai.TextContent{Text: label}, image)
	}
	return result, nil
}

func formatError(failure *cm.Error, calls []NestedCall) string {
	var head string
	switch failure.Kind {
	case cm.ErrorScript:
		head = failure.Stack
		if head == "" {
			name := failure.Name
			if name == "" {
				name = "Error"
			}
			head = name + ": " + failure.Message
		}
	case cm.ErrorTimeout:
		head = "Script timed out: " + failure.Message
	case cm.ErrorAborted:
		head = "Script aborted: " + failure.Message
	default:
		head = "Script sandbox failed: " + failure.Message
	}
	summary := "No tool calls were made."
	if len(calls) > 0 {
		made := make([]string, len(calls))
		for i, call := range calls {
			made[i] = call.Name + " (" + call.Status + ")"
		}
		summary = "Tool calls made before the failure (they are not undone): " + strings.Join(made, ", ")
	}
	return head + "\n\n" + summary
}

// formatOptions are what formatResult needs besides the script's result.
type formatOptions struct {
	// generatedImages are the images `models.generateImages()` returned; when the script showed none, the result says so.
	generatedImages int
	wallMs          float64
	maxOutputTokens int
	calls           []NestedCall
	limits          outputLimits
	write           outputFile
}

// formatResult is the tool result's content for a script's result, and the path of the complete text when it was cut.
func formatResult(result cm.Result, options formatOptions) ([]ai.ToolResultMessageContent, string, error) {
	output := append([]cm.OutputItem(nil), result.Output...)
	if result.OK && result.Value != nil {
		output = append(output, cm.OutputItem{Type: cm.OutputItemText, Text: valueText(result.Value)})
	}
	items := formatItems(output)
	if !result.OK {
		items = append(items, ai.TextContent{Text: "Script error:\n" + formatError(result.Error, options.calls)})
	}
	if generated := options.generatedImages; generated > 0 {
		shown := false
		for _, item := range items {
			if _, ok := item.(ai.ImageContent); ok {
				shown = true
			}
		}
		if !shown {
			plural := "s"
			if generated == 1 {
				plural = ""
			}
			items = append(items, ai.TextContent{Text: fmt.Sprintf("Note: models.generateImages() returned %d image%s that the script did not show. Show each image block of result.output with image(block).", generated, plural)})
		}
	}
	truncated, fullOutputPath := truncate(joinAdjacentText(items), options.maxOutputTokens, options.limits, options.write)
	// After truncation, which joins the text items and moves images after them, so each path stays next to its image.
	saved, err := saveImages(truncated, options.write)
	if err != nil {
		return nil, "", err
	}
	status := "Script failed"
	if result.OK {
		status = "Script completed"
	}
	header := fmt.Sprintf("%s\nWall time %s seconds\nOutput:\n", status, jsstring.ToFixed(options.wallMs/1000, 1))
	return append([]ai.ToolResultMessageContent{ai.TextContent{Text: header}}, joinAdjacentText(saved)...), fullOutputPath, nil
}
