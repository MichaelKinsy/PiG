package tui

import (
	"slices"
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

// FuzzTerminalFile: whatever path the sandbox asks for, an answer is a path of the terminal surface's files on the server itself
// (no dot segment, no query, no other directory, no other origin), and nothing panics.
func FuzzTerminalFile(f *testing.F) {
	for _, seed := range []string{
		"/_chord/tui/base.abc.js", "/_chord/tui/plugin/@test~lines.abc.js", "/_chord/tui/../chord", "/_chord/tui/%2e%2e/x",
		"/_chord/tui/a?b=c", "//evil.example/_chord/tui/x", "http://127.0.0.1:9/_chord/tui/x", "/_chord/tui/", "\x00", "/_chord/tui/a b",
	} {
		f.Add(seed)
	}
	const server = "http://127.0.0.1:9"
	f.Fuzz(func(t *testing.T, path string) {
		got, err := TerminalFile(path, server)
		if err != nil {
			return
		}
		if !strings.HasPrefix(got, "/_chord/tui/") || slices.ContainsFunc(strings.Split(got, "/"), func(segment string) bool { return segment == "." || segment == ".." }) ||
			strings.ContainsAny(got, "?#%\\") || got != strings.TrimSpace(got) {
			t.Fatalf("TerminalFile(%q) = %q: not a terminal surface file", path, got)
		}
	})
}
