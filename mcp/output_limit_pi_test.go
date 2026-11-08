package mcp_test

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/mcp"
)

// Ports packages/mcp/test/output-limit.test.ts (Pi 76c040a2a).

var limitImage = mcp.LLMContent{Type: mcp.LLMContentTypeImage, Data: "AAAA", MimeType: "image/png"}

func limitText(t *testing.T, content []mcp.LLMContent) string {
	t.Helper()
	if len(content) == 0 || content[0].Type != mcp.LLMContentTypeText {
		t.Fatal("expected a text block first")
	}
	return content[0].Text
}

func TestLimitMcpContentReturnsContentWithinTheLimitAsItIsWithoutSaving(t *testing.T) {
	content := []mcp.LLMContent{{Type: mcp.LLMContentTypeText, Text: strings.Repeat("x", mcp.McpOutputMaxBytes)}, limitImage}
	saves := 0
	limited := mcp.LimitMcpContent(content, func([]byte, string) (string, error) { saves++; return "/tmp/full.txt", nil })
	if limited.Truncated || limited.FullOutputPath != "" || len(limited.Content) != 2 || &limited.Content[0] != &content[0] || saves != 0 {
		t.Fatalf("limited = %+v, saves = %d", limited, saves)
	}
}

func TestLimitMcpContentCutsTheMiddleOfLongerTextSavesTheFullTextAndKeepsImages(t *testing.T) {
	lines := make([]string, 3000)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %d", i+1)
	}
	full := strings.Join(lines, "\n")
	type save struct{ data, extension string }
	var saved []save
	split := 100 + strings.Index(full[100:], "\n")
	limited := mcp.LimitMcpContent([]mcp.LLMContent{
		{Type: mcp.LLMContentTypeText, Text: full[:split]}, limitImage, {Type: mcp.LLMContentTypeText, Text: full[split+1:]},
	}, func(data []byte, extension string) (string, error) {
		saved = append(saved, save{string(data), extension})
		return "/tmp/full.txt", nil
	})
	// Text blocks are joined with newlines before the limit applies.
	if len(saved) != 1 || saved[0] != (save{full, ".txt"}) {
		t.Fatalf("saved = %d entries", len(saved))
	}
	if limited.FullOutputPath != "/tmp/full.txt" || len(limited.Content) != 2 || limited.Content[1] != limitImage {
		t.Fatalf("limited = %+v", limited)
	}
	text := limitText(t, limited.Content)
	head := fmt.Sprintf("Warning: truncated output (original token count: %d)\nTotal output lines: 3000\n\nline 1\nline 2\n", (len(full)+3)/4)
	if !strings.HasPrefix(text, head) || !regexp.MustCompile(`…\d+ chars truncated…`).MatchString(text) {
		t.Fatalf("text = %.200q", text)
	}
	if !strings.HasSuffix(text, "line 3000\n\n[Full output: /tmp/full.txt (read it with offset/limit)]") || len(text) >= mcp.McpOutputMaxBytes+1024 {
		t.Fatalf("text ends %q (%d bytes)", text[len(text)-80:], len(text))
	}
}

func TestLimitMcpContentReportsAFailedSaveAndNamesNoFileWithoutASaver(t *testing.T) {
	content := []mcp.LLMContent{{Type: mcp.LLMContentTypeText, Text: strings.Repeat("y", mcp.McpOutputMaxBytes+1)}}
	failed := mcp.LimitMcpContent(content, func([]byte, string) (string, error) { return "", errors.New("disk full") })
	if failed.FullOutputPath != "" || !strings.HasSuffix(limitText(t, failed.Content), "\n\n[Could not save the full output: disk full]") {
		t.Fatalf("failed = %+v", failed)
	}
	unsaved := mcp.LimitMcpContent(content, nil)
	if unsaved.FullOutputPath != "" || !regexp.MustCompile(`(?s)^Warning: truncated output .*…\d+ chars truncated…y+$`).MatchString(limitText(t, unsaved.Content)) {
		t.Fatalf("unsaved = %.200q", limitText(t, unsaved.Content))
	}
}

func TestLimitMcpContentCutsMultiByteTextOnlyAtCharacterBoundaries(t *testing.T) {
	// After the one-byte prefix, characters start at odd bytes, and both cuts (byte 10240 and byte 40004 - 10240) are even: each lands inside a
	// two-byte character.
	text := "a" + strings.Repeat("é", 20_000) + "end"
	limited := limitText(t, mcp.LimitMcpContent([]mcp.LLMContent{{Type: mcp.LLMContentTypeText, Text: text}}, nil).Content)
	if strings.ContainsRune(limited, '\uFFFD') || !strings.HasSuffix(limited, "end") {
		t.Fatal("the cut split a character, or lost the end")
	}
	marker := regexp.MustCompile(`…(\d+) chars truncated…`).FindStringSubmatch(limited)
	if marker == nil {
		t.Fatal("expected the truncation marker")
	}
	parts := strings.Split(limited[strings.Index(limited, "\n\n")+2:], marker[0])
	if len(parts) != 2 || len(parts[0]) > mcp.McpOutputMaxBytes/2 || len(parts[1]) > mcp.McpOutputMaxBytes/2 {
		t.Fatalf("parts = %d", len(parts))
	}
	var removed int
	_, _ = fmt.Sscan(marker[1], &removed)
	if utf8.RuneCountInString(parts[0])+utf8.RuneCountInString(parts[1])+removed != utf8.RuneCountInString(text) {
		t.Fatal("the characters kept and removed do not add up to the text")
	}
}

// limitMcpContent reports Math.ceil(totalBytes / 4) as the original token count.
func TestLimitMcpContentRoundsTheOriginalTokenCountUp(t *testing.T) {
	text := strings.Repeat("x", 30_001)
	limited := mcp.LimitMcpContent([]mcp.LLMContent{{Type: mcp.LLMContentTypeText, Text: text}}, func([]byte, string) (string, error) {
		return "/tmp/full.txt", nil
	})
	if limited.FullOutputPath != "/tmp/full.txt" || len(limited.Content) != 1 {
		t.Fatalf("limited = %+v", limited)
	}
	if got := limited.Content[0].Text; !strings.HasPrefix(got, "Warning: truncated output (original token count: 7501)\n") {
		t.Fatalf("text = %q", got[:80])
	}
}
