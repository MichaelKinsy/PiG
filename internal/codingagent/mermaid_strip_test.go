//go:build !pig_strip_mermaid

package codingagent

import (
	"os/exec"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/pigstrip"
)

// A pig_strip_mermaid build of cmd/pig compiles mermaid_transform.go and the Marked block parsers out, so it does not link
// the diagram renderer; stock does.
func TestStripMermaidBuildOmitsMermaidRenderer(t *testing.T) {
	if testing.Short() {
		t.Skip("go list of cmd/pig in -short mode")
	}
	deps := func(tags ...string) []string {
		args := append(append([]string{"list"}, tags...), "-deps", "github.com/MichaelKinsy/PiG/cmd/pig")
		out, err := exec.Command("go", args...).Output()
		if err != nil {
			t.Fatalf("go %s: %v", strings.Join(args, " "), err)
		}
		return strings.Fields(string(out))
	}
	const pkg = "github.com/MichaelKinsy/PiG/internal/mermaid"
	if !slices.Contains(deps(), pkg) {
		t.Errorf("the stock cmd/pig does not link %s", pkg)
	}
	if slices.Contains(deps("-tags", pigstrip.Tag(pigstrip.Mermaid)), pkg) {
		t.Errorf("a %s build of cmd/pig links %s", pigstrip.Tag(pigstrip.Mermaid), pkg)
	}
}

// A Piglet that strips mermaid leaves a mermaid fence raw, as mermaid mode "off" does; stock draws it.
func TestRuntimeStripMermaidLeavesFenceRaw(t *testing.T) {
	const markdown = "```mermaid\nflowchart LR\nA --> B\n```\n"
	ctx := extension.MarkdownTransformContext{MessageType: extension.MarkdownMessageAssistant, AvailableWidth: 200}
	streaming := func() string { return "streaming" }
	if got := createMermaidMarkdownTransformer(streaming, nil)(markdown, ctx); got == markdown || !strings.Contains(got, "┌") {
		t.Fatalf("stock transformer left the diagram undrawn: %q", got)
	}
	t.Cleanup(pigstrip.Strip(pigstrip.ListFeatures, pigstrip.Mermaid))
	if got := createMermaidMarkdownTransformer(streaming, nil)(markdown, ctx); got != markdown {
		t.Fatalf("stripped transformer = %q, want the raw markdown", got)
	}
}
