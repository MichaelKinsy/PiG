package durable

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
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

// Utf8ByteLength returns the UTF-8 byte length of content. Go strings hold UTF-8 (lone UTF-16 surrogates arrive as
// U+FFFD or as their 3-byte WTF-8 form), so the length is the byte count, as Node's Buffer.byteLength reports it.
func Utf8ByteLength(content string) int {
	return len(content)
}

func splitLinesForCounting(content string) []string {
	if content == "" {
		return nil
	}
	lines := strings.Split(content, "\n")
	if strings.HasSuffix(content, "\n") {
		lines = lines[:len(lines)-1]
	}
	return lines
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
	return TruncateHeadOf(content, TruncationTotals{Lines: len(splitLinesForCounting(content)), Bytes: Utf8ByteLength(content)}, options)
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
	lines := splitLinesForCounting(prefix)
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

	// Upstream reads lines[0] even for empty content; its undefined byte length is then NaN and never exceeds.
	if len(lines) > 0 && Utf8ByteLength(lines[0]) > maxBytes {
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

	output := make([]string, 0, min(len(lines), max(maxLines, 0)))
	outputBytes := 0
	truncatedBy := TruncatedByLines
	for i := 0; i < len(lines) && i < maxLines; i++ {
		lineBytes := Utf8ByteLength(lines[i])
		if i > 0 {
			lineBytes++
		}
		if outputBytes+lineBytes > maxBytes {
			truncatedBy = TruncatedByBytes
			break
		}
		output = append(output, lines[i])
		outputBytes += lineBytes
	}

	// Without a byte break, only omitted lines prove the line limit was reached; otherwise a trailing newline exceeded
	// bytes.
	if truncatedBy != TruncatedByBytes {
		if len(output) < totalLines {
			truncatedBy = TruncatedByLines
		} else {
			truncatedBy = TruncatedByBytes
		}
	}

	outputContent := strings.Join(output, "\n")
	return TruncationResult{
		Content:     outputContent,
		Truncated:   true,
		TruncatedBy: truncatedBy,
		TotalLines:  totalLines,
		TotalBytes:  totalBytes,
		OutputLines: len(output),
		OutputBytes: Utf8ByteLength(outputContent),
		MaxLines:    maxLines,
		MaxBytes:    maxBytes,
	}
}
