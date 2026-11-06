package codingagent

// pi: packages/coding-agent/src/modes/interactive/interactive-mode.ts getRegisteredToolDefinition

// pi: packages/coding-agent/src/core/tools/renderers/index.ts

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
)

// A model's call to a built-in tool reaches the interactive mode with no extension having registered that tool, so the card is
// built from the session's own built-in definition with its built-in renderers (getRegisteredToolDefinition: getToolDefinition(name)
// through withBuiltInRenderers). Every probe of TestBuiltinToolCardsMatchPi is drawn that way and compared with Pi's rows byte for byte:
// colours, resets, padding, images and elapsed times included.
func TestBuiltinToolCardsWithoutRegisteredDefinitionMatchPi(t *testing.T) {
	probes, names := builtinToolCardProbes(t)
	for i := range probes {
		if !slices.Contains([]string{"read", "write", "edit", "bash", "powershell", "grep", "find", "ls"}, probes[i].Tool) {
			t.Fatalf("probe %s names %q, which is not a built-in tool", names[i], probes[i].Tool)
		}
		probes[i].NoDefinition = true
		names[i] = fmt.Sprintf("%s (no registered definition)", names[i])
	}
	compareToolCards(t, probes, names)
}

// tool-execution.ts for a call whose tool has no definition (an unknown tool name): the Box with the tool name and the arguments, then the output text,
// in the lifecycle background. Rows are compared with pinned Pi byte for byte at three widths, pending, successful and failed, collapsed and expanded.
func TestUnregisteredToolCardsMatchPi(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	rows := make([]string, 30)
	for i := range rows {
		rows[i] = fmt.Sprintf("row %d", i+1)
	}
	type card struct {
		name   string
		args   map[string]any
		result *toolCardResult
	}
	var probes []toolCardProbe
	var names []string
	for _, c := range []card{
		{"pending", map[string]any{"alpha": 1, "beta": "two"}, nil},
		{"no arguments", map[string]any{}, &toolCardResult{Content: cardText("done")}},
		{"ok", map[string]any{"alpha": 1}, &toolCardResult{Content: cardText("fine")}},
		{"long output", map[string]any{"alpha": "x"}, &toolCardResult{Content: cardText(strings.Join(rows, "\n"))}},
		{"error", map[string]any{"alpha": true}, &toolCardResult{Content: cardText("it broke"), IsError: true}},
		{"wide and ansi", map[string]any{"text": "日本語 \x1b[31mred\x1b[0m"}, &toolCardResult{Content: cardText("日本語 \x1b[31mred\x1b[0m\ttab")}},
	} {
		for _, theme := range []string{"dark", "light"} {
			for _, width := range []int{80, 28, 5} {
				for _, expanded := range []bool{false, true} {
					probes = append(probes, toolCardProbe{Tool: "custom_tool", Args: c.args, Result: c.result, Expanded: expanded, Width: width, Theme: theme, CWD: cwd})
					names = append(names, fmt.Sprintf("custom_tool %s theme=%s width=%d expanded=%v", c.name, theme, width, expanded))
				}
			}
		}
	}
	compareToolCards(t, probes, names)
}
