// Type-aware /tree row rendering.
//
// Mirrors upstream tree-selector.ts:
//   - getEntryDisplayText  (.upstream :708-808)
//   - formatToolCall       (.upstream :849-895)
//
// Pure presentation; selection / navigation / fork behaviour is
// unchanged. Connector glyphs (├─ / └─ / │ ) are still produced by
// tui.TreeSelectorComponent.flatten: this file only produces the trailing
// label string.
//
// Why it lives in internal/codingagent rather than tui:
// internal/codingagent already imports tui (the modal
// selector wiring); putting the formatter in tui would force tui →
// codingagent for SessionEntry → import cycle.
//
// Assistant aborted/error states render explicitly. Unknown message roles use
// upstream's `[<role>]` fallback.

package codingagent

import (
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
	"github.com/MichaelKinsy/PiG/internal/jsonstringify"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/tui"
)

func fg(color, s string) string {
	if color == "" {
		return s
	}
	return color + s + tui.FgClose(color)
}

// treeRowFormatter renders a SessionEntry into the single-line
// label shown in the /tree picker. Built once per /tree open; the
// toolCallMap is a pre-walk of every assistant tool_use content
// block so tool_result messages can be rendered via their parent
// call's name + arguments (mirrors upstream toolCallMap at
// tree-selector.ts:733-740).
type treeRowFormatter struct {
	toolCallMap map[string]toolCallInfo
	home        string

	// leafID is the current session leaf at /tree open time. Used by
	// shouldSuppress (3.1d-j) so the active leaf is always shown,
	// even when it would otherwise qualify for tool-call-only
	// suppression. Mirrors upstream `currentLeafId` checked at
	// tree-selector.ts:289.
	leafID string

	// sess, when set, counts the message entries that did not decode
	// (Session.UndecodableCount). Nil in unit tests that format standalone
	// entries and for a formatter built from a tree.
	sess *Session
}

type toolCallInfo struct {
	call ai.ToolCall
}

func newTreeRowFormatter(s *Session) *treeRowFormatter {
	home := os.Getenv("HOME")
	if home == "" {
		home = os.Getenv("USERPROFILE")
	}
	f := &treeRowFormatter{
		toolCallMap: make(map[string]toolCallInfo),
		home:        home,
		sess:        s,
	}
	if s == nil {
		return f
	}
	if lid := s.GetLeafID(); lid != nil {
		f.leafID = *lid
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, e := range s.entries {
		if e.Base().Type != "message" {
			continue
		}
		me, ok := f.asMessage(e)
		if !ok {
			continue
		}
		for _, blk := range me.Message.ContentBlocks() {
			call, ok := blk.(ai.ToolCall)
			if !ok || call.ID == "" {
				continue
			}
			f.toolCallMap[call.ID] = toolCallInfo{call: call}
		}
	}
	return f
}

// newTreeRowFormatterFromTree builds the formatter from the tree alone, as Pi's TreeSelectorComponent does: the tool-call map holds every
// tool call of every message entry in the tree (tree-selector.ts:255-264) and the current leaf is always shown (tree-selector.ts:289).
func newTreeRowFormatterFromTree(tree []*SessionTreeNode, currentLeafID *string) *treeRowFormatter {
	f := newTreeRowFormatter(nil)
	if currentLeafID != nil {
		f.leafID = *currentLeafID
	}
	var walk func(nodes []*SessionTreeNode)
	walk = func(nodes []*SessionTreeNode) {
		for _, node := range nodes {
			if node.Entry.Base().Type == "message" {
				if me, ok := node.Entry.(MessageEntry); ok {
					for _, blk := range me.Message.ContentBlocks() {
						if call, ok := blk.(ai.ToolCall); ok && call.ID != "" {
							f.toolCallMap[call.ID] = toolCallInfo{call: call}
						}
					}
				}
			}
			walk(node.Children)
		}
	}
	walk(tree)
	return f
}

// shouldSuppressInTree reports whether `e` should be hidden from
// the /tree picker per the unconditional pre-filter at upstream
// tree-selector.ts:287-296: assistant messages whose content has no
// text blocks (only tool_use) are hidden, EXCEPT the row that IS
// the current leaf, OR the row is errored/aborted (which must stay
// visible per upstream isErrorOrAborted escape clause). WireMessage supplies
// StopReason so the escape clause is active.
func (f *treeRowFormatter) shouldSuppressInTree(e SessionEntry) bool {
	if e.Base().Type != "message" {
		return false
	}
	me, ok := f.asMessage(e)
	if !ok {
		return false
	}
	if me.Message.Assistant == nil {
		return false
	}
	// Always show the current leaf.
	if f.leafID != "" && e.Base().ID == f.leafID {
		return false
	}
	// Upstream isErrorOrAborted escape: aborted/errored turns stay visible
	// even when their content is empty (tree-selector.ts:289-291).
	if r := me.Message.Assistant.StopReason; r == "error" || r == "aborted" {
		return false
	}
	// Hide if no text content (only tool_use blocks, or empty).
	if strings.TrimSpace(extractMessageText(me)) == "" {
		return true
	}
	return false
}

// FormatTreeRow returns the upstream-style label for e.
func (f *treeRowFormatter) asMessage(e SessionEntry) (MessageEntry, bool) {
	if f.sess != nil {
		return f.sess.messageFor(e)
	}
	return asMessage(e)
}

func (f *treeRowFormatter) FormatTreeRow(e SessionEntry) string {
	switch e.Base().Type {
	case "message":
		return f.formatMessage(e)

	case "model_change":
		var ent struct {
			ModelID string `json:"modelId"`
		}
		_ = json.Unmarshal(e.Raw(), &ent)
		return fg(tui.ActiveTheme().Dim, "[model: "+ent.ModelID+"]")

	case "thinking_level_change":
		var ent struct {
			ThinkingLevel string `json:"thinkingLevel"`
		}
		_ = json.Unmarshal(e.Raw(), &ent)
		return fg(tui.ActiveTheme().Dim, "[thinking: "+ent.ThinkingLevel+"]")

	case "compaction":
		var ent CompactionEntry
		_ = json.Unmarshal(e.Raw(), &ent)
		k := int(math.Round(float64(ent.TokensBefore) / 1000))
		return fg(tui.ActiveTheme().BorderAccent, fmt.Sprintf("[compaction: %dk tokens]", k))

	case "branch_summary":
		var ent BranchSummaryEntry
		_ = json.Unmarshal(e.Raw(), &ent)
		return fg(tui.ActiveTheme().Warning, "[branch summary]: ") + normalizeText(ent.Summary)

	case "label":
		var ent LabelEntry
		_ = json.Unmarshal(e.Raw(), &ent)
		text := "(cleared)"
		if ent.Label != nil {
			text = *ent.Label
		}
		return fg(tui.ActiveTheme().Dim, "[label: "+text+"]")

	case "custom":
		var ent CustomEntry
		_ = json.Unmarshal(e.Raw(), &ent)
		return fg(tui.ActiveTheme().Dim, "[custom: "+ent.CustomType+"]")

	case "context_edit":
		mode, targetID := contextEditSummary(e)
		return fg(tui.ActiveTheme().Dim, "[context "+mode+": "+targetID+"]")

	case "custom_message":
		var ent CustomMessageEntry
		_ = json.Unmarshal(e.Raw(), &ent)
		return fg(tui.ActiveTheme().CustomMessageLabel, "["+ent.CustomType+"]: ") + normalizeText(extractCustomMessageText(ent))

	case "session_info":
		var ent SessionInfoEntry
		_ = json.Unmarshal(e.Raw(), &ent)
		if ent.Name == "" {
			return fg(tui.ActiveTheme().Dim, "[title: empty]")
		}
		return fg(tui.ActiveTheme().Dim, "[title: "+ent.Name+"]")

	case "bash_execution":
		// Mirrors upstream tree-selector.ts label for bash entries.
		var ent BashExecutionEntry
		_ = json.Unmarshal(e.Raw(), &ent)
		cmd := ent.Command
		if len(cmd) > 50 {
			cmd = cmd[:50] + "…"
		}
		prefix := "!"
		if ent.ExcludeFromContext {
			prefix = "!!"
		}
		return fg(tui.ActiveTheme().Dim, "[bash: "+prefix+cmd+"]")

	default:
		return ""
	}
}

// contextEditSummary reads a context_edit entry as upstream's tree selector
// does: a null replacement omits the target from model context ("omit");
// any other value replaces its content ("replace").
func contextEditSummary(e SessionEntry) (mode, targetID string) {
	var ent struct {
		TargetID    string          `json:"targetId"`
		Replacement json.RawMessage `json:"replacement"`
	}
	_ = json.Unmarshal(e.Raw(), &ent)
	if string(ent.Replacement) == "null" {
		return "omit", ent.TargetID
	}
	return "replace", ent.TargetID
}

func (f *treeRowFormatter) formatMessage(e SessionEntry) string {
	me, ok := f.asMessage(e)
	if !ok {
		return "[<unknown:message>]"
	}
	role := me.Message.Role()

	// Tool-result rows render through the matching parent tool call.
	if me.Message.ToolResult != nil {
		if call, ok := f.toolCallMap[me.Message.ToolResult.ToolCallID]; ok {
			return f.formatToolCall(call.call)
		}
		// tree-selector.ts: `[${toolMsg.toolName ?? "tool"}]`; an absent name decodes to "".
		toolName := me.Message.ToolResult.ToolName
		if toolName == "" {
			toolName = "tool"
		}
		return fg(tui.ActiveTheme().Muted, "["+toolName+"]")
	}

	text := normalizeText(extractMessageText(me))
	th := tui.ActiveTheme()
	switch role {
	case "user":
		return fg(th.Accent, "user: ") + text
	case "assistant":
		asstLabel := fg(th.Success, "assistant: ")
		// Render stopReason variants: mirrors upstream tree-selector.ts:719-731.
		switch me.Message.Assistant.StopReason {
		case "aborted":
			if text == "" {
				return asstLabel + fg(th.Muted, "(aborted)")
			}
			return asstLabel + fg(th.Muted, "(aborted) ") + text
		case "error":
			if me.Message.Assistant.ErrorMessage != "" {
				return asstLabel + fg(th.Error, normalizeText(me.Message.Assistant.ErrorMessage))
			}
			if text == "" {
				return asstLabel + fg(th.Error, "(error)")
			}
			return asstLabel + text
		}
		if text == "" {
			return asstLabel + fg(th.Muted, "(no content)")
		}
		return asstLabel + text
	case "bashExecution":
		command, _ := me.Message.Custom["command"].(string)
		command = strings.NewReplacer("\n", " ", "\t", " ").Replace(command)
		return fg(th.Dim, "[bash]: "+jsTrim(command))
	default:
		// Unknown roles use upstream's `[<role>]` fallback.
		return fg(th.Dim, "["+role+"]")
	}
}

func (f *treeRowFormatter) formatToolCall(call ai.ToolCall) string {
	name, args := call.Name, map[string]any(call.Arguments)
	muted := tui.ActiveTheme().Muted
	switch name {
	case "read":
		path := f.shortenPath(strArg(args, "path", "file_path"))
		display := path
		offset, hasOff := intArg(args, "offset")
		limit, hasLim := intArg(args, "limit")
		if hasOff || hasLim {
			start := offset
			if !hasOff {
				start = 1
			}
			display += ":" + strconv.Itoa(start)
			if hasLim {
				display += "-" + strconv.Itoa(start+limit-1)
			}
		}
		return fg(muted, "[read: "+display+"]")

	case "write":
		return fg(muted, "[write: "+f.shortenPath(strArg(args, "path", "file_path"))+"]")

	case "edit":
		return fg(muted, "[edit: "+f.shortenPath(strArg(args, "path", "file_path"))+"]")

	case "bash":
		raw := strArg(args, "command")
		cmd := strings.ReplaceAll(raw, "\n", " ")
		cmd = strings.ReplaceAll(cmd, "\t", " ")
		cmd = strings.TrimSpace(cmd)
		if len(cmd) > 50 {
			cmd = cmd[:50]
		}
		// Upstream computes ellipsis from RAW length, not normalized
		// (tree-selector.ts:874): `rawCmd.length > 50 ? "..." : ""`.
		ell := ""
		if len(raw) > 50 {
			ell = "..."
		}
		return fg(muted, "[bash: "+cmd+ell+"]")

	case "grep":
		pattern := strArg(args, "pattern")
		path := f.shortenPath(strArg(args, "path"))
		if path == "" {
			path = "."
		}
		return fg(muted, "[grep: /"+pattern+"/ in "+path+"]")

	case "find":
		pattern := strArg(args, "pattern")
		path := f.shortenPath(strArg(args, "path"))
		if path == "" {
			path = "."
		}
		return fg(muted, "[find: "+pattern+" in "+path+"]")

	case "ls":
		path := f.shortenPath(strArg(args, "path"))
		if path == "" {
			path = "."
		}
		return fg(muted, "[ls: "+path+"]")

	default:
		// Custom tool: JSON.stringify(args).slice(0, 40), with "..." when the whole text is longer (tree-selector.ts:996-999).
		// JSON.stringify keeps the model's member order and leaves <, > and & unescaped, and both lengths count UTF-16 units.
		s := "{}"
		if ordered, err := call.ArgumentsJSON(); err == nil {
			if canonical, err := jsonstringify.Canonicalize(ordered); err == nil {
				s = string(canonical)
			}
		}
		ell := ""
		if jsstring.Length(s) > 40 {
			ell = "..."
		}
		return fg(muted, "["+name+": "+jsstring.Slice(s, 0, 40)+ell+"]")
	}
}

// shortenPath replaces $HOME prefix with "~" (tree-selector.ts:851).
func (f *treeRowFormatter) shortenPath(p string) string {
	if f.home != "" && strings.HasPrefix(p, f.home) {
		return "~" + p[len(f.home):]
	}
	return p
}

// strArg returns the first string-valued arg matching any of `keys`,
// or "" when none are present / are non-strings / are empty.
func strArg(args map[string]any, keys ...string) string {
	for _, k := range keys {
		v, ok := args[k]
		if !ok {
			continue
		}
		if s, ok := v.(string); ok && s != "" {
			return s
		}
	}
	return ""
}

func intArg(args map[string]any, key string) (int, bool) {
	v, ok := args[key]
	if !ok {
		return 0, false
	}
	switch n := v.(type) {
	case float64:
		return int(n), true
	case int:
		return n, true
	case int64:
		return int(n), true
	}
	return 0, false
}

// extractCustomMessageText pulls visible text out of a
// CustomMessageEntry whose Content is either a string or a
// []ContentBlock (mirrors tree-selector.ts:721-728).
func extractCustomMessageText(c CustomMessageEntry) string {
	if s, ok := c.Content.(string); ok {
		return s
	}
	raw, err := json.Marshal(c.Content)
	if err != nil {
		return ""
	}
	var blocks []json.RawMessage
	if json.Unmarshal(raw, &blocks) != nil {
		return ""
	}
	var b strings.Builder
	for _, blk := range blocks {
		var head struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if json.Unmarshal(blk, &head) == nil && head.Type == "text" {
			b.WriteString(head.Text)
		}
	}
	return b.String()
}

// normalizeText replaces \n\t with spaces, trims, truncates to 200
// chars. Mirrors upstream `normalize` + extractContent maxLen
// (tree-selector.ts:707, :812).
func normalizeText(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\t", " ")
	s = strings.TrimSpace(s)
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}
