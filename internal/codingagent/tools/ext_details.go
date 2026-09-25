package tools

import "github.com/MichaelKinsy/PiG/coding/extension"

// This file implements extension.ToolDetailsConverter for the built-in tools'
// internal result Details structs, mapping each to the upstream extension SDK
// wire shape (the `details` field of a tool_result event). It mirrors
// upstream's per-tool detail construction: each object is sparse (fields
// present only when their condition held) and the whole object is omitted
// (nil) when empty.
//
// pig keeps richer internal detail structs for the TUI renderer (e.g.
// ReadDetails carries StartLine/TotalLines). These methods are the boundary
// that converts them into the lean upstream SDK shapes extensions and
// PostToolUse hooks expect. The edit tool's *EditToolDetails is already
// SDK-shaped and intentionally does not implement this interface (it passes
// through unchanged). Write returns nil because upstream attaches no details
// to write results (write.ts:223).

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
	if out.Truncation == nil && out.ResultLimitReached == 0 {
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
// upstream (write.ts:223 sets details: undefined). The internal WriteDetails
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
