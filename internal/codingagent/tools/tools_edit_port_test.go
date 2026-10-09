package tools

// pi: packages/coding-agent/src/core/tools/edit-diff.ts

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

func assertApplicablePatch(t *testing.T, original, patch, want string) {
	t.Helper()
	// Use Pi's unchanged diff dependency as the upstream tests do, rather than
	// validate GenerateUnifiedPatch with another implementation of itself.
	root, err := filepath.EvalSymlinks("../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent")
	if err != nil {
		t.Fatal(err)
	}
	input, err := json.Marshal([]string{original, patch})
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "-e", `const fs=require("node:fs"); const {applyPatch}=require(require.resolve("diff",{paths:[process.argv[1]]})); const [original,patch]=JSON.parse(fs.readFileSync(0,"utf8")); process.stdout.write(JSON.stringify(applyPatch(original,patch)));`, root)
	cmd.Stdin = bytes.NewReader(input)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("applyPatch: %v: %s", err, out)
	}
	var got string
	if err := json.Unmarshal(out, &got); err != nil || got != want {
		t.Fatalf("applyPatch = %s, %v; want %q", out, err, want)
	}
}

func TestToolsWritePort(t *testing.T) {
	// .upstream/v0.87.1/packages/coding-agent/test/tools.test.ts:253
	t.Run("should write file contents", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "write-test.txt")
		result := runFileTool(t, &WriteTool{CWD: dir, Queue: NewFileMutationQueue()}, t.Context(), map[string]any{"path": path, "content": "Test content"})
		if result.IsError || result.Text() != "Successfully wrote to "+path || extension.ToolResultDetailsFor(result.Details) != nil {
			t.Fatalf("result = %+v", result)
		}
		assertMutationFile(t, path, "Test content")
	})
	// .upstream/v0.87.1/packages/coding-agent/test/tools.test.ts:263
	t.Run("should create parent directories", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "nested", "dir", "test.txt")
		result := runFileTool(t, &WriteTool{CWD: dir, Queue: NewFileMutationQueue()}, t.Context(), map[string]any{"path": path, "content": "Nested content"})
		requireTextParts(t, result, []string{"Successfully wrote"}, nil)
		assertMutationFile(t, path, "Nested content")
	})
}

func TestToolsEditPort(t *testing.T) {
	// .upstream/v0.87.1/packages/coding-agent/test/tools.test.ts:274
	t.Run("should replace text in file", func(t *testing.T) {
		original := "Hello, world!"
		text, details, _, failed := runEdit(t, original, []editEntry{{"world", "testing"}})
		if failed || !strings.Contains(text, "Successfully replaced") || details == nil || !strings.Contains(details.Diff, "testing") {
			t.Fatalf("result = %q, %+v", text, details)
		}
		for _, part := range []string{"--- ", "+++ ", "@@", "-Hello, world!", "+Hello, testing!"} {
			if !strings.Contains(details.Patch, part) {
				t.Errorf("patch %q lacks %q", details.Patch, part)
			}
		}
		assertApplicablePatch(t, original, details.Patch, "Hello, testing!")
	})
	for _, tc := range []struct {
		name, original string
		edits          []editEntry
		errorPart      string
	}{
		// .upstream/v0.87.1/packages/coding-agent/test/tools.test.ts:297
		{"should fail if text not found", "Hello, world!", []editEntry{{"nonexistent", "testing"}}, "Could not find the exact text"},
		// .upstream/v0.87.1/packages/coding-agent/test/tools.test.ts:321
		{"should fail if text appears multiple times", "foo foo foo", []editEntry{{"foo", "bar"}}, "Found 3 occurrences"},
		// .upstream/v0.87.1/packages/coding-agent/test/tools.test.ts:390
		{"should fail when edits is empty", "hello\nworld\n", []editEntry{}, "edits must contain at least one replacement"},
		// .upstream/v0.87.1/packages/coding-agent/test/tools.test.ts:402
		{"should fail when multi-edit regions overlap", "one\ntwo\nthree\n", []editEntry{{"one\ntwo\n", "ONE\nTWO\n"}, {"two\nthree\n", "TWO\nTHREE\n"}}, "overlap"},
		// .upstream/v0.87.1/packages/coding-agent/test/tools.test.ts:417
		{"should not partially apply edits when one edit fails", "alpha\nbeta\ngamma\n", []editEntry{{"alpha\n", "ALPHA\n"}, {"missing\n", "MISSING\n"}}, "Could not find"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			text, _, after, failed := runEdit(t, tc.original, tc.edits)
			if !failed || !strings.Contains(text, tc.errorPart) || after != tc.original {
				t.Fatalf("result %q, after %q", text, after)
			}
		})
	}
	// .upstream/v0.87.1/packages/coding-agent/test/tools.test.ts:310
	t.Run("should include ENOENT when the edit target does not exist", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "missing.txt")
		result := runFileTool(t, &EditTool{CWD: dir, Queue: NewFileMutationQueue()}, t.Context(), map[string]any{"path": path, "edits": []editEntry{{"hello", "world"}}})
		if !result.IsError || result.Text() != "Could not edit file: "+path+". Error code: ENOENT." {
			t.Fatal(result)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/tools.test.ts:334
	t.Run("should replace multiple disjoint regions in one call", func(t *testing.T) {
		text, details, after, failed := runEdit(t, "alpha\nbeta\ngamma\ndelta\n", []editEntry{{"alpha\n", "ALPHA\n"}, {"gamma\n", "GAMMA\n"}})
		if failed || !strings.Contains(text, "Successfully replaced 2 block(s)") || after != "ALPHA\nbeta\nGAMMA\ndelta\n" || details == nil || !strings.Contains(details.Diff, "ALPHA") || !strings.Contains(details.Diff, "GAMMA") {
			t.Fatalf("result %q, details %+v, after %q", text, details, after)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/tools.test.ts:352
	t.Run("should collapse large unchanged gaps in multi-edit diffs", func(t *testing.T) {
		_, details, _, failed := runEdit(t, numberedLines(600, "line %03d")+"\n", []editEntry{{"line 100\n", "LINE 100\n"}, {"line 300\n", "LINE 300\n"}, {"line 500\n", "LINE 500\n"}})
		if failed || details == nil {
			t.Fatal("edit failed")
		}
		for _, part := range []string{"LINE 100", "LINE 300", "LINE 500", "..."} {
			if !strings.Contains(details.Diff, part) {
				t.Fatalf("diff %q lacks %q", details.Diff, part)
			}
		}
		if strings.Contains(details.Diff, "line 250") || len(strings.Split(details.Diff, "\n")) >= 50 {
			t.Fatal(details.Diff)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/tools.test.ts:375
	t.Run("should match edits against the original file, not incrementally", func(t *testing.T) {
		text, _, after, failed := runEdit(t, "foo\nbar\nbaz\n", []editEntry{{"foo\n", "foo bar\n"}, {"bar\n", "BAR\n"}})
		if failed || after != "foo bar\nBAR\nbaz\n" {
			t.Fatalf("result %q, after %q", text, after)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/tools.test.ts:435
	t.Run("should include EACCES for read-only files", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "edit-readonly.txt")
		if err := os.WriteFile(path, []byte("hello\n"), 0o444); err != nil {
			t.Fatal(err)
		}
		result := runFileTool(t, &EditTool{CWD: dir, Queue: NewFileMutationQueue()}, t.Context(), map[string]any{"path": path, "edits": []editEntry{{"hello", "world"}}})
		if !result.IsError || result.Text() != "Could not edit file: "+path+". Error code: "+readOnlyAccessCode()+"." {
			t.Fatal(result)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/tools.test.ts:448
	t.Run("should include the original error message for unknown edit access errors", func(t *testing.T) {
		tool := &EditTool{CWD: t.TempDir(), Queue: NewFileMutationQueue(), Operations: &EditOperations{
			Access: func(string) error { return errors.New("disk offline") }, ReadFile: func(string) ([]byte, error) { return []byte("hello\n"), nil }, WriteFile: func(string, string) error { return nil },
		}}
		result := runFileTool(t, tool, t.Context(), map[string]any{"path": "broken.txt", "edits": []editEntry{{"hello", "world"}}})
		if !result.IsError || result.Text() != "Could not edit file: broken.txt. Error: disk offline." {
			t.Fatal(result)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/tools.test.ts:467
	t.Run("should include ENOENT in diff preview for missing files", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "missing-preview.txt")
		result := ComputeEditsDiff(path, []EditReplacement{{"hello", "world"}}, dir)
		want := EditDiffError{Error: fmt.Sprintf("Could not edit file: %s. Error code: ENOENT.", path)}
		if !reflect.DeepEqual(result, want) {
			t.Fatalf("result %+v, want %+v", result, want)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/tools.test.ts:474
	t.Run("should include EACCES in diff preview for unreadable files", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			// Node's chmod and Go's os.WriteFile mode only set or clear the
			// read-only attribute on Windows; mode 0222 leaves the file readable.
			t.Skip("Windows files have no owner read permission bit to remove")
		}
		dir := t.TempDir()
		path := filepath.Join(dir, "unreadable-preview.txt")
		if err := os.WriteFile(path, []byte("hello\n"), 0o222); err != nil {
			t.Fatal(err)
		}
		result := ComputeEditsDiff(path, []EditReplacement{{"hello", "world"}}, dir)
		want := EditDiffError{Error: fmt.Sprintf("Could not edit file: %s. Error code: EACCES.", path)}
		if !reflect.DeepEqual(result, want) {
			t.Fatalf("result %+v, want %+v", result, want)
		}
	})
}

func TestToolsEditCRLFPort(t *testing.T) {
	for _, tc := range []struct {
		name, original string
		edits          []editEntry
		want           string
		errorPart      string
	}{
		// .upstream/v0.87.1/packages/coding-agent/test/tools.test.ts:1299
		{"should match LF oldText against CRLF file content", "line one\r\nline two\r\nline three\r\n", []editEntry{{"line two\n", "replaced line\n"}}, "line one\r\nreplaced line\r\nline three\r\n", ""},
		// .upstream/v0.87.1/packages/coding-agent/test/tools.test.ts:1312
		{"should preserve CRLF line endings after edit", "first\r\nsecond\r\nthird\r\n", []editEntry{{"second\n", "REPLACED\n"}}, "first\r\nREPLACED\r\nthird\r\n", ""},
		// .upstream/v0.87.1/packages/coding-agent/test/tools.test.ts:1325
		{"should preserve LF line endings for LF files", "first\nsecond\nthird\n", []editEntry{{"second\n", "REPLACED\n"}}, "first\nREPLACED\nthird\n", ""},
		// .upstream/v0.87.1/packages/coding-agent/test/tools.test.ts:1338
		{"should detect duplicates across CRLF/LF variants", "hello\r\nworld\r\n---\r\nhello\nworld\n", []editEntry{{"hello\nworld\n", "replaced\n"}}, "", "Found 2 occurrences"},
		// .upstream/v0.87.1/packages/coding-agent/test/tools.test.ts:1351
		{"should preserve UTF-8 BOM after edit", "\uFEFFfirst\r\nsecond\r\nthird\r\n", []editEntry{{"second\n", "REPLACED\n"}}, "\uFEFFfirst\r\nREPLACED\r\nthird\r\n", ""},
		// .upstream/v0.87.1/packages/coding-agent/test/tools.test.ts:1364
		{"should preserve CRLF line endings and BOM in multi-edit mode", "\uFEFFfirst\r\nsecond\r\nthird\r\nfourth\r\n", []editEntry{{"second\n", "SECOND\n"}, {"fourth\n", "FOURTH\n"}}, "\uFEFFfirst\r\nSECOND\r\nthird\r\nFOURTH\r\n", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			text, _, after, failed := runEdit(t, tc.original, tc.edits)
			if tc.errorPart != "" {
				if !failed || !strings.Contains(text, tc.errorPart) {
					t.Fatalf("result = %q", text)
				}
				return
			}
			if failed || !strings.Contains(text, "Successfully replaced") || after != tc.want {
				t.Fatalf("result %q, after %q, want %q", text, after, tc.want)
			}
		})
	}
}

// edit-diff.ts:501-509: computeEditsDiff returns an EditDiffResult or an EditDiffError, never both, and the result carries the first changed line.
func TestComputeEditsDiffReturnsEitherAResultOrAnError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "preview.txt")
	if err := os.WriteFile(path, []byte("one\ntwo\nthree\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, ok := ComputeEditsDiff(path, []EditReplacement{{"two", "2"}}, dir).(EditDiffResult)
	if !ok || result.Diff == "" || result.FirstChangedLine != 2 {
		t.Fatalf("result = %+v (%v), want a diff whose first changed line is 2", result, ok)
	}
	if failure, ok := ComputeEditsDiff(path, []EditReplacement{{"absent", "x"}}, dir).(EditDiffError); !ok || failure.Error == "" {
		t.Fatalf("a replacement that does not match must be an EditDiffError, got %+v", failure)
	}
}
