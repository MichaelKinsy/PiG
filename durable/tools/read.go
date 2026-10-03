package tools

import (
	"context"
	"fmt"
	"math"
	"strings"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
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

// jsSliceLines is allLines.slice(start, end) with JavaScript's integer
// conversion: a bound is truncated toward zero, and a negative bound counts from
// the end.
func jsSliceLines(lines []string, start, end float64) []string {
	bound := func(value float64) int {
		value = math.Trunc(value)
		if value < 0 {
			return int(math.Max(float64(len(lines))+value, 0))
		}
		return int(math.Min(value, float64(len(lines))))
	}
	from, to := bound(start), bound(end)
	if from >= to {
		return nil
	}
	return lines[from:to]
}

// decodeText decodes bytes like TextDecoder: invalid sequences become U+FFFD and
// a leading byte order mark is dropped.
func decodeText(data []byte) string {
	return strings.TrimPrefix(jsstring.FromUTF8(data), "\uFEFF")
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
	absolutePath, err := resolveReadToolPath(ctx, executionEnv, input.Path)
	if err != nil {
		return durable.ToolExecutionResult{}, err
	}
	data, err := executionEnv.ReadBinaryFile(ctx, absolutePath)
	if err != nil {
		return durable.ToolExecutionResult{}, err
	}
	if mimeType := detectSupportedImageMimeType(data); mimeType != "" {
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

	allLines := strings.Split(decodeText(data), "\n")
	totalFileLines := len(allLines)
	startLine := 0.0
	if input.Offset != nil && *input.Offset != 0 {
		startLine = math.Max(0, *input.Offset-1)
	}
	startLineDisplay := startLine + 1
	if startLine >= float64(len(allLines)) {
		return durable.ToolExecutionResult{}, fmt.Errorf("Offset %s is beyond end of file (%d lines total)", jsnumber.String(*input.Offset), len(allLines))
	}

	var selectedContent string
	userLimitedLines := math.NaN()
	if input.Limit != nil {
		endLine := math.Min(startLine+*input.Limit, float64(len(allLines)))
		selectedContent = strings.Join(jsSliceLines(allLines, startLine, endLine), "\n")
		userLimitedLines = endLine - startLine
	} else {
		selectedContent = strings.Join(jsSliceLines(allLines, startLine, float64(len(allLines))), "\n")
	}

	truncation := durable.TruncateHead(selectedContent, durable.TruncationOptions{})
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
		if startLine == math.Trunc(startLine) {
			lineBytes = []byte(allLines[int(startLine)])
		}
		end := harness.CharacterEnd(lineBytes, durable.DEFAULT_MAX_BYTES)
		outputText = decodeText(lineBytes[:min(end, len(lineBytes))])
		diagnostics = append(diagnostics, durable.ToolDiagnostic{
			Severity: durable.SeverityWarn,
			Code:     "truncated",
			Message: fmt.Sprintf("Line %s is %s, exceeds the %s limit; showing its first %s. Use bash: sed -n %s%sp%s %s | tail -c +%d",
				jsnumber.String(startLineDisplay), durable.FormatSize(len(lineBytes)), durable.FormatSize(durable.DEFAULT_MAX_BYTES),
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
	case !math.IsNaN(userLimitedLines) && startLine+userLimitedLines < float64(len(allLines)):
		remaining := float64(len(allLines)) - (startLine + userLimitedLines)
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
