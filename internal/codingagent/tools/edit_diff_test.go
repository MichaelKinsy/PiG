package tools

// pi: packages/coding-agent/src/core/tools/edit-diff.ts

// pi: packages/coding-agent/src/core/tools/edit.ts

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestEditMultipleReplacementsPositionBased verifies that multiple replacements are matched against
// the ORIGINAL file content (not after prior edits) and applied in
// reverse-position order so they don't interfere.
func TestEditMultipleReplacementsPositionBased(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.txt")
	// "aaa" and "bbb" are disjoint, both unique in the original.
	const content = "header\naaa middle bbb\nfooter\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	et := &EditTool{CWD: dir, Queue: NewFileMutationQueue()}
	args, _ := json.Marshal(editParams{
		Path: "x.txt",
		Edits: []editEntry{
			{OldText: "aaa", NewText: "XXX"},
			{OldText: "bbb", NewText: "YYY"},
		},
	})
	res, err := et.Execute(context.Background(), "", args, nil)
	if err != nil || res.IsError {
		t.Fatalf("multi-file edit failed: err=%v res=%s", err, res.Text())
	}
	got, _ := os.ReadFile(path)
	want := "header\nXXX middle YYY\nfooter\n"
	if string(got) != want {
		t.Errorf("got %q want %q", got, want)
	}
}

// TestEditOverlapDetected verifies that overlapping edits are rejected.
func TestEditOverlapDetected(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.txt")
	if err := os.WriteFile(path, []byte("abcdef\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	et := &EditTool{CWD: dir, Queue: NewFileMutationQueue()}
	args, _ := json.Marshal(editParams{
		Path: "x.txt",
		Edits: []editEntry{
			{OldText: "abcd", NewText: "X"},
			{OldText: "cdef", NewText: "Y"},
		},
	})
	res, _ := et.Execute(context.Background(), "", args, nil)
	if !res.IsError || !strings.Contains(res.Text(), "overlap") {
		t.Errorf("expected overlap error, got: IsError=%v %q", res.IsError, res.Text())
	}
}

// TestEditCRLFPreserved verifies that CRLF line endings are preserved
// across an edit. Matches upstream restoreLineEndings semantics.
func TestEditCRLFPreserved(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.txt")
	content := "alpha\r\nbeta\r\ngamma\r\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	et := &EditTool{CWD: dir, Queue: NewFileMutationQueue()}
	args, _ := json.Marshal(editParams{
		Path:  "x.txt",
		Edits: []editEntry{{OldText: "beta", NewText: "BETA"}},
	})
	res, err := et.Execute(context.Background(), "", args, nil)
	if err != nil || res.IsError {
		t.Fatalf("edit failed: err=%v res=%s", err, res.Text())
	}
	got, _ := os.ReadFile(path)
	want := "alpha\r\nBETA\r\ngamma\r\n"
	if string(got) != want {
		t.Errorf("CRLF not preserved: got %q want %q", got, want)
	}
}

// TestEditBOMPreserved verifies the UTF-8 BOM is preserved through an edit.
func TestEditBOMPreserved(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.txt")
	content := "\uFEFFhello world\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	et := &EditTool{CWD: dir, Queue: NewFileMutationQueue()}
	args, _ := json.Marshal(editParams{
		Path:  "x.txt",
		Edits: []editEntry{{OldText: "world", NewText: "pig"}},
	})
	res, err := et.Execute(context.Background(), "", args, nil)
	if err != nil || res.IsError {
		t.Fatalf("edit failed: err=%v res=%s", err, res.Text())
	}
	got, _ := os.ReadFile(path)
	want := "\uFEFFhello pig\n"
	if string(got) != want {
		t.Errorf("BOM not preserved: got %q want %q", got, want)
	}
}

// TestEditNoChangeError verifies that an edit that produces identical
// content is rejected with the upstream wording.
func TestEditNoChangeError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.txt")
	if err := os.WriteFile(path, []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	et := &EditTool{CWD: dir, Queue: NewFileMutationQueue()}
	args, _ := json.Marshal(editParams{
		Path:  "x.txt",
		Edits: []editEntry{{OldText: "hello", NewText: "hello"}},
	})
	res, _ := et.Execute(context.Background(), "", args, nil)
	if !res.IsError || !strings.Contains(res.Text(), "No changes made") {
		t.Errorf("expected no-change error, got IsError=%v %q", res.IsError, res.Text())
	}
}

// TestEditPrepareArgumentsLegacy verifies the legacy {oldText, newText}
// top-level shape is upgraded into edits[].
func TestEditPrepareArgumentsLegacy(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.txt")
	if err := os.WriteFile(path, []byte("hello world\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	et := &EditTool{CWD: dir, Queue: NewFileMutationQueue()}
	// Legacy shape: top-level oldText/newText, no edits[].
	args := json.RawMessage(`{"path":"x.txt","oldText":"world","newText":"pig"}`)
	res, err := et.Execute(context.Background(), "", args, nil)
	if err != nil || res.IsError {
		t.Fatalf("legacy edit failed: err=%v res=%s", err, res.Text())
	}
	got, _ := os.ReadFile(path)
	if string(got) != "hello pig\n" {
		t.Errorf("legacy edit produced wrong content: %q", got)
	}
}

// TestEditPrepareArgumentsEditsAsJSONString verifies models that emit
// edits as a JSON-encoded string get parsed (Opus 4.6, GLM-5.1).
func TestEditPrepareArgumentsEditsAsJSONString(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.txt")
	if err := os.WriteFile(path, []byte("hello world\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	et := &EditTool{CWD: dir, Queue: NewFileMutationQueue()}
	args := json.RawMessage(`{"path":"x.txt","edits":"[{\"oldText\":\"world\",\"newText\":\"pig\"}]"}`)
	res, err := et.Execute(context.Background(), "", args, nil)
	if err != nil || res.IsError {
		t.Fatalf("string-edits failed: err=%v res=%s", err, res.Text())
	}
	got, _ := os.ReadFile(path)
	if string(got) != "hello pig\n" {
		t.Errorf("string-edits produced wrong content: %q", got)
	}
}

// Cases from .upstream/v0.87.1/packages/coding-agent/test/edit-tool-legacy-input.test.ts.
func TestEditPrepareArgumentsUpstreamCases(t *testing.T) {
	et := &EditTool{}
	t.Run("keeps legacy fields out of the public schema", func(t *testing.T) { // .upstream/v0.87.1/packages/coding-agent/test/edit-tool-legacy-input.test.ts:21
		props := et.Schema().Parameters["properties"].(map[string]any)
		for _, name := range []string{"oldText", "newText"} {
			if _, ok := props[name]; ok {
				t.Errorf("legacy %s leaked into the public schema", name)
			}
		}
	})
	for _, tc := range []struct{ name, input, want string }{
		{"folds top-level oldText/newText into edits", `{"path":"file.txt","oldText":"before","newText":"after"}`, `{"path":"file.txt","edits":[{"oldText":"before","newText":"after"}]}`},                                                       // .upstream/v0.87.1/packages/coding-agent/test/edit-tool-legacy-input.test.ts:27
		{"appends legacy replacement to existing edits", `{"path":"file.txt","edits":[{"oldText":"a","newText":"b"}],"oldText":"c","newText":"d"}`, `{"path":"file.txt","edits":[{"oldText":"a","newText":"b"},{"oldText":"c","newText":"d"}]}`}, // .upstream/v0.87.1/packages/coding-agent/test/edit-tool-legacy-input.test.ts:40
		{"parses edits from a JSON string", `{"path":"file.txt","edits":"[{\"oldText\":\"a\",\"newText\":\"b\"}]"}`, `{"path":"file.txt","edits":[{"oldText":"a","newText":"b"}]}`},                                                              // .upstream/v0.87.1/packages/coding-agent/test/edit-tool-legacy-input.test.ts:93
		{"leaves edits alone when the string is not valid JSON", `{"path":"file.txt","edits":"not json"}`, `{"path":"file.txt","edits":"not json"}`},                                                                                             // .upstream/v0.87.1/packages/coding-agent/test/edit-tool-legacy-input.test.ts:105
		{"single object", `{"path":"file.txt","edits":{"oldText":"a","newText":"b"}}`, `{"path":"file.txt","edits":[{"oldText":"a","newText":"b"}]}`},
		{"stringified single object", `{"path":"file.txt","edits":"{\"oldText\":\"a\",\"newText\":\"b\"}"}`, `{"path":"file.txt","edits":[{"oldText":"a","newText":"b"}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := et.PrepareArguments(json.RawMessage(tc.input))
			if err != nil {
				t.Fatal(err)
			}
			var actual, want any
			if json.Unmarshal(got, &actual) != nil || json.Unmarshal([]byte(tc.want), &want) != nil || !reflect.DeepEqual(actual, want) {
				t.Fatalf("prepare(%s) = %s, want %s", tc.input, got, tc.want)
			}
		})
	}
	t.Run("passes through valid input unchanged", func(t *testing.T) { // .upstream/v0.87.1/packages/coding-agent/test/edit-tool-legacy-input.test.ts:57
		input := json.RawMessage(`{"path":"file.txt","edits":[{"oldText":"a","newText":"b"}]}`)
		got, err := et.PrepareArguments(input)
		if err != nil || len(got) != len(input) || &got[0] != &input[0] {
			t.Fatalf("valid input identity changed: %s, %v", got, err)
		}
	})
	t.Run("passes through non-object input unchanged", func(t *testing.T) { // .upstream/v0.87.1/packages/coding-agent/test/edit-tool-legacy-input.test.ts:67
		// nil RawMessage is the Go argument boundary's omitted/undefined value.
		for _, input := range []json.RawMessage{nil, json.RawMessage(`null`), json.RawMessage(`"garbage"`)} {
			got, err := et.PrepareArguments(input)
			if err != nil || !reflect.DeepEqual(got, input) || (len(input) > 0 && &got[0] != &input[0]) {
				t.Fatalf("non-object %s changed to %s, %v", input, got, err)
			}
		}
	})
	t.Run("prepared args execute correctly", func(t *testing.T) { // .upstream/v0.87.1/packages/coding-agent/test/edit-tool-legacy-input.test.ts:74
		dir := t.TempDir()
		path := filepath.Join(dir, "legacy.txt")
		if err := os.WriteFile(path, []byte("before\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		tool := &EditTool{CWD: dir, Queue: NewFileMutationQueue()}
		prepared, err := tool.PrepareArguments(json.RawMessage(`{"path":"legacy.txt","oldText":"before","newText":"after"}`))
		if err != nil {
			t.Fatal(err)
		}
		result, err := tool.Execute(t.Context(), "tool-1", prepared, nil)
		if err != nil || result.IsError || result.Text() != "Successfully replaced 1 block(s) in legacy.txt." {
			t.Fatalf("execute = %+v, %v", result, err)
		}
		if got, err := os.ReadFile(path); err != nil || string(got) != "after\n" {
			t.Fatalf("file = %q, %v", got, err)
		}
	})
}

func TestEditSingleObjectEditsExecutes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f")
	if err := os.WriteFile(path, []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	et := &EditTool{CWD: dir, Queue: NewFileMutationQueue()}
	res, err := et.Execute(context.Background(), "", json.RawMessage(`{"path":"f","edits":{"oldText":"a","newText":"b"}}`), nil)
	if err != nil || res.IsError {
		t.Fatalf("err=%v res=%+v", err, res)
	}
	if got, _ := os.ReadFile(path); string(got) != "b\n" {
		t.Fatalf("file = %q", got)
	}
}

// prepareEditArguments (edit.ts:103-133) assigns args.edits in place and returns { ...rest, edits }: the members keep the
// order the model sent them in, and a new edits member goes last. The prepared arguments are what a tool_call handler
// sees (#165: Pi keeps a tool_call input's members in order).
func TestEditPrepareArgumentsKeepsMemberOrder(t *testing.T) {
	et := &EditTool{}
	for _, tc := range []struct{ name, input, want string }{
		{"legacy", `{"zeta":1,"oldText":"a","path":"f","newText":"b","alpha":true}`, `{"zeta":1,"path":"f","alpha":true,"edits":[{"oldText":"a","newText":"b"}]}`},
		{"legacy keeps edits in place", `{"edits":[{"oldText":"x","newText":"y"}],"path":"f","oldText":"a","newText":"b"}`, `{"edits":[{"oldText":"x","newText":"y"},{"oldText":"a","newText":"b"}],"path":"f"}`},
		{"string edits keep place", `{"z":0,"edits":"[{\"oldText\":\"a\",\"newText\":\"b\"}]","path":"f"}`, `{"z":0,"edits":[{"oldText":"a","newText":"b"}],"path":"f"}`},
		{"single edit", `{"path":"f","a":1,"edits":{"oldText":"a","newText":"b"},"b":2}`, `{"path":"f","a":1,"edits":[{"oldText":"a","newText":"b"}],"b":2}`},
		{"html is not escaped", `{"path":"f","oldText":"<a>","newText":"&"}`, `{"path":"f","edits":[{"oldText":"<a>","newText":"&"}]}`},
		// JSON.parse throws on a vertical tab or U+00A0 before the array, so edits stays a string.
		{"string edits with non-JSON whitespace", `{"path":"f","edits":"\u000b[{\"oldText\":\"a\",\"newText\":\"b\"}]"}`, `{"path":"f","edits":"\u000b[{\"oldText\":\"a\",\"newText\":\"b\"}]"}`},
		{"string edits with no-break space", `{"path":"f","edits":"\u00a0[{\"oldText\":\"a\",\"newText\":\"b\"}]"}`, `{"path":"f","edits":"\u00a0[{\"oldText\":\"a\",\"newText\":\"b\"}]"}`},
		{"string edits with JSON whitespace", `{"path":"f","edits":" \n\t[{\"oldText\":\"a\",\"newText\":\"b\"}]\r "}`, `{"path":"f","edits":[{"oldText":"a","newText":"b"}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := et.PrepareArguments(json.RawMessage(tc.input))
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tc.want {
				t.Fatalf("prepared = %s\nwant      %s", got, tc.want)
			}
		})
	}
}
