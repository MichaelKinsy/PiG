package main

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
)

func TestSetupCliSetsInheritedProcessMarkers(t *testing.T) {
	t.Setenv("PI_CODING_AGENT", "")
	t.Setenv("AI_AGENT", "other")
	setupCli()
	if got := os.Getenv("PI_CODING_AGENT"); got != "true" {
		t.Fatalf("PI_CODING_AGENT = %q, want true", got)
	}
	if got := os.Getenv("AI_AGENT"); got != "pi" {
		t.Fatalf("AI_AGENT = %q, want pi", got)
	}
	if runtime.GOOS == "windows" {
		return
	}
	out, err := exec.Command("sh", "-c", `printf '%s|%s' "$PI_CODING_AGENT" "$AI_AGENT"`).Output()
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(out)); got != "true|pi" {
		t.Fatalf("child process saw %q, want true|pi", got)
	}
}
