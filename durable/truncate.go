package durable

import (
	"encoding/json"
	"fmt"

	"github.com/MichaelKinsy/PiG/chord/output"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/internal/truncate"
)

// Ports packages/durable/src/truncate.ts
//
// Shared truncation utilities for tool outputs. Truncation is based on two independent limits, whichever is hit
// first: a line limit (default 2000 lines) and a byte limit (default 50KB). It never returns partial lines. Tool output
// streams are bounded by the harness output buffer instead.

const (
	DEFAULT_MAX_LINES = 2000      //nolint:revive // upstream identifier
	DEFAULT_MAX_BYTES = 50 * 1024 //nolint:revive // upstream identifier
)

// TruncatedBy is which limit truncated the content: "lines", "bytes", or empty when not truncated (upstream null).
type TruncatedBy string

const (
	TruncatedByLines TruncatedBy = "lines"
	TruncatedByBytes TruncatedBy = "bytes"
)

// MarshalJSON encodes the empty value as null.
func (by TruncatedBy) MarshalJSON() ([]byte, error) {
	if by == "" {
		return []byte("null"), nil
	}
	return json.Marshal(string(by))
}

// UnmarshalJSON decodes null as the empty value.
func (by *TruncatedBy) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		*by = ""
		return nil
	}
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	*by = TruncatedBy(value)
	return nil
}

// TruncationResult describes one truncation.
type TruncationResult struct {
	// Content is the truncated content.
	Content string `json:"content"`
	// Truncated reports whether truncation occurred.
	Truncated bool `json:"truncated"`
	// TruncatedBy is the limit that was hit; empty when not truncated.
	TruncatedBy TruncatedBy `json:"truncatedBy"`
	// TotalLines is the number of lines in the original content.
	TotalLines int `json:"totalLines"`
	// TotalBytes is the number of UTF-8 bytes in the original content.
	TotalBytes int `json:"totalBytes"`
	// OutputLines is the number of complete lines in the truncated output.
	OutputLines int `json:"outputLines"`
	// OutputBytes is the number of bytes in the truncated output.
	OutputBytes int `json:"outputBytes"`
	// LastLinePartial reports whether the last line was partially truncated (only for the tail truncation edge case).
	LastLinePartial bool `json:"lastLinePartial"`
	// FirstLineExceedsLimit reports whether the first line exceeded the byte limit (for head truncation).
	FirstLineExceedsLimit bool `json:"firstLineExceedsLimit"`
	// MaxLines is the line limit that was applied.
	MaxLines int `json:"maxLines"`
	// MaxBytes is the byte limit that was applied.
	MaxBytes int `json:"maxBytes"`
}

// TruncationOptions holds the limits; nil uses the default.
type TruncationOptions struct {
	// MaxLines defaults to DEFAULT_MAX_LINES.
	MaxLines *int
	// MaxBytes defaults to DEFAULT_MAX_BYTES.
	MaxBytes *int
}

// Utf8ByteLength is chord/output.Utf8ByteLength, the one implementation shared with the output buffer (truncate.ts re-exports it).
func Utf8ByteLength(content string) int {
	return output.Utf8ByteLength(content)
}

// FormatSize formats bytes as a human-readable size.
func FormatSize(bytes int) string {
	switch {
	case bytes < 1024:
		return fmt.Sprintf("%dB", bytes)
	case bytes < 1024*1024:
		return jsstring.ToFixed(float64(bytes)/1024, 1) + "KB"
	default:
		return jsstring.ToFixed(float64(bytes)/(1024*1024), 1) + "MB"
	}
}

// TruncateHead truncates content from the head, keeping the first lines and bytes. It suits file reads where the
// beginning matters.
//
// It never returns partial lines. When the first line exceeds the byte limit, it returns empty content with
// FirstLineExceedsLimit set.
func TruncateHead(content string, options TruncationOptions) TruncationResult {
	return TruncateHeadOf(content, TruncationTotals{Lines: truncate.CountLines(content), Bytes: Utf8ByteLength(content)}, options)
}

// TruncationTotals are the line and byte counts of a whole text: lines counted like TruncateHead, ignoring a trailing
// newline.
type TruncationTotals struct {
	Lines int
	Bytes int
}

// TruncateHeadOf is TruncateHead of a text known by a prefix and its totals. The prefix must be the whole text, or
// longer than MaxBytes + 1 UTF-8 bytes, or hold at least MaxLines newlines; then the result equals TruncateHead of the
// whole text.
func TruncateHeadOf(prefix string, totals TruncationTotals, options TruncationOptions) TruncationResult {
	maxLines := DEFAULT_MAX_LINES
	if options.MaxLines != nil {
		maxLines = *options.MaxLines
	}
	maxBytes := DEFAULT_MAX_BYTES
	if options.MaxBytes != nil {
		maxBytes = *options.MaxBytes
	}

	totalBytes := totals.Bytes
	totalLines := totals.Lines

	if totalLines <= maxLines && totalBytes <= maxBytes {
		return TruncationResult{
			Content:     prefix,
			TotalLines:  totalLines,
			TotalBytes:  totalBytes,
			OutputLines: totalLines,
			OutputBytes: totalBytes,
			MaxLines:    maxLines,
			MaxBytes:    maxBytes,
		}
	}

	// Upstream reads lines[0] even for empty content and Buffer.byteLength(undefined) throws a TypeError there, reachable only past a
	// negative limit; an empty text is then truncated to nothing instead of failing.
	if prefix != "" && truncate.FirstLineBytes(prefix) > maxBytes {
		return TruncationResult{
			Truncated:             true,
			TruncatedBy:           TruncatedByBytes,
			TotalLines:            totalLines,
			TotalBytes:            totalBytes,
			FirstLineExceedsLimit: true,
			MaxLines:              maxLines,
			MaxBytes:              maxBytes,
		}
	}

	// The kept lines are a prefix of the text: the walk finds where they end, and the content is that substring.
	cut := truncate.CutHead(prefix, maxBytes, maxLines)
	truncatedBy := TruncatedByBytes
	// Without a byte break, only omitted lines prove the line limit was reached; otherwise a trailing newline exceeded
	// bytes.
	if !cut.ByBytes && cut.Lines < totalLines {
		truncatedBy = TruncatedByLines
	}

	return TruncationResult{
		Content:     prefix[:cut.End],
		Truncated:   true,
		TruncatedBy: truncatedBy,
		TotalLines:  totalLines,
		TotalBytes:  totalBytes,
		OutputLines: cut.Lines,
		OutputBytes: cut.End,
		MaxLines:    maxLines,
		MaxBytes:    maxBytes,
	}
}
