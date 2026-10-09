package cli

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pigstrip"
)

// A Binary without MCP answers `pig mcp ...` with the strip message and exit 1 instead of sending `mcp list` as a prompt.
func TestStrippedBinaryPigMcpReportsStripped(t *testing.T) {
	stdout, stderr, code := runStrippedPig(t, "", "mcp", "list")
	want := pigstrip.Error("pig mcp", pigstrip.ListExtensions, "mcp").Error()
	if code != 1 || strings.TrimSpace(stderr) != want || stdout != "" {
		t.Fatalf("exit %d\nstdout: %q\nstderr: %q\nwant stderr %q", code, stdout, stderr, want)
	}
}

// A Binary that compiled the built-in extensions out skips `-e builtin:<name>` of each silently, like upstream's `--no-mcp`;
// a name that was never a built-in still fails with Pi's message.
func TestStrippedBinarySkipsExplicitStrippedBuiltins(t *testing.T) {
	args := []string{"-p", "--model", "test-faux/faux-1", "-e", "builtin:codemode", "-e", "builtin:tool-search", "-e", "builtin:mcp", "-e", "builtin:pig-login", "reply with exactly: done"}
	stdout, stderr, code := runStrippedPig(t, "", args...)
	if code != 0 || strings.TrimSpace(stdout) != "done" || strings.Contains(stderr, "extension") {
		t.Fatalf("exit %d\nstdout: %q\nstderr: %q", code, stdout, stderr)
	}
	_, stderr, _ = runStrippedPig(t, "", "-p", "--model", "test-faux/faux-1", "-e", "builtin:missing", "reply with exactly: done")
	if want := `Failed to load extension "builtin:missing": Unknown built-in extension: builtin:missing`; !strings.Contains(stderr, want) {
		t.Fatalf("stderr = %q, want %q", stderr, want)
	}
}
