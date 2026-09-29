package tools

import "github.com/MichaelKinsy/PiG/coding/extension"

// These converters expose the typed extension details contract. Absent details stay nil, and renderer-only state never becomes extension metadata.

// ToolResultDetails maps bash result details to extension.BashToolDetails.
// Upstream attaches details only when output was truncated (bash.ts:354).
func (d *BashDetails) ToolResultDetails() any {
	if d == nil || d.Truncation == nil || !d.Truncation.Truncated {
		return nil
	}
	return &extension.BashToolDetails{
		Truncation:     toWireTruncation(d.Truncation),
		FullOutputPath: d.FullOutputPath,
	}
}

// ToolResultDetails maps read result details to extension.ReadToolDetails.
func (d *ReadDetails) ToolResultDetails() any {
	if d == nil || d.Truncation == nil {
		return nil
	}
	return &extension.ReadToolDetails{Truncation: toWireTruncation(d.Truncation)}
}

// ToolResultDetails maps grep result details to extension.GrepToolDetails.
func (d *GrepDetails) ToolResultDetails() any {
	if d == nil {
		return nil
	}
	out := extension.GrepToolDetails{
		Truncation:        toWireTruncation(d.Truncation),
		MatchLimitReached: d.MatchLimitReached,
		LinesTruncated:    d.LinesTruncated,
	}
	if out.Truncation == nil && out.MatchLimitReached == 0 && !out.LinesTruncated {
		return nil
	}
	return &out
}

// ToolResultDetails maps find result details to extension.FindToolDetails.
func (d *FindDetails) ToolResultDetails() any {
	if d == nil {
		return nil
	}
	out := extension.FindToolDetails{
		Truncation:         toWireTruncation(d.Truncation),
		ResultLimitReached: d.ResultLimitReached,
	}
	if out.Truncation == nil && out.ResultLimitReached == nil {
		return nil
	}
	return &out
}

// ToolResultDetails maps ls result details to extension.LsToolDetails.
func (d *LsDetails) ToolResultDetails() any {
	if d == nil {
		return nil
	}
	out := extension.LsToolDetails{
		Truncation:        toWireTruncation(d.Truncation),
		EntryLimitReached: d.EntryLimitReached,
	}
	if out.Truncation == nil && out.EntryLimitReached == 0 {
		return nil
	}
	return &out
}

// ToolResultDetails reports that write results carry no SDK details, matching
// upstream (write.ts:87 sets details: undefined). The internal WriteDetails
// exists only for the TUI renderer.
func (d *WriteDetails) ToolResultDetails() any { return nil }

// toWireTruncation converts the internal TruncationResult to the extension SDK
// ToolTruncation wire shape. Returns nil when no truncation occurred so the
// `truncation` field is omitted, matching upstream's conditional attach.
func toWireTruncation(tr *TruncationResult) *extension.ToolTruncation {
	if tr == nil || !tr.Truncated {
		return nil
	}
	return &extension.ToolTruncation{
		Content:               tr.Content,
		Truncated:             tr.Truncated,
		TruncatedBy:           tr.TruncatedBy,
		TotalLines:            tr.TotalLines,
		TotalBytes:            tr.TotalBytes,
		OutputLines:           tr.OutputLines,
		OutputBytes:           tr.OutputBytes,
		LastLinePartial:       tr.LastLinePartial,
		FirstLineExceedsLimit: tr.FirstLineExceedsLimit,
		MaxLines:              tr.MaxLines,
		MaxBytes:              tr.MaxBytes,
	}
}
