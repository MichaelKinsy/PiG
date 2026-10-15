package tools

// pi: packages/coding-agent/src/core/tools/read.ts

// pi: packages/coding-agent/src/core/tools/grep.ts

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// read.ts:48-57 ReadOperations: readFile and access replace the local filesystem for text files.
func TestReadToolUsesTheConfiguredOperations(t *testing.T) {
	var accessed, read []string
	tool := &ReadTool{CWD: "/remote", Operations: &ReadOperations{
		Access: func(path string) error {
			accessed = append(accessed, path)
			if path == "/remote/denied.txt" {
				return &fs.PathError{Op: "open", Path: path, Err: fs.ErrPermission}
			}
			return nil
		},
		ReadFile: func(path string) ([]byte, error) {
			read = append(read, path)
			return []byte("one\ntwo\nthree"), nil
		},
	}}
	result, err := tool.Execute(t.Context(), "id", json.RawMessage(`{"path":"notes.txt","offset":2}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := resultText(t, result); got != "two\nthree" {
		t.Fatalf("read = %q, want the remote file from line 2", got)
	}
	if len(accessed) != 1 || accessed[0] != "/remote/notes.txt" || len(read) != 1 || read[0] != "/remote/notes.txt" {
		t.Fatalf("access %v, read %v: the operations did not see the resolved path", accessed, read)
	}
	denied, _ := tool.Execute(t.Context(), "id", json.RawMessage(`{"path":"denied.txt"}`), nil)
	if !denied.IsError || len(read) != 1 {
		t.Fatalf("a failed access must stop the read: %+v, reads %v", denied, read)
	}
}

// read.ts:129-135: detectImageMimeType decides whether the file is an image; without it, or when it reports none, every file is text.
func TestReadToolOperationsDecideImageDetection(t *testing.T) {
	png, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg==")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		detect func(string) (string, error)
		image  bool
	}{
		{"no detector", nil, false},
		{"detector says text", func(string) (string, error) { return "", nil }, false},
		{"detector says png", func(string) (string, error) { return "image/png", nil }, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tool := &ReadTool{CWD: "/remote", Operations: &ReadOperations{
				Access:              func(string) error { return nil },
				ReadFile:            func(string) ([]byte, error) { return png, nil },
				DetectImageMimeType: tc.detect,
			}}
			result, err := tool.Execute(t.Context(), "id", json.RawMessage(`{"path":"a.png"}`), nil)
			if err != nil {
				t.Fatal(err)
			}
			hasImage := slices.ContainsFunc(result.Content, func(block ai.ToolResultMessageContent) bool { _, ok := block.(ai.ImageContent); return ok })
			if hasImage != tc.image {
				t.Fatalf("image block = %v, want %v: %+v", hasImage, tc.image, result)
			}
		})
	}
}

// grep.ts:53-58 GrepOperations: isDirectory and readFile (context lines) replace the local filesystem.
func TestGrepToolUsesTheConfiguredOperations(t *testing.T) {
	var dirs, reads []string
	tool := &GrepTool{CWD: "/remote", Operations: &GrepOperations{
		IsDirectory: func(path string) (bool, error) {
			dirs = append(dirs, path)
			if path == "/remote/missing" {
				return false, errors.New("no such file")
			}
			return false, nil
		},
		ReadFile: func(path string) (string, error) {
			reads = append(reads, path)
			return "before\nneedle\nafter", nil
		},
	}}
	missing, _ := tool.Execute(t.Context(), "id", json.RawMessage(`{"pattern":"needle","path":"missing"}`), nil)
	if !missing.IsError || resultText(t, missing) != "Path not found: /remote/missing" {
		t.Fatalf("missing = %+v", missing)
	}
	if len(dirs) != 1 || dirs[0] != "/remote/missing" {
		t.Fatalf("isDirectory saw %v", dirs)
	}
	_ = reads
}

// grep.ts:152: context lines come from operations.readFile, not from the file ripgrep matched in.
func TestGrepToolReadsContextLinesThroughTheOperations(t *testing.T) {
	if LookupToolPath("rg", "") == "" {
		t.Skip("ripgrep is not installed")
	}
	dir := t.TempDir()
	file := filepath.Join(dir, "match.txt")
	if err := os.WriteFile(file, []byte("needle\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var reads []string
	tool := &GrepTool{CWD: dir, Operations: &GrepOperations{
		IsDirectory: func(string) (bool, error) { return true, nil },
		ReadFile: func(path string) (string, error) {
			reads = append(reads, path)
			return "remote before\nneedle\nremote after", nil
		},
	}}
	result, err := tool.Execute(t.Context(), "id", json.RawMessage(`{"pattern":"needle","context":1}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	text := resultText(t, result)
	if !strings.Contains(text, "remote before") || len(reads) == 0 {
		t.Fatalf("context lines did not come from the operations (reads %v): %q", reads, text)
	}
}
