package tui

// TruncationResult captures every fact a renderer needs to format the
// "[Showing lines X-Y of Z. Full output: <path>]" warning row.
//
// Layout matches upstream TruncationResult:
//
//	{ content, truncated, truncatedBy, totalLines, totalBytes,
//	  outputLines, outputBytes, lastLinePartial, firstLineExceedsLimit,
//	  maxLines, maxBytes }
type TruncationResult struct {
	Content     string `json:"content"`
	Truncated   bool   `json:"truncated"`
	TruncatedBy string `json:"truncatedBy"` // "lines" | "bytes" | ""
	TotalLines  int    `json:"totalLines"`
	TotalBytes  int    `json:"totalBytes"`
	OutputLines int    `json:"outputLines"`
	OutputBytes int    `json:"outputBytes"`
	// LastLinePartial: the last line of the original output is the
	// only one that fit (and was truncated from its end). Bash-only
	// edge case.
	LastLinePartial bool `json:"lastLinePartial"`
	// FirstLineExceedsLimit: head-truncation case where the first line
	// alone exceeds maxBytes. We return empty content and let the
	// caller decide.
	FirstLineExceedsLimit bool `json:"firstLineExceedsLimit"`
	MaxLines              int  `json:"maxLines"`
	MaxBytes              int  `json:"maxBytes"`
}
