package main

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigidentity"
)

// D26: Pi's setupCli exports AI_AGENT=pi so tools can name the agent that
// launched them (cli/setup.ts:7, rpc-entry.ts:8). PiG names itself.

func TestSetupCliSetsInheritedProcessMarkers(t *testing.T) {
	t.Setenv("PI_CODING_AGENT", "")
	t.Setenv("AI_AGENT", "other")
	setupCli()
	if got := os.Getenv("PI_CODING_AGENT"); got != "true" {
		t.Fatalf("PI_CODING_AGENT = %q, want true", got)
	}
	if got := os.Getenv("AI_AGENT"); got != pigidentity.AgentMarker || got == "" || got == "pi" {
		t.Fatalf("AI_AGENT = %q, want PiG's %q", got, pigidentity.AgentMarker)
	}
	if runtime.GOOS == "windows" {
		return
	}
	out, err := exec.Command("sh", "-c", `printf '%s|%s' "$PI_CODING_AGENT" "$AI_AGENT"`).Output()
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(out)); got != "true|"+pigidentity.AgentMarker {
		t.Fatalf("child process saw %q, want true|%s", got, pigidentity.AgentMarker)
	}
}
