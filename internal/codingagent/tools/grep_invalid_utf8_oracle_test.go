package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

// ripgrep --json spells a path or a line that is not valid UTF-8 as {"bytes": base64} with no "text" member. Pi's grep counts the match toward the limit, drops it when the path has no text, and reads a line without text back from the file when it formats a block (core/tools/grep.ts). Pig must list the same rows for the same bytes.
func TestGrepOnBytesThatAreNotValidUTF8MatchesPi(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("file names are arbitrary bytes only on Linux")
	}
	if _, err := exec.LookPath("rg"); err != nil {
		t.Skip("ripgrep is not installed")
	}
	// Several files make ripgrep's output order depend on its threads, so those requests compare the sorted rows; a directory with one file keeps ripgrep's own order and compares exactly, including the match limit.
	many := t.TempDir()
	single, nameOnly := t.TempDir(), t.TempDir()
	write := func(dir, name, content string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Skipf("file system refuses the name %q: %v", name, err)
		}
	}
	const withBadLines = "needle one\nneedle \xff tail\nplain\nneedle \xe2\x82\nneedle last\n"
	write(many, "ok.txt", withBadLines)
	write(many, "a\xffb.txt", "needle in a file whose name is not UTF-8\n")
	write(many, "b.txt", "x\nneedle two\ny\n")
	write(many, "\xe2\x82.md", "needle\n")
	write(single, "ok.txt", withBadLines)
	write(nameOnly, "a\xffb.txt", "needle\nneedle\n")

	type request struct {
		dir, args string
		sorted    bool
	}
	var requests []request
	for _, args := range []string{
		`{"pattern":"needle"}`,
		`{"pattern":"needle","context":1}`,
		`{"pattern":"needle","glob":"*.txt"}`,
		`{"pattern":"needle","literal":true,"ignoreCase":true}`,
	} {
		requests = append(requests, request{many, args, true})
	}
	for _, args := range []string{
		`{"pattern":"needle"}`,
		`{"pattern":"needle","limit":1}`,
		`{"pattern":"needle","limit":2}`,
		`{"pattern":"needle","limit":4}`,
		`{"pattern":"needle","context":1}`,
		`{"pattern":"needle","limit":2,"context":1}`,
		`{"pattern":"needle","limit":3,"context":2}`,
	} {
		requests = append(requests, request{single, args, false}, request{nameOnly, args, false})
	}
	for _, r := range requests {
		cmd := exec.CommandContext(t.Context(), "node", "testdata/real_fs_tool.mjs", pigversion.UpstreamPackageVersion, "grep", r.dir, r.args)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		output, err := cmd.Output()
		if err != nil {
			t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
		}
		var want struct{ Text, Thrown string }
		if err := json.Unmarshal(output, &want); err != nil {
			t.Fatal(err)
		}
		result, err := (&GrepTool{CWD: r.dir}).Execute(context.Background(), "call", json.RawMessage(r.args), nil)
		got := ""
		switch {
		case err != nil:
			got = err.Error()
			want.Text = want.Thrown
		case len(result.Content) > 0:
			got = result.Content[0].(ai.TextContent).Text
		}
		if r.sorted {
			got, want.Text = sortedRows(got), sortedRows(want.Text)
		}
		if got != want.Text {
			t.Errorf("%s in %s\n  Pig %q\n  Pi  %q", r.args, filepath.Base(r.dir), got, want.Text)
		}
	}
}

func sortedRows(text string) string {
	rows := strings.Split(text, "\n")
	slices.Sort(rows)
	return strings.Join(rows, "\n")
}
