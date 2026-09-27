package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestReportOutputFlagAndImplicitStdoutNotice(t *testing.T) {
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	binary := filepath.Join(dir, "coverage")
	build := exec.CommandContext(t.Context(), "go", "build", "-o", binary, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build coverage: %v\n%s", err, output)
	}
	run := func(args ...string) ([]byte, string) {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), binary, args...)
		cmd.Dir = root
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		stdout, err := cmd.Output()
		if err != nil {
			t.Fatalf("coverage %v: %v\n%s", args, err, stderr.String())
		}
		return stdout, stderr.String()
	}
	implicit, notice := run()
	for _, want := range []string{"writing the report to stdout", "run: make generate", "make coverage RESULTS=", "-out -"} {
		if !strings.Contains(notice, want) {
			t.Errorf("missing %q in notice: %s", want, notice)
		}
	}
	explicit, notice := run("-out", "-")
	if notice != "" || len(explicit) == 0 || !bytes.Equal(explicit, implicit) {
		t.Fatal("explicit stdout changed the report or warned")
	}
	file := filepath.Join(dir, "coverage.md")
	stdout, notice := run("-out", file)
	if len(stdout) != 0 || notice != "" {
		t.Fatal("explicit output file wrote stdout or warned")
	}
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, implicit) {
		t.Fatal("output file and stdout differ")
	}
}
