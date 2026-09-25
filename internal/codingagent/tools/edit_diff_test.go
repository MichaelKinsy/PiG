package tools

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
		t.Fatalf("multi-file edit failed: err=%v res=%s", err, res.Content)
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
	if !res.IsError || !strings.Contains(res.Content, "overlap") {
		t.Errorf("expected overlap error, got: IsError=%v %q", res.IsError, res.Content)
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
		t.Fatalf("edit failed: err=%v res=%s", err, res.Content)
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
		t.Fatalf("edit failed: err=%v res=%s", err, res.Content)
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
	if !res.IsError || !strings.Contains(res.Content, "No changes made") {
		t.Errorf("expected no-change error, got IsError=%v %q", res.IsError, res.Content)
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
		t.Fatalf("legacy edit failed: err=%v res=%s", err, res.Content)
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
		t.Fatalf("string-edits failed: err=%v res=%s", err, res.Content)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "hello pig\n" {
		t.Errorf("string-edits produced wrong content: %q", got)
	}
}

// Ported from upstream test/edit-tool-legacy-input.test.ts, plus the
// single-object branches of prepareEditArguments (TOOL-16).
func TestEditPrepareArgumentsUpstreamCases(t *testing.T) {
	et := &EditTool{}
	prepare := func(in string) string {
		t.Helper()
		out, err := et.PrepareArguments(json.RawMessage(in))
		if err != nil {
			t.Fatal(err)
		}
		return string(out)
	}
	sameJSON := func(got, want string) bool {
		var a, b any
		return json.Unmarshal([]byte(got), &a) == nil && json.Unmarshal([]byte(want), &b) == nil && reflect.DeepEqual(a, b)
	}
	for in, want := range map[string]string{
		`{"path":"file.txt","oldText":"before","newText":"after"}`:                                `{"path":"file.txt","edits":[{"oldText":"before","newText":"after"}]}`,
		`{"path":"file.txt","edits":[{"oldText":"a","newText":"b"}],"oldText":"c","newText":"d"}`: `{"path":"file.txt","edits":[{"oldText":"a","newText":"b"},{"oldText":"c","newText":"d"}]}`,
		`{"path":"file.txt","edits":"[{\"oldText\":\"a\",\"newText\":\"b\"}]"}`:                   `{"path":"file.txt","edits":[{"oldText":"a","newText":"b"}]}`,
		`{"path":"file.txt","edits":"not json"}`:                                                  `{"path":"file.txt","edits":"not json"}`,
		`{"path":"file.txt","edits":{"oldText":"a","newText":"b"}}`:                               `{"path":"file.txt","edits":[{"oldText":"a","newText":"b"}]}`,
		`{"path":"file.txt","edits":"{\"oldText\":\"a\",\"newText\":\"b\"}"}`:                     `{"path":"file.txt","edits":[{"oldText":"a","newText":"b"}]}`,
	} {
		if got := prepare(in); !sameJSON(got, want) {
			t.Errorf("prepare(%s) = %s, want %s", in, got, want)
		}
	}
	valid := `{"path":"file.txt","edits":[{"oldText":"a","newText":"b"}]}`
	if got := prepare(valid); got != valid {
		t.Errorf("valid input changed: %s", got)
	}
	for _, in := range []string{`null`, `"garbage"`} {
		if got := prepare(in); got != in {
			t.Errorf("non-object %s changed to %s", in, got)
		}
	}
	props := et.Schema().Parameters["properties"].(map[string]any)
	if _, ok := props["oldText"]; ok {
		t.Error("legacy oldText leaked into the public schema")
	}
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
