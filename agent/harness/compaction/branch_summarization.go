package compaction

// Ports packages/agent/src/harness/compaction/branch-summarization.ts.

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/agent/harness"
	"github.com/MichaelKinsy/PiG/agent/harness/session"
	"github.com/MichaelKinsy/PiG/ai"
)

// BranchSummaryResult is generated branch content ready for a durable summary entry.
type BranchSummaryResult struct {
	Summary       string    `json:"summary"`
	Usage         *ai.Usage `json:"usage,omitempty"`
	ReadFiles     []string  `json:"readFiles"`
	ModifiedFiles []string  `json:"modifiedFiles"`
}

// BranchSummaryDetails tracks the files mentioned in a branch summary.
type BranchSummaryDetails struct {
	ReadFiles     []string `json:"readFiles"`
	ModifiedFiles []string `json:"modifiedFiles"`
}

// BranchPreparation is the content selected for branch summarization.
type BranchPreparation struct {
	Messages    []agent.AgentMessage
	FileOps     FileOperations
	TotalTokens int
}

// CollectEntriesResult identifies the abandoned path and its deepest common ancestor.
type CollectEntriesResult struct {
	Entries          []session.Entry
	CommonAncestorID *string
}

// CollectEntriesForBranchSummary collects the abandoned path in chronological order.
func CollectEntriesForBranchSummary(ctx context.Context, branch session.Branch, opened session.Session, oldTipID *string, targetID string) (CollectEntriesResult, error) {
	result := CollectEntriesResult{Entries: []session.Entry{}}
	if oldTipID == nil || *oldTipID == "" {
		return result, nil
	}
	oldPath, err := branch.FindEntries(ctx, &session.BranchScan{Start: oldTipID})
	if err != nil {
		return result, err
	}
	oldIDs := make(map[string]bool, len(oldPath))
	for _, entry := range oldPath {
		oldIDs[entry.ID] = true
	}
	targetPath, err := branch.FindEntries(ctx, &session.BranchScan{Start: &targetID})
	if err != nil {
		return result, err
	}
	for _, entry := range targetPath {
		if oldIDs[entry.ID] {
			result.CommonAncestorID = new(entry.ID)
			break
		}
	}
	for current := oldTipID; current != nil && *current != "" && (result.CommonAncestorID == nil || *current != *result.CommonAncestorID); {
		entry, err := opened.GetEntry(ctx, *current)
		if err != nil {
			return result, err
		}
		if entry == nil {
			return result, fmt.Errorf("Corrupt session: entry %s not found", *current)
		}
		result.Entries = append(result.Entries, *entry)
		current = entry.ParentID
	}
	slices.Reverse(result.Entries)
	return result, nil
}

func branchEntryMessage(entry session.Entry) (agent.AgentMessage, bool) {
	switch entry.Type {
	case session.EntryTypeMessage:
		if entry.Message.ToolResult != nil {
			return agent.AgentMessage{}, false
		}
		return entry.Message, true
	case session.EntryTypeBranchSummary:
		return messageFromEntry(entry)
	case session.EntryTypeCompaction:
		return agent.AgentMessage{Custom: map[string]any{"role": "compactionSummary", "summary": entry.Summary, "tokensBefore": entry.TokensBefore, "timestamp": entry.Timestamp}}, true
	default:
		return agent.AgentMessage{}, false
	}
}

// PrepareBranchEntries selects the newest content within a token budget, preserving summary priority and cumulative file operations. Zero means no budget.
func PrepareBranchEntries(entries []session.Entry, tokenBudget int) BranchPreparation {
	prepared := BranchPreparation{Messages: []agent.AgentMessage{}, FileOps: CreateFileOps()}
	for _, entry := range entries {
		if entry.Type != session.EntryTypeBranchSummary || entry.Details == nil {
			continue
		}
		if details, ok := (*entry.Details).(map[string]any); ok {
			restoreFilePaths(details["readFiles"], prepared.FileOps.AddRead)
			restoreFilePaths(details["modifiedFiles"], prepared.FileOps.AddEdited)
		}
	}
	for _, entry := range slices.Backward(entries) {
		message, ok := branchEntryMessage(entry)
		if !ok {
			continue
		}
		ExtractFileOpsFromMessage(message, &prepared.FileOps)
		tokens := EstimateTokens(message)
		if tokenBudget > 0 && prepared.TotalTokens+tokens > tokenBudget {
			if (entry.Type == session.EntryTypeCompaction || entry.Type == session.EntryTypeBranchSummary) && float64(prepared.TotalTokens) < float64(tokenBudget)*0.9 {
				prepared.Messages = append(prepared.Messages, message)
				prepared.TotalTokens += tokens
			}
			break
		}
		prepared.Messages = append(prepared.Messages, message)
		prepared.TotalTokens += tokens
	}
	slices.Reverse(prepared.Messages)
	return prepared
}

const branchSummaryPreamble = "The user explored a different conversation branch before returning here.\nSummary of that exploration:\n\n"
const branchSummaryPrompt = `Create a structured summary of this conversation branch for context when returning later.

Use this EXACT format:

## Goal
[What was the user trying to accomplish in this branch?]

## Constraints & Preferences
- [Any constraints, preferences, or requirements mentioned]
- [Or "(none)" if none were mentioned]

## Progress
### Done
- [x] [Completed tasks/changes]

### In Progress
- [ ] [Work that was started but not finished]

### Blocked
- [Issues preventing progress, if any]

## Key Decisions
- **[Decision]**: [Brief rationale]

## Next Steps
1. [What should happen next to continue this work]

Keep each section concise. Preserve exact file paths, function names, and error messages.`

// PreparedBranchSummaryOptions control the summarization instructions.
type PreparedBranchSummaryOptions struct {
	CustomInstructions  string
	ReplaceInstructions bool
}

// GenerateBranchSummaryOptions bind a provider collection and retry policy to branch summarization.
type GenerateBranchSummaryOptions struct {
	PreparedBranchSummaryOptions
	Models        Models
	Model         *ai.Model
	ReserveTokens *int
	Retry         *ai.RetryPolicy
	Callbacks     ai.RetryCallbacks
}

// GenerateBranchSummary prepares abandoned entries and generates their summary through the provider collection.
func GenerateBranchSummary(ctx context.Context, entries []session.Entry, options GenerateBranchSummaryOptions) (BranchSummaryResult, error) {
	reserve := 16384
	if options.ReserveTokens != nil {
		reserve = *options.ReserveTokens
	}
	window := options.Model.Capabilities.ContextWindow
	if window == 0 {
		window = 128000
	}
	preparation := PrepareBranchEntries(entries, window-reserve)
	return GenerateBranchSummaryWithRequest(ctx, preparation, options.PreparedBranchSummaryOptions, func(ctx context.Context, request ai.Context, stream ai.StreamOptions) (*ai.AssistantMessage, error) {
		return CompleteSimpleWithRetries(ctx, options.Models, options.Model, request, stream, options.Retry, options.Callbacks)
	})
}

// GenerateBranchSummaryWithRequest awaits one caller-owned request and preserves coded response errors without swallowing request failures.
func GenerateBranchSummaryWithRequest(ctx context.Context, preparation BranchPreparation, options PreparedBranchSummaryOptions, request SummaryRequest) (BranchSummaryResult, error) {
	if len(preparation.Messages) == 0 {
		return BranchSummaryResult{Summary: "No content to summarize", ReadFiles: []string{}, ModifiedFiles: []string{}}, nil
	}
	instructions := branchSummaryPrompt
	if options.CustomInstructions != "" {
		if options.ReplaceInstructions {
			instructions = options.CustomInstructions
		} else {
			instructions += "\n\nAdditional focus: " + options.CustomInstructions
		}
	}
	prompt := "<conversation>\n" + SerializeConversation(harness.ConvertToLlm(preparation.Messages)) + "\n</conversation>\n\n" + instructions
	response, err := request(ctx, summaryContext(prompt), CreateSummaryRequestOptions(ai.StreamOptions{MaxTokens: 2048}))
	if err != nil {
		return BranchSummaryResult{}, err
	}
	if response == nil {
		return BranchSummaryResult{}, fmt.Errorf("summary request returned no assistant message")
	}
	if response.StopReason == ai.StopReasonAborted {
		message := response.ErrorMessage
		if message == "" {
			message = "Branch summary aborted"
		}
		return BranchSummaryResult{}, &harness.BranchSummaryError{Code: harness.BranchSummaryErrorAborted, Message: message}
	}
	if response.StopReason == ai.StopReasonError {
		message := response.ErrorMessage
		if message == "" {
			message = "Unknown error"
		}
		return BranchSummaryResult{}, &harness.BranchSummaryError{Code: harness.BranchSummaryErrorSummarizationFailed, Message: "Branch summary failed: " + message}
	}
	var text []string
	for _, block := range response.Content {
		if block, ok := block.(ai.TextContent); ok {
			text = append(text, block.Text)
		}
	}
	read, modified := ComputeFileLists(preparation.FileOps)
	return BranchSummaryResult{Summary: branchSummaryPreamble + strings.Join(text, "\n") + FormatFileOperations(read, modified), Usage: &response.Usage, ReadFiles: read, ModifiedFiles: modified}, nil
}
