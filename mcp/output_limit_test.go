package mcp_test

import (
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/mcp"
)

var testImage = mcp.LLMContent{Type: mcp.LLMContentTypeImage, Data: "AAAA", MimeType: "image/png"}

func textBlock(text string) mcp.LLMContent {
	return mcp.LLMContent{Type: mcp.LLMContentTypeText, Text: text}
}

func firstText(t *testing.T, content []mcp.LLMContent) string {
	t.Helper()
	if len(content) == 0 || content[0].Type != mcp.LLMContentTypeText {
		t.Fatalf("expected a text block first, got %+v", content)
	}
	return content[0].Text
}

// Pi packages/mcp/test/output-limit.test.ts "returns content within the limit as it is, without saving": packages/mcp/src/output-limit.ts limitMcpContent.
// mutation-checked: `>=` for `>` at the limit, a limit other than 20KB, and a save before the limit check fail it.
func TestLimitMcpContentReturnsContentWithinTheLimitAsItIs(t *testing.T) {
	content := []mcp.LLMContent{textBlock(strings.Repeat("x", mcp.McpOutputMaxBytes)), testImage}
	saves := 0
	limited := mcp.LimitMcpContent(content, func([]byte, string) (string, error) {
		saves++
		return "/tmp/full.txt", nil
	})
	if !reflect.DeepEqual(limited, mcp.LimitedMcpContent{Content: content}) {
		t.Errorf("limited = %+v, want the content as it is", limited)
	}
	if saves != 0 {
		t.Errorf("saves = %d, want 0", saves)
	}
}

// Pi packages/mcp/test/output-limit.test.ts "cuts the middle of longer text, saves the full text and keeps images".
// mutation-checked: joining text blocks with a space, a token count rounded down, a line count off by one, a saved text other than the joined text, a `.bin` extension, a dropped image, and a missing path line fail it.
func TestLimitMcpContentCutsTheMiddleOfLongerTextSavesTheFullTextAndKeepsImages(t *testing.T) {
	lines := make([]string, 3000)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %d", i+1)
	}
	full := strings.Join(lines, "\n")
	type saved struct{ data, extension string }
	var saves []saved
	split := 100 + strings.Index(full[100:], "\n")
	limited := mcp.LimitMcpContent(
		[]mcp.LLMContent{textBlock(full[:split]), testImage, textBlock(full[split+1:])},
		func(data []byte, extension string) (string, error) {
			saves = append(saves, saved{string(data), extension})
			return "/tmp/full.txt", nil
		},
	)
	// Text blocks are joined with newlines before the limit applies.
	if !reflect.DeepEqual(saves, []saved{{full, ".txt"}}) {
		t.Errorf("saves = %+v, want the joined text once as .txt", saves)
	}
	if limited.FullOutputPath != "/tmp/full.txt" {
		t.Errorf("FullOutputPath = %q", limited.FullOutputPath)
	}
	if len(limited.Content) != 2 {
		t.Fatalf("content = %+v, want the text and the image", limited.Content)
	}
	if limited.Content[1] != testImage {
		t.Errorf("content[1] = %+v, want the image", limited.Content[1])
	}
	text := firstText(t, limited.Content)
	head := regexp.MustCompile(`^Warning: truncated output \(original token count: ` + strconv.Itoa((len(full)+3)/4) + `\)\nTotal output lines: 3000\n\nline 1\nline 2\n`)
	if !head.MatchString(text) {
		t.Errorf("text starts %.120q", text)
	}
	if !regexp.MustCompile(`…\d+ chars truncated…`).MatchString(text) {
		t.Error("no truncation marker")
	}
	if !strings.HasSuffix(text, "line 3000\n\n[Full output: /tmp/full.txt (read it with offset/limit)]") {
		t.Errorf("text ends %q", text[len(text)-90:])
	}
	if len(text) >= mcp.McpOutputMaxBytes+1024 {
		t.Errorf("len(text) = %d, want below %d", len(text), mcp.McpOutputMaxBytes+1024)
	}
}

// Pi packages/mcp/test/output-limit.test.ts "reports a failed save, and names no file without a saver".
// mutation-checked: a failed save that reports a path, a missing error line, and a path line for a missing saver fail it.
func TestLimitMcpContentReportsAFailedSaveAndNamesNoFileWithoutASaver(t *testing.T) {
	content := []mcp.LLMContent{textBlock(strings.Repeat("y", mcp.McpOutputMaxBytes+1))}
	failed := mcp.LimitMcpContent(content, func([]byte, string) (string, error) { return "", errors.New("disk full") })
	if failed.FullOutputPath != "" {
		t.Errorf("FullOutputPath = %q after a failed save", failed.FullOutputPath)
	}
	if text := firstText(t, failed.Content); !strings.HasSuffix(text, "\n\n[Could not save the full output: disk full]") {
		t.Errorf("text ends %q", text[len(text)-60:])
	}

	unsaved := mcp.LimitMcpContent(content, nil)
	if unsaved.FullOutputPath != "" {
		t.Errorf("FullOutputPath = %q without a saver", unsaved.FullOutputPath)
	}
	if text := firstText(t, unsaved.Content); !regexp.MustCompile(`(?s)^Warning: truncated output .*…\d+ chars truncated…y+$`).MatchString(text) {
		t.Errorf("text without a saver = %.80q … %q", text, text[len(text)-30:])
	}
}

// Pi packages/mcp/test/output-limit.test.ts "cuts multi-byte text only at character boundaries" (moved from coding-agent's mcp-extension.test.ts).
// mutation-checked: a head walk or a tail walk that does not move to a character start, and a removed-character count taken in bytes fail it.
func TestLimitMcpContentCutsMultiByteTextOnlyAtCharacterBoundaries(t *testing.T) {
	// After the one-byte prefix, characters start at odd bytes, and both cuts (byte 10240 and byte
	// 40004 - 10240) are even: each lands inside a two-byte character.
	text := "a" + strings.Repeat("é", 20_000) + "end"
	limited := firstText(t, mcp.LimitMcpContent([]mcp.LLMContent{textBlock(text)}, nil).Content)
	if strings.ContainsRune(limited, utf8.RuneError) {
		t.Error("the cut text contains a replacement character")
	}
	if !strings.HasSuffix(limited, "end") {
		t.Error("the cut text does not end with the end of the text")
	}
	marker := regexp.MustCompile(`…(\d+) chars truncated…`).FindStringSubmatch(limited)
	if marker == nil {
		t.Fatal("expected the truncation marker")
	}
	_, afterWarning, _ := strings.Cut(limited, "\n\n")
	head, tail, _ := strings.Cut(afterWarning, marker[0])
	if len(head) > mcp.McpOutputMaxBytes/2 || len(tail) > mcp.McpOutputMaxBytes/2 {
		t.Errorf("head %d bytes, tail %d bytes, want at most %d each", len(head), len(tail), mcp.McpOutputMaxBytes/2)
	}
	removed, _ := strconv.Atoi(marker[1])
	if utf8.RuneCountInString(head)+utf8.RuneCountInString(tail)+removed != utf8.RuneCountInString(text) {
		t.Error("kept and removed characters do not add up")
	}
}

// upstream limitMcpContent reports Math.ceil(totalBytes / 4) as the original token count, and a failed save in place of the path (moved from coding-agent's mcp extension tests).
// mutation-checked: a token count rounded down and a failed save that keeps a path fail it.
func TestLimitMcpContentRoundsTheOriginalTokenCountUp(t *testing.T) {
	content := []mcp.LLMContent{textBlock(strings.Repeat("x", 30_001))}
	limited := mcp.LimitMcpContent(content, func([]byte, string) (string, error) { return "/tmp/full.txt", nil })
	if limited.FullOutputPath != "/tmp/full.txt" || len(limited.Content) != 1 {
		t.Fatalf("limited = %+v", limited)
	}
	if got := limited.Content[0].Text; !strings.HasPrefix(got, "Warning: truncated output (original token count: 7501)\n") {
		t.Fatalf("text = %q", got[:80])
	}
	failed := mcp.LimitMcpContent(content, func([]byte, string) (string, error) { return "", errors.New("disk full") })
	if got := failed.Content[0].Text; failed.FullOutputPath != "" || !strings.HasSuffix(got, "[Could not save the full output: disk full]") {
		t.Fatalf("path = %q, text tail = %q", failed.FullOutputPath, got[len(got)-60:])
	}
}

// Pi packages/mcp/src/output-limit.ts MCP_OUTPUT_MAX_BYTES is 20 * 1024.
// mutation-checked: a limit of 21KB fails it.
func TestMcpOutputMaxBytesIs20KB(t *testing.T) {
	if mcp.McpOutputMaxBytes != 20*1024 {
		t.Errorf("McpOutputMaxBytes = %d, want %d", mcp.McpOutputMaxBytes, 20*1024)
	}
	content := []mcp.LLMContent{textBlock(strings.Repeat("x", 20*1024+1))}
	if limited := mcp.LimitMcpContent(content, nil); reflect.DeepEqual(limited.Content, content) {
		t.Error("text one byte over 20KB is not cut")
	}
}
