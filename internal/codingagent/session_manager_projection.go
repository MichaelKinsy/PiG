// SPDX-FileCopyrightText: Copyright (c) 2025 Mario Zechner
// SPDX-License-Identifier: MIT

package codingagent

import (
	"bytes"
	"errors"
	"fmt"
	"math"

	"github.com/MichaelKinsy/PiG/internal/jsstring"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
	"github.com/MichaelKinsy/PiG/internal/orderedjson"
)

// marshalSessionLine encodes one JSONL record the way JSON.stringify does ('<', '>', '&' and the line and paragraph separators stay literal, a
// negative zero is 0), so Pig writes the bytes Pi writes. The session keeps a message entry's record for its life, so the record holds no
// spare room: a line is encoded in the encoder's pooled buffer and copied out at its length. A message entry whose text outgrows that buffer
// is encoded into one buffer sized for its text and its escapes instead, where the room reserved beyond the record is a small part of it: a
// buffer the encoder outgrows is replaced by one of twice its size, which the session would keep.
func marshalSessionLine(value any) ([]byte, error) {
	if entry, ok := value.(MessageEntry); ok {
		text := entry.Message.TextBytes()
		if size := text + sessionLineOverhead; size > sessionLinePooledMax {
			return jsstring.AppendMarshalJSON(make([]byte, 0, size+text/sessionLineEscapeShare), entry)
		}
	}
	return jsstring.MarshalJSON(value)
}

// sessionLineOverhead is the room a message entry's record takes beyond its message's text: the entry's members and the message's metadata.
const sessionLineOverhead = 1 << 10

// sessionLineEscapeShare sizes the room a large record reserves for its text's escapes, one byte in eight, as the encoder reserves for one
// long string (extensions/sdk/json stringEncoder): source text escapes a newline, tab or quote every few dozen bytes.
const sessionLineEscapeShare = 8

// sessionLinePooledMax is the largest buffer a message entry's record is encoded in through the encoder's pool (the pool keeps buffers up to
// 64 KiB, extensions/sdk/json maxPooledEncodeBuffer). Beyond it a sized buffer reserves at most an eighth of the record beyond it.
const sessionLinePooledMax = 64 << 10

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
	targetIdx, found := s.slotOf(targetID)
	var target SessionEntry
	onBranch := false
	if found {
		target = s.entryAtLocked(targetIdx)
		if s.leafID != nil {
			for _, idx := range s.pathIndexesLocked(*s.leafID) {
				if s.slots[idx].id == targetID {
					onBranch = true
					break
				}
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
	s.leafAppendMu.Lock()
	defer s.leafAppendMu.Unlock()
	id, err := s.generateEntryID()
	if err != nil {
		return "", err
	}
	entry := ContextEditEntry{
		SessionEntryBase: SessionEntryBase{
			Type:      "context_edit",
			ID:        id,
			ParentID:  s.GetLeafID(),
			Timestamp: RFC3339NowNano(),
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
func isContextEditableContent(raw ContextEditableContent) bool {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || (trimmed[0] != '"' && trimmed[0] != '[') {
		return false
	}
	return json.Valid(trimmed)
}

// textBlockContent converts a JSON string to a one-text-block array and
// returns any other content unchanged.
func textBlockContent(raw ContextEditableContent) (ContextEditableContent, error) {
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
	switch entry.Base().Type {
	case "custom_message":
		return agent.RoleCustom
	case "message":
		var probe struct {
			Message struct {
				Role string `json:"role"`
			} `json:"message"`
		}
		if json.Unmarshal(entry.Raw(), &probe) != nil {
			return ""
		}
		switch probe.Message.Role {
		case agent.RoleUser, agent.RoleAssistant, agent.RoleToolResult:
			return probe.Message.Role
		}
	}
	return ""
}

// BuildSessionProjection projects the branch of entries that ends at leafID: the compaction-aware entries, their model-visible messages, and the latest thinking level and model on the branch. It ports session-manager.ts buildSessionProjection with [BuildSessionContext]'s leaf rules: no leafID selects the last entry, an unknown ID falls back to it, and an explicit nil is an empty branch. For a branch already walked from root to leaf, use [ProjectSessionPath].
func BuildSessionProjection(entries []SessionEntry, leafID *string, byID map[string]SessionEntry) SessionProjection {
	return buildSessionProjection(buildSessionPath(entries, leafID, byID), asMessage, true)
}

// ProjectSessionPath projects a root-to-leaf entry path as it stands, without walking parent links again. Compaction preparation uses it on SessionManager.getBranch() output (compaction.ts prepareCompaction passes that path to buildSessionProjection, which returns the same projection for a chained path).
func ProjectSessionPath(path []SessionEntry) SessionProjection {
	return buildSessionProjection(path, asMessage, true)
}

// SessionEntryToContextMessages projects one entry into context messages
// before context edits (session-manager.ts sessionEntryToContextMessages).
// State-only entries project to nothing.
func SessionEntryToContextMessages(entry SessionEntry) []agent.AgentMessage {
	return sessionEntryToContextMessages(entry, asMessage, true)
}

type messageDecoder func(SessionEntry) (MessageEntry, bool)

// buildSessionProjection projects a path. Its messages are copies the caller owns when clone is set, and the entries' own messages, to be read and never modified, when it is not.
func buildSessionProjection(path []SessionEntry, decode messageDecoder, clone bool) SessionProjection {
	p := newSessionProjector(path, decode, clone, 0)
	return p.projection
}

// sessionProjector builds a projection entry by entry, so a projection of a path can be extended by entries appended to the path's leaf.
type sessionProjector struct {
	decode messageDecoder
	clone  bool
	// edits holds the latest context_edit of each target in the projected entries.
	edits      map[string]ContextEditEntry
	projection SessionProjection
	// slab is the backing array the one-message slices of message entries are carved from.
	slab []agent.AgentMessage
	// thinkingFound and modelFound report that the projection's ThinkingLevel and Model come from an entry of the path.
	thinkingFound, modelFound bool
}

// newSessionProjector projects path, with room for spare more message entries in its slab.
func newSessionProjector(path []SessionEntry, decode messageDecoder, clone bool, spare int) *sessionProjector {
	contextEntries := buildContextEntries(path, decode)
	p := &sessionProjector{decode: decode, clone: clone, edits: make(map[string]ContextEditEntry)}
	hasContextEdits := false
	for _, entry := range contextEntries {
		if entry.Base().Type != "context_edit" {
			continue
		}
		hasContextEdits = true
		var edit ContextEditEntry
		if json.Unmarshal(entry.Raw(), &edit) == nil {
			p.edits[edit.TargetID] = edit
		}
	}
	p.projection = SessionProjection{Entries: make([]ProjectedSessionEntry, 0, len(contextEntries)+spare), Messages: make([]agent.AgentMessage, 0, len(contextEntries)+spare), HasContextEdits: hasContextEdits}
	// One backing array holds the one-message slices of the message entries, so a projection of n entries allocates one slab and not n slices. Each slice is capped at its length: an append to one cannot overwrite its neighbor.
	p.slab = make([]agent.AgentMessage, 0, len(contextEntries)+spare)
	p.projection.ThinkingLevel, p.thinkingFound, p.projection.Model, p.modelFound = sessionContextSettings(path)
	for i, entry := range contextEntries {
		p.add(entry, i == 0)
	}
	return p
}

// add projects entry after the projected ones; first reports that it is the first context entry.
func (p *sessionProjector) add(entry SessionEntry, first bool) {
	projected := ProjectedSessionEntry{SourceEntry: entry}
	// buildContextEntries can retain an older compaction whose ID lies in
	// the newest compaction's kept range. Only the newest compaction, at
	// index zero, contributes a checkpoint and summary.
	if entry.Base().Type != "compaction" || first {
		var edit *ContextEditEntry
		if found, ok := p.edits[entry.Base().ID]; ok {
			edit = &found
		}
		messages := sessionEntryToContextMessagesInto(entry, p.decode, p.clone, &p.slab)
		projected.Messages = projectContextEntry(entry, messages, edit)
	}
	p.projection.Entries = append(p.projection.Entries, projected)
	p.projection.Messages = append(p.projection.Messages, projected.Messages...)
}

// extend projects entries appended, in order, to the projected path's leaf, none of them a compaction (a compaction restarts the context
// entries, so the caller projects the path again), and reports false when one of them is a context edit, which replaces an earlier
// entry's messages: the path must then be projected again too. The projected settings follow the appended entries as
// getSessionContextSettings' backward walk reads them.
func (p *sessionProjector) extend(entries []SessionEntry) bool {
	for _, entry := range entries {
		if entry.Base().Type == "context_edit" {
			return false
		}
	}
	if spare := cap(p.slab) - len(p.slab); spare < len(entries) {
		p.slab = make([]agent.AgentMessage, 0, max(len(entries), len(p.projection.Messages)/4, 16))
	}
	for _, entry := range entries {
		p.add(entry, len(p.projection.Entries) == 0)
		if thinking, thinkingFound, model, modelFound := sessionContextSettings([]SessionEntry{entry}); thinkingFound || modelFound {
			if thinkingFound {
				p.projection.ThinkingLevel, p.thinkingFound = thinking, true
			}
			if modelFound {
				p.projection.Model, p.modelFound = model, true
			}
		}
	}
	return true
}

// view is the projection with its slices capped at their length, so a caller's append cannot write into the projector's spare
// capacity, and its own copy of the model.
func (p *sessionProjector) view() SessionProjection {
	out := p.projection
	out.Entries = out.Entries[:len(out.Entries):len(out.Entries)]
	out.Messages = out.Messages[:len(out.Messages):len(out.Messages)]
	if out.Model != nil {
		model := *out.Model
		out.Model = &model
	}
	return out
}

// buildContextEntries selects the compaction-aware entry list for a path
// (session-manager.ts buildContextEntries): the latest compaction, the entries
// from its firstKeptEntryId up to it (system messages excluded), then every
// entry after it.
func buildContextEntries(path []SessionEntry, decode messageDecoder) []SessionEntry {
	compactionIdx := -1
	for i, entry := range path {
		if entry.Base().Type == "compaction" {
			compactionIdx = i
		}
	}
	if compactionIdx < 0 {
		return path
	}
	var compaction struct {
		FirstKeptEntryID string `json:"firstKeptEntryId"`
	}
	// A decoded compaction is read as decoded: one held without its record would encode it for the read.
	if decoded, ok := path[compactionIdx].(CompactionEntry); ok {
		compaction.FirstKeptEntryID = decoded.FirstKeptEntryID
	} else {
		_ = json.Unmarshal(path[compactionIdx].Raw(), &compaction)
	}
	out := make([]SessionEntry, 0, len(path)-compactionIdx)
	out = append(out, path[compactionIdx])
	foundFirstKept := false
	for _, entry := range path[:compactionIdx] {
		if entry.Base().ID == compaction.FirstKeptEntryID {
			foundFirstKept = true
		}
		if foundFirstKept && !isSystemMessageEntry(entry, decode) {
			out = append(out, entry)
		}
	}
	return append(out, path[compactionIdx+1:]...)
}

func isSystemMessageEntry(entry SessionEntry, decode messageDecoder) bool {
	if entry.Base().Type != "message" {
		return false
	}
	message, ok := decode(entry)
	return ok && message.Message.Role() == "system"
}

func sessionEntryToContextMessages(entry SessionEntry, decode messageDecoder, clone bool) []agent.AgentMessage {
	return sessionEntryToContextMessagesInto(entry, decode, clone, nil)
}

// sessionEntryToContextMessagesInto is sessionEntryToContextMessages that carves the one-message result of a message entry from slab when slab has room.
func sessionEntryToContextMessagesInto(entry SessionEntry, decode messageDecoder, clone bool, slab *[]agent.AgentMessage) []agent.AgentMessage {
	one := func(message agent.AgentMessage) []agent.AgentMessage {
		if slab == nil || len(*slab) == cap(*slab) {
			return []agent.AgentMessage{message}
		}
		*slab = append(*slab, message)
		last := len(*slab) - 1
		return (*slab)[last : last+1 : last+1]
	}
	switch entry.Base().Type {
	case "message":
		if message, ok := decode(entry); ok {
			// A hand-edited or old file can hold a message with null or missing content; Pi projects { ...message, content: [] } (session-manager.ts sessionEntryToContextMessages).
			if message.Message.NullContent() {
				return one(message.Message.WithEmptyContent())
			}
			if !clone {
				return one(message.Message)
			}
			return one(message.Message.Clone())
		}
	case "custom_message":
		var custom CustomMessageEntry
		if json.Unmarshal(entry.Raw(), &custom) != nil {
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
		if json.Unmarshal(entry.Raw(), &summary) != nil || summary.Summary == "" {
			return nil
		}
		return []agent.AgentMessage{{Custom: map[string]any{
			"role":      agent.RoleBranchSummary,
			"summary":   summary.Summary,
			"fromId":    summary.FromID,
			"timestamp": entryTimestampMs(summary.Timestamp),
		}}}
	case "compaction":
		compaction, ok := entry.(CompactionEntry)
		if !ok && json.Unmarshal(entry.Raw(), &compaction) != nil {
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
	if json.Unmarshal(entry.Raw(), &wire) == nil && wire.Message != nil {
		return []agent.AgentMessage{{Custom: wire.Message}}
	}
	var bash BashExecutionEntry
	if json.Unmarshal(entry.Raw(), &bash) != nil {
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
	if bash.ExcludeFromContext != nil && *bash.ExcludeFromContext {
		message["excludeFromContext"] = true
	}
	return []agent.AgentMessage{{Custom: message}}
}

// entryTimestampMs is new Date(timestamp).getTime() (messages.ts:105-136). A timestamp Date cannot read is NaN there, which JSON.stringify writes as
// null; the integer here is 0.
func entryTimestampMs(timestamp string) int64 {
	if ms := ai.DateParse(timestamp); !math.IsNaN(ms) {
		return int64(ms)
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

// replaceContextContent is session-manager.ts applyContextEditReplacement: { ...message, content } for user, assistant, toolResult and custom messages, with string content given as one text block to an assistant or toolResult message. The content member keeps its place in the message's object.
func replaceContextContent(message agent.AgentMessage, content json.RawMessage) agent.AgentMessage {
	var role string
	switch {
	case message.User != nil:
		role = agent.RoleUser
	case message.Assistant != nil:
		role = agent.RoleAssistant
	case message.ToolResult != nil:
		role = agent.RoleToolResult
	case message.Custom != nil:
		role, _ = message.Custom["role"].(string)
	}
	switch role {
	case agent.RoleUser, agent.RoleCustom:
	case agent.RoleAssistant, agent.RoleToolResult:
		var text string
		if json.Unmarshal(content, &text) == nil {
			block := orderedjson.New()
			block.Set("type", json.RawMessage(`"text"`))
			block.Set("text", content)
			encoded, err := block.MarshalJSON()
			if err != nil {
				return message
			}
			content = json.RawMessage("[" + string(encoded) + "]")
		}
	default:
		return message
	}
	raw, err := json.Marshal(message)
	if err != nil {
		return message
	}
	fields, err := orderedjson.Parse(raw)
	if err != nil {
		return message
	}
	fields.Set("content", content)
	merged, err := fields.MarshalJSON()
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
