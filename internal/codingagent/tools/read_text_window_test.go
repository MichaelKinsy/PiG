package tools

import (
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// referenceReadTextResult is readTextResult as it was: the whole file decoded, split into lines and the selection joined again.
func referenceReadTextResult(p readParams, textContent string) (agent.AgentToolResult, error) {
	allLines := strings.Split(textContent, "\n")
	totalFileLines := len(allLines)
	lineCount := float64(totalFileLines)
	// Line numbers stay JavaScript numbers as in read.ts:160-199: a fractional offset or limit shows up in the
	// continuation notices, and slice truncates it only when selecting lines.
	startLine := 0.0
	if p.Offset != nil && *p.Offset != 0 {
		startLine = math.Max(0, *p.Offset-1)
	}
	startLineDisplay := startLine + 1
	if startLine >= lineCount {
		return agent.AgentToolResult{}, fmt.Errorf("Offset %s is beyond end of file (%d lines total)", jsNumber(*p.Offset), totalFileLines)
	}
	//portlint:allow numbers 0 <= startLine < totalFileLines (checked above), so the truncated index is in range
	sliceStart := int(startLine)

	var selected string
	userLimitedLines, hasUserLimit := 0.0, p.Limit != nil
	if hasUserLimit {
		endLine := math.Min(startLine+*p.Limit, lineCount)
		// slice truncates the end and counts a negative end from the end of the array.
		relativeEnd := math.Trunc(endLine)
		if relativeEnd < 0 {
			relativeEnd = math.Max(lineCount+relativeEnd, 0)
		}
		//portlint:allow numbers relativeEnd is clamped to [0, totalFileLines] above
		selected = strings.Join(allLines[sliceStart:max(sliceStart, int(relativeEnd))], "\n")
		userLimitedLines = endLine - startLine
	} else {
		selected = strings.Join(allLines[sliceStart:], "\n")
	}
	tr := TruncateHead(selected, TruncationOptions{})

	var output string
	var truncation *TruncationResult
	switch {
	case tr.FirstLineExceedsLimit:
		if startLine != float64(sliceStart) {
			// allLines[startLine] is undefined for a fractional index, so Buffer.byteLength throws (read.ts:181).
			return agent.AgentToolResult{}, errors.New(`The "string" argument must be of type string or an instance of Buffer or ArrayBuffer. Received undefined`)
		}
		firstLineSize := FormatSize(len(allLines[sliceStart]))
		output = fmt.Sprintf("[Line %d is %s, exceeds %s limit. Use bash: sed -n '%dp' %s | head -c %d]",
			sliceStart+1, firstLineSize, FormatSize(DefaultMaxBytes), sliceStart+1, p.Path, DefaultMaxBytes)
		truncation = &tr
	case tr.Truncated:
		endLineDisplay := startLineDisplay + float64(tr.OutputLines) - 1
		nextOffset := endLineDisplay + 1
		output = tr.Content
		if tr.TruncatedBy == "lines" {
			output += fmt.Sprintf("\n\n[Showing lines %s-%s of %d. Use offset=%s to continue.]",
				jsNumber(startLineDisplay), jsNumber(endLineDisplay), totalFileLines, jsNumber(nextOffset))
		} else {
			output += fmt.Sprintf("\n\n[Showing lines %s-%s of %d (%s limit). Use offset=%s to continue.]",
				jsNumber(startLineDisplay), jsNumber(endLineDisplay), totalFileLines, FormatSize(DefaultMaxBytes), jsNumber(nextOffset))
		}
		truncation = &tr
	case hasUserLimit && startLine+userLimitedLines < lineCount:
		remaining := lineCount - (startLine + userLimitedLines)
		nextOffset := startLine + userLimitedLines + 1
		output = fmt.Sprintf("%s\n\n[%s more lines in file. Use offset=%s to continue.]", tr.Content, jsNumber(remaining), jsNumber(nextOffset))
	default:
		output = tr.Content
	}
	result := agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: output}}}
	if truncation != nil {
		shareTruncatedContent(truncation, output)
		result.Details = &ReadDetails{Truncation: truncation}
	}
	return result, nil
}

// TestReadTextResultMatchesWholeFileDecode compares the selected-range decode with decoding and splitting the whole file, on randomized
// bytes with invalid UTF-8 (sequences cut by a newline or by the end of the file), BOMs, CRLF and long lines, for integer, fractional
// and negative offsets and limits.
func TestReadTextResultMatchesWholeFileDecode(t *testing.T) {
	rng := rand.New(rand.NewPCG(20261012, 4))
	pieces := []string{"a", "line", "\n", "\r\n", "\xef\xbb\xbf", "\xe2\x82", "\xf0\x9f\x98", "\xff", "\xc3\xa9", "\u2028", strings.Repeat("x", 30000)}
	numbers := []float64{0, 1, 2, 3, 2.5, 0.5, -1, -2.5, 7, 100, 1e9}
	for range 4000 {
		var b strings.Builder
		for range rng.IntN(40) {
			b.WriteString(pieces[rng.IntN(len(pieces))])
		}
		data := []byte(b.String())
		var p readParams
		p.Path = "f.txt"
		if rng.IntN(3) != 0 {
			p.Offset = &numbers[rng.IntN(len(numbers))]
		}
		if rng.IntN(2) == 0 {
			p.Limit = &numbers[rng.IntN(len(numbers))]
		}
		want, wantErr := referenceReadTextResult(p, decodeText(data))
		got, gotErr := readTextResult(p, data)
		if fmt.Sprint(gotErr) != fmt.Sprint(wantErr) || !reflect.DeepEqual(got, want) {
			t.Fatalf("readTextResult(%+v, %q):\n got  %+v, %v\n want %+v, %v", p, data, got, gotErr, want, wantErr)
		}
	}
}

// TestReadWindowOfLargeFileDecodesOnlyTheWindow pins TO-T4: reading 20 lines of a 5 MB file holds the file once and decodes only the
// window (the whole-file decode, split and join allocated about 4 times the file).
func TestReadWindowOfLargeFileDecodesOnlyTheWindow(t *testing.T) {
	dir := t.TempDir()
	content := strings.Repeat("\tfmt.Println(\"a line of a large log file with some text\")\n", 100_000)
	if err := os.WriteFile(filepath.Join(dir, "big.log"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := &ReadTool{CWD: dir}
	args := []byte(`{"path":"big.log","offset":50000,"limit":20}`)
	run := func() agent.AgentToolResult {
		result, err := tool.Execute(t.Context(), "id", args, nil)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	if result := run(); result.IsError {
		t.Fatalf("read failed: %+v", result)
	}
	bytes := testing.Benchmark(func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			run()
		}
	}).AllocedBytesPerOp()
	if budget := int64(len(content)) + 64<<10; bytes > budget {
		t.Fatalf("reading 20 lines of a %d-byte file allocated %d bytes/op, want <= %d", len(content), bytes, budget)
	}
}
