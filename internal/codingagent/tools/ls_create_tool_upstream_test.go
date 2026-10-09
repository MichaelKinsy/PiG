package tools

// pi: packages/coding-agent/src/core/tools/ls.ts

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// virtualTree is a directory of names whose stat says directory for the ones listed in dirs, behind LsOperations.
func virtualTree(root string, names []string, dirs ...string) *LsOperations {
	return &LsOperations{
		Exists: func(path string) (bool, error) { return path == root, nil },
		Stat: func(path string) (LsStat, error) {
			return fakeFileInfo{dir: path == root || slices.Contains(dirs, filepath.Base(path))}, nil
		},
		Readdir: func(string) ([]string, error) { return slices.Clone(names), nil },
	}
}

// createLsTool (ls.ts:173-175) builds the tool for a cwd and options; the tool is named ls and lists the cwd by default.
func TestCreateLsToolUpstream(t *testing.T) {
	t.Run("names the tool ls and lists dotfiles and directories of the cwd (tools.test.ts:985)", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, ".hidden-file"), []byte("secret"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(filepath.Join(dir, ".hidden-dir"), 0o755); err != nil {
			t.Fatal(err)
		}
		tool := CreateLsTool(dir, nil)
		if tool.Name() != "ls" {
			t.Errorf("name = %q, want ls", tool.Name())
		}
		out := runFileTool(t, tool, context.Background(), map[string]any{}).Text()
		if !strings.Contains(out, ".hidden-file") || !strings.Contains(out, ".hidden-dir/") {
			t.Errorf("output = %q, want .hidden-file and .hidden-dir/", out)
		}
	})

	t.Run("a relative path resolves against the constructor cwd and reaches the configured operations", func(t *testing.T) {
		root := filepath.Join(string(filepath.Separator), "virtual", "sub")
		var asked []string
		ops := virtualTree(root, []string{"b.txt", "a"}, "a")
		exists := ops.Exists
		ops.Exists = func(path string) (bool, error) { asked = append(asked, path); return exists(path) }
		tool := CreateLsTool(filepath.Join(string(filepath.Separator), "virtual"), &LsToolOptions{Operations: ops})
		out := runFileTool(t, tool, context.Background(), map[string]any{"path": "sub"}).Text()
		if out != "a/\nb.txt" || !slices.Equal(asked, []string{root}) {
			t.Errorf("output = %q asked = %v, want a/\\nb.txt for %s", out, asked, root)
		}
	})

	t.Run("an empty directory says so", func(t *testing.T) {
		root := filepath.Join(string(filepath.Separator), "virtual")
		tool := CreateLsTool(root, &LsToolOptions{Operations: virtualTree(root, nil)})
		if out := runFileTool(t, tool, context.Background(), map[string]any{}).Text(); out != "(empty directory)" {
			t.Errorf("output = %q", out)
		}
	})

	t.Run("the entry limit adds Pi's notice and the details (ls.ts:119-146)", func(t *testing.T) {
		root := filepath.Join(string(filepath.Separator), "virtual")
		tool := CreateLsTool(root, &LsToolOptions{Operations: virtualTree(root, []string{"e", "d", "c", "b", "a"})})
		res := runFileTool(t, tool, context.Background(), map[string]any{"limit": 2})
		want := "a\nb\n\n[2 entries limit reached. Use limit=4 for more]"
		if res.Text() != want {
			t.Errorf("output = %q, want %q", res.Text(), want)
		}
		details, ok := res.Details.(*LsDetails)
		if !ok || details.EntryLimitReached != 2 {
			t.Errorf("details = %#v, want entryLimitReached 2", res.Details)
		}
	})

	t.Run("a cancelled call fails with Operation aborted", func(t *testing.T) {
		root := filepath.Join(string(filepath.Separator), "virtual")
		tool := CreateLsTool(root, &LsToolOptions{Operations: virtualTree(root, []string{"a"})})
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		res := runFileTool(t, tool, ctx, map[string]any{})
		if !res.IsError || res.Text() != "Operation aborted" {
			t.Errorf("result = %+v", res)
		}
	})
}

// ls.ts:109 sorts `entries.sort((a, b) => a.toLowerCase().localeCompare(b.toLowerCase()))`: String.prototype.localeCompare is ICU root collation, not a
// byte comparison, so punctuation sorts before digits and letters and an accented letter sits beside its base letter. The expected order is what
// node v24 prints for the same names (ICU 78.3, en-US).
func TestLsSortsLikeLocaleCompareUpstream(t *testing.T) {
	root := filepath.Join(string(filepath.Separator), "virtual")
	names := []string{"~tilde", "zeta", "_under", "-dash", "a1", "A2", "file.txt", "File2", "é", "e", "f", "[br", "Ünder", ".dot", "10", "9", "a b", "ab"}
	tool := CreateLsTool(root, &LsToolOptions{Operations: virtualTree(root, names)})
	var out string
	for _, line := range strings.Split(runFileTool(t, tool, context.Background(), map[string]any{}).Text(), "\n") {
		out += line + ","
	}
	want := "_under,-dash,.dot,[br,~tilde,10,9,a b,a1,A2,ab,e,é,f,file.txt,File2,Ünder,zeta,"
	if out != want {
		t.Errorf("order = %s\nwant    %s", out, want)
	}
}
