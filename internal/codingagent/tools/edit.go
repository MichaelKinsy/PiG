package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/jsonstringify"
	"github.com/MichaelKinsy/PiG/internal/orderedjson"
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

// EditOperations delegates file editing. Each callback completes before the mutation queue advances.
// Ports packages/coding-agent/src/core/tools/edit.ts.
type EditOperations struct {
	ReadFile  func(string) ([]byte, error)
	WriteFile func(string, string) error
	Access    func(string) error
}

// EditTool performs exact-text-replacement edits on files.
// Mirrors upstream packages/coding-agent/src/core/tools/edit.ts.
type EditTool struct {
	CWD        string
	Queue      *FileMutationQueue // serialises concurrent edits
	Operations *EditOperations
}

func (t *EditTool) operations() EditOperations {
	if t.Operations != nil {
		return *t.Operations
	}
	return EditOperations{ReadFile: os.ReadFile, WriteFile: func(path, content string) error { return os.WriteFile(path, []byte(content), 0o644) }, Access: accessReadWrite}
}

func (t *EditTool) Name() string  { return "edit" }
func (t *EditTool) Label() string { return "" }

func (t *EditTool) Schema() ai.ToolSchema {
	return toolSchemaWithParameters(ai.ToolSchema{
		Name:                "edit",
		Description:         "Edit a single file using exact text replacement. Every edits[].oldText must match a unique, non-overlapping region of the original file. If two changes affect the same block or nearby lines, merge them into one edit instead of emitting overlapping edits. Do not include large unchanged regions just to connect distant changes.",
		ConstrainedSampling: strictToolSampling(),
		PromptGuidelines: []string{
			"Use edit for precise changes (edits[].oldText must match exactly)",
			"When changing multiple separate locations in one file, use one edit call with multiple entries in edits[] instead of multiple edit calls",
			"Each edits[].oldText is matched against the original file, not after earlier edits are applied. Do not emit overlapping or nested edits. Merge nearby changes into one edit.",
			"Keep edits[].oldText as small as possible while still being unique in the file. Do not pad with large unchanged regions.",
		},
	}, `{"type":"object","required":["path","edits"],"properties":{
		"path":{"type":"string","description":"Path to the file to edit (relative or absolute)"},
		"edits":{"type":"array","items":{"type":"object","required":["oldText","newText"],"properties":{
			"oldText":{"type":"string","description":"Exact text for one targeted replacement. It must be unique in the original file and must not overlap with any other edits[].oldText in the same call."},
			"newText":{"type":"string","description":"Replacement text for this targeted edit."}
		}},"description":"One or more targeted replacements. Each edit is matched against the original file, not incrementally. Do not include overlapping or nested edits. If two changes touch the same block or nearby lines, merge them into one edit instead."}
	}}`)
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
	path, err := resolvePath(t.CWD, p.Path)
	if err != nil {
		return nil, false
	}
	ticket, err := t.Queue.Reserve(path)
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

// prepareEditArgumentsRaw mirrors upstream prepareEditArguments on the raw
// JSON arguments.
func prepareEditArgumentsRaw(raw json.RawMessage) json.RawMessage {
	args, err := orderedjson.Parse(raw)
	if err != nil {
		return raw
	}
	changed := false
	// Assigning args.edits keeps its position when the member exists and appends it otherwise, as a JS object does.
	if editsRaw, ok := args.Get("edits"); ok && isJSONString(editsRaw) {
		var inner string
		if json.Unmarshal(editsRaw, &inner) == nil {
			// JSON.parse skips only JSON whitespace; strings.TrimSpace would also accept \v, \f, U+0085 and U+00A0.
			switch innerRaw := json.RawMessage(strings.Trim(inner, " \t\n\r")); {
			case json.Valid(innerRaw) && len(innerRaw) > 0 && innerRaw[0] == '[':
				args.Set("edits", innerRaw)
				changed = true
			case json.Valid(innerRaw) && isSingleEditInput(innerRaw):
				args.Set("edits", append(append(json.RawMessage("["), innerRaw...), ']'))
				changed = true
			}
		}
	} else if ok && isSingleEditInput(editsRaw) {
		args.Set("edits", append(append(json.RawMessage("["), editsRaw...), ']'))
		changed = true
	}
	oldRaw, _ := args.Get("oldText")
	newRaw, _ := args.Get("newText")
	if isJSONString(oldRaw) && isJSONString(newRaw) {
		// {...rest, edits}: rest keeps its order minus oldText and newText, and edits keeps its place in rest or goes last.
		edits := []json.RawMessage{}
		if current, ok := args.Get("edits"); ok {
			_ = json.Unmarshal(current, &edits)
		}
		edit := orderedjson.New()
		edit.Set("oldText", oldRaw)
		edit.Set("newText", newRaw)
		editRaw, _ := edit.MarshalJSON()
		edits = append(edits, editRaw)
		list, err := json.Marshal(edits)
		if err != nil {
			return raw
		}
		args.Delete("oldText")
		args.Delete("newText")
		args.Set("edits", list)
		changed = true
	}
	if !changed {
		return raw
	}
	out, err := args.MarshalJSON()
	if err != nil {
		return raw
	}
	// JSON.stringify form: no HTML escaping, integer-like members first.
	canonical, err := jsonstringify.Canonicalize(out)
	if err != nil {
		return raw
	}
	return canonical
}

// isSingleEditInput mirrors upstream isSingleEditInput: a non-array object whose oldText and newText are strings.
func isSingleEditInput(raw json.RawMessage) bool {
	edit, err := orderedjson.Parse(raw)
	if err != nil {
		return false
	}
	oldRaw, _ := edit.Get("oldText")
	newRaw, _ := edit.Get("newText")
	return isJSONString(oldRaw) && isJSONString(newRaw)
}

// isJSONString reports whether raw is a JSON string.
func isJSONString(raw json.RawMessage) bool {
	return len(raw) > 0 && raw[0] == '"'
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
	cwd, err := toolCWD(ctx, t.CWD)
	if err != nil {
		return agent.AgentToolResult{}, err
	}
	absPath, err := resolvePath(cwd, p.Path)
	if err != nil {
		return agent.AgentToolResult{}, err
	}

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
	return agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: message}}, Details: map[string]any{}, IsError: true, Thrown: true}
}

func (t *EditTool) editLocked(ctx context.Context, absPath string, p editParams) agent.AgentToolResult {
	aborted := func() bool { return ctx.Err() != nil }
	if aborted() {
		return editError("Operation aborted")
	}
	ops := t.operations()
	if err := ops.Access(absPath); err != nil {
		if aborted() {
			return editError("Operation aborted")
		}
		detail := "Error: " + err.Error()
		if code := nodeErrorCode(err); code != "" {
			detail = "Error code: " + code
		}
		return editError(fmt.Sprintf("Could not edit file: %s. %s.", p.Path, detail))
	}
	if aborted() {
		return editError("Operation aborted")
	}
	data, err := ops.ReadFile(absPath)
	if err != nil {
		return editError(NodeFSError(err, "read", ""))
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
	if err := ops.WriteFile(absPath, final); err != nil {
		return editError(NodeFSError(err, "open", absPath))
	}
	if aborted() {
		return editError("Operation aborted")
	}
	diff, firstChangedLine := diffAndFirstLine(applied.baseContent, applied.newContent)
	patch := GenerateUnifiedPatch(p.Path, applied.baseContent, applied.newContent)
	return agent.AgentToolResult{
		Content: []ai.ToolResultMessageContent{ai.TextContent{Text: fmt.Sprintf("Successfully replaced %d block(s) in %s.", len(p.Edits), p.Path)}},
		Details: &EditToolDetails{Diff: diff, Patch: patch, FirstChangedLine: firstChangedLine},
	}
}
