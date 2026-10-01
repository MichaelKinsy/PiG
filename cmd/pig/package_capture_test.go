package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

func TestPackageCaptureTimeoutWaitsForChild(t *testing.T) {
	// Pi package-manager.ts:2640-2662 sends termination on timeout but rejects only after the child's close event.
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), executable, "-test.run=^TestPackageCaptureChild$")
	cmd.Env = append(os.Environ(), "PIG_PACKAGE_CAPTURE_CHILD=wait")
	output, err := runWithTimeout(cmd, 0)
	if err == nil || !strings.Contains(err.Error(), "timed out after 0ms") {
		t.Fatalf("output=%q error=%v, want timeout error", output, err)
	}
	if cmd.ProcessState == nil {
		t.Fatal("timeout returned before the child was reaped")
	}
}

func TestPackageCaptureOutputAndFailure(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"success", "failure", "stdout-failure"} {
		t.Run(mode, func(t *testing.T) {
			cmd := exec.CommandContext(t.Context(), executable, "-test.run=^TestPackageCaptureChild$")
			cmd.Env = append(os.Environ(), "PIG_PACKAGE_CAPTURE_CHILD="+mode)
			output, err := runWithTimeout(cmd, testbudget.Wait(t))
			if mode == "success" {
				if err != nil || output != "captured output" {
					t.Fatalf("output=%q error=%v", output, err)
				}
			} else {
				diagnostic := "captured stderr"
				if mode == "stdout-failure" {
					diagnostic = "captured stdout"
				}
				if err == nil || !strings.Contains(err.Error(), "failed with code 7: "+diagnostic) {
					t.Fatalf("error=%v, want exit status and captured diagnostic", err)
				}
			}
			if cmd.ProcessState == nil {
				t.Fatal("capture returned before child cleanup")
			}
		})
	}
}

func TestPackageCaptureStartFailure(t *testing.T) {
	cmd := exec.CommandContext(t.Context(), t.TempDir()+"/missing-command")
	if output, err := runWithTimeout(cmd, 0); err == nil || output != "" {
		t.Fatalf("output=%q error=%v, want launch error", output, err)
	}
	if cmd.Process != nil {
		t.Fatal("failed launch unexpectedly created a process")
	}
}

func BenchmarkPackageCapture(b *testing.B) {
	executable, err := os.Executable()
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		cmd := exec.CommandContext(b.Context(), executable, "-test.run=^TestPackageCaptureChild$")
		cmd.Env = append(os.Environ(), "PIG_PACKAGE_CAPTURE_CHILD=success")
		if output, err := runWithTimeout(cmd, time.Minute); err != nil || output != "captured output" {
			b.Fatalf("output=%q error=%v", output, err)
		}
	}
}

func TestPackageCaptureChild(t *testing.T) {
	switch os.Getenv("PIG_PACKAGE_CAPTURE_CHILD") {
	case "wait":
		// Keep the subprocess alive so the tested zero-duration timeout owns its termination.
		time.Sleep(time.Hour)
	case "success":
		_, _ = os.Stdout.WriteString("  captured output\n")
		os.Exit(0)
	case "failure":
		_, _ = os.Stderr.WriteString("captured stderr")
		os.Exit(7)
	case "stdout-failure":
		_, _ = os.Stdout.WriteString("captured stdout")
		os.Exit(7)
	}
}
