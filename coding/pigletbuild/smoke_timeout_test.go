package pigletbuild

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// writeHungArtifact writes an artifact that prints banner (when not empty) and then never exits: a shell script, or on
// Windows, which starts only executables, a small program built as hung.exe.
func writeHungArtifact(t *testing.T, banner string) string {
	t.Helper()
	dir := t.TempDir()
	if runtime.GOOS != "windows" {
		script := "#!/bin/sh\n"
		if banner != "" {
			script += "echo " + banner + "\n"
		}
		path := filepath.Join(dir, "hung")
		if err := os.WriteFile(path, []byte(script+"exec sleep 3600\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		return path
	}
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "go.mod"), []byte("module hungpig\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	program := "package main\n\nimport (\n\t\"fmt\"\n\t\"time\"\n)\n\nfunc main() {\n\tfmt.Println(\"" + banner + "\")\n\ttime.Sleep(time.Hour)\n}\n"
	if err := os.WriteFile(filepath.Join(src, "main.go"), []byte(program), 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "hung.exe")
	build := exec.Command("go", "build", "-o", path, ".")
	build.Dir = src
	build.Env = append(os.Environ(), "GOWORK=off")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build hung artifact: %v\n%s", err, output)
	}
	return path
}

func setSmokeBudget(t *testing.T, budget time.Duration) {
	t.Helper()
	previous := smokeArtifactTimeout
	smokeArtifactTimeout = budget
	t.Cleanup(func() { smokeArtifactTimeout = previous })
}

// A hung artifact fails the smoke verification with a message that names the deadline, not the killed process's exit
// status, which on Windows is a bare "exit status 1", and says the process had started and not exited. Where the
// artifact is a shell script it also shows what the process printed, so a log tells a hang from a slow start; on Windows
// the program is built here and may not have run its first line within the short budget of this test, so the output is
// not required.
func TestSmokeArtifactReportsItsDeadline(t *testing.T) {
	path := writeHungArtifact(t, "pig-test-banner")
	setSmokeBudget(t, 200*time.Millisecond)
	err := smokeArtifact(t.Context(), path)
	if err == nil {
		t.Fatal("smokeArtifact accepted an artifact that never exits")
	}
	wants := []string{"timed out after 200ms running " + path + " --version", "had not exited"}
	if runtime.GOOS != "windows" {
		wants = append(wants, "pig-test-banner")
	}
	for _, want := range wants {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("smokeArtifact error = %v, want %q", err, want)
		}
	}
}

// The first start of a freshly written executable can block in the operating system (Windows scans a new executable
// before it runs it); that wait is not the artifact running, so a healthy artifact that starts slowly passes, however
// long the start takes against the budget for the run.
func TestSmokeArtifactDoesNotChargeASlowFirstStartToTheRunBudget(t *testing.T) {
	path := writeFakePigArtifact(t, t.TempDir(), "pig-slow-start")
	const budget = 300 * time.Millisecond
	setSmokeBudget(t, budget)
	previous := smokeStart
	smokeStart = func(cmd *exec.Cmd) error {
		time.Sleep(3 * budget)
		return cmd.Start()
	}
	t.Cleanup(func() { smokeStart = previous })
	if err := smokeArtifact(t.Context(), path); err != nil {
		t.Fatalf("a healthy artifact with a slow first start failed verification: %v", err)
	}
}

// The wait for a run is bounded by the build's own context as well as by the budget: a canceled build stops the
// artifact and reports the cancellation, not a killed process.
func TestSmokeArtifactStopsWhenTheBuildIsCanceled(t *testing.T) {
	path := writeHungArtifact(t, "")
	setSmokeBudget(t, time.Hour)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	time.AfterFunc(200*time.Millisecond, cancel)
	err := smokeArtifact(ctx, path)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("smokeArtifact error = %v, want the build's cancellation", err)
	}
}
