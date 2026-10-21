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

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/compactiontypes"
	"github.com/MichaelKinsy/PiG/internal/usagetotals"
)

// ─── Types ────────────────────────────────────────────────────────────────────

// CompactionDetails is stored in a CompactionEntry.Details for file tracking
// across compactions.
// Mirrors upstream CompactionDetails (compaction.ts:36).
type CompactionDetails struct {
	ReadFiles     []string `json:"readFiles"`
	ModifiedFiles []string `json:"modifiedFiles"`
}

// CompactionSettings controls when and how compaction runs. upstream: compaction.ts:115
type CompactionSettings = compactiontypes.CompactionSettings

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

// CompactionPreparation is the output of PrepareCompaction. upstream: compaction.ts:772
type CompactionPreparation = compactiontypes.CompactionPreparation

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
	sum := usagetotals.CombineUsage(*a, *b)
	return &sum
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
		if entry.Base().Type != "message" {
			continue
		}
		message, ok := codingagent.AsMessage(entry)
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
	if entry.Base().Type == "compaction" {
		return false
	}
	return slices.ContainsFunc(codingagent.SessionEntryToContextMessages(entry), isTurnStartMessage)
}

// findValidCutPoints returns the indices of context-visible user-like or
// assistant entries (compaction.ts findValidCutPoints).
func findValidCutPoints(entries []codingagent.SessionEntry, startIdx, endIdx int) []int {
	var points []int
	for i := startIdx; i < endIdx; i++ {
		if entries[i].Base().Type == "compaction" {
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
		if previous.Base().Type == "compaction" || len(codingagent.SessionEntryToContextMessages(previous)) > 0 {
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
	if entry.SourceEntry.Base().Type == "compaction" {
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
	if len(pathEntries) > 0 && pathEntries[len(pathEntries)-1].Base().Type == "compaction" {
		return nil
	}

	projection := codingagent.ProjectSessionPath(pathEntries)
	entries := projection.Entries
	// The newest compaction is projected first. Older compaction entries can
	// occur in its retained range, but their projected contribution is empty.
	prevCompactionIndex := slices.IndexFunc(entries, func(entry codingagent.ProjectedSessionEntry) bool {
		return entry.SourceEntry.Base().Type == "compaction" && len(entry.Messages) > 0
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
	firstKeptEntryID := entries[cutPoint.FirstKeptEntryIndex].SourceEntry.Base().ID
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
			out = append(out, m.User.LLMMessage())
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
				Content: m.ToolResult.Content, Details: m.ToolResult.Details, DetailsNull: m.ToolResult.DetailsNull,
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
		if content, ok := decoded.User.Content.(ai.UserText); ok {
			return text(string(content)), true
		}
		user := decoded.User.LLMMessage()
		user.Timestamp = timestamp
		return user, true
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
// call. The thinking level applies only to reasoning-capable models. No
// maxTokens: summaries use the model's output limit (#9512, Pi 6fcb4867).
// Mirrors upstream createSummarizationOptions (compaction.ts).
func createSummarizationOptions(model *ai.Model, apiKey string, headers, env map[string]string, thinkingLevel ai.ModelThinkingLevel, sessionID string) ai.StreamOptions {
	options := ai.StreamOptions{APIKey: apiKey, Headers: ai.ProviderHeadersFromStrings(headers), Env: env, SessionID: sessionID}
	if model != nil && model.Capabilities.MaxThinking != "" {
		options.IsReasoning = true
		options.Thinking = thinkingLevel.ReasoningOption()
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
	retry *ai.RetryPolicy,
	callbacks ai.RetryCallbacks,
	systemPrompt string,
	messages []agent.AgentMessage,
	options ai.StreamOptions,
) (string, *ai.Usage, error) {
	requestOptions := options
	if model != nil {
		requestOptions.ModelCost = model.CostRates()
	}
	// compaction.ts completeSummarization: cacheRetention "none" wins over the environment's PI_CACHE_RETENTION in every provider, so the caller's env is passed as given.
	requestOptions.Env = maps.Clone(options.Env)
	requestOptions.CacheRetention = ai.CacheRetentionNone
	if requestOptions.SessionID == "" {
		// upstream: packages/coding-agent/src/core/compaction/compaction.ts: sessionId: options.sessionId ?? uuidv7()
		id, err := ai.UUIDv7(nil)
		if err != nil {
			return "", nil, err
		}
		requestOptions.SessionID = id
	}
	return completeSimpleWithRetries(ctx, retry, callbacks, func() (string, *ai.Usage, error) {
		if streamFn != nil {
			text, usage, err := streamFn(ctx, model, systemPrompt, messages, requestOptions)
			if err != nil {
				if completer, ok := errors.AsType[*completerError](err); ok {
					return "", nil, completer.err
				}
				return "", nil, &summarizationCallError{err}
			}
			return text, usage, nil
		}
		return completer.CompleteSimple(ctx, model, systemPrompt, messages, requestOptions)
	})
}

// GenerateSummary generates or updates a conversation summary with the model's provider and returns its text (compaction.ts:645 generateSummary, which
// returns generateSummaryWithUsage's `text`). apiKey, headers and env are the request auth; a nil streamFn uses the provider's own stream, as upstream's
// completeSimple does. An empty sessionID gives each request a fresh routing session.
func GenerateSummary(
	ctx context.Context,
	messages []agent.AgentMessage,
	model *ai.Model,
	reserveTokens int,
	apiKey string,
	headers map[string]string,
	customInstructions string,
	previousSummary string,
	thinkingLevel ai.ModelThinkingLevel,
	streamFn StreamFn,
	env map[string]string,
	retry *ai.RetryPolicy,
	callbacks ai.RetryCallbacks,
	sessionID string,
) (string, error) {
	text, _, err := GenerateSummaryWithUsage(ctx, messages, model, reserveTokens, apiKey, headers, customInstructions, previousSummary, thinkingLevel, streamFn, env, retry, callbacks, sessionID)
	return text, err
}

// GenerateSummaryWithUsage generates or updates a conversation summary with the model's provider and returns the provider usage (compaction.ts:696
// generateSummaryWithUsage). Its parameters are those of [GenerateSummary].
func GenerateSummaryWithUsage(
	ctx context.Context,
	messages []agent.AgentMessage,
	model *ai.Model,
	reserveTokens int,
	apiKey string,
	headers map[string]string,
	customInstructions string,
	previousSummary string,
	thinkingLevel ai.ModelThinkingLevel,
	streamFn StreamFn,
	env map[string]string,
	retry *ai.RetryPolicy,
	callbacks ai.RetryCallbacks,
	sessionID string,
) (string, *ai.Usage, error) {
	return GenerateSummaryWithUsageUsing(ctx, messages, model, reserveTokens, apiKey, headers, customInstructions, previousSummary, thinkingLevel, ProviderCompleter{}, streamFn, env, retry, callbacks, sessionID)
}

// GenerateSummaryWithUsageUsing is [GenerateSummaryWithUsage] with the completer that stands in for pi-ai's completeSimple when streamFn is nil.
// (compaction.ts:696 generateSummaryWithUsage)
// apiKey, headers and env are the request auth (agent-session.ts _getSummarizationRequestAuth). completer stands in for pi-ai's completeSimple when streamFn is nil.
func GenerateSummaryWithUsageUsing(
	ctx context.Context,
	messages []agent.AgentMessage,
	model *ai.Model,
	reserveTokens int,
	apiKey string,
	headers map[string]string,
	customInstructions string,
	previousSummary string,
	thinkingLevel ai.ModelThinkingLevel,
	completer SimpleCompleter,
	streamFn StreamFn,
	env map[string]string,
	retry *ai.RetryPolicy,
	callbacks ai.RetryCallbacks,
	sessionID string,
) (string, *ai.Usage, error) {
	chunks := splitForContextWindow(messages, model, reserveTokens, previousSummary, customInstructions)
	if len(chunks) <= 1 {
		return generateSummaryAdaptive(ctx, messages, model, reserveTokens, apiKey, headers, customInstructions, previousSummary, thinkingLevel, completer, streamFn, env, retry, callbacks, sessionID)
	}
	// pig divergence (D167): pi sends the whole history in one request, so a history longer than the model's context window fails with the provider's overflow error and the session can never be compacted. PiG summarizes it window-sized chunk by chunk, each chunk updating the summary of the one before (the UPDATE_SUMMARIZATION_PROMPT path pi uses for a previous compaction).
	summary := previousSummary
	var usage *ai.Usage
	for _, chunk := range chunks {
		var chunkUsage *ai.Usage
		var err error
		summary, chunkUsage, err = generateSummaryAdaptive(ctx, chunk, model, reserveTokens, apiKey, headers, customInstructions, summary, thinkingLevel, completer, streamFn, env, retry, callbacks, sessionID)
		if err != nil {
			return "", nil, err
		}
		usage = combineUsage(usage, chunkUsage)
	}
	return summary, usage, nil
}

// generateSummaryAdaptive summarizes messages in one request and, when the provider answers with a context overflow, in two halves, the second updating the summary of the first, until a single message is left.
func generateSummaryAdaptive(
	ctx context.Context,
	messages []agent.AgentMessage,
	model *ai.Model,
	reserveTokens int,
	apiKey string,
	headers map[string]string,
	customInstructions string,
	previousSummary string,
	thinkingLevel ai.ModelThinkingLevel,
	completer SimpleCompleter,
	streamFn StreamFn,
	env map[string]string,
	retry *ai.RetryPolicy,
	callbacks ai.RetryCallbacks,
	sessionID string,
) (string, *ai.Usage, error) {
	summary, usage, err := generateSummaryChunk(ctx, messages, model, reserveTokens, apiKey, headers, customInstructions, previousSummary, thinkingLevel, completer, streamFn, env, retry, callbacks, sessionID)
	if err == nil || len(messages) < 2 || !ai.IsContextOverflow(ai.AssistantMessage{StopReason: ai.StopReasonError, ErrorMessage: err.Error()}, 0) {
		return summary, usage, err
	}
	middle := len(messages) / 2
	summary, firstUsage, err := generateSummaryAdaptive(ctx, messages[:middle], model, reserveTokens, apiKey, headers, customInstructions, previousSummary, thinkingLevel, completer, streamFn, env, retry, callbacks, sessionID)
	if err != nil {
		return "", nil, err
	}
	summary, secondUsage, err := generateSummaryAdaptive(ctx, messages[middle:], model, reserveTokens, apiKey, headers, customInstructions, summary, thinkingLevel, completer, streamFn, env, retry, callbacks, sessionID)
	if err != nil {
		return "", nil, err
	}
	return summary, combineUsage(firstUsage, secondUsage), nil
}

// summarizationInputShare is the share of the room a summary request has left that its conversation text may fill by character estimate, because the estimate is four characters per token.
const summarizationInputShare = 0.75

// splitForContextWindow splits messages into consecutive chunks whose serialized text fits one summary request of the model's context window: the window minus the summary the request carries and the one it asks for, the instructions, and a quarter of what is left. A history that fits, a model without a known window, and a single message larger than a chunk each stay whole.
func splitForContextWindow(messages []agent.AgentMessage, model *ai.Model, reserveTokens int, previousSummary, customInstructions string) [][]agent.AgentMessage {
	if model == nil || model.Capabilities.ContextWindow <= 0 || len(messages) < 2 {
		return [][]agent.AgentMessage{messages}
	}
	// The summary a request carries and the one it asks for are each bounded by the model's output limit, which summary
	// requests use since Pi 6fcb4867; without a known limit, by the reserve as before.
	maxTokens := capMaxTokens((8*reserveTokens)/10, nil)
	if model.Capabilities.MaxOutputTokens > 0 {
		maxTokens = model.Capabilities.MaxOutputTokens
	}
	overhead := ceilQuarter(len(SummarizationSystemPrompt)+len(UPDATE_SUMMARIZATION_PROMPT)+len(customInstructions)) + 2*maxTokens
	room := int(float64(model.Capabilities.ContextWindow-overhead) * summarizationInputShare)
	if room <= 0 {
		return [][]agent.AgentMessage{messages}
	}
	sizes := make([]int, len(messages))
	total := 0
	for index := range messages {
		sizes[index] = ceilQuarter(jsLength(SerializeConversation(convertToLlm(messages[index:index+1])))) + 1
		total += sizes[index]
	}
	if total <= room {
		return [][]agent.AgentMessage{messages}
	}
	var chunks [][]agent.AgentMessage
	start, used := 0, 0
	for index, size := range sizes {
		if used > 0 && used+size > room {
			chunks = append(chunks, messages[start:index])
			start, used = index, 0
		}
		used += size
	}
	return append(chunks, messages[start:])
}

// generateSummaryChunk is one summary request: the body of compaction.ts generateSummaryWithUsage.
func generateSummaryChunk(
	ctx context.Context,
	messages []agent.AgentMessage,
	model *ai.Model,
	reserveTokens int,
	apiKey string,
	headers map[string]string,
	customInstructions string,
	previousSummary string,
	thinkingLevel ai.ModelThinkingLevel,
	completer SimpleCompleter,
	streamFn StreamFn,
	env map[string]string,
	retry *ai.RetryPolicy,
	callbacks ai.RetryCallbacks,
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
				Content: ai.UserContentBlocks{
					ai.TextContent{Text: promptText},
				},
				Timestamp: ts,
			},
		},
	}

	options := createSummarizationOptions(model, apiKey, headers, env, thinkingLevel, sessionID)
	result, usage, err := completeSummarization(ctx, model, completer, streamFn, retry, callbacks, SummarizationSystemPrompt, req, options)
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
	model *ai.Model,
	reserveTokens int,
	apiKey string,
	headers map[string]string,
	env map[string]string,
	thinkingLevel ai.ModelThinkingLevel,
	completer SimpleCompleter,
	streamFn StreamFn,
	retry *ai.RetryPolicy,
	callbacks ai.RetryCallbacks,
	sessionID string,
	customInstructions string,
) (string, *ai.Usage, error) {
	convText := SerializeConversation(convertToLlm(messages))
	promptText := "# Conversation\n" + convText + "\n\n# Instructions\n" + turnPrefixSummarizationPrompt
	if customInstructions != "" {
		promptText += "\n\nAdditional focus: " + customInstructions
	}
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
	options := createSummarizationOptions(model, apiKey, headers, env, thinkingLevel, sessionID)
	result, usage, err := completeSummarization(ctx, model, completer, streamFn, retry, callbacks, SummarizationSystemPrompt, req, options)
	if err != nil {
		return "", nil, summarizationFailure("Turn prefix summarization", err)
	}
	return result, usage, nil
}

// ErrSummarizationToolCall rejects a provider response containing a tool call;
// standalone summarization requests never provide executable tools.
var ErrSummarizationToolCall = errors.New("summarization attempted to call a tool")

// completerError is an error of a [SimpleCompleter] that [CompleterStreamFn] hands back through a [StreamFn]: it is worded as a failure of the summary request, as a completer's error is, and not returned verbatim as a stream function's own error is.
type completerError struct{ err error }

func (e *completerError) Error() string { return e.err.Error() }
func (e *completerError) Unwrap() error { return e.err }

// CompleterStreamFn is the [StreamFn] that sends each summary request through completer, for a caller that has a completer and not a stream function.
func CompleterStreamFn(completer SimpleCompleter) StreamFn {
	return func(ctx context.Context, model *ai.Model, systemPrompt string, messages []agent.AgentMessage, options ai.StreamOptions) (string, *ai.Usage, error) {
		text, usage, err := completer.CompleteSimple(ctx, model, systemPrompt, messages, options)
		if err != nil {
			return "", nil, &completerError{err}
		}
		return text, usage, nil
	}
}

type summarizationCallError struct{ err error }

func (e *summarizationCallError) Error() string { return e.err.Error() }
func (e *summarizationCallError) Unwrap() error { return e.err }

func summarizationFailure(operation string, err error) error {
	if errors.Is(err, ErrSummarizationToolCall) {
		return fmt.Errorf("%s attempted to call a tool", operation)
	}
	if callError, ok := errors.AsType[*summarizationCallError](err); ok {
		return callError.err
	}
	message := err.Error()
	if errors.Is(err, context.Canceled) {
		message = "This operation was aborted"
	}
	return fmt.Errorf("%s failed: %s", operation, message)
}

// ─── Main Compaction Function ─────────────────────────────────────────────────

// Compact generates the summaries of a compaction using PrepareCompaction output (compaction.ts:965 compact). thinkingLevel applies to
// reasoning-capable models; an empty sessionID gives each summary request a fresh routing ID; apiKey, headers and env are the request auth. streamFn
// sends each summary request, as upstream's streamFn does (agent-session.ts passes this.agent.streamFunction): a [CompleterStreamFn] sends it through a
// [SimpleCompleter] such as a [ProviderCompleter] holding the Agent's stream function, and a nil streamFn streams with the model's own provider.
func Compact(
	ctx context.Context,
	prep CompactionPreparation,
	model *ai.Model,
	apiKey string,
	headers map[string]string,
	customInstructions string,
	thinkingLevel ai.ModelThinkingLevel,
	streamFn StreamFn,
	env map[string]string,
	retry *ai.RetryPolicy,
	callbacks ai.RetryCallbacks,
	sessionID string,
) (CompactionResult, error) {
	// completeSimple: the model's own provider, when no streamFn sends the request.
	completer := ProviderCompleter{}
	var summary string
	var usage *ai.Usage

	if prep.IsSplitTurn && len(prep.TurnPrefixMessages) > 0 {
		history, prefix := prep.MessagesToSummarize, prep.TurnPrefixMessages
		if chunks := splitForContextWindow(prefix, model, prep.Settings.ReserveTokens, prep.PreviousSummary, customInstructions); len(chunks) > 1 {
			// pig divergence (D167): pi sends the whole turn prefix in one request. A prefix longer than the window moves all but its last window-sized part into the chunked history summary.
			last := chunks[len(chunks)-1]
			history = append(slices.Clip(history), prefix[:len(prefix)-len(last)]...)
			prefix = last
		}
		historySummary := cmp.Or(prep.PreviousSummary, "No prior history.")
		var historyUsage *ai.Usage
		if len(history) > 0 {
			var err error
			historySummary, historyUsage, err = GenerateSummaryWithUsageUsing(ctx, history, model, prep.Settings.ReserveTokens, apiKey, headers, customInstructions, prep.PreviousSummary, thinkingLevel, completer, streamFn, env, retry, callbacks, sessionID)
			if err != nil {
				return CompactionResult{}, err
			}
		}

		prefixSummary, prefixUsage, err := generateTurnPrefixSummary(ctx, prefix, model, prep.Settings.ReserveTokens, apiKey, headers, env, thinkingLevel, completer, streamFn, retry, callbacks, sessionID, customInstructions)
		if err != nil {
			return CompactionResult{}, err
		}
		summary = historySummary + "\n\n---\n\n**Turn Context (split turn):**\n\n" + prefixSummary
		usage = combineUsage(historyUsage, prefixUsage)
	} else {
		var err error
		summary, usage, err = GenerateSummaryWithUsageUsing(ctx, prep.MessagesToSummarize, model, prep.Settings.ReserveTokens, apiKey, headers, customInstructions, prep.PreviousSummary, thinkingLevel, completer, streamFn, env, retry, callbacks, sessionID)
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
