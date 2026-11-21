//go:build !pig_strip_syntax_highlight

package codingagent

// pi: packages/coding-agent/src/modes/interactive/interactive-mode.ts getRegisteredToolDefinition

// pi: packages/coding-agent/src/core/tools/renderers/index.ts

import (
	"fmt"
	"slices"
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
