package tools

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"unsafe"

	"github.com/MichaelKinsy/PiG/agent"
)

// sharesReturnedText fails unless tr is a truncation whose content is the start of text, the same bytes, followed by a notice.
func sharesReturnedText(t *testing.T, text string, tr *TruncationResult) {
	t.Helper()
	if tr == nil || !tr.Truncated || tr.Content == "" {
		t.Fatalf("truncation = %+v, want a truncated result with content", tr)
	}
	if !strings.HasPrefix(text, tr.Content) || len(text) == len(tr.Content) {
		t.Fatalf("text %d bytes does not start with the %d-byte truncation content and a notice", len(text), len(tr.Content))
	}
	if unsafe.StringData(tr.Content) != unsafe.StringData(text) { //nolint:gosec // G103: compares the data pointers of two strings to prove they share bytes; nothing is read or written through them.
		t.Fatalf("details.truncation.content is a separate %d-byte copy of the returned text", len(tr.Content))
	}
}

// detailsOf returns the result's details as T, failing with the result's text when it has none.
func detailsOf[T any](t *testing.T, res agent.AgentToolResult) T {
	t.Helper()
	details, ok := res.Details.(T)
	if !ok {
		text := res.Text()
		t.Fatalf("details %T; result %d bytes ending %q", res.Details, len(text), text[max(0, len(text)-200):])
	}
	return details
}

// A truncated tool result's details.truncation.content is the start of the text the tool returns, not a copy of it. Pi returns
// `truncation.content + notice` with truncation in details (read.ts, ls.ts, find.ts, grep.ts, bash.ts formatOutput), one string in V8;
// a session and its tool cards keep every result, so a copy doubles the memory of each truncated output (a 4.2M-token session of reads
// kept 13 MB twice).
func TestTruncatedResultDetailsShareTheReturnedText(t *testing.T) {
	t.Run("read", func(t *testing.T) {
		res := readFileTool(t, "large.txt", []byte(numberedLines(2500, "Line %d")), map[string]any{})
		sharesReturnedText(t, res.Text(), detailsOf[*ReadDetails](t, res).Truncation)
	})
	t.Run("ls", func(t *testing.T) {
		dir := t.TempDir()
		for i := range 400 {
			if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("%03d-%s.txt", i, strings.Repeat("n", 150))), nil, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		res := runFileTool(t, &LsTool{CWD: dir}, t.Context(), map[string]any{})
		sharesReturnedText(t, res.Text(), detailsOf[*LsDetails](t, res).Truncation)
	})
	t.Run("find", func(t *testing.T) {
		names := make([]string, 600)
		for i := range names {
			names[i] = fmt.Sprintf("dir/%03d-%s.txt", i, strings.Repeat("n", 90))
		}
		res := formatFindResults(names, 1000, false)
		sharesReturnedText(t, res.Text(), detailsOf[*FindDetails](t, res).Truncation)
	})
	t.Run("grep", func(t *testing.T) {
		if _, err := exec.LookPath("rg"); err != nil {
			t.Skip("ripgrep unavailable")
		}
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte(numberedLines(600, "TARGET %d "+strings.Repeat("x", 100))), 0o644); err != nil {
			t.Fatal(err)
		}
		res := runFileTool(t, &GrepTool{CWD: dir}, t.Context(), map[string]any{"pattern": "TARGET", "limit": 1000})
		sharesReturnedText(t, res.Text(), detailsOf[*GrepDetails](t, res).Truncation)
	})
	t.Run("bash", func(t *testing.T) {
		tr := TruncateTail(numberedLines(3000, "out %d"), TruncationOptions{})
		text, details := formatShellOutput(OutputSnapshot{Content: tr.Content, Truncation: tr, FullOutputPath: "/tmp/full.log"}, 0, "(no output)")
		sharesReturnedText(t, text, details.Truncation)
	})
}
