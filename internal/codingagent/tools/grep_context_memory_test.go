package tools

import (
	"bufio"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"testing"
)

// Pi #9276: grep.ts getFileLines reads and splits every matched file to print a
// few context lines, so a 100 MB log with context 2 costs several times its size
// in memory. The same output must come from a window over the file.
func TestGrepContextDoesNotLoadTheMatchedFile(t *testing.T) {
	if _, err := exec.LookPath("rg"); err != nil {
		t.Skip("ripgrep unavailable")
	}
	// The heap is process-wide: goroutines other tests in the package leave behind can allocate during the sample. The
	// measurement runs alone in a fresh test process.
	if os.Getenv("PIG_GREP_HEAP_CHILD") == "" {
		cmd := exec.Command(os.Args[0], "-test.run=^TestGrepContextDoesNotLoadTheMatchedFile$", "-test.v")
		cmd.Env = append(os.Environ(), "PIG_GREP_HEAP_CHILD=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		return
	}
	const lines = 3_000_000
	dir := t.TempDir()
	path := filepath.Join(dir, "server.log")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	w := bufio.NewWriter(f)
	for i := 1; i <= lines; i++ {
		if i == 1_000_000 || i == 2_500_000 {
			_, _ = fmt.Fprintf(w, "needle %d\n", i)
		} else {
			_, _ = fmt.Fprintf(w, "log line %d\n", i)
		}
	}
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	tool := &GrepTool{CWD: dir}
	// HeapAlloc counts garbage not yet collected, and at the default GOGC the pacer lets it grow to about the live heap
	// before the next cycle, so the peak would depend on GC timing (a macOS runner measured 21 MB of slack). A low GOGC
	// keeps the peak near what grep retains; loading the ~50 MB file still exceeds the bound.
	defer debug.SetGCPercent(debug.SetGCPercent(10))
	runtime.GC()
	peak := samplePeakHeap(t)
	result := runFileTool(t, tool, t.Context(), map[string]any{"pattern": "needle", "context": 2})
	grown := peak()

	want := "server.log-999998- log line 999998\nserver.log-999999- log line 999999\nserver.log:1000000: needle 1000000\n" +
		"server.log-1000001- log line 1000001\nserver.log-1000002- log line 1000002\n" +
		"server.log-2499998- log line 2499998\nserver.log-2499999- log line 2499999\nserver.log:2500000: needle 2500000\n" +
		"server.log-2500001- log line 2500001\nserver.log-2500002- log line 2500002"
	if result.IsError || result.Text() != want {
		t.Fatalf("result = %+v", result)
	}
	if grown > uint64(info.Size())/4 {
		t.Fatalf("grep with context grew the heap by %d bytes for a %d byte file; it must not load the file", grown, info.Size())
	}
}

// samplePeakHeap samples HeapAlloc until the returned function is called, and
// that function returns the highest sample above the heap at the start.
func samplePeakHeap(t *testing.T) func() uint64 {
	t.Helper()
	var start runtime.MemStats
	runtime.ReadMemStats(&start)
	stop, done := make(chan struct{}), make(chan uint64)
	go func() {
		peak := start.HeapAlloc
		for {
			var m runtime.MemStats
			runtime.ReadMemStats(&m)
			peak = max(peak, m.HeapAlloc)
			select {
			case <-stop:
				done <- peak - start.HeapAlloc
				return
			default:
			}
		}
	}()
	return func() uint64 {
		close(stop)
		return <-done
	}
}

// referenceGrepLines is upstream getFileLines (grep.ts): decode, normalize line
// endings, split.
func referenceGrepLines(data []byte) []string {
	var decoder utf8StreamDecoder
	content := strings.ReplaceAll(decoder.decode(data, false), "\r\n", "\n")
	return strings.Split(strings.ReplaceAll(content, "\r", "\n"), "\n")
}

// referenceGrepBlock is upstream formatBlock over a whole-file line array.
func referenceGrepBlock(f *grepFormatter, lines []string, m grepMatch) []string {
	relativePath := f.formatPath(m.filePath)
	lineNumber := float64(m.lineNumber)
	start, end := lineNumber, lineNumber
	if f.contextValue > 0 {
		start = math.Max(1, lineNumber-f.contextValue)
		end = math.Min(float64(len(lines)), lineNumber+f.contextValue)
	}
	var block []string
	for current := start; current <= end; current++ {
		lineText := ""
		if current == math.Trunc(current) && current >= 1 && int(current) <= len(lines) {
			lineText = lines[int(current)-1]
		}
		truncated := f.truncate(strings.ReplaceAll(lineText, "\r", ""))
		if current == lineNumber {
			block = append(block, fmt.Sprintf("%s:%s: %s", relativePath, jsNumber(current), truncated))
		} else {
			block = append(block, fmt.Sprintf("%s-%s- %s", relativePath, jsNumber(current), truncated))
		}
	}
	return block
}

// The window must yield the lines and context blocks the whole-file array does,
// for every chunking of the input and for line endings, invalid UTF-8, split
// multi-byte characters, long lines and overlapping or repeated matches.
func TestGrepLineWindowMatchesTheWholeFileSplit(t *testing.T) {
	long := strings.Repeat("x", GrepMaxLineLength+40)
	longWide := strings.Repeat("😀", GrepMaxLineLength)
	inputs := map[string]string{
		"empty":            "",
		"one line":         "only",
		"trailing newline": "a\nb\n",
		"crlf":             "a\r\nb\r\nc",
		"lone cr":          "a\rb\r\rc\r",
		"cr then lf split": "a\r\n\r\n\nb\n\r",
		"invalid utf8":     "a\xffb\xc3\n\xe2\x82",
		"multibyte":        "héllo\nwörld ✓ 😀\nend",
		"bom":              "\ufeffa\nb",
		"long lines":       "short\n" + long + "\n" + longWide + "\n" + long + "y\nend",
	}
	contexts := []float64{0, 1, 2, 2.5, 0.5, 7, 1e12}
	for name, text := range inputs {
		want := referenceGrepLines([]byte(text))
		for _, chunk := range []int{1, 2, 3, 5, 64} {
			t.Run(fmt.Sprintf("%s/chunk=%d", name, chunk), func(t *testing.T) {
				for _, contextValue := range contexts {
					matches := make([]grepMatch, 0, len(want)+2)
					for n := 1; n <= len(want)+1; n++ {
						matches = append(matches, grepMatch{filePath: "/d/f.txt", lineNumber: n})
					}
					matches = append(matches, grepMatch{filePath: "/d/f.txt", lineNumber: 1}) // a match above the window reopens the file
					got := grepFormatter{searchPath: "/d", isDirectory: true, contextValue: contextValue}
					got.openFile = func(string) (io.ReadCloser, error) { return io.NopCloser(strings.NewReader(text)), nil }
					ref := grepFormatter{searchPath: "/d", isDirectory: true, contextValue: contextValue}
					for _, m := range matches {
						if contextValue == 0 {
							// formatMatch only reads the file for context when the match carries no text.
							m.lineText = nil
						}
						w := got.fileWindow(m.filePath, math.Max(1, float64(m.lineNumber)-contextValue))
						w.chunk = make([]byte, chunk)
						gotBlock := formatMatchLines(&got, m)
						wantBlock := referenceGrepBlock(&ref, want, m)
						if strings.Join(gotBlock, "\n") != strings.Join(wantBlock, "\n") {
							t.Fatalf("context %v match line %d:\ngot  %q\nwant %q", contextValue, m.lineNumber, gotBlock, wantBlock)
						}
					}
					got.close()
					if got.linesTruncated != ref.linesTruncated {
						t.Fatalf("context %v: linesTruncated = %v, want %v", contextValue, got.linesTruncated, ref.linesTruncated)
					}
				}
			})
		}
	}
}

func TestGrepContextOfUnreadableFile(t *testing.T) {
	f := grepFormatter{searchPath: "/d", isDirectory: true, contextValue: 1, openFile: func(string) (io.ReadCloser, error) { return nil, os.ErrNotExist }}
	got := formatMatchLines(&f, grepMatch{filePath: "/d/f.txt", lineNumber: 3})
	if strings.Join(got, "\n") != "f.txt:3: (unable to read file)" {
		t.Fatalf("got %q", got)
	}
}

// formatMatchLines is the output lines of one match, collected without a byte cap.
func formatMatchLines(f *grepFormatter, m grepMatch) []string {
	out := newHeadJoin(truncationLimits(1<<30, 1<<53-1))
	f.emitMatch(m, out)
	content := out.Result().Content
	if content == "" {
		return nil
	}
	return strings.Split(content, "\n")
}
