package tools

import (
	"context"
	"fmt"
	"math"
	"strings"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/env"
	"github.com/MichaelKinsy/PiG/durable/harness"
	"github.com/MichaelKinsy/PiG/internal/jsnumber"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// Ports packages/durable/src/tools/read.ts.

const readParameters = `{"type":"object","required":["path"],"properties":{"path":{"type":"string","description":"Path to the file to read (relative or absolute)"},"offset":{"type":"number","description":"Line number to start reading from (1-indexed)"},"limit":{"type":"number","description":"Maximum number of lines to read"}}}`

// ReadToolInput is the arguments of the read tool. Offset and Limit are JSON
// numbers, which need not be whole.
type ReadToolInput struct {
	Path   string   `json:"path"`
	Offset *float64 `json:"offset,omitempty"`
	Limit  *float64 `json:"limit,omitempty"`
}

// ReadTruncation is how the shown text was cut; the text itself is the result
// content. It is a TruncationResult without its Content.
type ReadTruncation struct {
	Truncated             bool                `json:"truncated"`
	TruncatedBy           durable.TruncatedBy `json:"truncatedBy"`
	TotalLines            int                 `json:"totalLines"`
	TotalBytes            int                 `json:"totalBytes"`
	OutputLines           int                 `json:"outputLines"`
	OutputBytes           int                 `json:"outputBytes"`
	LastLinePartial       bool                `json:"lastLinePartial"`
	FirstLineExceedsLimit bool                `json:"firstLineExceedsLimit"`
	MaxLines              int                 `json:"maxLines"`
	MaxBytes              int                 `json:"maxBytes"`
}

// ReadToolDetails are the details of a read result that was truncated.
type ReadToolDetails struct {
	Truncation *ReadTruncation `json:"truncation,omitempty"`
}

// CreateReadTool creates the read tool. It reads text files. Remarks about
// truncation and continuation are diagnostics; the content is only file text.
func CreateReadTool() *durable.ToolRegistration {
	description := fmt.Sprintf("Read the contents of a text file. Output is truncated to %d lines or %dKB (whichever is hit first). Use offset/limit for large files. When you need the full file, continue with offset until complete.", durable.DEFAULT_MAX_LINES, durable.DEFAULT_MAX_BYTES/1024)
	return &durable.ToolRegistration{
		ToolSchema: toolSchema("read", description, readParameters),
		Execute:    executeRead,
	}
}

// readChunk is the largest single read while collecting the shown head.
const readChunk = 64 * 1024

// sliceIndex is Array.prototype.slice's conversion of an index: NaN is 0, other values truncate toward zero.
func sliceIndex(value float64) float64 {
	if math.IsNaN(value) {
		return 0
	}
	return math.Trunc(value)
}

// isSafeInteger is Number.isSafeInteger.
func isSafeInteger(value float64) bool {
	return value == math.Trunc(value) && math.Abs(value) <= 1<<53-1
}

// decodeText decodes bytes like TextDecoder: invalid sequences become U+FFFD and
// a leading byte order mark is dropped.
func decodeText(data []byte) string {
	return strings.TrimPrefix(jsstring.FromUTF8(data), "\uFEFF")
}

// readHead is the decoded start of bytes [start, end) of the file, decoded as part of the whole file: all of it, or
// enough for TruncateHeadOf (more than DEFAULT_MAX_BYTES + 1 bytes, or DEFAULT_MAX_LINES newlines).
func readHead(ctx context.Context, reader env.BinaryReader, start, end int64, skipBom bool) (string, error) {
	decoder := env.NewRangeDecoder()
	var text strings.Builder
	newlines := 0
	position := start
	if skipBom && start == 0 {
		position = 3
	}
	for position < end {
		bytes, err := reader.Read(ctx, position, min(readChunk, end-position))
		if err != nil {
			return "", err
		}
		if len(bytes) == 0 {
			break
		}
		position += int64(len(bytes))
		decoded := decoder.Decode(bytes)
		text.WriteString(decoded)
		newlines += strings.Count(decoded, "\n")
		if newlines >= durable.DEFAULT_MAX_LINES || text.Len() > durable.DEFAULT_MAX_BYTES+1 {
			return text.String(), nil
		}
	}
	return text.String() + decoder.Flush(), nil
}

func executeRead(ctx context.Context, args any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
	input, err := durable.FromJsonValue[ReadToolInput](args)
	if err != nil {
		return durable.ToolExecutionResult{}, err
	}
	executionEnv, err := requireEnv(api)
	if err != nil {
		return durable.ToolExecutionResult{}, err
	}
	return readFile(ctx, executionEnv, input)
}

// readFile reads the file input names through the environment's binary reader.
func readFile(ctx context.Context, executionEnv env.ExecutionEnv, input ReadToolInput) (durable.ToolExecutionResult, error) {
	absolutePath, err := resolveReadToolPath(ctx, executionEnv, input.Path)
	if err != nil {
		return durable.ToolExecutionResult{}, err
	}
	reader, err := executionEnv.OpenBinaryReader(ctx, absolutePath, nil)
	if err != nil {
		return durable.ToolExecutionResult{}, err
	}
	defer func() { _ = reader.Close(ctx) }()
	// A concurrent writer can change the file between the scan and the reads. Appending (a growing log) leaves the scanned
	// bytes as they were; a file that shrank or was rewritten in place is read again once.
	for attempt := 0; ; attempt++ {
		before, err := reader.Info(ctx)
		if err != nil {
			return durable.ToolExecutionResult{}, err
		}
		result, err := readText(ctx, reader, before, input)
		if err != nil {
			return durable.ToolExecutionResult{}, err
		}
		after, err := reader.Info(ctx)
		if err != nil {
			return durable.ToolExecutionResult{}, err
		}
		if after.Size > before.Size || after.Size == before.Size && after.MtimeMs == before.MtimeMs {
			return result, nil
		}
		if attempt == 1 {
			return durable.ToolExecutionResult{}, fmt.Errorf("%s changed while it was read", input.Path)
		}
	}
}

// readText is the read result for the opened file. It equals decoding the whole file with TextDecoder, splitting it on
// "\n", and bounding the selected lines with TruncateHead, while reading only one scan's worth of the file plus the
// head.
func readText(ctx context.Context, reader env.BinaryReader, info env.FileInfo, input ReadToolInput) (durable.ToolExecutionResult, error) {
	mimeType, err := detectSupportedImageMimeTypeOf(byteSource{size: info.Size, read: func(offset, length int64) ([]byte, error) {
		return reader.Read(ctx, offset, length)
	}})
	if err != nil {
		return durable.ToolExecutionResult{}, err
	}
	if mimeType != "" {
		// Image content is not supported yet.
		isError := true
		return durable.ToolExecutionResult{
			Content: []ai.ToolResultMessageContent{},
			IsError: &isError,
			Diagnostics: []durable.ToolDiagnostic{{
				Severity: durable.SeverityError,
				Code:     "unsupported_image",
				Message:  fmt.Sprintf("%s is an image (%s); reading images is not supported", input.Path, mimeType),
			}},
		}, nil
	}

	startLine := 0.0
	if input.Offset != nil && *input.Offset != 0 && !math.IsNaN(*input.Offset) {
		startLine = math.Max(0, *input.Offset-1)
	}
	startLineDisplay := startLine + 1
	// Lines are selected like allLines.slice(startLine, endLine), which truncates fractional indices.
	sliceStart := sliceIndex(startLine)
	// One pass finds the line count and the selection; a selection past the last line ends with it, as slice does, and
	// an empty one (a zero or negative limit) is scanned as one line and then ignored. A start beyond any file is
	// scanned from 0 only to count lines; the offset check below then fails as before.
	scanStart := 0.0
	if isSafeInteger(sliceStart) {
		scanStart = sliceStart
	}
	var scanEnd *int64
	if input.Limit != nil {
		if requestedEnd := math.Max(scanStart+1, sliceIndex(startLine+*input.Limit)); isSafeInteger(requestedEnd) {
			scanEnd = new(int64(requestedEnd))
		}
	}
	scanOf := func(endLine *int64) (env.LineScan, error) {
		return reader.ScanLines(ctx, env.ScanLinesOptions{StartLine: int64(scanStart), EndLine: endLine})
	}
	scan, err := scanOf(scanEnd)
	if err != nil {
		return durable.ToolExecutionResult{}, err
	}
	totalFileLines := scan.Newlines + 1
	if startLine >= float64(totalFileLines) {
		return durable.ToolExecutionResult{}, fmt.Errorf("Offset %s is beyond end of file (%d lines total)", jsnumber.String(*input.Offset), totalFileLines)
	}

	userLimitedLines := math.NaN()
	selectedLineCount := float64(totalFileLines) - sliceStart
	if input.Limit != nil {
		endLine := math.Min(startLine+*input.Limit, float64(totalFileLines))
		userLimitedLines = endLine - startLine
		// slice counts a negative end from the end of the lines, which only the line count tells; scan again for it.
		relativeEnd := sliceIndex(endLine)
		sliceEnd := relativeEnd
		if relativeEnd < 0 {
			sliceEnd = math.Max(float64(totalFileLines)+relativeEnd, 0)
		}
		selectedLineCount = math.Max(0, sliceEnd-sliceStart)
		if selectedLineCount > 0 && relativeEnd < 0 {
			if scan, err = scanOf(new(int64(sliceEnd))); err != nil {
				return durable.ToolExecutionResult{}, err
			}
		}
	}
	empty := selectedLineCount == 0
	// Counted like TruncateHead: a trailing newline adds no line, and empty text has none.
	endsWithNewline := !empty && scan.LastLineStart == scan.End && scan.LastLineStart > scan.Start
	totals := durable.TruncationTotals{}
	if !empty {
		totals.Bytes = int(scan.SelectedBytes)
		if scan.SelectedBytes != 0 {
			totals.Lines = int(selectedLineCount)
			if endsWithNewline {
				totals.Lines--
			}
		}
	}
	firstBytes, err := reader.Read(ctx, 0, 3)
	if err != nil {
		return durable.ToolExecutionResult{}, err
	}
	head := ""
	if !empty {
		if head, err = readHead(ctx, reader, scan.Start, scan.End, env.StartsWithBom(firstBytes)); err != nil {
			return durable.ToolExecutionResult{}, err
		}
	}

	truncation := durable.TruncateHeadOf(head, totals, durable.TruncationOptions{})
	diagnostics := []durable.ToolDiagnostic{}
	outputText := truncation.Content
	var details *ReadToolDetails
	shown := ReadTruncation{
		Truncated: truncation.Truncated, TruncatedBy: truncation.TruncatedBy,
		TotalLines: truncation.TotalLines, TotalBytes: truncation.TotalBytes,
		OutputLines: truncation.OutputLines, OutputBytes: truncation.OutputBytes,
		LastLinePartial: truncation.LastLinePartial, FirstLineExceedsLimit: truncation.FirstLineExceedsLimit,
		MaxLines: truncation.MaxLines, MaxBytes: truncation.MaxBytes,
	}
	switch {
	case truncation.FirstLineExceedsLimit:
		// Show the start of the line, cut at the byte limit on a character boundary. A fractional start line
		// indexes no line, which upstream encodes as no bytes.
		var lineBytes []byte
		lineSize := 0
		if startLine == math.Trunc(startLine) {
			firstLine, _, _ := strings.Cut(head, "\n")
			lineBytes = []byte(firstLine)
			lineSize = int(scan.FirstLineBytes)
		}
		end := harness.CharacterEnd(lineBytes, durable.DEFAULT_MAX_BYTES)
		outputText = decodeText(lineBytes[:min(end, len(lineBytes))])
		diagnostics = append(diagnostics, durable.ToolDiagnostic{
			Severity: durable.SeverityWarn,
			Code:     "truncated",
			Message: fmt.Sprintf("Line %s is %s, exceeds the %s limit; showing its first %s. Use bash: sed -n %s%sp%s %s | tail -c +%d",
				jsnumber.String(startLineDisplay), durable.FormatSize(lineSize), durable.FormatSize(durable.DEFAULT_MAX_BYTES),
				durable.FormatSize(end), "'", jsnumber.String(startLineDisplay), "'", input.Path, end+1),
		})
		shown.OutputBytes, shown.OutputLines = end, 1
		details = &ReadToolDetails{Truncation: &shown}
	case truncation.Truncated:
		endLineDisplay := startLineDisplay + float64(truncation.OutputLines) - 1
		nextOffset := endLineDisplay + 1
		limitText := ""
		if truncation.TruncatedBy != durable.TruncatedByLines {
			limitText = fmt.Sprintf(" (%s limit)", durable.FormatSize(durable.DEFAULT_MAX_BYTES))
		}
		diagnostics = append(diagnostics, durable.ToolDiagnostic{
			Severity: durable.SeverityInfo,
			Code:     "truncated",
			Message: fmt.Sprintf("Showing lines %s-%s of %d%s. Use offset=%s to continue.",
				jsnumber.String(startLineDisplay), jsnumber.String(endLineDisplay), totalFileLines, limitText, jsnumber.String(nextOffset)),
		})
		details = &ReadToolDetails{Truncation: &shown}
	case !math.IsNaN(userLimitedLines) && startLine+userLimitedLines < float64(totalFileLines):
		remaining := float64(totalFileLines) - (startLine + userLimitedLines)
		nextOffset := startLine + userLimitedLines + 1
		diagnostics = append(diagnostics, durable.ToolDiagnostic{
			Severity: durable.SeverityInfo,
			Message:  fmt.Sprintf("%s more lines in file. Use offset=%s to continue.", jsnumber.String(remaining), jsnumber.String(nextOffset)),
		})
	}

	result := durable.ToolExecutionResult{Content: []ai.ToolResultMessageContent{}, Diagnostics: diagnostics}
	if outputText != "" {
		result.Content = []ai.ToolResultMessageContent{ai.TextContent{Text: outputText}}
	}
	if details != nil {
		encoded, err := durable.ToJsonValue(details)
		if err != nil {
			return durable.ToolExecutionResult{}, err
		}
		result.Details, result.HasDetails = encoded, true
	}
	return result, nil
}
