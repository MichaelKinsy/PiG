package compaction

// Ports packages/coding-agent/test/compaction.test.ts "does not count !! bash output that is excluded from the LLM context" (Pi e73029b8e).

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

func TestEstimateContextTokens_ExcludesDoubleBangBashOutput(t *testing.T) {
	makeBash := func(exclude bool) agent.AgentMessage {
		return agent.AgentMessage{
			Custom: map[string]any{
				"role":               agent.RoleBashExecution,
				"command":            "git diff",
				"output":             strings.Repeat("x", 800),
				"exitCode":           0,
				"cancelled":          false,
				"truncated":          false,
				"excludeFromContext": exclude,
			},
		}
	}
	anchor := []agent.AgentMessage{
		agent.AgentMessage{User: &agent.UserMessage{Content: ai.UserText("Hello")}},
		agent.AgentMessage{Assistant: &agent.AssistantMessage{
			Content: []ai.AssistantContentBlock{ai.TextContent{Text: "Hi"}},
			Usage:   &ai.Usage{Input: 100, Output: 50, TotalTokens: 150},
		}},
	}

	if got := EstimateContextTokens(append(append([]agent.AgentMessage(nil), anchor...), makeBash(true))).TrailingTokens; got != 0 {
		t.Fatalf("excluded bash trailingTokens = %d, want 0", got)
	}
	if got := EstimateContextTokens(append(append([]agent.AgentMessage(nil), anchor...), makeBash(false))).TrailingTokens; got != 202 {
		t.Fatalf("included bash trailingTokens = %d, want 202", got)
	}
}
