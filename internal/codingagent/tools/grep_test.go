package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TruncateLine mirrors upstream truncateLine: JavaScript string length and
// the "... [truncated]" suffix.
func TestTruncateLine(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		maxChars  int
		want      string
		truncated bool
	}{
		{"short", "hello", 10, "hello", false},
		{"exact", "hello", 5, "hello", false},
		{"long", "hello world", 5, "hello... [truncated]", true},
		{"empty", "", 5, "", false},
		{"multibyte kept whole", "ééééé", 5, "ééééé", false},
		{"multibyte cut by characters", "éééééé", 5, "ééééé... [truncated]", true},
		{"surrogate pair counts two", "a😀b", 2, "a\ufffd... [truncated]", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, wasTruncated := TruncateLine(tc.input, tc.maxChars)
			if got != tc.want || wasTruncated != tc.truncated {
				t.Errorf("TruncateLine(%q, %d) = %q, %v; want %q, %v", tc.input, tc.maxChars, got, wasTruncated, tc.want, tc.truncated)
			}
		})
	}
}

func TestGrepLiteralPatternStartingWithDash(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sample.txt")
	if err := os.WriteFile(path, []byte("-n flag\nplain\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	fakeBin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(fakeBin, 0o755); err != nil {
		t.Fatal(err)
	}
	fakeRG := filepath.Join(fakeBin, "rg")
	script := `#!/bin/sh
	pattern=""
	search=""
	seen_sep=0
	for arg in "$@"; do
		if [ "$seen_sep" = "1" ]; then
			if [ -z "$pattern" ]; then
				pattern="$arg"
			else
				search="$arg"
			fi
		elif [ "$arg" = "--" ]; then
			seen_sep=1
		fi
	done
	if [ "$pattern" = "-n" ] && [ -n "$search" ]; then
		printf '{"type":"match","data":{"path":{"text":"%s"},"line_number":1,"lines":{"text":"-n flag\\n"}}}\n' "$search"
		exit 0
	fi
	exit 1
`
	if err := os.WriteFile(fakeRG, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))

	gt := &GrepTool{CWD: dir}
	args, _ := json.Marshal(grepParams{Pattern: "-n", Path: path, Literal: true})
	res, err := gt.Execute(context.Background(), "", args, nil)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected success, got error: %s", res.Content)
	}
	if !strings.Contains(res.Content, "sample.txt:1: -n flag") {
		t.Fatalf("unexpected grep output: %q", res.Content)
	}
}
