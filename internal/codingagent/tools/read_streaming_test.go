package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
)

// Ported from upstream test/read-streaming.test.ts (3a7bb6f48, #11750).

// readOutcome is a read's text and details, or its error message.
type readOutcome struct {
	text    string
	details string
	err     string
}

const streamingTestPath = "/virtual/pi-read-streaming-test-missing/file.txt"

// referenceRead is the read tool's text path before it streamed (#9654), kept as the reference the new path must match.
func referenceRead(text string, offset, limit *float64) readOutcome {
	allLines := strings.Split(text, "\n")
	totalFileLines := len(allLines)
	startLine := 0
	if offset != nil && *offset != 0 {
		startLine = max(0, int(*offset)-1)
	}
	startLineDisplay := startLine + 1
	if startLine >= len(allLines) {
		return readOutcome{err: fmt.Sprintf("Offset %s is beyond end of file (%d lines total)", jsNumber(*offset), len(allLines))}
	}
	userLimitedLines := -1
	var selected string
	if limit != nil {
		endLine := min(startLine+int(*limit), len(allLines))
		selected = strings.Join(allLines[startLine:endLine], "\n")
		userLimitedLines = endLine - startLine
	} else {
		selected = strings.Join(allLines[startLine:], "\n")
	}
	tr := TruncateHead(selected, TruncationOptions{})
	var out string
	var details *TruncationResult
	switch {
	case tr.FirstLineExceedsLimit:
		out = fmt.Sprintf("[Line %d is %s, exceeds %s limit. Use bash: sed -n '%dp' %s | head -c %d]", startLineDisplay, FormatSize(len(allLines[startLine])), FormatSize(DefaultMaxBytes), startLineDisplay, streamingTestPath, DefaultMaxBytes)
		details = &tr
	case tr.Truncated:
		endLineDisplay := startLineDisplay + tr.OutputLines - 1
		out = tr.Content
		if tr.TruncatedBy == "lines" {
			out += fmt.Sprintf("\n\n[Showing lines %d-%d of %d. Use offset=%d to continue.]", startLineDisplay, endLineDisplay, totalFileLines, endLineDisplay+1)
		} else {
			out += fmt.Sprintf("\n\n[Showing lines %d-%d of %d (%s limit). Use offset=%d to continue.]", startLineDisplay, endLineDisplay, totalFileLines, FormatSize(DefaultMaxBytes), endLineDisplay+1)
		}
		details = &tr
	case userLimitedLines >= 0 && startLine+userLimitedLines < len(allLines):
		out = fmt.Sprintf("%s\n\n[%d more lines in file. Use offset=%d to continue.]", tr.Content, len(allLines)-(startLine+userLimitedLines), startLine+userLimitedLines+1)
	default:
		out = tr.Content
	}
	outcome := readOutcome{text: out}
	if details != nil {
		raw, _ := json.Marshal(details)
		outcome.details = string(raw)
	}
	return outcome
}

func outcomeOf(t *testing.T, result agent.AgentToolResult) readOutcome {
	t.Helper()
	if result.IsError {
		return readOutcome{err: result.Text()}
	}
	outcome := readOutcome{text: result.Text()}
	if details, ok := result.Details.(*ReadDetails); ok && details != nil && details.Truncation != nil {
		raw, err := json.Marshal(details.Truncation)
		if err != nil {
			t.Fatal(err)
		}
		outcome.details = string(raw)
	}
	return outcome
}

// chunkReader yields data in chunks of size, so characters and lines split at chunk ends.
type chunkReader struct {
	data []byte
	size int
}

func (r *chunkReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, io.EOF
	}
	n := min(r.size, len(r.data), len(p))
	copy(p, r.data[:n])
	r.data = r.data[n:]
	return n, nil
}

func streamingOps(data []byte, chunkSize int) *ReadOperations {
	return &ReadOperations{
		ReadFile: func(string) ([]byte, error) {
			return nil, errors.New("readFile must not be used when createReadStream is present")
		},
		Access: func(string) error { return nil },
		CreateReadStream: func(context.Context, string) (io.ReadCloser, error) {
			return io.NopCloser(&chunkReader{data: data, size: chunkSize}), nil
		},
	}
}

func readWith(t *testing.T, ops *ReadOperations, offset, limit *float64) readOutcome {
	t.Helper()
	tool := &ReadTool{CWD: "/virtual", Operations: ops}
	params := map[string]any{"path": streamingTestPath}
	if offset != nil {
		params["offset"] = *offset
	}
	if limit != nil {
		params["limit"] = *limit
	}
	return outcomeOf(t, runFileTool(t, tool, t.Context(), params))
}

func repeatLines(count int, line func(i int) string) string {
	lines := make([]string, count)
	for i := range lines {
		lines[i] = line(i)
	}
	return strings.Join(lines, "\n")
}

func TestReadStreamingGivesTheSameOutputAsTheWholeFileAlgorithm(t *testing.T) {
	fiftyKBOfLines := repeatLines(512, func(int) string { return strings.Repeat("y", 99) }) + "y"
	cases := map[string]string{
		"empty":                              "",
		"no trailing newline":                "alpha\nbeta\ngamma",
		"trailing newline":                   "alpha\nbeta\n",
		"CRLF":                               "alpha\r\nbeta\r\n\r\ngamma\r\n",
		"multi-byte and BOM":                 "\uFEFFé€😀\nñ\U00010348字\n😀😀😀",
		"2001 lines":                         repeatLines(2001, func(i int) string { return fmt.Sprintf("line %d", i) }),
		"2001 lines, an empty line and more": repeatLines(2001, func(i int) string { return fmt.Sprintf("line %d", i) }) + "\n\nmore",
		"first line over 50KB":               strings.Repeat("é", DefaultMaxBytes/2+1) + "\nsecond",
		"line over 50KB after short lines":   "a\nb\n" + strings.Repeat("z", 60*1024) + "\nc",
		// 50KB + 1 byte: truncated, yet every line fits, so TruncateHead reports truncatedBy "lines".
		"exactly 50KB and a trailing newline":  fiftyKBOfLines + "\n",
		"exactly 50KB, an empty line and more": fiftyKBOfLines + "\n\nmore",
		"multi-byte characters over 50KB":      repeatLines(700, func(i int) string { return fmt.Sprintf("%d %s", i, strings.Repeat("€😀", 13)) }),
	}
	for name, text := range cases {
		total := float64(len(strings.Split(text, "\n")))
		for _, offset := range []*float64{nil, new(float64(2)), new(float64(3)), new(float64(total)), new(float64(total + 1))} {
			for _, limit := range []*float64{nil, new(float64(0)), new(float64(1)), new(float64(total))} {
				want := referenceRead(text, offset, limit)
				for _, chunkSize := range []int{1, 4096} {
					label := fmt.Sprintf("%s, offset %v, limit %v, chunks of %d", name, optionalValue(offset), optionalValue(limit), chunkSize)
					if got := readWith(t, streamingOps([]byte(text), chunkSize), offset, limit); got != want {
						t.Fatalf("%s:\n got  %+v\n want %+v", label, got, want)
					}
				}
			}
		}
	}
}

func optionalValue(v *float64) any {
	if v == nil {
		return nil
	}
	return *v
}

func TestReadStreamingKeepsDetailsKeyOrder(t *testing.T) {
	text := repeatLines(2001, func(i int) string { return fmt.Sprintf("line %d", i) })
	got := readWith(t, streamingOps([]byte(text), 4096), new(float64(1)), nil)
	want := referenceRead(text, new(float64(1)), nil)
	if got.details == "" || got.details != want.details {
		t.Fatalf("details = %s, want %s", got.details, want.details)
	}
}

func TestReadStreamingDecodesChunkSplitCharactersAndInvalidUTF8LikeBufferToString(t *testing.T) {
	data := append([]byte("aé€😀\n😀b\U00010348\n"), 0x61, 0xc3, 0x0a, 0xe2, 0x82, 0x0a, 0xff, 0xfe, 0x80, 0xf0, 0x9f, 0x98, 0x0a, 0xed, 0xa0, 0x80)
	want := referenceRead(decodeText(data), nil, nil)
	for _, chunkSize := range []int{1, 2, 3} {
		if got := readWith(t, streamingOps(data, chunkSize), nil, nil); got != want {
			t.Fatalf("chunks of %d:\n got  %+v\n want %+v", chunkSize, got, want)
		}
	}
}

func TestReadStreamingUsesReadFileWithoutCreateReadStreamAndForFractionalOrNegativeRanges(t *testing.T) {
	text := "one\ntwo\nthree\nfour"
	plain := &ReadOperations{
		ReadFile: func(string) ([]byte, error) { return []byte(text), nil },
		Access:   func(string) error { return nil },
	}
	withStream := streamingOps([]byte(text), 1)
	withStream.ReadFile = plain.ReadFile
	for _, r := range [][2]*float64{{nil, nil}, {new(float64(2)), new(float64(1))}, {new(float64(9)), nil}} {
		if got, want := readWith(t, plain, r[0], r[1]), referenceRead(text, r[0], r[1]); got != want {
			t.Fatalf("plain %v: got %+v want %+v", r, got, want)
		}
	}
	// Slicing defines these: limit -1 selects all lines but the last. The whole-file path must run, never the stream.
	for _, r := range [][2]*float64{{new(float64(2.5)), new(float64(1))}, {nil, new(float64(-1))}, {new(float64(1)), new(float64(1.5))}} {
		got := readWith(t, withStream, r[0], r[1])
		if got.err != "" && strings.Contains(got.err, "must not be used") {
			t.Fatalf("range %v used the stream: %+v", optionalValue(r[0]), got)
		}
	}
	// Pi's slicing gives limit -1 all lines but the last, and a continuation notice counted from a negative end.
	if got := readWith(t, withStream, nil, new(float64(-1))); got.text != "one\ntwo\nthree\n\n[5 more lines in file. Use offset=0 to continue.]" {
		t.Fatalf("limit -1 = %+v", got)
	}
}

func TestReadStreamingReadsLocalFilesThroughTheDefaultStream(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "local.txt")
	text := repeatLines(3000, func(i int) string { return fmt.Sprintf("%d %s", i, strings.Repeat("w", i%40)) })
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	tool := &ReadTool{CWD: dir}
	for _, r := range [][2]*float64{{nil, nil}, {new(float64(1500)), new(float64(10))}} {
		params := map[string]any{"path": path}
		if r[0] != nil {
			params["offset"] = *r[0]
		}
		if r[1] != nil {
			params["limit"] = *r[1]
		}
		got := outcomeOf(t, runFileTool(t, tool, t.Context(), params))
		want := referenceRead(text, r[0], r[1])
		want.text = strings.ReplaceAll(want.text, streamingTestPath, path)
		if r[0] == nil && r[1] == nil && got.text != want.text {
			t.Fatalf("whole read differs:\n got  %q\n want %q", got.text, want.text)
		}
		if r[0] != nil && got != want {
			t.Fatalf("got %+v want %+v", got, want)
		}
	}
	res := runFileTool(t, tool, t.Context(), map[string]any{"path": dir})
	if !res.IsError || !strings.Contains(res.Text(), "EISDIR") {
		t.Fatalf("directory = %+v", res)
	}
}

func TestReadStreamingReadsALocalFileUpToItsSizeWhenOpened(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "growing.txt")
	// Larger than one stream chunk, so the append lands before the stream reaches the end.
	if err := os.WriteFile(path, []byte("head\n"+strings.Repeat("x", 200_000)), 0o600); err != nil {
		t.Fatal(err)
	}
	afterReadChunkHook = func() {
		afterReadChunkHook = nil
		f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			t.Error(err)
			return
		}
		_, _ = f.WriteString("\nadded")
		_ = f.Close()
	}
	t.Cleanup(func() { afterReadChunkHook = nil })
	tool := &ReadTool{CWD: dir}
	res := runFileTool(t, tool, t.Context(), map[string]any{"path": path, "limit": 1})
	if res.IsError || res.Text() != "head\n\n[1 more lines in file. Use offset=2 to continue.]" {
		t.Fatalf("result = %+v", res)
	}
}

func TestReadStreamingKeepsMemoryBoundedForA200MBLine(t *testing.T) {
	// 64 KB chunks, as fs streams them: the line is never stored whole.
	const chunks = 3200
	chunk := bytes.Repeat([]byte{'x'}, 64*1024)
	var peak uint64
	sent := 0
	ops := &ReadOperations{
		ReadFile: func(string) ([]byte, error) { return nil, nil },
		Access:   func(string) error { return nil },
		CreateReadStream: func(context.Context, string) (io.ReadCloser, error) {
			return io.NopCloser(readerFunc(func(p []byte) (int, error) {
				if sent == chunks {
					return 0, io.EOF
				}
				if sent%100 == 0 {
					var m runtime.MemStats
					runtime.ReadMemStats(&m)
					peak = max(peak, m.HeapAlloc)
				}
				sent++
				return copy(p, chunk), nil
			})), nil
		},
	}
	tool := &ReadTool{CWD: "/virtual", Operations: ops}
	res := runFileTool(t, tool, t.Context(), map[string]any{"path": "big.log"})
	want := "[Line 1 is 200.0MB, exceeds 50.0KB limit. Use bash: sed -n '1p' big.log | head -c 51200]"
	if res.Text() != want {
		t.Fatalf("text = %q", res.Text())
	}
	if peak > 64<<20 {
		t.Fatalf("heap peaked at %d MB reading a 200 MB line", peak>>20)
	}
}

type readerFunc func([]byte) (int, error)

func (f readerFunc) Read(p []byte) (int, error) { return f(p) }

func TestReadStreamingStopsConsumingTheStreamWhenAborted(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	chunksRead := 0
	closed := false
	ops := &ReadOperations{
		ReadFile: func(string) ([]byte, error) { return nil, nil },
		Access:   func(string) error { return nil },
		CreateReadStream: func(streamCtx context.Context, _ string) (io.ReadCloser, error) {
			if streamCtx != ctx {
				t.Error("the stream did not get the tool's context")
			}
			return &closeTracker{Reader: readerFunc(func(p []byte) (int, error) {
				chunksRead++
				if chunksRead == 3 {
					cancel()
				}
				return copy(p, "line\n"), nil
			}), onClose: func() { closed = true }}, nil
		},
	}
	tool := &ReadTool{CWD: "/virtual", Operations: ops}
	res := runFileTool(t, tool, ctx, map[string]any{"path": streamingTestPath})
	if !res.IsError || res.Text() != "Operation aborted" {
		t.Fatalf("result = %+v", res)
	}
	if !closed || chunksRead != 3 {
		t.Fatalf("closed = %v, chunksRead = %d, want true and 3", closed, chunksRead)
	}
}

type closeTracker struct {
	io.Reader
	onClose func()
}

func (c *closeTracker) Close() error { c.onClose(); return nil }
