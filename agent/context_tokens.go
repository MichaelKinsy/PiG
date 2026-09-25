package agent

// Context-token estimation. These pure helpers size an agent's message context
// so callers (auto-compaction threshold checks, pre-prompt checks, the
// compaction cut-point search) can decide whether to compact. They live in
// agent because they operate purely on AgentMessage / ai.Usage and are
// needed by both the codingagent interactive loop and the compaction package;
// the latter imports codingagent, so they cannot live there without an import
// cycle. Mirrors upstream packages/agent/src/harness/compaction/compaction.ts,
// which keeps the same helpers in the agent harness.

import (
	"bytes"
	"encoding/json"
	"slices"

	"github.com/MichaelKinsy/PiG/ai"
)

// ContextUsageEstimate is the result of EstimateContextTokens.
// Mirrors upstream ContextUsageEstimate (compaction.ts:177).
type ContextUsageEstimate struct {
	Tokens         int
	UsageTokens    int
	TrailingTokens int
	LastUsageIndex int // -1 if not found
}

// CalculateContextTokens computes context tokens from usage stats.
// Mirrors upstream calculateContextTokens (compaction.ts:135): prefer the
// provider-reported total, else sum the components.
func CalculateContextTokens(usage ai.Usage) int {
	if usage.TotalTokens != 0 {
		return usage.TotalTokens
	}
	return usage.Input + usage.Output + usage.CacheRead + usage.CacheWrite
}

// EstimateContextTokens estimates context token usage from a message slice.
// Uses the last assistant usage that is not aborted, errored, or all-zero
// (upstream getAssistantUsage), plus a chars/4 estimate of the messages
// trailing it; falls back to a full estimate when no usage is present.
// Mirrors upstream estimateContextTokens (compaction.ts:186).
func EstimateContextTokens(messages []AgentMessage) ContextUsageEstimate {
	lastUsageIdx := -1
	var lastUsage *ai.Usage
	for i, m := range slices.Backward(messages) {
		if m.Assistant == nil {
			continue
		}
		if m.Assistant.StopReason == "aborted" || m.Assistant.StopReason == "error" {
			continue
		}
		if m.Assistant.Usage != nil && CalculateContextTokens(*m.Assistant.Usage) > 0 {
			lastUsageIdx = i
			lastUsage = m.Assistant.Usage
			break
		}
	}

	if lastUsageIdx < 0 {
		estimated := 0
		for _, m := range messages {
			estimated += EstimateTokens(m)
		}
		return ContextUsageEstimate{
			Tokens:         estimated,
			TrailingTokens: estimated,
			LastUsageIndex: -1,
		}
	}

	usageTokens := CalculateContextTokens(*lastUsage)
	trailing := 0
	for i := lastUsageIdx + 1; i < len(messages); i++ {
		trailing += EstimateTokens(messages[i])
	}
	return ContextUsageEstimate{
		Tokens:         usageTokens + trailing,
		UsageTokens:    usageTokens,
		TrailingTokens: trailing,
		LastUsageIndex: lastUsageIdx,
	}
}

// EstimateTokens estimates the token count for a single message using a
// chars/4 heuristic covering all roles.
// Mirrors upstream estimateTokens (compaction.ts:232).
func EstimateTokens(msg AgentMessage) int {
	chars := 0
	switch {
	case msg.User != nil:
		for _, blk := range msg.User.Content {
			if t, ok := blk.(ai.TextContent); ok {
				chars += len(t.Text)
			}
		}
	case msg.Assistant != nil:
		for _, blk := range msg.Assistant.Content {
			switch b := blk.(type) {
			case ai.TextContent:
				chars += len(b.Text)
			case ai.ThinkingContent:
				chars += len(b.Thinking)
			case ai.ToolCall:
				chars += len(b.Name)
				// Match upstream JSON.stringify length without HTML escaping.
				if b.Arguments != nil {
					var buf bytes.Buffer
					enc := json.NewEncoder(&buf)
					enc.SetEscapeHTML(false)
					_ = enc.Encode(b.Arguments)
					chars += buf.Len()
				}
			}
		}
	case msg.ToolResult != nil:
		for _, block := range msg.ToolResult.Content {
			if text, ok := block.(ai.TextContent); ok {
				chars += len(text.Text)
			}
		}
	case msg.Custom != nil:
		role, _ := msg.Custom["role"].(string)
		switch role {
		case RoleBashExecution:
			cmd, _ := msg.Custom["command"].(string)
			out, _ := msg.Custom["output"].(string)
			chars = len(cmd) + len(out)
		case RoleBranchSummary, RoleCompactionSummary:
			sum, _ := msg.Custom["summary"].(string)
			chars = len(sum)
		case RoleCustom:
			if content, ok := msg.Custom["content"].(string); ok {
				chars = len(content)
			}
		default:
			if content, ok := msg.Custom["content"].(string); ok {
				chars = len(content)
			}
		}
	}
	if chars == 0 {
		return 0
	}
	return (chars + 3) / 4 // ceil(chars/4)
}
