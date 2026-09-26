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

// EditsDiffPreview is upstream EditDiffResult or EditDiffError: the diff the
// edits would make, with its first changed line, or why they cannot apply.
type EditsDiffPreview struct {
	Diff             string
	FirstChangedLine int
	Error            string
}

// ComputeEditsDiff computes the diff the edits would make to path without
// writing it, as the upstream edit renderer previews an edit call.
func ComputeEditsDiff(path string, edits []EditReplacement, cwd string) EditsDiffPreview {
	absolutePath := resolveToCwd(path, cwd)
	file, err := os.Open(absolutePath)
	if err != nil {
		detail := "Error: " + err.Error()
		if code := nodeErrorCode(err); code != "" {
			detail = "Error code: " + code
		}
		return EditsDiffPreview{Error: fmt.Sprintf("Could not edit file: %s. %s.", path, detail)}
	}
	_ = file.Close()
	data, err := os.ReadFile(absolutePath)
	if err != nil {
		return EditsDiffPreview{Error: err.Error()}
	}
	var decoder utf8StreamDecoder
	_, content := text.SplitBom(decoder.decode(data, false))
	entries := make([]editEntry, len(edits))
	for i, edit := range edits {
		entries[i] = editEntry(edit)
	}
	applied, err := applyEditsToNormalizedContent(normalizeToLF(content), entries, path)
	if err != nil {
		return EditsDiffPreview{Error: err.Error()}
	}
	diff, firstChangedLine := GenerateDiffString(applied.baseContent, applied.newContent)
	return EditsDiffPreview{Diff: diff, FirstChangedLine: firstChangedLine}
}
