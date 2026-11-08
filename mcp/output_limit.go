package mcp

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// Ports packages/mcp/src/output-limit.ts (Pi 76c040a2a, moved from coding-agent's extensions/mcp/tools.ts and core/tools/truncate.ts).
//
// The size limit of the model-facing text of MCP results. Longer text keeps its start and end with the middle cut out, like Codex does, and the
// caller may save the full text where the model can read it.

// McpOutputMaxBytes bounds the model-facing text of an MCP result; text beyond it is cut in the middle.
const McpOutputMaxBytes = 20 * 1024

// McpOutputSaver saves data the model cannot get in full, such as the full text of a truncated result, and returns where it was saved (a path the
// model can read). extension includes the dot, for example `.txt`.
type McpOutputSaver func(data []byte, extension string) (string, error)

// LimitedMcpContent is model-facing content within the limit, and where the full text went when it was saved.
type LimitedMcpContent struct {
	Content []LLMContent
	// FullOutputPath is where saveOutput saved the full text, or "".
	FullOutputPath string
	// Truncated reports that the text was over the limit, so Content is not the content passed in. Go mechanic: Pi returns the same array
	// when the content is within the limit, which a caller tells by identity.
	Truncated bool
}

// LimitMcpContent keeps model-facing text within [McpOutputMaxBytes]. Longer text becomes one text block in Codex's truncation format, followed
// by the path of the file with the full text when saveOutput is not nil; images follow it. Content within the limit is returned as it is.
func LimitMcpContent(content []LLMContent, saveOutput McpOutputSaver) LimitedMcpContent {
	var texts []string
	for _, block := range content {
		if block.Type == LLMContentTypeText {
			texts = append(texts, block.Text)
		}
	}
	combined := strings.Join(texts, "\n")
	truncation, truncated := truncateMiddle(combined, McpOutputMaxBytes)
	if !truncated {
		return LimitedMcpContent{Content: content}
	}
	var fullOutputPath, where string
	if saveOutput != nil {
		if path, err := saveOutput([]byte(combined), ".txt"); err == nil {
			fullOutputPath = path
			where = "\n\n[Full output: " + path + " (read it with offset/limit)]"
		} else {
			where = "\n\n[Could not save the full output: " + err.Error() + "]"
		}
	}
	//portlint:allow numbers Math.ceil(totalBytes / 4) of a non-negative byte count is (totalBytes + 3) / 4 (output-limit.ts limitMcpContent)
	tokens := (truncation.totalBytes + 3) / 4
	text := fmt.Sprintf("Warning: truncated output (original token count: %d)\nTotal output lines: %d\n\n%s%s", tokens, truncation.totalLines, truncation.content, where)
	limited := []LLMContent{{Type: LLMContentTypeText, Text: text}}
	for _, block := range content {
		if block.Type == LLMContentTypeImage {
			limited = append(limited, block)
		}
	}
	return LimitedMcpContent{Content: limited, FullOutputPath: fullOutputPath, Truncated: true}
}

// middleTruncation is text cut in the middle, with the size of the whole text.
type middleTruncation struct {
	// content is the start and the end of the text with a `…N chars truncated…` marker between them.
	content    string
	totalBytes int
	totalLines int
}

// truncateMiddle keeps the start and the end of text, half of maxBytes each, and replaces the middle with a `…N chars truncated…` marker. It cuts
// only at character boundaries. The second result is false when text fits.
func truncateMiddle(text string, maxBytes int) (middleTruncation, bool) {
	//portlint:allow utf16units upstream compares the Buffer byte length (output-limit.ts truncateMiddle)
	if len(text) <= maxBytes {
		return middleTruncation{}, false
	}
	// Continuation bytes (10xxxxxx) are not character starts.
	isBoundary := func(index int) bool { return index >= len(text) || text[index]&0xc0 != 0x80 }
	//portlint:allow numbers Math.floor(maxBytes / 2) of a non-negative byte count (output-limit.ts truncateMiddle)
	headEnd := maxBytes / 2
	for headEnd > 0 && !isBoundary(headEnd) {
		headEnd--
	}
	//portlint:allow numbers Math.floor(maxBytes / 2) of a non-negative byte count (output-limit.ts truncateMiddle)
	//portlint:allow utf16units upstream walks the Buffer UTF-8 bytes (output-limit.ts truncateMiddle)
	tailStart := len(text) - (maxBytes - maxBytes/2)
	//portlint:allow utf16units upstream walks the Buffer UTF-8 bytes (output-limit.ts truncateMiddle)
	for tailStart < len(text) && !isBoundary(tailStart) {
		tailStart++
	}
	//portlint:allow utf16units upstream cuts the Buffer of the text UTF-8 bytes at character starts, which is what these byte offsets do (output-limit.ts truncateMiddle)
	removedChars := utf8.RuneCountInString(text[headEnd:tailStart])
	// A final newline ends the last line instead of starting another.
	lines := strings.Count(text, "\n") + 1
	if strings.HasSuffix(text, "\n") {
		lines--
	}
	return middleTruncation{
		//portlint:allow utf16units upstream cuts the Buffer of the text UTF-8 bytes at character starts, which is what these byte offsets do (output-limit.ts truncateMiddle)
		content:    fmt.Sprintf("%s…%d chars truncated…%s", text[:headEnd], removedChars, text[tailStart:]),
		totalBytes: len(text),
		totalLines: lines,
	}, true
}
