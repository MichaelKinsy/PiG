// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-FileCopyrightText: Copyright (c) 2025 Mario Zechner
// SPDX-License-Identifier: MIT

// Package compaction prepares and summarizes durable harness entries with an embedded retained tail.
package compaction

import (
	"context"
	"fmt"
	"slices"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/agent/harness"
	"github.com/MichaelKinsy/PiG/agent/harness/session"
	"github.com/MichaelKinsy/PiG/ai"
)

// Ports packages/agent/src/harness/compaction/compaction.ts.
type CompactionSettings = harness.CompactionSettings

var DefaultCompactionSettings = CompactionSettings{Enabled: true, ReserveTokens: 16384, KeepRecentTokens: 20000}

type CompactionDetails struct {
	ReadFiles     []string `json:"readFiles"`
	ModifiedFiles []string `json:"modifiedFiles"`
}
type CompactionPreparation struct {
	MessagesToSummarize []agent.AgentMessage `json:"messagesToSummarize"`
	TurnPrefixMessages  []agent.AgentMessage `json:"turnPrefixMessages"`
	RetainedTail        []agent.AgentMessage `json:"retainedTail"`
	IsSplitTurn         bool                 `json:"isSplitTurn"`
	TokensBefore        int                  `json:"tokensBefore"`
	PreviousSummary     string               `json:"previousSummary,omitempty"`
	// PreviousSummaryPresent retains an empty prior compaction summary when building durable preparation.
	PreviousSummaryPresent bool               `json:"-"`
	FileOps                FileOperations     `json:"fileOps"`
	Settings               CompactionSettings `json:"settings"`
}
type CompactResult struct {
	Summary      string               `json:"summary"`
	TokensBefore int                  `json:"tokensBefore"`
	Usage        *ai.Usage            `json:"usage,omitempty"`
	RetainedTail []agent.AgentMessage `json:"retainedTail"`
	Details      harness.JsonValue    `json:"details,omitempty"`
}
type CutPointResult struct {
	FirstKeptEntryIndex int  `json:"firstKeptEntryIndex"`
	TurnStartIndex      int  `json:"turnStartIndex"`
	IsSplitTurn         bool `json:"isSplitTurn"`
}
type ContextUsageEstimate struct {
	Tokens         int  `json:"tokens"`
	UsageTokens    int  `json:"usageTokens"`
	TrailingTokens int  `json:"trailingTokens"`
	LastUsageIndex *int `json:"lastUsageIndex"`
}

// Models is the single provider operation used by standalone compaction helpers.
type Models interface {
	CompleteSimple(context.Context, *ai.Model, ai.Context, ai.StreamOptions) *ai.AssistantMessage
}
type SummaryResult struct {
	Text  string
	Usage ai.Usage
}

func CalculateContextTokens(usage ai.Usage) int {
	if usage.TotalTokens != 0 {
		return usage.TotalTokens
	}
	return usage.Input + usage.Output + usage.CacheRead + usage.CacheWrite
}
func stringLength(s string) int {
	units := 0
	for len(s) > 0 {
		r, n := utf8.DecodeRuneInString(s)
		s = s[n:]
		units++
		if r > 0xffff {
			units++
		}
	}
	return units
}
func contentChars(content any) int {
	switch value := content.(type) {
	case string:
		return stringLength(value)
	case ai.UserText:
		return stringLength(string(value))
	case ai.UserContentBlocks:
		return contentChars([]ai.UserContentBlock(value))
	case []ai.UserContentBlock:
		chars := 0
		for _, block := range value {
			switch block := block.(type) {
			case ai.TextContent:
				chars += stringLength(block.Text)
			case ai.ImageContent:
				chars += 4800
			}
		}
		return chars
	case []ai.ToolResultMessageContent:
		chars := 0
		for _, block := range value {
			switch block := block.(type) {
			case ai.TextContent:
				chars += stringLength(block.Text)
			case ai.ImageContent:
				chars += 4800
			}
		}
		return chars
	case []any:
		chars := 0
		for _, block := range value {
			if block, ok := block.(map[string]any); ok {
				kind, _ := block["type"].(string)
				switch kind {
				case "text":
					text, _ := block["text"].(string)
					chars += stringLength(text)
				case "image":
					chars += 4800
				}
			}
		}
		return chars
	}
	return 0
}

// EstimateTokens counts UTF-16 characters and fixed image estimates. Native system messages are not counted by the harness heuristic.
func EstimateTokens(message agent.AgentMessage) int {
	chars := 0
	switch {
	case message.User != nil:
		chars = contentChars(message.User.Content)
	case message.Assistant != nil:
		for _, block := range message.Assistant.Content {
			switch block := block.(type) {
			case ai.TextContent:
				chars += stringLength(block.Text)
			case ai.ThinkingContent:
				chars += stringLength(block.Thinking)
			case ai.ToolCall:
				chars += stringLength(block.Name) + stringLength(safeJSONStringify(block.Arguments))
			}
		}
	case message.ToolResult != nil:
		chars = contentChars(message.ToolResult.Content)
	case message.Custom != nil:
		switch message.Custom["role"] {
		case "user", "custom", "toolResult":
			chars = contentChars(message.Custom["content"])
		case "bashExecution":
			command, _ := message.Custom["command"].(string)
			output, _ := message.Custom["output"].(string)
			chars = stringLength(command) + stringLength(output)
		case "branchSummary", "compactionSummary":
			summary, _ := message.Custom["summary"].(string)
			chars = stringLength(summary)
		}
	}
	return (chars + 3) / 4
}
func assistantUsage(message agent.AgentMessage) *ai.Usage {
	a := message.Assistant
	if a != nil && a.Usage != nil && a.StopReason != ai.StopReasonError && a.StopReason != ai.StopReasonAborted && CalculateContextTokens(*a.Usage) > 0 {
		return a.Usage
	}
	return nil
}
func GetLastAssistantUsage(entries []session.Entry) *ai.Usage {
	for _, entry := range slices.Backward(entries) {
		if entry.Type == session.EntryTypeMessage {
			if usage := assistantUsage(entry.Message); usage != nil {
				return usage
			}
		}
	}
	return nil
}
func EstimateContextTokens(messages []agent.AgentMessage) ContextUsageEstimate {
	estimate := ContextUsageEstimate{}
	for i, message := range slices.Backward(messages) {
		if usage := assistantUsage(message); usage != nil {
			estimate.UsageTokens = CalculateContextTokens(*usage)
			estimate.LastUsageIndex = new(i)
			break
		}
	}
	start := 0
	if estimate.LastUsageIndex != nil {
		start = *estimate.LastUsageIndex + 1
	}
	for _, message := range messages[start:] {
		estimate.TrailingTokens += EstimateTokens(message)
	}
	estimate.Tokens = estimate.UsageTokens + estimate.TrailingTokens
	return estimate
}
func ShouldCompact(tokens, window int, settings CompactionSettings) bool {
	return settings.Enabled && tokens > window-settings.ReserveTokens
}
func roleOf(message agent.AgentMessage) string {
	switch {
	case message.User != nil:
		return "user"
	case message.Assistant != nil:
		return "assistant"
	case message.ToolResult != nil:
		return "toolResult"
	case message.System != nil:
		return "system"
	default:
		role, _ := message.Custom["role"].(string)
		return role
	}
}
func FindTurnStartIndex(entries []session.Entry, index, start int) int {
	for i := index; i >= start; i-- {
		entry := entries[i]
		if entry.Type == session.EntryTypeBranchSummary {
			return i
		}
		if entry.Type == session.EntryTypeMessage {
			role := roleOf(entry.Message)
			if role == "user" || role == "bashExecution" {
				return i
			}
		}
	}
	return -1
}
func FindCutPoint(entries []session.Entry, start, end, keep int) CutPointResult {
	var points []int
	for i := start; i < end; i++ {
		entry := entries[i]
		switch entry.Type {
		case session.EntryTypeBranchSummary:
			points = append(points, i)
		case session.EntryTypeMessage:
			switch roleOf(entry.Message) {
			case "bashExecution", "custom", "branchSummary", "compactionSummary", "user", "assistant":
				points = append(points, i)
			}
		}
	}
	if len(points) == 0 {
		return CutPointResult{FirstKeptEntryIndex: start, TurnStartIndex: -1}
	}
	tokens, cut := 0, points[0]
	for i := end - 1; i >= start; i-- {
		if entries[i].Type != session.EntryTypeMessage {
			continue
		}
		tokens += EstimateTokens(entries[i].Message)
		if tokens >= keep {
			for _, point := range points {
				if point >= i {
					cut = point
					break
				}
			}
			break
		}
	}
	for cut > start {
		previous := entries[cut-1]
		if previous.Type == session.EntryTypeMessage || previous.Type == session.EntryTypeCompaction {
			break
		}
		cut--
	}
	isUser := entries[cut].Type == session.EntryTypeMessage && entries[cut].Message.User != nil
	turn := -1
	if !isUser {
		turn = FindTurnStartIndex(entries, cut, start)
	}
	return CutPointResult{FirstKeptEntryIndex: cut, TurnStartIndex: turn, IsSplitTurn: !isUser && turn != -1}
}
func messageFromEntry(entry session.Entry) (agent.AgentMessage, bool) {
	switch entry.Type {
	case session.EntryTypeMessage:
		return entry.Message, true
	case session.EntryTypeBranchSummary:
		var from any
		if entry.FromID != nil {
			from = *entry.FromID
		}
		return agent.AgentMessage{Custom: map[string]any{"role": "branchSummary", "summary": entry.Summary, "fromId": from, "timestamp": entry.Timestamp}}, true
	}
	return agent.AgentMessage{}, false
}
func messageTimestamp(message agent.AgentMessage) int64 {
	switch {
	case message.User != nil:
		return message.User.Timestamp
	case message.Assistant != nil:
		return message.Assistant.Timestamp
	case message.ToolResult != nil:
		return message.ToolResult.Timestamp
	case message.System != nil:
		return message.System.Timestamp
	}
	switch value := message.Custom["timestamp"].(type) {
	case int64:
		return value
	case int:
		return int64(value)
	case float64:
		return int64(value)
	}
	return 0
}

// PrepareCompaction expands the latest embedded tail before finding a new cut point. Older summaries are carried as previousSummary rather than summarized again.
func PrepareCompaction(entries []session.Entry, settings CompactionSettings) (*CompactionPreparation, error) {
	if len(entries) == 0 || entries[len(entries)-1].Type == session.EntryTypeCompaction {
		return nil, nil
	}
	previous := -1
	for i, entry := range slices.Backward(entries) {
		if entry.Type == session.EntryTypeCompaction {
			previous = i
			break
		}
	}
	compactable := entries
	prep := &CompactionPreparation{MessagesToSummarize: []agent.AgentMessage{}, TurnPrefixMessages: []agent.AgentMessage{}, RetainedTail: []agent.AgentMessage{}, FileOps: CreateFileOps(), Settings: settings}
	if previous >= 0 {
		prior := entries[previous]
		prep.PreviousSummary = prior.Summary
		prep.PreviousSummaryPresent = true
		compactable = make([]session.Entry, 0, len(prior.RetainedTail)+len(entries)-previous-1)
		for i, message := range prior.RetainedTail {
			parent := prior.ID
			if i > 0 {
				parent = fmt.Sprintf("%s:retained:%d", prior.ID, i-1)
			}
			compactable = append(compactable, session.Entry{Type: session.EntryTypeMessage, ID: fmt.Sprintf("%s:retained:%d", prior.ID, i), ParentID: &parent, Seq: prior.Seq, Timestamp: messageTimestamp(message), Message: message})
		}
		compactable = append(compactable, entries[previous+1:]...)
		if prior.Details != nil {
			if details, ok := (*prior.Details).(map[string]any); ok {
				restoreFilePaths(details["readFiles"], prep.FileOps.AddRead)
				restoreFilePaths(details["modifiedFiles"], prep.FileOps.AddEdited)
			}
		}
	}
	var projected []agent.AgentMessage
	for _, entry := range session.BuildContextEntries(entries) {
		projected = append(projected, session.SessionEntryToContextMessages(entry)...)
	}
	prep.TokensBefore = EstimateContextTokens(projected).Tokens
	cut := FindCutPoint(compactable, 0, len(compactable), settings.KeepRecentTokens)
	prep.IsSplitTurn = cut.IsSplitTurn
	historyEnd := cut.FirstKeptEntryIndex
	if cut.IsSplitTurn {
		historyEnd = cut.TurnStartIndex
	}
	for i, entry := range compactable {
		message, ok := messageFromEntry(entry)
		if !ok {
			continue
		}
		switch {
		case i < historyEnd:
			prep.MessagesToSummarize = append(prep.MessagesToSummarize, message)
			ExtractFileOpsFromMessage(message, &prep.FileOps)
		case i < cut.FirstKeptEntryIndex:
			prep.TurnPrefixMessages = append(prep.TurnPrefixMessages, message)
			ExtractFileOpsFromMessage(message, &prep.FileOps)
		default:
			prep.RetainedTail = append(prep.RetainedTail, message)
		}
	}
	return prep, nil
}
func restoreFilePaths(value any, add func(string)) {
	switch value := value.(type) {
	case []any:
		for _, path := range value {
			if path, ok := path.(string); ok {
				add(path)
			}
		}
	case []string:
		for _, path := range value {
			add(path)
		}
	}
}
