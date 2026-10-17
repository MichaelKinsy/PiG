package tools

// Ports computeEditsDiff of packages/coding-agent/src/core/tools/edit-diff.ts.

import (
	"fmt"
	"os"

	"github.com/MichaelKinsy/PiG/internal/text"
)

// EditReplacement is one exact-text replacement of an edit call.
type EditReplacement struct {
	OldText string
	NewText string
}

// EditDiffResult is upstream's EditDiffResult: the diff the edits would make and the first changed line, 0 when there is none (upstream undefined, as EditToolDetails).
type EditDiffResult struct {
	Diff             string
	FirstChangedLine int
}

// EditDiffError is upstream's EditDiffError: why the edits cannot apply.
type EditDiffError struct {
	Error string
}

// EditDiffOutcome is upstream's EditDiffResult | EditDiffError union; EditDiffResult and EditDiffError are its only members.
type EditDiffOutcome interface{ editDiffOutcome() }

func (EditDiffResult) editDiffOutcome() {}
func (EditDiffError) editDiffOutcome()  {}

// ComputeEditsDiff computes the diff the edits would make to path without
// writing it, as the upstream edit renderer previews an edit call.
func ComputeEditsDiff(path string, edits []EditReplacement, cwd string) EditDiffOutcome {
	absolutePath, err := resolveToCwd(path, cwd)
	if err != nil {
		return EditDiffError{Error: fmt.Sprintf("Could not edit file: %s. Error: %s.", path, err.Error())}
	}
	file, err := os.Open(absolutePath)
	if err != nil {
		detail := "Error: " + err.Error()
		if code := nodeErrorCode(err); code != "" {
			detail = "Error code: " + code
		}
		return EditDiffError{Error: fmt.Sprintf("Could not edit file: %s. %s.", path, detail)}
	}
	_ = file.Close()
	data, err := os.ReadFile(absolutePath)
	if err != nil {
		return EditDiffError{Error: err.Error()}
	}
	var decoder utf8StreamDecoder
	_, content := text.SplitBom(decoder.decode(data, false))
	entries := make([]editEntry, len(edits))
	for i, edit := range edits {
		entries[i] = editEntry(edit)
	}
	applied, err := applyEditsToNormalizedContent(normalizeToLF(content), entries, path)
	if err != nil {
		return EditDiffError{Error: err.Error()}
	}
	diff, firstChangedLine := diffAndFirstLine(applied.baseContent, applied.newContent)
	return EditDiffResult{Diff: diff, FirstChangedLine: firstChangedLine}
}
