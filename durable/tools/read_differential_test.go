// Ports packages/durable/test/tools-read-differential.test.ts.

package tools

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	envnode "github.com/MichaelKinsy/PiG/durable/env/node"
	"github.com/MichaelKinsy/PiG/durable/harness"
	"github.com/MichaelKinsy/PiG/internal/jsnumber"
)

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

// referenceRead is the read tool before bounded reads, kept as the reference: it decodes the whole file, splits it into
// lines, and truncates the selection. The tool must give the same result while reading only a bounded part of the file.
func referenceRead(data []byte, path string, offset, limit *float64) (durable.ToolExecutionResult, error) {
	input := ReadToolInput{Path: path, Offset: offset, Limit: limit}
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
	if input.Offset != nil && *input.Offset != 0 && !math.IsNaN(*input.Offset) {
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

func mulberry32(seed uint32) func() float64 {
	state := seed
	return func() float64 {
		state += 0x6d2b79f5
		t := state
		t = (t ^ t>>15) * (t | 1)
		t ^= t + (t^t>>7)*(t|61)
		return float64(t^t>>14) / 4294967296
	}
}

var readPieces = [][]byte{
	{0x0a}, {0x0a}, {0x61}, {0x62, 0x63, 0x64, 0x65}, {0xef, 0xbb, 0xbf}, {0xc3, 0xa9}, {0xe2, 0x82, 0xac},
	{0xf0, 0x9f, 0x98, 0x80}, {0xe2, 0x82}, {0xff}, {0x0d, 0x0a},
}

// randomReadFile is mostly small files, some over the line limit, some over the byte limit, some with one huge line.
func randomReadFile(next func() float64) []byte {
	kind := next()
	if kind < 0.1 {
		return append([]byte{0xef, 0xbb, 0xbf}, []byte(strings.Repeat("x\n", 2100))...)
	}
	if kind < 0.2 {
		line := []byte(strings.Repeat("a", durable.DEFAULT_MAX_BYTES+200+int(next()*400)))
		line[durable.DEFAULT_MAX_BYTES-1+int(next()*3)] = 0xc3
		return append(line, 0x0a, 0x62)
	}
	if kind < 0.3 {
		return []byte(strings.Repeat(strings.Repeat("0123456789", 30)+"\n", 200))
	}
	var file []byte
	pieces := int(next() * 80)
	for range pieces {
		file = append(file, readPieces[int(next()*float64(len(readPieces)))]...)
	}
	return file
}

// undefined is JavaScript's undefined where a number is optional.
var undefined *float64

var (
	readOffsets = []*float64{undefined, new(float64(0)), new(float64(1)), new(float64(2)), new(float64(3)), new(2.5), new(float64(-4)), new(float64(50)), new(float64(2001)), new(float64(3000)), new(1e20), new(math.NaN())}
	readLimits  = []*float64{undefined, new(float64(0)), new(float64(1)), new(float64(2)), new(float64(-3)), new(-1e20), new(1.5), new(float64(7)), new(float64(2000)), new(float64(2500)), new(1e20), new(math.NaN())}
)

func describeNumber(value *float64) string {
	if value == nil {
		return "undefined"
	}
	return jsnumber.String(*value)
}

type readOutcome struct {
	result durable.ToolExecutionResult
	err    string
}

func outcomeOf(result durable.ToolExecutionResult, err error) readOutcome {
	if err != nil {
		return readOutcome{err: err.Error()}
	}
	return readOutcome{result: result}
}

func TestReadToolReturnsExactlyWhatReadingTheWholeFileReturned(t *testing.T) {
	// upstream: packages/durable/test/tools-read-differential.test.ts:156
	cwd := t.TempDir()
	executionEnv := envnode.NewNodeExecutionEnv(envnode.NodeExecutionEnvOptions{Cwd: cwd})
	for seed := uint32(1); seed <= 400; seed++ {
		next := mulberry32(seed)
		file := randomReadFile(next)
		if err := os.WriteFile(filepath.Join(cwd, "f.txt"), file, 0o600); err != nil {
			t.Fatal(err)
		}
		for range 4 {
			offset := readOffsets[int(next()*float64(len(readOffsets)))]
			limit := readLimits[int(next()*float64(len(readLimits)))]
			actual := outcomeOf(readFile(context.Background(), executionEnv, ReadToolInput{Path: "f.txt", Offset: offset, Limit: limit}))
			expected := outcomeOf(referenceRead(file, "f.txt", offset, limit))
			if !reflect.DeepEqual(actual, expected) {
				t.Fatalf("seed %d offset %s limit %s:\nactual   %+v\nexpected %+v", seed, describeNumber(offset), describeNumber(limit), actual, expected)
			}
		}
	}
}

func TestReadToolDetectsAnimatedPNGsWhoseAcTLChunkLiesFarBeyondTheHeader(t *testing.T) {
	// upstream: packages/durable/test/tools-read-differential.test.ts:180
	cwd := t.TempDir()
	chunk := func(kind string, length int) []byte {
		out := []byte{byte(length >> 24), byte(length >> 16), byte(length >> 8), byte(length)}
		out = append(out, kind...)
		return append(out, make([]byte, length+4)...)
	}
	png := []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a}
	for _, part := range [][]byte{chunk("IHDR", 13), chunk("iCCP", 200_000), chunk("acTL", 8), chunk("IDAT", 10)} {
		png = append(png, part...)
	}
	if err := os.WriteFile(filepath.Join(cwd, "a.png"), png, 0o600); err != nil {
		t.Fatal(err)
	}
	executionEnv := envnode.NewNodeExecutionEnv(envnode.NodeExecutionEnvOptions{Cwd: cwd})
	actual := outcomeOf(readFile(context.Background(), executionEnv, ReadToolInput{Path: "a.png"}))
	expected := outcomeOf(referenceRead(png, "a.png", nil, nil))
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("actual %+v, expected %+v", actual, expected)
	}
	// An animated PNG is not a still image, so it reads as text rather than as an unsupported image.
	if actual.result.IsError != nil {
		t.Fatalf("an animated PNG was refused as an image: %+v", actual.result)
	}
}
