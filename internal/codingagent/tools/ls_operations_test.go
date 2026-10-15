package tools

// pi: packages/coding-agent/src/core/tools/ls.ts

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
)

// fakeFileInfo is `{ isDirectory: () => boolean }`, all that ls.ts:34 asks of a stat result.
type fakeFileInfo struct{ dir bool }

func (f fakeFileInfo) IsDirectory() bool { return f.dir }

// ls.ts:34-41 LsOperations: exists, stat and readdir replace the local filesystem, so a remote or virtual tree lists like a local one.
func TestLsToolUsesTheConfiguredOperations(t *testing.T) {
	var statted []string
	root := virtualPath("/virtual")
	alpha, gone := filepath.Join(root, "alpha"), filepath.Join(root, "gone")
	tool := &LsTool{CWD: root, Operations: &LsOperations{
		Exists: func(path string) (bool, error) { return path == root || path == alpha, nil },
		Stat: func(path string) (LsStat, error) {
			statted = append(statted, path)
			switch path {
			case root, alpha:
				return fakeFileInfo{dir: true}, nil
			case gone:
				return nil, errors.New("broken link")
			}
			return fakeFileInfo{}, nil
		},
		Readdir: func(path string) ([]string, error) {
			if path == alpha {
				return []string{"inner.txt"}, nil
			}
			names := []string{"zeta.txt", "alpha", "beta.txt", "gone"}
			return names, nil
		},
	}}
	result, err := tool.Execute(t.Context(), "id", json.RawMessage(`{}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	// Case-insensitive order, directories get a slash, an entry that cannot be stat'ed is skipped.
	if got, want := resultText(t, result), "alpha/\nbeta.txt\nzeta.txt"; got != want {
		t.Fatalf("listing = %q, want %q", got, want)
	}
	missing, err := tool.Execute(t.Context(), "id", json.RawMessage(`{"path":"absent"}`), nil)
	if err != nil || !missing.IsError || resultText(t, missing) != "Path not found: "+filepath.Join(root, "absent") {
		t.Fatalf("missing = %+v, %v", missing, err)
	}
	notDir, _ := tool.Execute(t.Context(), "id", json.RawMessage(`{"path":"alpha/inner.txt"}`), nil)
	if resultText(t, notDir) != "Path not found: "+filepath.Join(alpha, "inner.txt") {
		t.Fatalf("a path the operations do not know is not found, got %q", resultText(t, notDir))
	}
	if len(statted) == 0 {
		t.Fatal("the configured stat never ran")
	}
}
