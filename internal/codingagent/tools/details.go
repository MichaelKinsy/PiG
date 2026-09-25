package tools

// EditToolDetails is attached to an EditTool result. It is the upstream
// SDK contract (edit.ts:61 `EditToolDetails`): a display-oriented diff,
// a standard unified patch, and the new-file line of the first change
// (for editor navigation). The TUI renders Diff directly; extensions and
// PostToolUse hooks receive this exact JSON shape.
type EditToolDetails struct {
	// Diff is the display-oriented, line-numbered diff (generateDiffString).
	Diff string `json:"diff"`
	// Patch is the standard unified patch (generateUnifiedPatch).
	Patch string `json:"patch"`
	// FirstChangedLine is the new-file line of the first change, 0 when
	// there is no change (upstream `firstChangedLine?: number`, omitted).
	FirstChangedLine int `json:"firstChangedLine,omitempty"`
}

// WriteDetails is attached to a WriteTool result. The TUI uses the path to
// select syntax highlighting and the content to render the upstream ten-line
// collapsed preview or complete expanded body.
type WriteDetails struct {
	Path    string
	Content string
	// Overwrote is true when the write replaced an existing file.
	Overwrote bool
}

// ReadDetails is attached to a ReadTool result. The TUI uses the path for
// syntax highlighting and the truncation details for the warning shown after
// the complete expanded body.
type ReadDetails struct {
	Path       string
	StartLine  int // 1-indexed line number of the first emitted line
	TotalLines int // total lines in the file (before truncation)
	Truncated  bool
	// Truncation is the upstream-shaped truncation object for the extension SDK
	// contract and the TUI warning. It is nil unless truncation occurred.
	Truncation *TruncationResult
}

// BashDetails is attached to a BashTool result. The renderer reads
// Truncation to format the warning row
// (e.g. `[Showing lines 1001-3000 of 3000. Full output: /tmp/...]`)
// and FullOutputPath to surface the temp file containing the full
// (untruncated) output.
//
// Either field may be zero/nil; both nil means the bash output fit
// fully in the rolling buffer.
type BashDetails struct {
	Truncation     *TruncationResult
	FullOutputPath string
}

// LsDetails is attached to a LsTool result. It carries the upstream SDK
// contract (ls.ts:23 LsToolDetails): a truncation object when the byte
// limit was hit and the entry-limit cap when reached. The TUI has no
// dedicated ls renderer; this exists only for the extension wire.
type LsDetails struct {
	Truncation        *TruncationResult
	EntryLimitReached int
}

// GrepDetails mirrors upstream GrepToolDetails (grep.ts:41): a truncation
// object when the byte limit was hit, the match-limit cap when reached,
// and a flag when individual lines were truncated to the max length.
type GrepDetails struct {
	Truncation        *TruncationResult
	MatchLimitReached int
	LinesTruncated    bool
}

// FindDetails mirrors upstream FindToolDetails (find.ts:32): a truncation
// object when the byte limit was hit and the result-limit cap when reached.
type FindDetails struct {
	Truncation         *TruncationResult
	ResultLimitReached int
}
