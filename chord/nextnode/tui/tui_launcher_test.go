package tui

import (
	"strings"
	"testing"
)

// Tests of the terminal launcher (slice 11 §4, decision "Slice 11, P8"): the Node it needs, and the files the sandbox may fetch.
// The launcher's runs with a kit app's terminal surface are in nextnode/app (tui_upstream_test.go). Ports the first two describes
// of packages/chord/test/node/tui/launcher.test.ts.

func TestTheNodeItNeedsRefusesANodeOlderThan2651WithAClearError(t *testing.T) {
	err := RequireSandboxNode("24.4.1")
	if err == nil || !strings.Contains(err.Error(), "needs Node 26.5.1 or later for its sandbox (this is Node 24.4.1)") {
		t.Fatalf("error = %v, want the version refusal", err)
	}
	if RequireSandboxNode("26.5.0") == nil {
		t.Fatal("26.5.0 was accepted")
	}
	for _, version := range []string{"26.5.1", "27.0.0"} {
		if err := RequireSandboxNode(version); err != nil {
			t.Fatalf("%s: %v", version, err)
		}
	}
}

func TestTheFilesTheSandboxMayFetchAreTheTerminalSurfacesAfterDotSegmentsAndEncodingsResolveOpusM2FableL1(t *testing.T) {
	server := "http://127.0.0.1:8788"
	for _, path := range []string{"/_chord/tui/plugin/@test~lines.abc.js", "/_chord/tui/plugins.0123.js"} {
		got, err := TerminalFile(path, server)
		if err != nil || got != path {
			t.Fatalf("%s: got %q, err %v", path, got, err)
		}
	}
	for _, path := range []string{
		"/_chord/tui/../../_chord/export/x", "/_chord/tui/%2e%2e/%2e%2e/admin", `/_chord/tui/..\..\admin`, "//evil.example/_chord/tui/x.js",
		"http://evil.example/_chord/tui/x.js", "/_chord/tui/x.js?y=1", "/_chord/tui/a/b/c.js", "/files/general/1",
	} {
		if _, err := TerminalFile(path, server); err == nil || !strings.Contains(err.Error(), "no terminal file") {
			t.Fatalf("%q: error = %v, want no terminal file", path, err)
		}
	}
}
