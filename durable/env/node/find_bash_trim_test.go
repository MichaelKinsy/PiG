//go:build !windows

package node

import (
	"os"
	"path/filepath"
	"testing"
)

// node.ts:198 takes `result.stdout.trim().split(/\r?\n/)[0]` of `which bash`: trim removes a leading BOM, so the path behind it is found.
func TestFindBashOnPathTrimsTheAnswerLikeJavaScript(t *testing.T) {
	dir := t.TempDir()
	bash := filepath.Join(dir, "bash")
	mustDo(t, os.WriteFile(bash, nil, 0o700))
	mustDo(t, os.WriteFile(filepath.Join(dir, "which"), []byte("#!/bin/sh\nprintf '\\357\\273\\277%s\\r\\nother\\n' '"+bash+"'\n"), 0o700))
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	if got := findBashOnPath(t.Context()); got != bash {
		t.Fatalf("findBashOnPath = %q, want %q", got, bash)
	}
}
