package tools

import (
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
)

// TOOL-18: upstream's tool definitions set no executionMode, so every
// built-in runs in parallel batches.
func TestBuiltinToolsRunInParallel(t *testing.T) {
	for _, tool := range CreateAllTools(t.TempDir(), nil, "") {
		if mode := tool.ExecutionMode(); mode != agent.ToolModeParallel {
			t.Errorf("%s execution mode = %q, want parallel", tool.Name(), mode)
		}
	}
}
