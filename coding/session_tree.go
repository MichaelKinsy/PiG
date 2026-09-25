package coding

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// treeNavigationTarget returns the leaf navigateTree moves to and the text it
// hands back to the editor. A user message or custom message target moves the
// leaf to its parent (nil for a root entry) and returns its text; any other
// entry becomes the leaf itself (agent-session.ts navigateTree).
func treeNavigationTarget(entry icodingagent.SessionEntry) (newLeafID *string, editorText string) {
	if message, ok := entry.AsMessage(); ok && message.Message.User != nil {
		var text strings.Builder
		for _, block := range message.Message.User.Content {
			if textBlock, ok := block.(ai.TextContent); ok {
				text.WriteString(textBlock.Text)
			}
		}
		return entry.Base.ParentID, text.String()
	}
	if entry.Base.Type == "custom_message" {
		return entry.Base.ParentID, customMessageEditorText(entry.Raw())
	}
	id := entry.Base.ID
	return &id, ""
}

// customMessageEditorText mirrors contentText(entry.content, ""): a string
// content is returned as is, and block content joins its text blocks.
func customMessageEditorText(raw json.RawMessage) string {
	var wire struct {
		Content json.RawMessage `json:"content"`
	}
	if json.Unmarshal(raw, &wire) != nil {
		return ""
	}
	var text string
	if json.Unmarshal(wire.Content, &text) == nil {
		return text
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(wire.Content, &blocks) != nil {
		return ""
	}
	var joined strings.Builder
	for _, block := range blocks {
		if block.Type == "text" {
			joined.WriteString(block.Text)
		}
	}
	return joined.String()
}

// treeBranchSummary is the summary navigateTree attaches at the new leaf.
type treeBranchSummary struct {
	Summary       string
	Details       any
	Usage         *ai.Usage
	FromExtension bool
}

// moveTreeLeaf applies a tree navigation to the session manager. With a
// non-empty summary it branches at newLeafID with a branch_summary entry and
// labels that entry; otherwise it moves the leaf (nil resets it to the root)
// and labels the target. It returns the summary entry id, if one was created.
// The caller holds s.mu.
func (s *Session) moveTreeLeaf(targetID string, newLeafID *string, summary *treeBranchSummary, label string) (string, error) {
	if summary != nil && summary.Summary != "" {
		summaryID, err := s.inner.AppendBranchSummary(newLeafID, summary.Summary, summary.Details, summary.FromExtension, summary.Usage)
		if err != nil {
			return "", err
		}
		if label != "" {
			if err := s.inner.AppendLabelChange(summaryID, &label); err != nil {
				return summaryID, err
			}
		}
		return summaryID, nil
	}
	if err := s.inner.SetLeafID(newLeafID); err != nil {
		return "", err
	}
	if label != "" {
		return "", s.inner.AppendLabelChange(targetID, &label)
	}
	return "", nil
}

func derefLeafID(id *string) string {
	if id == nil {
		return ""
	}
	return *id
}

// branchSummaryEntry decodes the branch_summary entry with the given id, or
// returns nil when id is empty or names no branch summary.
func (s *Session) branchSummaryEntry(id string) *BranchSummaryEntry {
	if id == "" {
		return nil
	}
	entry, ok := s.inner.EntryByID(id)
	if !ok || entry.Base.Type != "branch_summary" {
		return nil
	}
	var summary BranchSummaryEntry
	if json.Unmarshal(entry.Raw(), &summary) != nil {
		return nil
	}
	return &summary
}

// TreePreparation is the session_before_tree preparation (extensions
// types.ts TreePreparation). Extensions receive it as
// SessionBeforeTreeEvent.Preparation.
type TreePreparation struct {
	TargetID           string                      `json:"targetId"`
	OldLeafID          *string                     `json:"oldLeafId"`
	CommonAncestorID   *string                     `json:"commonAncestorId"`
	EntriesToSummarize []icodingagent.SessionEntry `json:"entriesToSummarize"`
	UserWantsSummary   bool                        `json:"userWantsSummary"`
	// CustomInstructions are the custom summarization instructions.
	CustomInstructions string `json:"customInstructions,omitempty"`
	// ReplaceInstructions makes CustomInstructions replace the default prompt.
	ReplaceInstructions bool `json:"replaceInstructions,omitempty"`
	// Label is the label to attach to the branch summary entry.
	Label string `json:"label,omitempty"`
}

// sessionBeforeTreeResult is a decoded session_before_tree handler result.
// Nil pointers are fields the extension left undefined.
type sessionBeforeTreeResult struct {
	Cancel  bool `json:"cancel"`
	Summary *struct {
		Summary string          `json:"summary"`
		Details json.RawMessage `json:"details"`
		Usage   *ai.Usage       `json:"usage"`
	} `json:"summary"`
	CustomInstructions  *string `json:"customInstructions"`
	ReplaceInstructions *bool   `json:"replaceInstructions"`
	Label               *string `json:"label"`
}

// emitSessionBeforeTree runs session_before_tree handlers and decodes the
// winning result. It returns nil when no handler is registered or none
// returned a result. ctx is the navigation's abort signal.
func (s *Session) emitSessionBeforeTree(ctx context.Context, preparation *TreePreparation) (*sessionBeforeTreeResult, error) {
	runner := s.currentRunner()
	if runner == nil || !runner.HasHandlers(icodingagent.EventSessionBeforeTree) {
		return nil, nil
	}
	result, err := runner.Emit(ctx, extension.SessionBeforeTreeEvent{
		Type:        icodingagent.EventSessionBeforeTree,
		Preparation: preparation,
		Signal:      ctx,
	})
	if err != nil || result == nil {
		return nil, err
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	var decoded sessionBeforeTreeResult
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		return nil, err
	}
	return &decoded, nil
}

// extensionTreeSummary converts an extension-provided summary.
func extensionTreeSummary(result *sessionBeforeTreeResult) (*treeBranchSummary, error) {
	summary := &treeBranchSummary{Summary: result.Summary.Summary, Usage: result.Summary.Usage, FromExtension: true}
	if len(result.Summary.Details) > 0 && string(result.Summary.Details) != "null" {
		if err := json.Unmarshal(result.Summary.Details, &summary.Details); err != nil {
			return nil, err
		}
	}
	return summary, nil
}

// emitSessionTree runs session_tree handlers after a navigation. summaryID
// names the branch summary entry the navigation created, if any;
// fromExtension is reported only with a summary.
func (s *Session) emitSessionTree(ctx context.Context, newLeafID, oldLeafID *string, summaryID string, fromExtension bool) {
	runner := s.currentRunner()
	if runner == nil || !runner.HasHandlers(icodingagent.EventSessionTree) {
		return
	}
	event := extension.SessionTreeEvent{
		Type:      icodingagent.EventSessionTree,
		NewLeafID: newLeafID,
		OldLeafID: oldLeafID,
	}
	if summaryID != "" {
		if entry, ok := s.inner.EntryByID(summaryID); ok {
			event.SummaryEntry = entry
			event.FromExtension = fromExtension
		}
	}
	_, _ = runner.Emit(ctx, event)
}
