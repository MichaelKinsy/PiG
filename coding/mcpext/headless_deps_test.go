package mcpext_test

import (
	"os/exec"
	"strings"
	"testing"
)

const modulePath = "github.com/MichaelKinsy/PiG/"

func goList(t *testing.T, format string, args ...string) []string {
	t.Helper()
	out, err := exec.Command("go", append([]string{"list", "-f", format}, args...)...).Output()
	if err != nil {
		t.Fatalf("go list %v: %v", args, err)
	}
	return strings.Fields(string(out))
}

func forbidden(deps []string) []string {
	var bad []string
	for _, d := range deps {
		if d == modulePath+"tui" || strings.HasPrefix(d, modulePath+"tui/") || strings.Contains(d, "wordseg") {
			bad = append(bad, d)
		}
	}
	return bad
}

// The MCP client itself is fully headless: no terminal UI and no word-segmentation dictionaries.
func TestMcpClientLinksNoTerminalUI(t *testing.T) {
	if bad := forbidden(goList(t, `{{join .Deps "\n"}}`, modulePath+"mcp", modulePath+"mcp/oauth")); len(bad) > 0 {
		t.Fatalf("package mcp links the terminal UI: %v", bad)
	}
}

// coding/mcpext is the headless core: it imports no terminal UI itself (that is package mcptui). It still reaches tui
// through coding/extension and, via it, internal/codingagent/tools; no other import may.
func TestMcpextReachesTerminalUIOnlyThroughCodingExtension(t *testing.T) {
	imports := goList(t, `{{join .Imports "\n"}}`, modulePath+"coding/mcpext")
	for _, imp := range imports {
		if !strings.HasPrefix(imp, modulePath) {
			continue
		}
		if bad := forbidden([]string{imp}); len(bad) > 0 {
			t.Errorf("coding/mcpext imports %v directly", bad)
			continue
		}
		if imp == modulePath+"coding/extension" || imp == modulePath+"internal/codingagent/tools" {
			continue // Known: coding/extension aliases tui types (opaque_types.go, view_surface.go, ...); tools imports coding/extension.
		}
		deps := append(goList(t, `{{join .Deps "\n"}}`, imp), imp)
		if bad := forbidden(deps); len(bad) > 0 {
			t.Errorf("coding/mcpext reaches the terminal UI through %s: %v", imp, bad)
		}
	}
}
