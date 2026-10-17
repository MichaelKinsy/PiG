package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildACPBuildsLocalSourceOffline(t *testing.T) {
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "go.mod"), []byte("module example.test/acp\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "bin", "pig-acp")
	var stderr bytes.Buffer
	if err := buildACP(t.Context(), src, target, &stderr); err != nil {
		t.Fatalf("buildACP: %v\n%s", err, stderr.String())
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("binary missing: %v", err)
	}
}

func TestACPInstallTargetBesidePigBin(t *testing.T) {
	t.Setenv("PIG_BIN", filepath.Join(t.TempDir(), "bin", "pig"))
	got, err := acpBuildTarget()
	if err != nil || filepath.Dir(got) != filepath.Dir(os.Getenv("PIG_BIN")) || !strings.HasPrefix(filepath.Base(got), "pig-acp") {
		t.Fatalf("target = %q, %v", got, err)
	}
}

func TestACPCommandRoutingAndUsage(t *testing.T) {
	var out, errb bytes.Buffer
	if runACPCommand([]string{"models"}, &out, &errb) != -1 {
		t.Fatal("must ignore other commands")
	}
	if code := runACPCommand([]string{"acp", "bogus"}, &out, &errb); code != 2 || !strings.Contains(errb.String(), "pig acp install") {
		t.Fatalf("code=%d %q", code, errb.String())
	}
}
