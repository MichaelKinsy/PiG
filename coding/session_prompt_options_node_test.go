//go:build !pig_strip_node_extensions

package coding

import (
	"testing"
)

// Caller level: a Node extension over the production subprocess wire receives Pi's option content in before_agent_start. forceSystemPrompt reaches a later handler after an earlier one returns systemPrompt (runner.ts:1346-1348).
func TestSessionBeforeAgentStartReceivesPiOptionContent(t *testing.T) {
	for _, forced := range []string{"FORCED", ""} {
		t.Run("force="+forced, func(t *testing.T) {
			testSessionBeforeAgentStartOptionContent(t, forced)
		})
	}
}
