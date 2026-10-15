package cli

import (
	"bytes"
	"strings"
	"testing"
)

// The auth help names PiG's command (D2), where Pi's printAuthCommandHelp names `pi`.
func TestAuthHelpNamesPig(t *testing.T) {
	for line := range strings.SplitSeq(authCommandHelp, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "pi ") {
			t.Errorf("auth help names pi: %q", line)
		}
	}
	if !strings.Contains(authCommandHelp, "  pig auth print-api-key ") {
		t.Errorf("auth help lacks `pig auth print-api-key`:\n%s", authCommandHelp)
	}
}

// `pig extension --help` lists every flag `pig extension init` accepts, --login included.
func TestExtensionHelpListsInitLogin(t *testing.T) {
	for _, flag := range []string{"--name", "--lang", "--login", "--isolated", "--force", "--json"} {
		if !strings.Contains(extensionCommandHelp, " ["+flag) {
			t.Errorf("extension help lacks %s:\n%s", flag, extensionCommandHelp)
		}
	}
}

// `pig --help` lists PiG's working top-level commands.
func TestHelpListsPigCommands(t *testing.T) {
	var out bytes.Buffer
	printHelp(&out, false)
	for _, cmd := range []string{"pig login ", "pig logout ", "pig reload ", "pig package ", "pig extension upgrade ", "pig diagnose "} {
		if !strings.Contains(out.String(), "\n  "+cmd) {
			t.Errorf("pig --help does not list %q", strings.TrimSpace(cmd))
		}
	}
}
