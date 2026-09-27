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

// WriteDetails is display-only metadata derived from the retained write call. Tool results do not carry it.
type WriteDetails struct {
	Path    string
	Content string
	// Overwrote is true when the write replaced an existing file.
	Overwrote bool
}

// ReadDetails carries the optional upstream truncation object. The renderer derives display-only fields from the retained read call.
type ReadDetails struct {
	Path       string `json:"-"`
	StartLine  int    `json:"-"`
	TotalLines int    `json:"-"`
	Truncated  bool   `json:"-"`
	// Truncation is the upstream-shaped truncation object for the extension SDK
	// contract and the TUI warning. It is nil unless truncation occurred.
	Truncation *TruncationResult `json:"truncation,omitempty"`
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
	Truncation     *TruncationResult `json:"truncation,omitempty"`
	FullOutputPath string            `json:"fullOutputPath,omitempty"`
}

// LsDetails carries the sparse upstream ls result metadata. Limits retain the requested number rather than an integer-rounded rendering count.
type LsDetails struct {
	Truncation        *TruncationResult `json:"truncation,omitempty"`
	EntryLimitReached float64           `json:"entryLimitReached,omitempty"`
}

// GrepDetails carries sparse truncation metadata and the requested match limit, including fractional values.
type GrepDetails struct {
	Truncation        *TruncationResult `json:"truncation,omitempty"`
	MatchLimitReached float64           `json:"matchLimitReached,omitempty"`
	LinesTruncated    bool              `json:"linesTruncated,omitempty"`
}

// FindDetails mirrors upstream FindToolDetails (find.ts:32): a truncation
// object when the byte limit was hit and the result-limit cap when reached.
type FindDetails struct {
	Truncation         *TruncationResult `json:"truncation,omitempty"`
	ResultLimitReached int               `json:"resultLimitReached,omitempty"`
}
