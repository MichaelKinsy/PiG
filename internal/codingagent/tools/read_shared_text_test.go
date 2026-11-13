// SPDX-License-Identifier: MIT

package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unsafe"

	"github.com/MichaelKinsy/PiG/ai"
)

func textData(s string) *byte { return unsafe.StringData(s) } //nolint:gosec // G103: the test compares the addresses of two strings.

// A file read again returns the string the first read returned: the conversation then holds the file's text once however often the
// model reads it. Pi returns a new string for each read (core/tools/read.ts execute); which of two equal strings a result holds
// shows only in the memory the process keeps.
func TestAFileReadAgainReturnsTheStringOfTheFirstRead(t *testing.T) {
	dir := t.TempDir()
	whole := filepath.Join(dir, "whole.txt")
	if err := os.WriteFile(whole, []byte(strings.Repeat("a line of a file that fits the limits\n", 300)), 0o600); err != nil {
		t.Fatal(err)
	}
	// Past the byte limit: the text is the kept lines and a notice, and the details hold the kept lines.
	long := filepath.Join(dir, "long.txt")
	if err := os.WriteFile(long, []byte(strings.Repeat("a line of a file that is cut at the byte limit\n", 4000)), 0o600); err != nil {
		t.Fatal(err)
	}
	tool := &ReadTool{CWD: dir}
	for _, path := range []string{whole, long} {
		first := runFileTool(t, tool, t.Context(), map[string]any{"path": path})
		second := runFileTool(t, tool, t.Context(), map[string]any{"path": path})
		firstText, secondText := first.Content[0].(ai.TextContent).Text, second.Content[0].(ai.TextContent).Text
		if first.IsError || second.IsError || firstText != secondText || len(firstText) < 4<<10 {
			t.Fatalf("%s: the two reads differ or are small: %d and %d bytes", filepath.Base(path), len(firstText), len(secondText))
		}
		if textData(firstText) != textData(secondText) {
			t.Errorf("%s: the second read returned its own copy of the text", filepath.Base(path))
		}
		if string(first.StructuredContent) != string(second.StructuredContent) {
			t.Errorf("%s: the structured content of the two reads differs", filepath.Base(path))
		}
		details, _ := second.Details.(*ReadDetails)
		if path == long {
			if details == nil || details.Truncation == nil || !details.Truncation.Truncated {
				t.Fatalf("%s: the read was not truncated: %+v", filepath.Base(path), second.Details)
			}
			if kept := details.Truncation.Content; !strings.HasPrefix(secondText, kept) || textData(kept) != textData(secondText) {
				t.Errorf("%s: the truncation details hold their own copy of the kept lines", filepath.Base(path))
			}
		}
	}
	// A file that changed is read as it is now.
	if err := os.WriteFile(whole, []byte(strings.Repeat("another line of the file after an edit\n", 300)), 0o600); err != nil {
		t.Fatal(err)
	}
	changed := runFileTool(t, tool, t.Context(), map[string]any{"path": whole})
	if text := changed.Content[0].(ai.TextContent).Text; !strings.HasPrefix(text, "another line") {
		t.Fatalf("the changed file was read as %q", text[:40])
	}
}
