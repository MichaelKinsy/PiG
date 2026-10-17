package tools

// Ports packages/coding-agent/src/core/tools/ls.ts createLsTool (:173) with the inputs and expected results of packages/coding-agent/test/tools.test.ts "ls tool" (:984-995) and the ls.ts execute branches it does not exercise.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func lsRun(t *testing.T, tool *LsTool, params string) (string, bool, any) {
	t.Helper()
	result, err := tool.Execute(t.Context(), "ls-call", json.RawMessage(params), nil)
	if err != nil {
		t.Fatal(err)
	}
	return resultText(t, result), result.IsError, result.Details
}

func lsWrite(t *testing.T, dir string, files []string, dirs []string) {
	t.Helper()
	for _, name := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range dirs {
		if err := os.Mkdir(filepath.Join(dir, name), 0o750); err != nil {
			t.Fatal(err)
		}
	}
}

// tools.test.ts:985-995 "should list dotfiles and directories": a created ls tool lists .hidden-file and .hidden-dir/ of an absolute path.
//
// mutation-checked: the mutant "CreateLsTool drops the cwd" fails it (the relative-path case).
// mutation-checked: the mutant "ls drops the directory suffix" fails it.
func TestCreateLsToolListsDotfilesAndDirectories(t *testing.T) {
	dir := t.TempDir()
	lsWrite(t, dir, []string{".hidden-file"}, []string{".hidden-dir"})
	tool := CreateLsTool("/not-the-test-dir", nil)
	got, isErr, _ := lsRun(t, tool, `{"path":`+quoteJSON(dir)+`}`)
	if isErr || got != ".hidden-dir/\n.hidden-file" {
		t.Fatalf("listing = %q (error %v), want the dotfile and the slash-suffixed dotdir", got, isErr)
	}
	// ls.ts:93 resolveToCwd(path || ".", ctx.cwd || cwd): the cwd the tool was created with resolves a relative path and the default ".".
	lsWrite(t, dir, []string{"inner.txt"}, nil)
	inCwd := CreateLsTool(dir, nil)
	if got, _, _ := lsRun(t, inCwd, `{}`); got != ".hidden-dir/\n.hidden-file\ninner.txt" {
		t.Fatalf("default path listing = %q", got)
	}
	if got, _, _ := lsRun(t, inCwd, `{"path":".hidden-dir"}`); got != "(empty directory)" {
		t.Fatalf("relative empty directory = %q, want (empty directory) (ls.ts:131)", got)
	}
}

// ls.ts:113 sorts case-insensitively, :90 and :98 reject a missing path and a file.
//
// mutation-checked: the mutant "ls sorts case-sensitively" fails it.
func TestCreateLsToolSortsAndRejectsBadPaths(t *testing.T) {
	dir := t.TempDir()
	lsWrite(t, dir, []string{"B.txt", "a.txt"}, []string{"c"})
	tool := CreateLsTool(dir, nil)
	if got, _, _ := lsRun(t, tool, `{}`); got != "a.txt\nB.txt\nc/" {
		t.Fatalf("listing = %q, want a.txt before B.txt (case-insensitive)", got)
	}
	got, isErr, _ := lsRun(t, tool, `{"path":"missing"}`)
	if !isErr || got != "Path not found: "+filepath.Join(dir, "missing") {
		t.Fatalf("missing = %q (error %v)", got, isErr)
	}
	got, isErr, _ = lsRun(t, tool, `{"path":"B.txt"}`)
	if !isErr || got != "Not a directory: "+filepath.Join(dir, "B.txt") {
		t.Fatalf("file = %q (error %v)", got, isErr)
	}
}

// ls.ts:118-141: past the limit the listing stops and the notice names the limit and twice the limit; details carry entryLimitReached.
//
// mutation-checked: the mutant "ls ignores the entry limit" fails it.
func TestCreateLsToolEntryLimitNotice(t *testing.T) {
	dir := t.TempDir()
	lsWrite(t, dir, []string{"a", "b", "c"}, nil)
	got, isErr, details := lsRun(t, CreateLsTool(dir, nil), `{"limit":2}`)
	if isErr || got != "a\nb\n\n[2 entries limit reached. Use limit=4 for more]" {
		t.Fatalf("listing = %q (error %v)", got, isErr)
	}
	if d, ok := details.(*LsDetails); !ok || d.EntryLimitReached != 2 {
		t.Fatalf("details = %#v, want entryLimitReached 2", details)
	}
	if got, _, details := lsRun(t, CreateLsTool(dir, nil), `{"limit":3}`); got != "a\nb\nc" || details != nil {
		t.Fatalf("at the limit = %q, details %#v: want no notice when every entry fits", got, details)
	}
}

// ls.ts:46-52 options.operations replace the filesystem for a created tool.
//
// mutation-checked: the mutant "CreateLsTool drops the operations" fails it.
func TestCreateLsToolUsesOptionOperations(t *testing.T) {
	root := virtualPath("/virtual")
	tool := CreateLsTool(root, &LsToolOptions{Operations: &LsOperations{
		Exists:  func(path string) (bool, error) { return path == root, nil },
		Stat:    func(string) (LsStat, error) { return fakeFileInfo{dir: true}, nil },
		Readdir: func(string) ([]string, error) { return []string{"only-here"}, nil },
	}})
	if got, _, _ := lsRun(t, tool, `{}`); got != "only-here/" {
		t.Fatalf("listing = %q, want the operations' entry", got)
	}
}

func quoteJSON(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
