package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/text"
)

// ─── Edit Tool ────────────────────────────────────────────────────────────────

type editEntry struct {
	OldText string `json:"oldText"`
	NewText string `json:"newText"`
}

type editParams struct {
	Path  string      `json:"path"`
	Edits []editEntry `json:"edits"`
}

// EditTool performs exact-text-replacement edits on files.
// Mirrors upstream packages/coding-agent/src/core/tools/edit.ts.
type EditTool struct {
	CWD   string
	Queue *FileMutationQueue // serialises concurrent edits
}

func (t *EditTool) Name() string  { return "edit" }
func (t *EditTool) Label() string { return "" }

func (t *EditTool) Schema() ai.ToolSchema {
	return ai.ToolSchema{
		Name:        "edit",
		Description: "Edit a single file using exact text replacement. Every edits[].oldText must match a unique, non-overlapping region of the original file. If two changes affect the same block or nearby lines, merge them into one edit instead of emitting overlapping edits. Do not include large unchanged regions just to connect distant changes.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{"type": "string", "description": "Path to the file to edit (relative or absolute)"},
				"edits": map[string]any{
					"type":        "array",
					"description": "One or more targeted replacements. Each edit is matched against the original file, not incrementally. Do not include overlapping or nested edits. If two changes touch the same block or nearby lines, merge them into one edit instead.",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"oldText": map[string]any{"type": "string", "description": "Exact text for one targeted replacement. It must be unique in the original file and must not overlap with any other edits[].oldText in the same call."},
							"newText": map[string]any{"type": "string", "description": "Replacement text for this targeted edit."},
						},
						"required": []string{"oldText", "newText"},
					},
				},
			},
			"required": []string{"path", "edits"},
		},
		ConstrainedSampling: strictToolSampling(),
		PromptGuidelines: []string{
			"Use edit for precise changes (edits[].oldText must match exactly)",
			"When changing multiple separate locations in one file, use one edit call with multiple entries in edits[] instead of multiple edit calls",
			"Each edits[].oldText is matched against the original file, not after earlier edits are applied. Do not emit overlapping or nested edits. Merge nearby changes into one edit.",
			"Keep edits[].oldText as small as possible while still being unique in the file. Do not pad with large unchanged regions.",
		},
	}
}

// ExecutionMode is parallel: upstream's edit definition sets no
// executionMode, and the file mutation queue serialises edits to one file.
func (t *EditTool) ExecutionMode() agent.ToolExecutionMode { return agent.ToolModeParallel }

// ReserveMutationOrder implements agent.QueueOrderable: the parallel
// dispatcher calls this synchronously, in tool-call order, before spawning
// this call's goroutine, so this edit's place in the shared file mutation
// queue is fixed before goroutine scheduling can reorder it. Returns
// ok=false whenever Execute would not reach the queue either (invalid
// params or no edits), so no reservation is ever left un-awaited.
func (t *EditTool) ReserveMutationOrder(rawParams json.RawMessage) (*agent.MutationTicket, bool) {
	if t.Queue == nil {
		return nil, false
	}
	p, err := prepareEditArguments(rawParams)
	if err != nil || len(p.Edits) == 0 {
		return nil, false
	}
	ticket, err := t.Queue.Reserve(resolvePath(t.CWD, p.Path))
	if err != nil {
		return nil, false
	}
	return &agent.MutationTicket{Wait: ticket.Wait, Release: ticket.Release}, true
}

// PrepareArguments implements agent.ArgumentPreparer, mirroring upstream
// edit.ts `prepareArguments: prepareEditArguments`: models that send edits
// as a JSON string, as a single edit object, or in the legacy top-level
// {oldText, newText} shape reach the schema validator as {path, edits:[…]}.
// Anything else passes through unchanged.
func (t *EditTool) PrepareArguments(raw json.RawMessage) (json.RawMessage, error) {
	return prepareEditArgumentsRaw(raw), nil
}

// isSingleEditInput mirrors upstream isSingleEditInput: a non-array object
// whose oldText and newText are strings.
func isSingleEditInput(value any) bool {
	edit, ok := value.(map[string]any)
	if !ok {
		return false
	}
	_, oldOK := edit["oldText"].(string)
	_, newOK := edit["newText"].(string)
	return oldOK && newOK
}

// prepareEditArgumentsRaw mirrors upstream prepareEditArguments on the raw
// JSON arguments.
func prepareEditArgumentsRaw(raw json.RawMessage) json.RawMessage {
	var args map[string]any
	if err := json.Unmarshal(raw, &args); err != nil || args == nil {
		return raw
	}
	changed := false
	switch edits := args["edits"].(type) {
	case string:
		var parsed any
		if err := json.Unmarshal([]byte(edits), &parsed); err == nil {
			if list, ok := parsed.([]any); ok {
				args["edits"], changed = list, true
			} else if isSingleEditInput(parsed) {
				args["edits"], changed = []any{parsed}, true
			}
		}
	default:
		if isSingleEditInput(edits) {
			args["edits"], changed = []any{edits}, true
		}
	}
	oldText, oldOK := args["oldText"].(string)
	newText, newOK := args["newText"].(string)
	if oldOK && newOK {
		existing, _ := args["edits"].([]any)
		args["edits"] = append(append([]any(nil), existing...), map[string]any{"oldText": oldText, "newText": newText})
		delete(args, "oldText")
		delete(args, "newText")
		changed = true
	}
	if !changed {
		return raw
	}
	out, err := json.Marshal(args)
	if err != nil {
		return raw
	}
	return out
}

// prepareEditArguments prepares the raw arguments and decodes them.
func prepareEditArguments(raw json.RawMessage) (editParams, error) {
	var p editParams
	if err := json.Unmarshal(prepareEditArgumentsRaw(raw), &p); err != nil {
		return editParams{}, fmt.Errorf("edit: invalid params: %w", err)
	}
	return p, nil
}

// Execute mirrors upstream edit.ts execute: inside the file mutation queue
// it checks access, reads, applies the edits, and writes, checking for an
// abort after each step so the queue is never released mid-write.
func (t *EditTool) Execute(ctx context.Context, _ string, rawParams json.RawMessage, _ agent.ToolUpdateCallback) (agent.AgentToolResult, error) {
	p, err := prepareEditArguments(rawParams)
	if err != nil {
		return agent.AgentToolResult{}, err
	}
	if len(p.Edits) == 0 {
		return editError("Edit tool input is invalid. edits must contain at least one replacement."), nil
	}
	absPath := resolvePath(t.CWD, p.Path)

	var result agent.AgentToolResult
	err = runQueued(ctx, t.Queue, absPath, func() error {
		result = t.editLocked(ctx, absPath, p)
		return nil
	})
	if err != nil {
		return editError(err.Error()), nil
	}
	return result, nil
}

func editError(message string) agent.AgentToolResult {
	return agent.AgentToolResult{Content: message, IsError: true}
}

func (t *EditTool) editLocked(ctx context.Context, absPath string, p editParams) agent.AgentToolResult {
	aborted := func() bool { return ctx.Err() != nil }
	if aborted() {
		return editError("Operation aborted")
	}
	if err := accessReadWrite(absPath); err != nil {
		if aborted() {
			return editError("Operation aborted")
		}
		detail := "Error: " + err.Error()
		if code := nodeErrorCode(err); code != "" {
			detail = "Error code: " + code
		}
		return editError(fmt.Sprintf("Could not edit file: %s. %s.", p.Path, detail))
	}
	data, err := os.ReadFile(absPath)
	if err != nil {
		return editError(nodeFSError(err, "read", ""))
	}
	if aborted() {
		return editError("Operation aborted")
	}
	var decoder utf8StreamDecoder
	bom, content := text.SplitBom(decoder.decode(data, false))
	originalEnding := detectLineEnding(content)
	normalized := normalizeToLF(content)
	applied, err := applyEditsToNormalizedContent(normalized, p.Edits, p.Path)
	if err != nil {
		return editError(err.Error())
	}
	if aborted() {
		return editError("Operation aborted")
	}
	final := bom + restoreLineEndings(applied.newContent, originalEnding)
	if err := os.WriteFile(absPath, []byte(final), 0o644); err != nil {
		return editError(nodeFSError(err, "open", absPath))
	}
	if aborted() {
		return editError("Operation aborted")
	}
	diff, firstChangedLine := GenerateDiffString(applied.baseContent, applied.newContent)
	patch := GenerateUnifiedPatch(p.Path, applied.baseContent, applied.newContent)
	return agent.AgentToolResult{
		Content: fmt.Sprintf("Successfully replaced %d block(s) in %s.", len(p.Edits), p.Path),
		Details: &EditToolDetails{Diff: diff, Patch: patch, FirstChangedLine: firstChangedLine},
	}
}
