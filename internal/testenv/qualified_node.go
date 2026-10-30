package testenv

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// RequireQualifiedNode returns the node on PATH once it is the release .node-version names. WHATWG URL parsing (ada) and ICU data change between Node releases, so an oracle that compares a Go port with Node's answers describes one release only: Node 22.19 and 24.19 reject an ASCII host with an "xn--" label that Node 24.22 and 26 accept. A different release fails the test with that explanation, where a comparison would report differences that are not port bugs.
func RequireQualifiedNode(t testing.TB) string {
	t.Helper()
	path := node(t)
	pin, err := os.ReadFile(filepath.Join(ModuleRoot(t), ".node-version"))
	if err != nil {
		t.Fatalf("the qualified Node release: %v", err)
	}
	//nolint:gosec // G204 (class b): program is node from testenv node(t), i.e. exec.LookPath("node"); the only arg is fixed --version.
	out, err := exec.CommandContext(t.Context(), path, "--version").Output()
	if err != nil {
		t.Fatalf("%s --version: %v", path, err)
	}
	checkQualifiedNode(t, string(out), string(pin))
	return path
}

func checkQualifiedNode(t testing.TB, version, pin string) {
	t.Helper()
	if message := qualifiedNodeMismatch(strings.TrimSpace(version), strings.TrimSpace(pin)); message != "" {
		t.Fatalf("%s", message)
	}
}

// qualifiedNodeMismatch returns why version (the output of node --version) is not the release pin (.node-version) names, or "" when it is.
func qualifiedNodeMismatch(version, pin string) string {
	if version == "v"+pin {
		return ""
	}
	return fmt.Sprintf("node %q is not the qualified Node v%s (.node-version): this oracle's answers depend on the Node release (URL parsing in ada, ICU data), so it compares against that release only; put it on PATH (CI: actions/setup-node with node-version-file .node-version; developer hosts: mise)", version, pin)
}
