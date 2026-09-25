// Package compaction: core compaction logic.
//
// Mirrors upstream:
//
//	.upstream/current/packages/coding-agent/src/core/compaction/compaction.ts
//
// All functions are pure except Compact (one LLM call via SimpleCompleter).
package compaction

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// ─── Types ────────────────────────────────────────────────────────────────────

// CompactionDetails is stored in a CompactionEntry.Details for file tracking
// across compactions.
// Mirrors upstream CompactionDetails (compaction.ts:36).
type CompactionDetails struct {
	ReadFiles     []string `json:"readFiles"`
	ModifiedFiles []string `json:"modifiedFiles"`
}

// CompactionSettings controls when and how compaction runs.
// Mirrors upstream CompactionSettings (compaction.ts:115).
type CompactionSettings struct {
	Enabled          bool `json:"enabled"`
	ReserveTokens    int  `json:"reserveTokens"`    // tokens reserved for response; default 16384
	KeepRecentTokens int  `json:"keepRecentTokens"` // tokens to keep from recent history; default 20000
}

// DefaultCompactionSettings mirrors upstream DEFAULT_COMPACTION_SETTINGS.
var DefaultCompactionSettings = CompactionSettings{
	Enabled:          true,
	ReserveTokens:    16384,
	KeepRecentTokens: 20000,
}

// CutPointResult from FindCutPoint.
// Mirrors upstream CutPointResult (compaction.ts:383).
type CutPointResult struct {
	// FirstKeptEntryIndex is the index in entries[] of the first entry to keep.
	FirstKeptEntryIndex int
	// TurnStartIndex is the index of the user/bash message starting the split
	// turn, or -1 if not splitting.
	TurnStartIndex int
	// IsSplitTurn is true when the cut lands in the middle of a turn.
	IsSplitTurn bool
}

// CompactionPreparation is the output of PrepareCompaction.
// Mirrors upstream CompactionPreparation (compaction.ts:600).
//
// Extensions receive it in session_before_compact, so the JSON keys are
// upstream's field names.
type CompactionPreparation struct {
	FirstKeptEntryID    string               `json:"firstKeptEntryId"`
	MessagesToSummarize []agent.AgentMessage `json:"messagesToSummarize"`
	TurnPrefixMessages  []agent.AgentMessage `json:"turnPrefixMessages"`
	IsSplitTurn         bool                 `json:"isSplitTurn"`
	TokensBefore        int                  `json:"tokensBefore"`
	PreviousSummary     string               `json:"previousSummary,omitempty"`
	FileOps             FileOperations       `json:"fileOps"`
	Settings            CompactionSettings   `json:"settings"`
}

// CompactionResult is the output of Compact.
// Mirrors upstream CompactionResult (compaction.ts:100).
type CompactionResult struct {
	Summary          string
	FirstKeptEntryID string
	TokensBefore     int
	Details          CompactionDetails
	// Usage is the combined usage of the summarization LLM call(s), if the
	// completer reported it. Mirrors upstream CompactionResult.usage.
	Usage *ai.Usage
}

// combineUsage sums two usage snapshots for the split-turn case (history +
// turn-prefix summaries run as two calls). Mirrors upstream combineUsage.
func combineUsage(a, b *ai.Usage) *ai.Usage {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	optionalSum := func(first, second *int) *int {
		if first == nil && second == nil {
			return nil
		}
		value := 0
		if first != nil {
			value += *first
		}
		if second != nil {
			value += *second
		}
		return &value
	}
	return &ai.Usage{
		Input:        a.Input + b.Input,
		Output:       a.Output + b.Output,
		Reasoning:    optionalSum(a.Reasoning, b.Reasoning),
		CacheRead:    a.CacheRead + b.CacheRead,
		CacheWrite:   a.CacheWrite + b.CacheWrite,
		CacheWrite1h: optionalSum(a.CacheWrite1h, b.CacheWrite1h),
		TotalTokens:  a.TotalTokens + b.TotalTokens,
		Cost: ai.UsageCost{
			Input: a.Cost.Input + b.Cost.Input, Output: a.Cost.Output + b.Cost.Output,
			CacheRead: a.Cost.CacheRead + b.Cost.CacheRead, CacheWrite: a.Cost.CacheWrite + b.Cost.CacheWrite,
			Total: a.Cost.Total + b.Cost.Total,
		},
	}
}

// SimpleCompleter is a minimal interface for LLM calls used by Compact.
// Allows test injection without live LLM calls. options are the request
// options completeSummarization built (upstream SimpleStreamOptions).
type SimpleCompleter interface {
	CompleteSimple(ctx context.Context, model *ai.Model, systemPrompt string, messages []agent.AgentMessage, options ai.StreamOptions) (string, *ai.Usage, error)
}

// StreamFn is an optional summarization path used instead of CompleteSimple.
// Mirrors upstream's streamFn forwarding for compaction requests.
type StreamFn func(ctx context.Context, model *ai.Model, systemPrompt string, messages []agent.AgentMessage, options ai.StreamOptions) (string, *ai.Usage, error)

// ─── Summarization Prompts ────────────────────────────────────────────────────

// SUMMARIZATION_PROMPT is the prompt for initial context summarization.
// Verbatim from upstream compaction.ts:454.
const SUMMARIZATION_PROMPT = `The messages above are a conversation to summarize. Create a structured context checkpoint summary that another LLM will use to continue the work.

Use this EXACT format:

## Goal
[What is the user trying to accomplish? Can be multiple items if the session covers different tasks.]

## Constraints & Preferences
- [Any constraints, preferences, or requirements mentioned by user]
- [Or "(none)" if none were mentioned]

## Progress
### Done
- [x] [Completed tasks/changes]

### In Progress
- [ ] [Current work]

### Blocked
- [Issues preventing progress, if any]

## Key Decisions
- **[Decision]**: [Brief rationale]

## Next Steps
1. [Ordered list of what should happen next]

## Critical Context
- [Any data, examples, or references needed to continue]
- [Or "(none)" if not applicable]

Keep each section concise. Preserve exact file paths, function names, and error messages.`

// UPDATE_SUMMARIZATION_PROMPT is the prompt used when updating an existing summary.
// Verbatim from upstream compaction.ts:487.
const UPDATE_SUMMARIZATION_PROMPT = `The messages above are NEW conversation messages to incorporate into the existing summary provided in <previous-summary> tags.

Update the existing structured summary with new information. RULES:
- PRESERVE all existing information from the previous summary
- ADD new progress, decisions, and context from the new messages
- UPDATE the Progress section: move items from "In Progress" to "Done" when completed
- UPDATE "Next Steps" based on what was accomplished
- PRESERVE exact file paths, function names, and error messages
- If something is no longer relevant, you may remove it

Use this EXACT format:

## Goal
[Preserve existing goals, add new ones if the task expanded]

## Constraints & Preferences
- [Preserve existing, add new ones discovered]

## Progress
### Done
- [x] [Include previously done items AND newly completed items]

### In Progress
- [ ] [Current work - update based on progress]

### Blocked
- [Current blockers - remove if resolved]

## Key Decisions
- **[Decision]**: [Brief rationale] (preserve all previous, add new)

## Next Steps
1. [Update based on current state]

## Critical Context
- [Preserve important context, add new if needed]

Keep each section concise. Preserve exact file paths, function names, and error messages.`

// turnPrefixSummarizationPrompt instructs the summary of a split user-message
// span's prefix. Byte-identical to upstream TURN_PREFIX_SUMMARIZATION_PROMPT.
const turnPrefixSummarizationPrompt = `The messages above are earlier context from an ongoing conversation. Later messages are stored separately and do not need to be reconstructed.

Create a concise checkpoint of the user's request and the progress shown above. This checkpoint will be placed before the later messages so the conversation can continue with the necessary context.

## Original Request
[What did the user ask for?]

## Progress So Far
- [Key decisions and work completed in these messages]

## Context Needed to Continue
- [Information from these messages needed to understand the later work]

Only summarize information explicitly present above. Do not infer or recreate later messages.`

// ─── Token Calculation ────────────────────────────────────────────────────────

// GetLastAssistantUsage returns the usage of the last assistant message with
// valid usage in entries (compaction.ts getLastAssistantUsage).
func GetLastAssistantUsage(entries []codingagent.SessionEntry) *ai.Usage {
	for _, entry := range slices.Backward(entries) {
		if entry.Base.Type != "message" {
			continue
		}
		message, ok := entry.AsMessage()
		if !ok {
			continue
		}
		if usage := getAssistantUsage(message.Message); usage != nil {
			return usage
		}
	}
	return nil
}

// ShouldCompact reports whether compaction should trigger.
// Mirrors upstream shouldCompact (compaction.ts).
func ShouldCompact(contextTokens, contextWindow int, s CompactionSettings) bool {
	if !s.Enabled {
		return false
	}
	return contextTokens > contextWindow-s.ReserveTokens
}

// ─── Cut Point Detection ──────────────────────────────────────────────────────

// isCutPointMessage reports whether a context message may start the kept
// suffix (compaction.ts isCutPointMessage). Tool results never can: they must
// follow their tool call.
func isCutPointMessage(message agent.AgentMessage) bool {
	switch message.Role() {
	case agent.RoleUser, agent.RoleAssistant, agent.RoleBashExecution, agent.RoleCustom,
		agent.RoleBranchSummary, agent.RoleCompactionSummary:
		return true
	}
	return false
}

// isTurnStartMessage reports whether a context message starts a user-message
// span (compaction.ts isTurnStartMessage).
func isTurnStartMessage(message agent.AgentMessage) bool {
	switch message.Role() {
	case agent.RoleUser, agent.RoleBashExecution, agent.RoleCustom,
		agent.RoleBranchSummary, agent.RoleCompactionSummary:
		return true
	}
	return false
}

func isTurnStartEntry(entry codingagent.SessionEntry) bool {
	if entry.Base.Type == "compaction" {
		return false
	}
	return slices.ContainsFunc(codingagent.SessionEntryToContextMessages(entry), isTurnStartMessage)
}

// findValidCutPoints returns the indices of context-visible user-like or
// assistant entries (compaction.ts findValidCutPoints).
func findValidCutPoints(entries []codingagent.SessionEntry, startIdx, endIdx int) []int {
	var points []int
	for i := startIdx; i < endIdx; i++ {
		if entries[i].Base.Type == "compaction" {
			continue
		}
		if slices.ContainsFunc(codingagent.SessionEntryToContextMessages(entries[i]), isCutPointMessage) {
			points = append(points, i)
		}
	}
	return points
}

// FindTurnStartIndex returns the index of the context-visible user-role entry
// that starts the span containing entryIndex, or -1 when none exists at or
// after startIndex (compaction.ts findTurnStartIndex).
func FindTurnStartIndex(entries []codingagent.SessionEntry, entryIndex, startIndex int) int {
	for i := entryIndex; i >= startIndex; i-- {
		if isTurnStartEntry(entries[i]) {
			return i
		}
	}
	return -1
}

func estimateMessagesTokens(messages []agent.AgentMessage) int {
	tokens := 0
	for _, message := range messages {
		tokens += EstimateTokens(message)
	}
	return tokens
}

// FindCutPoint finds the raw-entry cut point that keeps approximately
// keepRecentTokens of recent context between startIndex and endIndex
// (exclusive) (compaction.ts findCutPoint).
func FindCutPoint(entries []codingagent.SessionEntry, startIndex, endIndex, keepRecentTokens int) CutPointResult {
	cutPoints := findValidCutPoints(entries, startIndex, endIndex)
	if len(cutPoints) == 0 {
		return CutPointResult{FirstKeptEntryIndex: startIndex, TurnStartIndex: -1}
	}
	accumulated := 0
	cutIdx := cutPoints[0]
	for i := endIndex - 1; i >= startIndex; i-- {
		tokens := estimateMessagesTokens(codingagent.SessionEntryToContextMessages(entries[i]))
		if tokens == 0 {
			continue
		}
		accumulated += tokens
		if accumulated >= keepRecentTokens {
			cutIdx = closestCutPointAtOrAfter(cutPoints, i)
			break
		}
	}
	// Absorb adjacent metadata entries that do not affect context.
	for cutIdx > startIndex {
		previous := entries[cutIdx-1]
		if previous.Base.Type == "compaction" || len(codingagent.SessionEntryToContextMessages(previous)) > 0 {
			break
		}
		cutIdx--
	}
	startsTurn := isTurnStartEntry(entries[cutIdx])
	turnStartIdx := -1
	if !startsTurn {
		turnStartIdx = FindTurnStartIndex(entries, cutIdx, startIndex)
	}
	return CutPointResult{
		FirstKeptEntryIndex: cutIdx,
		TurnStartIndex:      turnStartIdx,
		IsSplitTurn:         !startsTurn && turnStartIdx != -1,
	}
}

// closestCutPointAtOrAfter prefers the closest cut point at or after index. If
// trailing tool results exceed the budget by themselves, the last cut point
// keeps their preceding assistant tool call.
func closestCutPointAtOrAfter(cutPoints []int, index int) int {
	for _, candidate := range cutPoints {
		if candidate >= index {
			return candidate
		}
	}
	return cutPoints[len(cutPoints)-1]
}

// ─── File Operation Extraction ────────────────────────────────────────────────

// extractFileOperations builds FileOperations from messages and the previous
// Pi-generated compaction's details (compaction.ts extractFileOperations).
func extractFileOperations(
	messages []agent.AgentMessage,
	entries []codingagent.SessionEntry,
	prevCompactionIndex int,
) FileOperations {
	ops := NewFileOps()

	if prevCompactionIndex >= 0 {
		var raw struct {
			FromHook bool               `json:"fromHook"`
			Details  *CompactionDetails `json:"details"`
		}
		if err := json.Unmarshal(entries[prevCompactionIndex].Raw(), &raw); err == nil && !raw.FromHook && raw.Details != nil {
			for _, f := range raw.Details.ReadFiles {
				ops.Read[f] = struct{}{}
			}
			for _, f := range raw.Details.ModifiedFiles {
				ops.Edited[f] = struct{}{}
			}
		}
	}

	for _, msg := range messages {
		ExtractFileOpsFromMessage(msg, &ops)
	}

	return ops
}

// ─── Compaction Preparation ───────────────────────────────────────────────────

// getMessagesFromProjectedEntryForCompaction returns an entry's projected
// messages without system messages, which are prompt state rather than
// conversation; the compaction entry carries their replay. The previous
// compaction contributes its summary through PreviousSummary instead.
func getMessagesFromProjectedEntryForCompaction(entry codingagent.ProjectedSessionEntry) []agent.AgentMessage {
	if entry.SourceEntry.Base.Type == "compaction" {
		return nil
	}
	out := make([]agent.AgentMessage, 0, len(entry.Messages))
	for _, message := range entry.Messages {
		if message.Role() != "system" {
			out = append(out, message)
		}
	}
	return out
}

// projectedMessages returns a non-nil slice: upstream's flatMap yields [],
// which the session_before_compact wire carries as an empty array.
func projectedMessages(entries []codingagent.ProjectedSessionEntry) []agent.AgentMessage {
	out := []agent.AgentMessage{}
	for _, entry := range entries {
		out = append(out, getMessagesFromProjectedEntryForCompaction(entry)...)
	}
	return out
}

// PrepareCompaction selects what a compaction of pathEntries, a root-to-leaf
// branch, summarizes and keeps (compaction.ts prepareCompaction). It works on
// the canonical session projection, so context edits, omissions, and the
// previous compaction's retained range all apply. It returns nil when the
// branch ends in a compaction or nothing would be summarized.
func PrepareCompaction(
	pathEntries []codingagent.SessionEntry,
	s CompactionSettings,
) *CompactionPreparation {
	if len(pathEntries) > 0 && pathEntries[len(pathEntries)-1].Base.Type == "compaction" {
		return nil
	}

	projection := codingagent.BuildSessionProjection(pathEntries)
	entries := projection.Entries
	// The newest compaction is projected first. Older compaction entries can
	// occur in its retained range, but their projected contribution is empty.
	prevCompactionIndex := slices.IndexFunc(entries, func(entry codingagent.ProjectedSessionEntry) bool {
		return entry.SourceEntry.Base.Type == "compaction" && len(entry.Messages) > 0
	})

	var previousSummary string
	boundaryStart := 0
	if prevCompactionIndex >= 0 {
		var previous codingagent.CompactionEntry
		_ = json.Unmarshal(entries[prevCompactionIndex].SourceEntry.Raw(), &previous)
		previousSummary = previous.Summary
		// The projection has already selected the previous compaction's kept range.
		boundaryStart = prevCompactionIndex + 1
	}
	tokensBefore := EstimateProjectedContextTokens(projection, pathEntries).Tokens
	cutPoint := findProjectedCutPoint(entries, boundaryStart, len(entries), s.KeepRecentTokens)
	if cutPoint.FirstKeptEntryIndex >= len(entries) {
		return nil
	}
	firstKeptEntryID := entries[cutPoint.FirstKeptEntryIndex].SourceEntry.Base.ID
	if firstKeptEntryID == "" {
		return nil
	}
	historyEnd := cutPoint.FirstKeptEntryIndex
	if cutPoint.IsSplitTurn {
		historyEnd = cutPoint.TurnStartIndex
	}

	toSummarize := projectedMessages(entries[boundaryStart:historyEnd])
	turnPrefix := []agent.AgentMessage{}
	if cutPoint.IsSplitTurn {
		turnPrefix = projectedMessages(entries[cutPoint.TurnStartIndex:cutPoint.FirstKeptEntryIndex])
	}
	if len(toSummarize) == 0 && len(turnPrefix) == 0 {
		return nil
	}

	sourceEntries := make([]codingagent.SessionEntry, len(entries))
	for i, entry := range entries {
		sourceEntries[i] = entry.SourceEntry
	}
	fileOps := extractFileOperations(toSummarize, sourceEntries, prevCompactionIndex)
	for _, message := range turnPrefix {
		ExtractFileOpsFromMessage(message, &fileOps)
	}

	return &CompactionPreparation{
		FirstKeptEntryID:    firstKeptEntryID,
		MessagesToSummarize: toSummarize,
		TurnPrefixMessages:  turnPrefix,
		IsSplitTurn:         cutPoint.IsSplitTurn,
		TokensBefore:        tokensBefore,
		PreviousSummary:     previousSummary,
		FileOps:             fileOps,
		Settings:            s,
	}
}

// ─── LLM Summarization ────────────────────────────────────────────────────────

// convertToLlm converts context messages to provider messages for a
// summarization prompt (messages.ts convertToLlm). Custom, summary, and bash
// messages become user messages; `!!cmd` bash messages are dropped.
func convertToLlm(msgs []agent.AgentMessage) []ai.Message {
	out := make([]ai.Message, 0, len(msgs))
	for _, m := range msgs {
		switch {
		case m.System != nil:
			out = append(out, *m.System)
		case m.User != nil:
			out = append(out, ai.UserMessage{Content: ai.UserContentBlocks(m.User.Content), Timestamp: m.User.Timestamp})
		case m.Assistant != nil:
			usage := ai.Usage{}
			if m.Assistant.Usage != nil {
				usage = *m.Assistant.Usage
			}
			out = append(out, ai.AssistantMessage{
				Content: m.Assistant.Content, API: m.Assistant.API, Provider: m.Assistant.Provider,
				Model: m.Assistant.ModelID, ResponseModel: m.Assistant.ResponseModel,
				ResponseID: m.Assistant.ResponseID, Diagnostics: m.Assistant.Diagnostics,
				Usage: usage, StopReason: m.Assistant.StopReason, Deferred: m.Assistant.Deferred,
				ErrorMessage: m.Assistant.ErrorMessage, RawStopReason: m.Assistant.RawStopReason,
				Timestamp: m.Assistant.Timestamp,
			})
		case m.ToolResult != nil:
			out = append(out, ai.ToolResultMessage{
				ToolCallID: m.ToolResult.ToolCallID, ToolName: m.ToolResult.ToolName,
				Content: m.ToolResult.Content, Details: m.ToolResult.Details,
				Usage: m.ToolResult.Usage, IsError: m.ToolResult.IsError,
				Timestamp: m.ToolResult.Timestamp,
			})
		case m.Custom != nil:
			if message, ok := customMessageToLlm(m.Custom); ok {
				out = append(out, message)
			}
		}
	}
	return out
}

func customMessageToLlm(custom map[string]any) (ai.Message, bool) {
	timestamp := customTimestamp(custom["timestamp"])
	text := func(value string) ai.Message {
		return ai.UserMessage{Content: ai.UserContentBlocks{ai.TextContent{Text: value}}, Timestamp: timestamp}
	}
	switch role, _ := custom["role"].(string); role {
	case agent.RoleCustom:
		if content, ok := custom["content"].(string); ok {
			return text(content), true
		}
		raw, err := json.Marshal(map[string]any{"role": agent.RoleUser, "content": custom["content"]})
		if err != nil {
			return nil, false
		}
		var decoded agent.AgentMessage
		if json.Unmarshal(raw, &decoded) != nil || decoded.User == nil {
			return nil, false
		}
		return ai.UserMessage{Content: ai.UserContentBlocks(decoded.User.Content), Timestamp: timestamp}, true
	case agent.RoleBranchSummary:
		summary, _ := custom["summary"].(string)
		return text(agent.BranchSummaryContextText(summary)), true
	case agent.RoleCompactionSummary:
		summary, _ := custom["summary"].(string)
		return text(agent.CompactionSummaryContextText(summary)), true
	case agent.RoleBashExecution:
		if excluded, _ := custom["excludeFromContext"].(bool); excluded {
			return nil, false
		}
		return text(bashExecutionToText(custom)), true
	}
	return nil, false
}

func customTimestamp(value any) int64 {
	switch value := value.(type) {
	case int64:
		return value
	case int:
		return int64(value)
	case float64:
		return int64(value)
	}
	return 0
}

// bashExecutionToText renders a bash execution for model context
// (messages.ts bashExecutionToText).
func bashExecutionToText(custom map[string]any) string {
	command, _ := custom["command"].(string)
	output, _ := custom["output"].(string)
	var text strings.Builder
	text.WriteString("Ran `" + command + "`\n")
	if output != "" {
		text.WriteString("```\n" + output + "\n```")
	} else {
		text.WriteString("(no output)")
	}
	if cancelled, _ := custom["cancelled"].(bool); cancelled {
		text.WriteString("\n\n(command cancelled)")
	} else if exitCode, ok := custom["exitCode"].(float64); ok && exitCode != 0 {
		fmt.Fprintf(&text, "\n\nCommand exited with code %d", int(exitCode))
	}
	if truncated, _ := custom["truncated"].(bool); truncated {
		if path, _ := custom["fullOutputPath"].(string); path != "" {
			fmt.Fprintf(&text, "\n\n[Output truncated. Full output: %s]", path)
		}
	}
	return text.String()
}

// generateSummary calls the LLM to summarize messagesToSummarize.
// Uses UPDATE_SUMMARIZATION_PROMPT when previousSummary is non-empty.
// Mirrors upstream generateSummary (compaction.ts:547).
func capMaxTokens(budget int, model *ai.Model) int {
	if budget < 0 {
		budget = 0
	}
	if model != nil && model.Capabilities.MaxOutputTokens > 0 {
		return min(budget, model.Capabilities.MaxOutputTokens)
	}
	return budget
}

// createSummarizationOptions builds the request options for one summary
// call. The thinking level applies only to reasoning-capable models.
// Mirrors upstream createSummarizationOptions (compaction.ts).
func createSummarizationOptions(model *ai.Model, maxTokens int, thinkingLevel ai.ThinkingLevel, sessionID string) ai.StreamOptions {
	options := ai.StreamOptions{MaxTokens: maxTokens, SessionID: sessionID}
	if model != nil && model.Capabilities.MaxThinking != "" {
		options.IsReasoning = true
		if thinkingLevel != "" && thinkingLevel != ai.ThinkingOff {
			options.Thinking = thinkingLevel
		}
	}
	return options
}

// completeSummarization is the shared choke point for every compaction,
// branch-summary, and bug-report summarization call. One-off summaries never
// write prompt cache; callers without a routing session ID get a fresh one,
// reused across retries. Mirrors upstream completeSummarization (compaction.ts).
func completeSummarization(
	ctx context.Context,
	model *ai.Model,
	completer SimpleCompleter,
	streamFn StreamFn,
	retry *RetryOptions,
	systemPrompt string,
	messages []agent.AgentMessage,
	options ai.StreamOptions,
) (string, *ai.Usage, error) {
	requestOptions := options
	if model != nil {
		requestOptions.ModelCost = model.CostRates()
	}
	requestOptions.Env = maps.Clone(options.Env)
	if requestOptions.Env == nil {
		requestOptions.Env = ai.ProviderEnv{}
	}
	requestOptions.Env["PI_CACHE_RETENTION"] = "none"
	requestOptions.CacheRetention = ai.CacheRetentionNone
	if requestOptions.SessionID == "" {
		requestOptions.SessionID = uuid.Must(uuid.NewV7()).String()
	}
	return completeSimpleWithRetries(ctx, retry, func() (string, *ai.Usage, error) {
		if streamFn != nil {
			return streamFn(ctx, model, systemPrompt, messages, requestOptions)
		}
		return completer.CompleteSimple(ctx, model, systemPrompt, messages, requestOptions)
	})
}

func generateSummary(
	ctx context.Context,
	messages []agent.AgentMessage,
	previousSummary string,
	reserveTokens int,
	model *ai.Model,
	completer SimpleCompleter,
	streamFn StreamFn,
	customInstructions string,
	thinkingLevel ai.ThinkingLevel,
	retry *RetryOptions,
	sessionID string,
) (string, *ai.Usage, error) {
	basePrompt := SUMMARIZATION_PROMPT
	if previousSummary != "" {
		basePrompt = UPDATE_SUMMARIZATION_PROMPT
	}
	if customInstructions != "" {
		basePrompt += "\n\nAdditional focus: " + customInstructions
	}

	convText := SerializeConversation(convertToLlm(messages))

	var sb strings.Builder
	sb.WriteString("<conversation>\n")
	sb.WriteString(convText)
	sb.WriteString("\n</conversation>\n\n")
	if previousSummary != "" {
		sb.WriteString("<previous-summary>\n")
		sb.WriteString(previousSummary)
		sb.WriteString("\n</previous-summary>\n\n")
	}
	sb.WriteString(basePrompt)
	promptText := sb.String()

	ts := time.Now().UnixMilli()
	req := []agent.AgentMessage{
		{
			User: &agent.UserMessage{
				Role: "user",
				Content: []ai.UserContentBlock{
					ai.TextContent{Text: promptText},
				},
				Timestamp: ts,
			},
		},
	}

	maxTokens := capMaxTokens((8*reserveTokens)/10, model)
	options := createSummarizationOptions(model, maxTokens, thinkingLevel, sessionID)
	result, usage, err := completeSummarization(ctx, model, completer, streamFn, retry, SummarizationSystemPrompt, req, options)
	if err != nil {
		return "", nil, summarizationFailure("Summarization", err)
	}
	return result, usage, nil
}

// generateTurnPrefixSummary summarizes the prefix of a split turn.
// Mirrors upstream generateTurnPrefixSummary (compaction.ts:804).
func generateTurnPrefixSummary(
	ctx context.Context,
	messages []agent.AgentMessage,
	reserveTokens int,
	model *ai.Model,
	completer SimpleCompleter,
	streamFn StreamFn,
	thinkingLevel ai.ThinkingLevel,
	retry *RetryOptions,
	sessionID string,
) (string, *ai.Usage, error) {
	convText := SerializeConversation(convertToLlm(messages))
	promptText := "# Conversation\n" + convText + "\n\n# Instructions\n" + turnPrefixSummarizationPrompt
	ts := time.Now().UnixMilli()
	req := []agent.AgentMessage{
		{
			User: &agent.UserMessage{
				Role: "user",
				Content: []ai.UserContentBlock{
					ai.TextContent{Text: promptText},
				},
				Timestamp: ts,
			},
		},
	}
	maxTokens := capMaxTokens(reserveTokens/2, model)
	options := createSummarizationOptions(model, maxTokens, thinkingLevel, sessionID)
	result, usage, err := completeSummarization(ctx, model, completer, streamFn, retry, SummarizationSystemPrompt, req, options)
	if err != nil {
		return "", nil, summarizationFailure("Turn prefix summarization", err)
	}
	return result, usage, nil
}

// ErrSummarizationToolCall rejects a provider response containing a tool call;
// standalone summarization requests never provide executable tools.
var ErrSummarizationToolCall = errors.New("summarization attempted to call a tool")

func summarizationFailure(operation string, err error) error {
	if errors.Is(err, ErrSummarizationToolCall) {
		return fmt.Errorf("%s attempted to call a tool", operation)
	}
	message := err.Error()
	if errors.Is(err, context.Canceled) {
		message = "This operation was aborted"
	}
	return fmt.Errorf("%s failed: %s", operation, message)
}

// ─── Main Compaction Function ─────────────────────────────────────────────────

// Compact generates summaries for compaction using PrepareCompaction output.
// thinkingLevel applies to reasoning-capable models; an empty sessionID gives
// each summary request a fresh routing ID.
// Mirrors upstream compact (compaction.ts:714).
func Compact(
	ctx context.Context,
	prep CompactionPreparation,
	model *ai.Model,
	completer SimpleCompleter,
	streamFn StreamFn,
	customInstructions string,
	thinkingLevel ai.ThinkingLevel,
	retry *RetryOptions,
	sessionID string,
) (CompactionResult, error) {
	var summary string
	var usage *ai.Usage

	if prep.IsSplitTurn && len(prep.TurnPrefixMessages) > 0 {
		historySummary := cmp.Or(prep.PreviousSummary, "No prior history.")
		var historyUsage *ai.Usage
		if len(prep.MessagesToSummarize) > 0 {
			var err error
			historySummary, historyUsage, err = generateSummary(ctx, prep.MessagesToSummarize, prep.PreviousSummary, prep.Settings.ReserveTokens, model, completer, streamFn, customInstructions, thinkingLevel, retry, sessionID)
			if err != nil {
				return CompactionResult{}, err
			}
		}

		prefixSummary, prefixUsage, err := generateTurnPrefixSummary(ctx, prep.TurnPrefixMessages, prep.Settings.ReserveTokens, model, completer, streamFn, thinkingLevel, retry, sessionID)
		if err != nil {
			return CompactionResult{}, err
		}
		summary = historySummary + "\n\n---\n\n**Turn Context (split turn):**\n\n" + prefixSummary
		usage = combineUsage(historyUsage, prefixUsage)
	} else {
		var err error
		summary, usage, err = generateSummary(ctx, prep.MessagesToSummarize, prep.PreviousSummary, prep.Settings.ReserveTokens, model, completer, streamFn, customInstructions, thinkingLevel, retry, sessionID)
		if err != nil {
			return CompactionResult{}, err
		}
	}

	readFiles, modifiedFiles := ComputeFileLists(prep.FileOps)
	summary += FormatFileOperations(readFiles, modifiedFiles)

	if prep.FirstKeptEntryID == "" {
		return CompactionResult{}, errors.New("First kept entry has no UUID - session may need migration")
	}

	return CompactionResult{
		Summary:          summary,
		FirstKeptEntryID: prep.FirstKeptEntryID,
		TokensBefore:     prep.TokensBefore,
		Usage:            usage,
		Details: CompactionDetails{
			ReadFiles:     readFiles,
			ModifiedFiles: modifiedFiles,
		},
	}, nil
}
