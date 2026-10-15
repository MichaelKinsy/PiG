//go:build unix

package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A go on PATH below the extension SDK's go directive is flagged with its
// version, the minimum and the fix, rather than listed as if it worked.
func TestSetupStatusFlagsAPathGoBelowTheSDKFloor(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"bin", filepath.Join("pkg", "tool")} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "VERSION"), []byte("go1.22.2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "bin", "go"), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", filepath.Join(root, "bin"))
	t.Setenv("PIG_HOME", t.TempDir())
	t.Setenv("GOROOT", "")
	var stdout, stderr bytes.Buffer
	if code := runSetupCommand([]string{"setup"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{"go1.22.2", "Go 1.26", "pig setup go"} {
		if !strings.Contains(out, want) {
			t.Fatalf("status output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Every toolchain is available") {
		t.Fatalf("status calls an old go available:\n%s", out)
	}
}
