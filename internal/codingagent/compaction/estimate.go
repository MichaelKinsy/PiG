package compaction

import (
	"slices"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// estimatedImageChars is Pi's per-image character estimate (ESTIMATED_IMAGE_CHARS).
const estimatedImageChars = 4800

// jsLength returns JavaScript's String.length for s: its UTF-16 code units.
func jsLength(s string) int {
	units := 0
	for len(s) > 0 {
		r, size := utf8.DecodeRuneInString(s)
		s = s[size:]
		if r > 0xFFFF {
			units += 2
		} else {
			units++
		}
	}
	return units
}

func ceilQuarter(chars int) int { return (chars + 3) / 4 }

// contentBlocksChars sums text lengths and a fixed estimate per image
// (compaction.ts estimateTextAndImageContentChars).
func contentBlocksChars[T any](blocks []T) int {
	chars := 0
	for _, block := range blocks {
		switch block := any(block).(type) {
		case ai.TextContent:
			chars += jsLength(block.Text)
		case ai.ImageContent:
			chars += estimatedImageChars
		}
	}
	return chars
}

// customContentChars sizes a decoded custom-message content value: a string or
// an array of content-block objects.
func customContentChars(content any) int {
	switch content := content.(type) {
	case string:
		return jsLength(content)
	case []any:
		chars := 0
		for _, block := range content {
			fields, _ := block.(map[string]any)
			switch fields["type"] {
			case "text":
				text, _ := fields["text"].(string)
				chars += jsLength(text)
			case "image":
				chars += estimatedImageChars
			}
		}
		return chars
	}
	return 0
}

// estimateSystemTokens sizes a system message with its sections and added
// tool declarations.
func estimateSystemTokens(system ai.SystemMessage) int {
	chars := 0
	switch content := system.Content.(type) {
	case ai.SystemText:
		chars += jsLength(string(content))
	case ai.SystemTextBlocks:
		chars += contentBlocksChars(content)
	}
	for _, section := range system.Sections {
		if section.Value != nil {
			chars += jsLength(*section.Value)
		}
	}
	if len(system.ToolsAdded) > 0 {
		chars += ai.JSONStringifyLength(system.ToolsAdded)
	}
	return ceilQuarter(chars)
}

// EstimateTokens estimates one context message with the chars/4 heuristic,
// counting JavaScript string lengths (compaction.ts estimateTokens). Unknown
// roles count as zero.
func EstimateTokens(message agent.AgentMessage) int {
	switch {
	case message.System != nil:
		return estimateSystemTokens(*message.System)
	case message.User != nil:
		return ceilQuarter(contentBlocksChars(message.User.Content))
	case message.Assistant != nil:
		chars := 0
		for _, block := range message.Assistant.Content {
			switch block := block.(type) {
			case ai.TextContent:
				chars += jsLength(block.Text)
			case ai.ThinkingContent:
				chars += jsLength(block.Thinking)
			case ai.ToolCall:
				chars += jsLength(block.Name) + ai.JSONStringifyLength(block.Arguments)
			}
		}
		return ceilQuarter(chars)
	case message.ToolResult != nil:
		return ceilQuarter(contentBlocksChars(message.ToolResult.Content))
	case message.Custom != nil:
		switch role, _ := message.Custom["role"].(string); role {
		case agent.RoleCustom:
			return ceilQuarter(customContentChars(message.Custom["content"]))
		case agent.RoleBashExecution:
			command, _ := message.Custom["command"].(string)
			output, _ := message.Custom["output"].(string)
			return ceilQuarter(jsLength(command) + jsLength(output))
		case agent.RoleBranchSummary, agent.RoleCompactionSummary:
			summary, _ := message.Custom["summary"].(string)
			return ceilQuarter(jsLength(summary))
		}
	}
	return 0
}

// getAssistantUsage returns an assistant message's usage when it is valid:
// not aborted or errored, and with a nonzero context size.
func getAssistantUsage(message agent.AgentMessage) *ai.Usage {
	assistant := message.Assistant
	if assistant == nil || assistant.Usage == nil || assistant.StopReason == ai.StopReasonAborted || assistant.StopReason == ai.StopReasonError {
		return nil
	}
	if agent.CalculateContextTokens(*assistant.Usage) <= 0 {
		return nil
	}
	return assistant.Usage
}

// EstimateContextTokens estimates context size from the last valid assistant
// usage plus an estimate of the messages after it, or from the messages alone
// when no usage exists (compaction.ts estimateContextTokens). LastUsageIndex
// is -1 for Pi's null.
func EstimateContextTokens(messages []agent.AgentMessage) agent.ContextUsageEstimate {
	for i, message := range slices.Backward(messages) {
		usage := getAssistantUsage(message)
		if usage == nil {
			continue
		}
		usageTokens := agent.CalculateContextTokens(*usage)
		trailing := estimateMessagesTokens(messages[i+1:])
		return agent.ContextUsageEstimate{
			Tokens:         usageTokens + trailing,
			UsageTokens:    usageTokens,
			TrailingTokens: trailing,
			LastUsageIndex: i,
		}
	}
	estimated := estimateMessagesTokens(messages)
	return agent.ContextUsageEstimate{Tokens: estimated, TrailingTokens: estimated, LastUsageIndex: -1}
}

// EstimateProjectedContextTokens estimates a projected context without
// trusting usage captured before a later context edit or compaction on the
// branch (compaction.ts estimateProjectedContextTokens). Without trusted
// usage, it counts the replayed current system state once plus every
// non-system projected message.
func EstimateProjectedContextTokens(projection codingagent.SessionProjection, branchEntries []codingagent.SessionEntry) agent.ContextUsageEstimate {
	estimate := EstimateContextTokens(projection.Messages)
	if estimate.LastUsageIndex >= 0 {
		usageEntryID := ""
		projectedIndex := 0
		for _, entry := range projection.Entries {
			next := projectedIndex + len(entry.Messages)
			if estimate.LastUsageIndex < next {
				usageEntryID = entry.SourceEntry.Base.ID
				break
			}
			projectedIndex = next
		}
		usageEntryIndex := -1
		if usageEntryID != "" {
			usageEntryIndex = slices.IndexFunc(branchEntries, func(entry codingagent.SessionEntry) bool {
				return entry.Base.ID == usageEntryID
			})
		}
		latestInvalidating := -1
		for i, entry := range slices.Backward(branchEntries) {
			if entry.Base.Type == "context_edit" || entry.Base.Type == "compaction" {
				latestInvalidating = i
				break
			}
		}
		if usageEntryIndex > latestInvalidating {
			return estimate
		}
	}
	tokens := 0
	if current := codingagent.CurrentSystemMessage(projection.Messages); current != nil {
		tokens = estimateSystemTokens(*current)
	}
	for _, message := range projection.Messages {
		if message.Role() != "system" {
			tokens += EstimateTokens(message)
		}
	}
	return agent.ContextUsageEstimate{Tokens: tokens, TrailingTokens: tokens, LastUsageIndex: -1}
}
