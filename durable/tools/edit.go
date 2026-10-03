package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/env"
)

// Ports packages/durable/src/tools/edit.ts.

const editParameters = `{"type":"object","required":["path","edits"],"properties":{"path":{"type":"string","description":"Path to the file to edit (relative or absolute)"},"edits":{"type":"array","items":{"type":"object","required":["oldText","newText"],"properties":{"oldText":{"type":"string","description":"Exact text for one targeted replacement. It must be unique in the original file and must not overlap with any other edits[].oldText in the same call."},"newText":{"type":"string","description":"Replacement text for this targeted edit."}}},"description":"One or more targeted replacements. Each edit is matched against the original file, not incrementally. Do not include overlapping or nested edits. If two changes touch the same block or nearby lines, merge them into one edit instead."}}}`

// EditToolInput is the arguments of the edit tool.
type EditToolInput struct {
	Path  string `json:"path"`
	Edits []Edit `json:"edits"`
}

// EditToolDetails are the details of an edit result: the display diff, the
// unified patch, and the first changed line in the new file.
type EditToolDetails struct {
	Diff             string `json:"diff"`
	Patch            string `json:"patch"`
	FirstChangedLine int    `json:"firstChangedLine,omitempty"`
}

// singleEditInput returns value as an edit when it is an object with a string
// oldText and a string newText.
func singleEditInput(value any) (map[string]any, bool) {
	edit, ok := value.(map[string]any)
	if !ok {
		return nil, false
	}
	_, oldIsString := edit["oldText"].(string)
	_, newIsString := edit["newText"].(string)
	return edit, oldIsString && newIsString
}

// prepareEditArguments repairs shapes models commonly send: edits as a JSON
// string or as a single edit object, and a top-level oldText/newText pair. It
// works on a copy; the call's arguments stay unchanged.
func prepareEditArguments(input any) (any, error) {
	original, ok := input.(map[string]any)
	if !ok {
		return input, nil
	}
	args := make(map[string]any, len(original))
	maps.Copy(args, original)
	if text, isString := args["edits"].(string); isString {
		var parsed any
		if json.Unmarshal([]byte(text), &parsed) == nil {
			if array, isArray := parsed.([]any); isArray {
				args["edits"] = array
			} else if edit, isEdit := singleEditInput(parsed); isEdit {
				args["edits"] = []any{edit}
			}
		}
	} else if edit, isEdit := singleEditInput(args["edits"]); isEdit {
		args["edits"] = []any{edit}
	}

	oldText, oldIsString := args["oldText"].(string)
	newText, newIsString := args["newText"].(string)
	if !oldIsString || !newIsString {
		return args, nil
	}
	var edits []any
	if array, isArray := args["edits"].([]any); isArray {
		edits = append(edits, array...)
	}
	edits = append(edits, map[string]any{"oldText": oldText, "newText": newText})
	delete(args, "oldText")
	delete(args, "newText")
	args["edits"] = edits
	return args, nil
}

// validateEditInput decodes the arguments; edits must hold a replacement.
func validateEditInput(args any) (EditToolInput, error) {
	invalid := errors.New("Edit tool input is invalid. edits must contain at least one replacement.")
	raw, err := durable.FromJsonValue[struct {
		Path  string          `json:"path"`
		Edits json.RawMessage `json:"edits"`
	}](args)
	if err != nil {
		return EditToolInput{}, err
	}
	var edits []Edit
	if json.Unmarshal(raw.Edits, &edits) != nil || len(edits) == 0 {
		return EditToolInput{}, invalid
	}
	return EditToolInput{Path: raw.Path, Edits: edits}, nil
}

func editAccessError(path string, err error) error {
	code := env.FileErrorUnknown
	if fileErr, ok := errors.AsType[*env.FileError](err); ok {
		code = fileErr.Code
	}
	return &editAccessFailure{message: fmt.Sprintf("Could not edit file: %s. Error code: %s.", path, code), cause: err}
}

// editAccessFailure is an edit that could not read or write its file; it keeps
// the file error as its cause.
type editAccessFailure struct {
	message string
	cause   error
}

func (failure *editAccessFailure) Error() string { return failure.message }
func (failure *editAccessFailure) Unwrap() error { return failure.cause }

// CreateEditTool creates the edit tool: exact-text replacement in one file.
func CreateEditTool() *durable.ToolRegistration {
	return &durable.ToolRegistration{
		ToolSchema:       toolSchema("edit", "Edit a single file using exact text replacement. Every edits[].oldText must match a unique, non-overlapping region of the original file. If two changes affect the same block or nearby lines, merge them into one edit instead of emitting overlapping edits. Do not include large unchanged regions just to connect distant changes.", editParameters),
		PrepareArguments: prepareEditArguments,
		Execute:          executeEdit,
	}
}

func executeEdit(ctx context.Context, args any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
	input, err := validateEditInput(args)
	if err != nil {
		return durable.ToolExecutionResult{}, err
	}
	executionEnv, err := requireEnv(api)
	if err != nil {
		return durable.ToolExecutionResult{}, err
	}
	absolutePath, err := resolveToolPath(ctx, executionEnv, input.Path)
	if err != nil {
		return durable.ToolExecutionResult{}, err
	}
	return withFileMutationQueue(ctx, executionEnv, absolutePath, func() (durable.ToolExecutionResult, error) {
		return editFile(ctx, executionEnv, absolutePath, input)
	})
}

var errOperationAborted = errors.New("Operation aborted")

func editFile(ctx context.Context, executionEnv env.ExecutionEnv, absolutePath string, input EditToolInput) (durable.ToolExecutionResult, error) {
	failed := func(err error) (durable.ToolExecutionResult, error) { return durable.ToolExecutionResult{}, err }
	if ctx.Err() != nil {
		return failed(errOperationAborted)
	}
	info, err := executionEnv.FileInfo(ctx, absolutePath)
	if err != nil {
		return failed(editAccessError(input.Path, err))
	}
	if info.Kind != env.FileKindFile && info.Kind != env.FileKindSymlink {
		return failed(fmt.Errorf("Could not edit file: %s. Path is not a file.", input.Path))
	}

	raw, err := executionEnv.ReadTextFile(ctx, absolutePath)
	if err != nil {
		return failed(editAccessError(input.Path, err))
	}
	if ctx.Err() != nil {
		return failed(errOperationAborted)
	}

	bom, content := stripBom(raw)
	originalEnding := detectLineEnding(content)
	normalizedContent := normalizeToLF(content)
	applied, err := applyEditsToNormalizedContent(normalizedContent, input.Edits, input.Path)
	if err != nil {
		return failed(err)
	}
	if ctx.Err() != nil {
		return failed(errOperationAborted)
	}

	finalContent := bom + restoreLineEndings(applied.newContent, originalEnding)
	if err := executionEnv.WriteFile(ctx, absolutePath, finalContent); err != nil {
		return failed(editAccessError(input.Path, err))
	}
	if ctx.Err() != nil {
		return failed(errOperationAborted)
	}

	diff, firstChangedLine := GenerateDiffString(applied.baseContent, applied.newContent)
	details, err := durable.ToJsonValue(EditToolDetails{
		Diff:             diff,
		Patch:            GenerateUnifiedPatch(input.Path, applied.baseContent, applied.newContent),
		FirstChangedLine: firstChangedLine,
	})
	if err != nil {
		return failed(err)
	}
	return durable.ToolExecutionResult{
		Content:    []ai.ToolResultMessageContent{ai.TextContent{Text: fmt.Sprintf("Successfully replaced %d block(s) in %s.", len(input.Edits), input.Path)}},
		Details:    details,
		HasDetails: true,
	}, nil
}
