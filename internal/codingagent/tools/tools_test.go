package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestEditUniqueMatchGuard verifies an edit that would match >1 location
// is rejected (upstream unique-match guard).
func TestEditUniqueMatchGuard(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.txt")
	const content = "foo bar\nfoo baz\nfoo qux\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	et := &EditTool{CWD: dir}

	args, _ := json.Marshal(editParams{
		Path:  "x.txt",
		Edits: []editEntry{{OldText: "foo", NewText: "FOO"}},
	})
	res, err := et.Execute(context.Background(), "", args, nil)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected error on ambiguous edit, got success: %s", res.Content)
	}
	if !strings.Contains(res.Content, "Found 3 occurrences") {
		t.Errorf("expected match count in error, got: %s", res.Content)
	}
	got, _ := os.ReadFile(path)
	if string(got) != content {
		t.Errorf("file mutated despite error: %s", got)
	}
}

// TestEditUniqueMatchSuccess verifies a unique-match edit still works.
func TestEditUniqueMatchSuccess(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.txt")
	if err := os.WriteFile(path, []byte("hello world\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	et := &EditTool{CWD: dir}
	args, _ := json.Marshal(editParams{
		Path:  "x.txt",
		Edits: []editEntry{{OldText: "world", NewText: "pig"}},
	})
	res, err := et.Execute(context.Background(), "", args, nil)
	if err != nil || res.IsError {
		t.Fatalf("unique edit failed: err=%v res=%s", err, res.Content)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "hello pig\n" {
		t.Errorf("got %q", got)
	}
}

// TestEditNotFound verifies the error message when oldText matches zero
// locations.
func TestEditNotFound(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.txt")
	if err := os.WriteFile(path, []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	et := &EditTool{CWD: dir}
	args, _ := json.Marshal(editParams{Path: "x.txt", Edits: []editEntry{{OldText: "missing", NewText: "x"}}})
	res, _ := et.Execute(context.Background(), "", args, nil)
	if !res.IsError || !strings.Contains(res.Content, "Could not find") {
		t.Errorf("expected not-found error, got %v / %q", res.IsError, res.Content)
	}
}

// TestEditWithoutReadFails verifies the read-before-edit guard (
// extends to edit too).
// to continue.]" notice. Mirrors upstream read.ts:224-240.
func TestReadTruncationContinuationNotice(t *testing.T) {
	dir := t.TempDir()
	var sb strings.Builder
	for i := range 2100 {
		fmt.Fprintf(&sb, "line%d\n", i+1)
	}
	path := filepath.Join(dir, "big.txt")
	if err := os.WriteFile(path, []byte(sb.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	rt := &ReadTool{CWD: dir}

	// Read with no limit: should trigger default line-cap and append notice.
	args, _ := json.Marshal(readParams{Path: "big.txt"})
	res, err := rt.Execute(context.Background(), "", args, nil)
	if err != nil || res.IsError {
		t.Fatalf("read failed: %v %s", err, res.Content)
	}
	if !strings.Contains(res.Content, "[Showing lines 1-") {
		t.Errorf("missing continuation notice for line-truncation: %q", res.Content[max(0, len(res.Content)-200):])
	}
	if !strings.Contains(res.Content, "Use offset=") {
		t.Errorf("missing offset hint: %q", res.Content[max(0, len(res.Content)-200):])
	}

	// Read with user limit that stops early: should append "[N more lines" notice.
	args, _ = json.Marshal(map[string]any{"path": "big.txt", "limit": 5})
	res, err = rt.Execute(context.Background(), "", args, nil)
	if err != nil || res.IsError {
		t.Fatalf("read with limit failed: %v %s", err, res.Content)
	}
	if !strings.Contains(res.Content, "more lines in file") {
		t.Errorf("missing 'more lines' notice for user-limit: %q", res.Content)
	}
}

// TestFindReturnsRelativePaths verifies the find tool returns relative paths
// (not absolute), matching upstream find.ts behavior.
// TestLsToolFormat verifies the new ls format: simple name/name/ (no sizes),
// alphabetical sort, and entry limit notice.
func TestLsToolFormat(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "zdir"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "afile.go"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "mfile.go"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	lt := &LsTool{CWD: dir}
	args, _ := json.Marshal(lsParams{})
	res, err := lt.Execute(context.Background(), "", args, nil)
	if err != nil || res.IsError {
		t.Fatalf("ls failed: %v %s", err, res.Content)
	}
	// Entries should be alphabetical (afile, mfile, zdir)
	lines := strings.Split(strings.TrimRight(res.Content, "\n"), "\n")
	if lines[0] != "afile.go" {
		t.Errorf("expected afile.go first, got %q", lines[0])
	}
	if lines[1] != "mfile.go" {
		t.Errorf("expected mfile.go second, got %q", lines[1])
	}
	if lines[2] != "zdir/" {
		t.Errorf("expected zdir/ third (dirs after files alphabetically), got %q", lines[2])
	}
	// No size columns
	if strings.Contains(res.Content, "KB") || strings.Contains(res.Content, "B ") {
		t.Errorf("ls output should not contain size columns: %q", res.Content)
	}
}

func TestDefaultToolGuidelines(t *testing.T) {
	m := DefaultToolGuidelines()
	// read, write, edit should all have guidelines.
	for _, name := range []string{"read", "write", "edit"} {
		if g, ok := m[name]; !ok || len(g) == 0 {
			t.Errorf("tool %q should have prompt guidelines", name)
		}
	}
	// bash carries upstream's single PI_* session guideline.
	if g := m["bash"]; len(g) != 1 || g[0] != sessionGuideline {
		t.Errorf("bash guidelines = %q, want upstream's session guideline", g)
	}
	// grep, find, ls define none upstream.
	for _, name := range []string{"grep", "find", "ls"} {
		if _, ok := m[name]; ok {
			t.Errorf("tool %q should not have prompt guidelines", name)
		}
	}
}

func TestLookupSystemToolPathUsesAlternateSystemBinaryNamesInOrder(t *testing.T) {
	rootDir := t.TempDir()
	firstDir := filepath.Join(rootDir, "first")
	secondDir := filepath.Join(rootDir, "second")
	if err := os.MkdirAll(firstDir, 0o755); err != nil {
		t.Fatalf("MkdirAll firstDir: %v", err)
	}
	if err := os.MkdirAll(secondDir, 0o755); err != nil {
		t.Fatalf("MkdirAll secondDir: %v", err)
	}

	oldPath := os.Getenv("PATH")
	separator := string(os.PathListSeparator)
	if err := os.Setenv("PATH", firstDir+separator+secondDir); err != nil {
		t.Fatalf("Setenv PATH: %v", err)
	}
	defer func() {
		_ = os.Setenv("PATH", oldPath)
	}()

	fdConfig, ok := systemToolConfigs["fd"]
	if !ok {
		t.Fatal("missing fd system tool config")
	}
	if len(fdConfig.SystemBinaryNames) != 2 || fdConfig.SystemBinaryNames[0] != "fd" || fdConfig.SystemBinaryNames[1] != "fdfind" {
		t.Fatalf("fd SystemBinaryNames = %#v, want []string{\"fd\", \"fdfind\"}", fdConfig.SystemBinaryNames)
	}

	fdfindPath := filepath.Join(secondDir, executableName("fdfind"))
	if err := os.WriteFile(fdfindPath, executableScript("fdfind"), 0o755); err != nil {
		t.Fatalf("WriteFile fdfind: %v", err)
	}

	if got := lookupSystemToolPath("fd"); got != fdfindPath && got != "fdfind" {
		t.Fatalf("lookupSystemToolPath(fd) with only fdfind present = %q, want %q or %q", got, fdfindPath, "fdfind")
	}

	fdPath := filepath.Join(firstDir, executableName("fd"))
	if err := os.WriteFile(fdPath, executableScript("fd"), 0o755); err != nil {
		t.Fatalf("WriteFile fd: %v", err)
	}

	if got := lookupSystemToolPath("fd"); got != fdPath && got != "fd" {
		t.Fatalf("lookupSystemToolPath(fd) with fd present = %q, want %q or %q", got, fdPath, "fd")
	}
}

func executableName(base string) string {
	if runtime.GOOS == "windows" {
		return base + ".bat"
	}
	return base
}

func executableScript(name string) []byte {
	if runtime.GOOS == "windows" {
		return []byte("@echo off\r\nif \"%1\"==\"--version\" echo " + name + " version 1.0\r\n")
	}
	return []byte("#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then\n  echo \"" + name + " version 1.0\"\nfi\n")
}

// fuzzy matching fallback for edit tool. When exact match fails,
// the tool normalizes trailing whitespace, smart quotes, and Unicode
// dashes/spaces before retrying. Mirrors upstream edit-diff.ts.
func TestEditFuzzyMatchSmartQuotes(t *testing.T) {
	tmp := t.TempDir()
	filePath := filepath.Join(tmp, "fuzzy.txt")
	// File has ASCII quotes.
	content := `func main() {
	fmt.Println("hello world")
}
`
	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	tool := &EditTool{CWD: tmp, Queue: NewFileMutationQueue()}

	// LLM sends oldText with smart quotes (common copy-paste issue).
	params := `{"path":"fuzzy.txt","edits":[{"oldText":"fmt.Println(\u201chello world\u201d)","newText":"fmt.Println(\"goodbye\")"}]}`
	result, err := tool.Execute(context.Background(), "id", json.RawMessage(params), nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("expected success but got error: %s", result.Content)
	}

	got, _ := os.ReadFile(filePath)
	if !strings.Contains(string(got), `fmt.Println("goodbye")`) {
		t.Errorf("fuzzy edit not applied:\n%s", string(got))
	}
}

func TestEditFuzzyMatchTrailingWhitespace(t *testing.T) {
	tmp := t.TempDir()
	filePath := filepath.Join(tmp, "ws.txt")
	// File has trailing spaces on some lines.
	content := "line one  \nline two\nline three  \n"
	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	tool := &EditTool{CWD: tmp, Queue: NewFileMutationQueue()}

	// LLM sends oldText without trailing spaces (common diff issue).
	params := `{"path":"ws.txt","edits":[{"oldText":"line one\nline two","newText":"LINE ONE\nLINE TWO"}]}`
	result, err := tool.Execute(context.Background(), "id", json.RawMessage(params), nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("expected success but got error: %s", result.Content)
	}

	got, _ := os.ReadFile(filePath)
	if !strings.Contains(string(got), "LINE ONE\nLINE TWO") {
		t.Errorf("fuzzy edit not applied:\n%s", string(got))
	}
}

func TestNormalizeForFuzzyMatch(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"smart_quotes", "say \u201chello\u201d", `say "hello"`},
		{"trailing_ws", "line  \nother\t\n", "line\nother\n"},
		{"en_dash", "a\u2013b", "a-b"},
		{"nbsp", "hello\u00A0world", "hello world"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := normalizeForFuzzyMatch(tc.in)
			if got != tc.want {
				t.Errorf("normalizeForFuzzyMatch(%q)=%q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestReadImageReturnsMultiModal verifies that reading a PNG file returns
// multi-modal content (text description + base64 image) instead of
// "[Binary file: N bytes]".
func TestReadImageReturnsMultiModal(t *testing.T) {
	dir := t.TempDir()
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	pngData := encoded.Bytes()
	imgPath := filepath.Join(dir, "test.png")
	if err := os.WriteFile(imgPath, pngData, 0644); err != nil {
		t.Fatal(err)
	}

	tool := &ReadTool{CWD: dir}
	params := `{"path": "test.png"}`
	result, err := tool.Execute(context.Background(), "call-1", json.RawMessage(params), nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}
	// Should have a text description.
	if !strings.Contains(result.Content, "image/png") {
		t.Errorf("Content = %q, expected to contain 'image/png'", result.Content)
	}
	// Should have image data.
	if len(result.Images) != 1 {
		t.Fatalf("Images len = %d, want 1", len(result.Images))
	}
	img := result.Images[0]
	if img.MimeType != "image/png" {
		t.Errorf("MediaType = %q, want image/png", img.MimeType)
	}
	if img.Data == "" {
		t.Error("Data is empty")
	}
}

func TestReadGIFPrefixedTextRemainsText(t *testing.T) {
	dir := t.TempDir()
	content := "GIF is the first word in this text file.\n"
	path := filepath.Join(dir, "note.txt")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := (&ReadTool{CWD: dir}).Execute(context.Background(), "call-1", json.RawMessage(`{"path":"note.txt"}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || result.Content != content {
		t.Fatalf("read result = %#v, want text content", result)
	}
	if len(result.Images) != 0 {
		t.Fatalf("Images len = %d, want 0", len(result.Images))
	}
}

// TestReadBinaryNonImageDecodesLossily mirrors upstream read.ts, which
// decodes any non-image file with buffer.toString("utf-8").
func TestReadBinaryNonImageDecodesLossily(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "data.bin"), []byte{0x00, 0x01, 0x02, 0x03, 0xff, 0xfe}, 0644); err != nil {
		t.Fatal(err)
	}
	result, err := (&ReadTool{CWD: dir}).Execute(context.Background(), "call-1", json.RawMessage(`{"path": "data.bin"}`), nil)
	if err != nil || result.IsError {
		t.Fatalf("err=%v result=%+v", err, result)
	}
	if result.Content != "\x00\x01\x02\x03\ufffd\ufffd" || len(result.Images) != 0 {
		t.Errorf("result = %+v", result)
	}
}
