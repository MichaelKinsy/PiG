// Ports packages/coding-agent/src/core/compaction/branch-summarization.ts
// Branch summarization for tree navigation.
//
// When navigating to a different point in the session tree, this generates
// a summary of the branch being left so context isn't lost.
//
// Mirrors upstream:
//
//	.upstream/current/packages/coding-agent/src/core/compaction/branch-summarization.ts
package compaction

import (
	"context"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/sessionentry"
)

// ─── Types ────────────────────────────────────────────────────────────────────

// BranchSummarySettings controls the token budget for branch summarization.
// Mirrors upstream BranchSummarySettings (branch-summarization.ts).
type BranchSummarySettings struct {
	ReserveTokens int
}

// BranchSummaryDetails is stored in BranchSummaryEntry.details for cumulative
// file tracking across nested branch summaries.
// Mirrors upstream BranchSummaryDetails (branch-summarization.ts:43).
type BranchSummaryDetails struct {
	ReadFiles     []string `json:"readFiles"`
	ModifiedFiles []string `json:"modifiedFiles"`
}

// BranchSummaryResult is the return type of GenerateBranchSummary.
// Mirrors upstream BranchSummaryResult (branch-summarization.ts:31).
type BranchSummaryResult struct {
	Summary       string
	ReadFiles     []string
	ModifiedFiles []string
	Aborted       bool
	Error         string
	// Usage is the summarization LLM call usage, if reported.
	Usage *ai.Usage
}

// BranchPreparation is the output of PrepareBranchEntries.
// Mirrors upstream BranchPreparation (branch-summarization.ts:55).
type BranchPreparation struct {
	// Messages extracted for summarization, in chronological order.
	Messages []agent.AgentMessage
	// FileOps extracted from tool calls and prior branch_summary details.
	FileOps FileOperations
	// TotalTokens is the estimated token count of Messages.
	TotalTokens int
}

// CollectEntriesResult is the output of CollectEntriesForBranchSummary.
// Mirrors upstream CollectEntriesResult (branch-summarization.ts:62).
type CollectEntriesResult struct {
	// Entries to summarize, in chronological order.
	Entries []codingagent.SessionEntry
	// CommonAncestorID is the deepest node on both the old and target paths,
	// or "" if none found.
	CommonAncestorID string
}

// GenerateBranchSummaryOptions controls how GenerateBranchSummary runs.
// Mirrors upstream GenerateBranchSummaryOptions (branch-summarization.ts:69).
type GenerateBranchSummaryOptions struct {
	// Model to use for summarization.
	Model *ai.Model
	// APIKey, Headers and Env are the request auth for the model (branch-summarization.ts:71-75).
	APIKey  string
	Headers map[string]string
	Env     map[string]string
	// Completer handles the actual LLM call.
	Completer SimpleCompleter
	// StreamFn overrides Completer when supplied, as in compaction.
	StreamFn StreamFn
	// CustomInstructions are appended to (or replace) the default prompt.
	CustomInstructions string
	// ReplaceInstructions, when true, replaces the default prompt entirely.
	ReplaceInstructions bool
	// ReserveTokens are subtracted from the model context window for the
	// prompt + response budget. Default 16384.
	ReserveTokens int
	// Retry, when non-nil, retries transient summarization errors with bounded
	// exponential backoff (upstream retry?: RetryPolicy).
	Retry *ai.RetryPolicy
	// Callbacks report retries, e.g. to TUI retry indicators (upstream callbacks?: RetryCallbacks).
	Callbacks ai.RetryCallbacks
}

// ─── ReadonlySession ──────────────────────────────────────────────────────────

// ReadonlySession provides read-only access needed for branch entry collection.
// Mirrors upstream ReadonlySessionManager used in branch-summarization.ts.
//
// *codingagent.Session satisfies this interface via its GetBranch and GetEntry methods.
type ReadonlySession interface {
	// GetBranch returns the root-first path ending at the first argument.
	// Returns nil if leafID is not found.
	GetBranch(fromID ...string) []codingagent.SessionEntry
	// EntryByID looks up a single entry by ID.
	// Returns (zero, false) if not found.
	GetEntry(id string) (codingagent.SessionEntry, bool)
}

// ─── Prompts ──────────────────────────────────────────────────────────────────

// BRANCH_SUMMARY_PREAMBLE is prepended to every generated branch summary so
// the model understands the provenance of the text.
// Verbatim from upstream branch-summarization.ts.
const BRANCH_SUMMARY_PREAMBLE = "The user explored a different conversation branch before returning here.\nSummary of that exploration:\n\n"

// BRANCH_SUMMARY_PROMPT is the instruction prompt for branch summarization.
// Verbatim from upstream branch-summarization.ts.
const BRANCH_SUMMARY_PROMPT = `Create a structured summary of this conversation branch for context when returning later.

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

// ─── Entry Collection ─────────────────────────────────────────────────────────

// CollectEntriesForBranchSummary finds the entries that should be summarized
// when navigating from oldLeafID to a different position in the tree.
//
// It walks from oldLeafID back to the deepest common ancestor with targetID
// and returns the collected entries in chronological order.
//
// If oldLeafID is empty, returns an empty result (nothing to summarize).
//
// Mirrors upstream collectEntriesForBranchSummary (branch-summarization.ts:104).
func CollectEntriesForBranchSummary(session ReadonlySession, oldLeafID, targetID string) CollectEntriesResult {
	if oldLeafID == "" {
		return CollectEntriesResult{}
	}

	// Build a set of IDs on the old path for O(1) ancestor lookup.
	oldPath := session.GetBranch(oldLeafID)
	oldPathIDs := make(map[string]struct{}, len(oldPath))
	for _, e := range oldPath {
		oldPathIDs[e.Base().ID] = struct{}{}
	}

	// Find deepest common ancestor: iterate targetPath root→leaf in reverse.
	targetPath := session.GetBranch(targetID)
	var commonAncestorID string
	for _, t := range slices.Backward(targetPath) {
		if _, ok := oldPathIDs[t.Base().ID]; ok {
			commonAncestorID = t.Base().ID
			break
		}
	}

	// Walk from oldLeafID back to commonAncestor (exclusive), collecting entries.
	var entries []codingagent.SessionEntry
	current := oldLeafID
	for sessionentry.Truthy(current) && current != commonAncestorID {
		e, ok := session.GetEntry(current)
		if !ok {
			break
		}
		entries = append(entries, e)
		if e.Base().ParentID == nil {
			break
		}
		current = *e.Base().ParentID
	}

	// Reverse to chronological order (root first, leaf last).
	for i, j := 0, len(entries)-1; i < j; i, j = i+1, j-1 {
		entries[i], entries[j] = entries[j], entries[i]
	}

	return CollectEntriesResult{
		Entries:          entries,
		CommonAncestorID: commonAncestorID,
	}
}

// ─── Entry → AgentMessage conversion ─────────────────────────────────────────

// getMessageFromEntryForBranch extracts the context message a branch entry
// contributes to its summary (branch-summarization.ts getMessageFromEntry).
// Tool results are skipped because the assistant tool call already carries
// their position; compaction entries contribute their summary.
func getMessageFromEntryForBranch(e codingagent.SessionEntry) (agent.AgentMessage, bool) {
	switch e.Base().Type {
	case "message", "bash_execution", "custom_message":
		messages := codingagent.SessionEntryToContextMessages(e)
		if len(messages) != 1 || messages[0].ToolResult != nil {
			return agent.AgentMessage{}, false
		}
		return messages[0], true
	case "branch_summary":
		var bs codingagent.BranchSummaryEntry
		if err := json.Unmarshal(e.Raw(), &bs); err != nil {
			return agent.AgentMessage{}, false
		}
		return agent.AgentMessage{Custom: map[string]any{
			"role":      agent.RoleBranchSummary,
			"summary":   bs.Summary,
			"fromId":    bs.FromID,
			"timestamp": entryTimestampMs(bs.Timestamp),
		}}, true
	case "compaction":
		var ce codingagent.CompactionEntry
		if err := json.Unmarshal(e.Raw(), &ce); err != nil {
			return agent.AgentMessage{}, false
		}
		return agent.AgentMessage{Custom: map[string]any{
			"role":         agent.RoleCompactionSummary,
			"summary":      ce.Summary,
			"tokensBefore": ce.TokensBefore,
			"timestamp":    entryTimestampMs(ce.Timestamp),
		}}, true
	}
	return agent.AgentMessage{}, false
}

// entryTimestampMs is new Date(timestamp).getTime() (messages.ts:105-136). A timestamp Date cannot read is NaN there, which JSON.stringify writes as
// null; the integer here is 0.
func entryTimestampMs(timestamp string) int64 {
	if ms := ai.DateParse(timestamp); !math.IsNaN(ms) {
		//portlint:allow numbers ai.DateParse is NaN (handled here) or an integer millisecond count within Date's ±8.64e15 range, which int64 holds exactly
		return int64(ms)
	}
	return 0
}

// ─── Branch Entry Preparation ─────────────────────────────────────────────────

// PrepareBranchEntries builds the message list and file-op summary for a
// slice of branch entries, respecting the given token budget.
//
// Two-pass algorithm:
//  1. Collect file ops from ALL entries (even if they exceed the budget).
//     File ops from prior branch_summary entries seed the cumulative tracker.
//  2. Walk newest-to-oldest, adding messages until the budget is hit.
//     Summary entries (compaction, branch_summary) get priority fit when
//     totalTokens < 90% of the budget.
//
// tokenBudget == 0 means no limit.
//
// Mirrors upstream prepareBranchEntries (branch-summarization.ts:196).
//
// Entries may be lazy (sessionentry.LazyEntry): the walk from newest to oldest reads and decodes them in growing runs as it reaches them, so
// a long abandoned branch costs the entries within the budget, and a record that changed on disk is the error.
func PrepareBranchEntries(entries []codingagent.SessionEntry, tokenBudget int) (BranchPreparation, error) {
	fileOps := NewFileOps()

	// First pass: seed file ops from prior branch_summary details and all
	// assistant tool calls. Only process pi-generated summaries (fromHook == false).
	for _, entry := range entries {
		if entry.Base().Type == "branch_summary" {
			var raw struct {
				FromHook bool                  `json:"fromHook"`
				Details  *BranchSummaryDetails `json:"details"`
			}
			if err := json.Unmarshal(entry.Raw(), &raw); err == nil && !raw.FromHook && raw.Details != nil {
				for _, f := range raw.Details.ReadFiles {
					fileOps.Read[f] = struct{}{}
				}
				for _, f := range raw.Details.ModifiedFiles {
					// Modified files go into Edited for proper ComputeFileLists deduplication.
					fileOps.Edited[f] = struct{}{}
				}
			}
		}
	}

	// Second pass: walk from newest to oldest, adding messages within the budget (collected newest first and reversed once; a prepend per message copied the list each time).
	var messages []agent.AgentMessage
	totalTokens := 0

	// window holds entries[lo:] decoded from lo up to the previous window, in runs that double up to 1024 entries.
	var window []codingagent.SessionEntry
	lo, run := len(entries), 32
	for i := range slices.Backward(entries) {
		if i < lo {
			hi := lo
			lo = max(0, lo-run)
			run = min(run*2, 1024)
			var err error
			if window, err = sessionentry.MaterializeAll(entries[lo:hi]); err != nil {
				return BranchPreparation{}, err
			}
		}
		entry := window[i-lo]

		msg, ok := getMessageFromEntryForBranch(entry)
		if !ok {
			continue
		}

		// Extract file ops from assistant messages (tool calls).
		ExtractFileOpsFromMessage(msg, &fileOps)

		tokens := EstimateTokens(msg)

		if tokenBudget > 0 && totalTokens+tokens > tokenBudget {
			// Summary entries get priority: include even if slightly over budget,
			// as long as we're under 90% utilisation.
			if entry.Base().Type == "compaction" || entry.Base().Type == "branch_summary" {
				if float64(totalTokens) < float64(tokenBudget)*0.9 {
					messages = append(messages, msg)
					totalTokens += tokens
				}
			}
			// Budget exhausted: stop adding messages.
			break
		}

		messages = append(messages, msg)
		totalTokens += tokens
	}

	// The walk collected newest first; the preparation is chronological.
	slices.Reverse(messages)
	return BranchPreparation{
		Messages:    messages,
		FileOps:     fileOps,
		TotalTokens: totalTokens,
	}, nil
}

// ─── Summary Generation ───────────────────────────────────────────────────────

// GenerateBranchSummary produces a structured summary of abandoned branch
// entries by calling the LLM via opts.Completer.
//
// Returns BranchSummaryResult{Aborted: true} when ctx is cancelled.
// Returns BranchSummaryResult{Error: "..."} with Pi's operation label and tool-call diagnostic on LLM failure.
//
// Mirrors upstream generateBranchSummary (branch-summarization.ts:250).
func GenerateBranchSummary(ctx context.Context, entries []codingagent.SessionEntry, opts GenerateBranchSummaryOptions) BranchSummaryResult {
	reserveTokens := opts.ReserveTokens
	if reserveTokens <= 0 {
		reserveTokens = 16384
	}

	contextWindow := 128000
	if opts.Model != nil && opts.Model.Capabilities.ContextWindow > 0 {
		contextWindow = opts.Model.Capabilities.ContextWindow
	}
	tokenBudget := contextWindow - reserveTokens

	// pig divergence (D167): pi sends one request for the newest entries that fit tokenBudget by the character estimate and reports the provider's context overflow when the real token count is higher. PiG retries with the newest half of what it sent until the provider accepts it or one message is left, whose overflow error is then reported.
	var refused error
	refusedCount := 0
	for {
		prep, err := PrepareBranchEntries(entries, tokenBudget)
		if err != nil {
			return BranchSummaryResult{Error: err.Error()}
		}

		// A smaller budget that selects nothing new cannot be accepted: the refusal stays the result.
		if refused != nil && (len(prep.Messages) == 0 || len(prep.Messages) >= refusedCount) {
			return BranchSummaryResult{Error: summarizationFailure("Branch summarization", refused).Error()}
		}

		if len(prep.Messages) == 0 {
			return BranchSummaryResult{Summary: "No content to summarize"}
		}

		raw, usage, err := summarizeBranchMessages(ctx, prep.Messages, opts)
		if err != nil {
			// Distinguish context cancellation (aborted) from other errors.
			if ctx.Err() != nil {
				return BranchSummaryResult{Aborted: true}
			}
			if len(prep.Messages) > 1 && ai.IsContextOverflow(ai.AssistantMessage{StopReason: ai.StopReasonError, ErrorMessage: err.Error()}, 0) {
				// The newest message always fits the next budget, so the halving ends at one message as the error path below expects; a zero budget would mean no limit.
				refused, refusedCount = err, len(prep.Messages)
				//portlint:allow numbers Go-only overflow retry (pig divergence (D167), no Pi counterpart); halving a non-negative token count by floor is the intended budget
				tokenBudget = max(prep.TotalTokens/2, EstimateTokens(prep.Messages[len(prep.Messages)-1]), 1)
				continue
			}
			return BranchSummaryResult{Error: summarizationFailure("Branch summarization", err).Error()}
		}

		// Build final summary with preamble and file ops.
		var sb strings.Builder
		sb.WriteString(BRANCH_SUMMARY_PREAMBLE)
		sb.WriteString(raw)

		readFiles, modifiedFiles := ComputeFileLists(prep.FileOps)
		sb.WriteString(FormatFileOperations(readFiles, modifiedFiles))

		return BranchSummaryResult{
			Summary:       sb.String(),
			ReadFiles:     readFiles,
			ModifiedFiles: modifiedFiles,
			Usage:         usage,
		}
	}
}

// summarizeBranchMessages sends one branch-summary request for messages (branch-summarization.ts generateBranchSummary).
func summarizeBranchMessages(ctx context.Context, messages []agent.AgentMessage, opts GenerateBranchSummaryOptions) (string, *ai.Usage, error) {
	// Serialize messages to text to prevent the model treating this as a
	// live conversation.
	wireMessages := convertToLlm(messages)
	conversationText := SerializeConversation(wireMessages)

	// Build the instruction prompt.
	var instructions string
	switch {
	case opts.ReplaceInstructions && opts.CustomInstructions != "":
		instructions = opts.CustomInstructions
	case opts.CustomInstructions != "":
		instructions = BRANCH_SUMMARY_PROMPT + "\n\nAdditional focus: " + opts.CustomInstructions
	default:
		instructions = BRANCH_SUMMARY_PROMPT
	}

	promptText := "<conversation>\n" + conversationText + "\n</conversation>\n\n" + instructions

	ts := time.Now().UnixMilli()
	req := []agent.AgentMessage{
		{
			User: &agent.UserMessage{
				Role: "user",
				Content: ai.UserContentBlocks{
					ai.TextContent{Text: promptText},
				},
				Timestamp: ts,
			},
		},
	}

	// No maxTokens: the summary uses the model's output limit (#9512, Pi 6fcb4867; branch-summarization.ts
	// generateBranchSummary).
	return completeSummarization(ctx, opts.Model, opts.Completer, opts.StreamFn, opts.Retry, opts.Callbacks, SummarizationSystemPrompt, req, ai.StreamOptions{APIKey: opts.APIKey, Headers: ai.ProviderHeadersFromStrings(opts.Headers), Env: opts.Env})
}
