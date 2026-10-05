package pigletbuild

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// A hung artifact fails the smoke verification with a message that names the deadline, not the killed process's exit
// status, which on Windows is a bare "exit status 1".
func TestSmokeArtifactReportsItsDeadline(t *testing.T) {
	dir := t.TempDir()
	var path string
	if runtime.GOOS == "windows" {
		src := t.TempDir()
		if err := os.WriteFile(filepath.Join(src, "go.mod"), []byte("module sleepypig\n\ngo 1.26\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(src, "main.go"), []byte("package main\n\nimport \"time\"\n\nfunc main() { time.Sleep(time.Hour) }\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		path = filepath.Join(dir, "sleepy.exe")
		build := exec.Command("go", "build", "-o", path, ".")
		build.Dir = src
		build.Env = append(os.Environ(), "GOWORK=off")
		if output, err := build.CombinedOutput(); err != nil {
			t.Fatalf("build sleeping artifact: %v\n%s", err, output)
		}
	} else {
		path = filepath.Join(dir, "sleepy")
		if err := os.WriteFile(path, []byte("#!/bin/sh\nexec sleep 3600\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	previous := smokeArtifactTimeout
	smokeArtifactTimeout = 200 * time.Millisecond
	t.Cleanup(func() { smokeArtifactTimeout = previous })
	err := smokeArtifact(path)
	if err == nil || !strings.Contains(err.Error(), "timed out after 200ms running "+path+" --version") {
		t.Fatalf("smokeArtifact error = %v, want the deadline named", err)
	}
}
