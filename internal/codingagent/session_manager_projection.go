// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-FileCopyrightText: Copyright (c) 2025 Mario Zechner
// SPDX-License-Identifier: MIT

package codingagent

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// ContextEditReplacement is the non-null replacement of a context_edit entry
// (session-manager.ts ContextEditEntry.replacement). Content is the JSON
// string or content-block array that replaces the target's content
// (ContextEditableContent).
type ContextEditReplacement struct {
	Content json.RawMessage `json:"content"`
}

// ContextEditEntry is an append-only change to one earlier entry's
// contribution to model context (session-manager.ts ContextEditEntry). A nil
// Replacement serializes as null and omits the target from model context; a
// replacement changes only the target's content. The target entry itself is
// never rewritten.
type ContextEditEntry struct {
	SessionEntryBase
	TargetID    string                  `json:"targetId"`
	Replacement *ContextEditReplacement `json:"replacement"`
}

// ProjectedSessionEntry pairs a raw append-only entry with the model-visible
// messages it contributes after context edits (session-manager.ts
// ProjectedSessionEntry). Messages is empty for state-only entries and
// omissions.
type ProjectedSessionEntry struct {
	SourceEntry SessionEntry
	Messages    []agent.AgentMessage
}

// SessionProjection is the provenance-preserving, compaction-aware model
// context of one branch (session-manager.ts SessionProjection).
type SessionProjection struct {
	Entries  []ProjectedSessionEntry
	Messages []agent.AgentMessage
}

// marshalSessionLine encodes one JSONL record the way JSON.stringify does:
// '<', '>', and '&' stay literal, so Pig writes the bytes Pi writes.
func marshalSessionLine(value any) ([]byte, error) {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}

// AppendContextEdit appends a branch-local edit to an earlier model-visible
// entry and returns the new entry ID (session-manager.ts appendContextEdit).
// A nil replacement omits the target. The target must be on the active branch
// and be a user, assistant, or tool-result message entry, or a custom_message
// entry. String replacements for assistant and tool-result targets are stored
// as one text block because those roles require content arrays.
func (s *Session) AppendContextEdit(targetID string, replacement *ContextEditReplacement) (string, error) {
	if replacement != nil && !isContextEditableContent(replacement.Content) {
		return "", errors.New("Context edit replacement must be null or contain string/array content")
	}
	s.mu.RLock()
	target, found := s.byID[targetID]
	onBranch := false
	if found && s.leafID != nil {
		for _, entry := range s.pathToLocked(*s.leafID) {
			if entry.Base.ID == targetID {
				onBranch = true
				break
			}
		}
	}
	s.mu.RUnlock()
	if !found {
		return "", fmt.Errorf("Entry %s not found", targetID)
	}
	if !onBranch {
		return "", fmt.Errorf("Entry %s is not on the active branch", targetID)
	}
	role := contextEditTargetRole(target)
	if role == "" {
		return "", fmt.Errorf("Entry %s does not contribute editable model content", targetID)
	}
	if replacement != nil && (role == agent.RoleAssistant || role == agent.RoleToolResult) {
		normalized, err := textBlockContent(replacement.Content)
		if err != nil {
			return "", err
		}
		replacement = &ContextEditReplacement{Content: normalized}
	}
	id, err := generateEntryID()
	if err != nil {
		return "", err
	}
	entry := ContextEditEntry{
		SessionEntryBase: SessionEntryBase{
			Type:      "context_edit",
			ID:        id,
			ParentID:  s.LeafID(),
			Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		},
		TargetID:    targetID,
		Replacement: replacement,
	}
	if err := s.AppendEntry(entry); err != nil {
		return "", err
	}
	return id, nil
}

// isContextEditableContent reports whether raw is a JSON string or array.
func isContextEditableContent(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || (trimmed[0] != '"' && trimmed[0] != '[') {
		return false
	}
	return json.Valid(trimmed)
}

// textBlockContent converts a JSON string to a one-text-block array and
// returns any other content unchanged.
func textBlockContent(raw json.RawMessage) (json.RawMessage, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '"' {
		return raw, nil
	}
	var text string
	if err := json.Unmarshal(trimmed, &text); err != nil {
		return nil, err
	}
	type textBlock struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	return marshalSessionLine([]textBlock{{Type: "text", Text: text}})
}

// contextEditTargetRole returns the role whose content a context edit may
// replace ("custom" for custom_message entries), or "" when the entry is not
// an editable target.
func contextEditTargetRole(entry SessionEntry) string {
	switch entry.Base.Type {
	case "custom_message":
		return agent.RoleCustom
	case "message":
		var probe struct {
			Message struct {
				Role string `json:"role"`
			} `json:"message"`
		}
		if json.Unmarshal(entry.raw, &probe) != nil {
			return ""
		}
		switch probe.Message.Role {
		case agent.RoleUser, agent.RoleAssistant, agent.RoleToolResult:
			return probe.Message.Role
		}
	}
	return ""
}

// BuildSessionProjection projects a root-to-leaf entry path (session-manager.ts
// buildSessionProjection with the path's final entry as leaf). Compaction
// preparation uses it on SessionManager.getBranch() output.
func BuildSessionProjection(pathEntries []SessionEntry) SessionProjection {
	return buildSessionProjection(pathEntries, SessionEntry.AsMessage)
}

// SessionEntryToContextMessages projects one entry into context messages
// before context edits (session-manager.ts sessionEntryToContextMessages).
// State-only entries project to nothing.
func SessionEntryToContextMessages(entry SessionEntry) []agent.AgentMessage {
	return sessionEntryToContextMessages(entry, SessionEntry.AsMessage)
}

type messageDecoder func(SessionEntry) (MessageEntry, bool)

func buildSessionProjection(path []SessionEntry, decode messageDecoder) SessionProjection {
	contextEntries := buildContextEntries(path, decode)
	edits := make(map[string]ContextEditEntry)
	for _, entry := range contextEntries {
		if entry.Base.Type != "context_edit" {
			continue
		}
		var edit ContextEditEntry
		if json.Unmarshal(entry.raw, &edit) == nil {
			edits[edit.TargetID] = edit
		}
	}
	projection := SessionProjection{Entries: make([]ProjectedSessionEntry, len(contextEntries))}
	for i, entry := range contextEntries {
		projected := ProjectedSessionEntry{SourceEntry: entry}
		// buildContextEntries can retain an older compaction whose ID lies in
		// the newest compaction's kept range. Only the newest compaction, at
		// index zero, contributes a checkpoint and summary.
		if entry.Base.Type != "compaction" || i == 0 {
			var edit *ContextEditEntry
			if found, ok := edits[entry.Base.ID]; ok {
				edit = &found
			}
			projected.Messages = projectContextEntry(entry, sessionEntryToContextMessages(entry, decode), edit)
		}
		projection.Entries[i] = projected
		projection.Messages = append(projection.Messages, projected.Messages...)
	}
	return projection
}

// buildContextEntries selects the compaction-aware entry list for a path
// (session-manager.ts buildContextEntries): the latest compaction, the entries
// from its firstKeptEntryId up to it (system messages excluded), then every
// entry after it.
func buildContextEntries(path []SessionEntry, decode messageDecoder) []SessionEntry {
	compactionIdx := -1
	for i, entry := range path {
		if entry.Base.Type == "compaction" {
			compactionIdx = i
		}
	}
	if compactionIdx < 0 {
		return path
	}
	var compaction struct {
		FirstKeptEntryID string `json:"firstKeptEntryId"`
	}
	_ = json.Unmarshal(path[compactionIdx].raw, &compaction)
	out := make([]SessionEntry, 0, len(path)-compactionIdx)
	out = append(out, path[compactionIdx])
	foundFirstKept := false
	for _, entry := range path[:compactionIdx] {
		if entry.Base.ID == compaction.FirstKeptEntryID {
			foundFirstKept = true
		}
		if foundFirstKept && !isSystemMessageEntry(entry, decode) {
			out = append(out, entry)
		}
	}
	return append(out, path[compactionIdx+1:]...)
}

func isSystemMessageEntry(entry SessionEntry, decode messageDecoder) bool {
	if entry.Base.Type != "message" {
		return false
	}
	message, ok := decode(entry)
	return ok && message.Message.Role() == "system"
}

func sessionEntryToContextMessages(entry SessionEntry, decode messageDecoder) []agent.AgentMessage {
	switch entry.Base.Type {
	case "message":
		if message, ok := decode(entry); ok {
			return []agent.AgentMessage{message.Message.Clone()}
		}
	case "custom_message":
		var custom CustomMessageEntry
		if json.Unmarshal(entry.raw, &custom) != nil {
			return nil
		}
		content := custom.Content
		if content == nil {
			content = []any{}
		}
		message := map[string]any{
			"role":       agent.RoleCustom,
			"customType": custom.CustomType,
			"content":    content,
			"display":    custom.Display,
			"timestamp":  entryTimestampMs(custom.Timestamp),
		}
		if custom.Details != nil {
			message["details"] = custom.Details
		}
		return []agent.AgentMessage{{Custom: message}}
	case "branch_summary":
		var summary BranchSummaryEntry
		if json.Unmarshal(entry.raw, &summary) != nil || summary.Summary == "" {
			return nil
		}
		return []agent.AgentMessage{{Custom: map[string]any{
			"role":      agent.RoleBranchSummary,
			"summary":   summary.Summary,
			"fromId":    summary.FromID,
			"timestamp": entryTimestampMs(summary.Timestamp),
		}}}
	case "compaction":
		var compaction CompactionEntry
		if json.Unmarshal(entry.raw, &compaction) != nil {
			return nil
		}
		summary := agent.AgentMessage{Custom: map[string]any{
			"role":         agent.RoleCompactionSummary,
			"summary":      compaction.Summary,
			"tokensBefore": compaction.TokensBefore,
			"timestamp":    entryTimestampMs(compaction.Timestamp),
		}}
		var system agent.AgentMessage
		if len(compaction.SystemMessage) > 0 && json.Unmarshal(compaction.SystemMessage, &system) == nil {
			return []agent.AgentMessage{system, summary}
		}
		return []agent.AgentMessage{summary}
	case "bash_execution":
		return bashExecutionContextMessages(entry)
	}
	return nil
}

// bashExecutionContextMessages returns a `!cmd` entry's bashExecution message
// as persisted. Provider conversion renders it to text and drops `!!cmd`
// (excludeFromContext) messages, as Pi's convertToLlm does. Entries in Pig's
// legacy flat shape are rebuilt into the same message.
func bashExecutionContextMessages(entry SessionEntry) []agent.AgentMessage {
	var wire struct {
		Message map[string]any `json:"message"`
	}
	if json.Unmarshal(entry.raw, &wire) == nil && wire.Message != nil {
		return []agent.AgentMessage{{Custom: wire.Message}}
	}
	var bash BashExecutionEntry
	if json.Unmarshal(entry.raw, &bash) != nil {
		return nil
	}
	message := map[string]any{
		"role":      agent.RoleBashExecution,
		"command":   bash.Command,
		"output":    bash.Output,
		"exitCode":  nil,
		"cancelled": bash.Cancelled,
		"truncated": bash.Truncated,
	}
	if bash.ExitCode != nil {
		message["exitCode"] = float64(*bash.ExitCode)
	}
	if bash.FullOutputPath != "" {
		message["fullOutputPath"] = bash.FullOutputPath
	}
	if bash.ExcludeFromContext {
		message["excludeFromContext"] = true
	}
	return []agent.AgentMessage{{Custom: message}}
}

func entryTimestampMs(timestamp string) int64 {
	if parsed, err := time.Parse(time.RFC3339Nano, timestamp); err == nil {
		return parsed.UnixMilli()
	}
	return 0
}

// projectContextEntry applies the latest context edit for one entry
// (session-manager.ts projectContextEntry). Only user, assistant, tool-result,
// and custom messages take replacement content; other roles pass through.
func projectContextEntry(entry SessionEntry, messages []agent.AgentMessage, edit *ContextEditEntry) []agent.AgentMessage {
	if edit == nil {
		return messages
	}
	if edit.Replacement == nil {
		return nil
	}
	out := make([]agent.AgentMessage, len(messages))
	for i, message := range messages {
		out[i] = replaceContextContent(message, edit.Replacement.Content)
	}
	return out
}

func replaceContextContent(message agent.AgentMessage, content json.RawMessage) agent.AgentMessage {
	if message.Custom != nil {
		if role, _ := message.Custom["role"].(string); role != agent.RoleCustom {
			return message
		}
		var value any
		if json.Unmarshal(content, &value) != nil {
			return message
		}
		custom := maps.Clone(message.Custom)
		custom["content"] = value
		return agent.AgentMessage{Custom: custom}
	}
	raw, err := json.Marshal(message)
	if err != nil {
		return message
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return message
	}
	fields["content"] = content
	merged, err := json.Marshal(fields)
	if err != nil {
		return message
	}
	var replaced agent.AgentMessage
	if json.Unmarshal(merged, &replaced) != nil {
		return message
	}
	return replaced
}

// SystemMessageFromAgentMessage reads a projected system-role message.
func SystemMessageFromAgentMessage(message agent.AgentMessage) (ai.SystemMessage, bool) {
	if message.System == nil {
		return ai.SystemMessage{}, false
	}
	return *message.System, true
}

// CurrentSystemMessage replays every projected system message into the
// current prompt and tool state (pi-ai getCurrentSystemMessage).
func CurrentSystemMessage(messages []agent.AgentMessage) *ai.SystemMessage {
	var systems []ai.Message
	for _, message := range messages {
		if system, ok := SystemMessageFromAgentMessage(message); ok {
			systems = append(systems, system)
		}
	}
	return ai.GetCurrentSystemMessage(systems)
}

// compactionSystemMessage returns the JSON of the current projected system
// state stamped with the compaction time, or nil when there is none.
func compactionSystemMessage(messages []agent.AgentMessage, timestampMs int64) (json.RawMessage, error) {
	current := CurrentSystemMessage(messages)
	if current == nil {
		return nil, nil
	}
	text, _ := current.Content.(ai.SystemText)
	// Key order follows Pi's getCurrentSystemMessage object literal.
	raw, err := marshalSessionLine(struct {
		Role       string             `json:"role"`
		Content    string             `json:"content"`
		Sections   ai.OrderedSections `json:"sections,omitempty"`
		ToolsAdded []ai.ToolSchema    `json:"toolsAdded,omitempty"`
		Timestamp  int64              `json:"timestamp"`
	}{Role: "system", Content: string(text), Sections: current.Sections, ToolsAdded: current.ToolsAdded, Timestamp: timestampMs})
	if err != nil {
		return nil, fmt.Errorf("session: marshal compaction system message: %w", err)
	}
	return raw, nil
}
