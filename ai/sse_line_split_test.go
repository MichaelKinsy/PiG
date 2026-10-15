package ai

import (
	"bufio"
	"bytes"
	"io"
	"math/rand"
	"strings"
	"testing"
	"time"
)

// referenceSSELines is the splitter before it kept a scan offset: it searches the whole buffer for a line ending on every call.
func referenceSSELines(data []byte, atEOF bool) (advance int, token []byte, err error) {
	if index := bytes.IndexAny(data, "\r\n"); index >= 0 {
		if data[index] == '\r' && index+1 == len(data) && !atEOF {
			return 0, nil, nil
		}
		advance = index + 1
		if data[index] == '\r' && index+1 < len(data) && data[index+1] == '\n' {
			advance++
		}
		return advance, data[:index], nil
	}
	if atEOF && len(data) > 0 {
		return len(data), data, nil
	}
	return 0, nil, nil
}

func scanSSELines(t testing.TB, reader io.Reader, split bufio.SplitFunc) []string {
	t.Helper()
	scanner := bufio.NewScanner(reader)
	scanner.Split(split)
	scanner.Buffer(make([]byte, 0, 64*1024), sseLineLimit)
	var lines []string
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return lines
}

// The scan offset changes how much of the buffer is searched, never which lines come out: LF, CRLF and lone CR endings, empty lines, a CR at the end of a chunk that a LF follows, and every chunk size down to one byte
// give the lines the full search gives.
func TestSSELineSplitterMatchesTheFullSearchForEveryChunking(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	pieces := []string{"data: x", "event: e", ":comment", "", "\r", "\n", "\r\n", "\r\n\r\n", "a", "\n\r", " ", "d\u00e9"}
	for range 400 {
		var input strings.Builder
		for range rng.Intn(40) {
			input.WriteString(pieces[rng.Intn(len(pieces))])
		}
		want := scanSSELines(t, strings.NewReader(input.String()), referenceSSELines)
		for _, chunk := range []int{1, 2, 3, 7, 64, 1 << 20} {
			got := scanSSELines(t, sseChunked(input.String(), chunk), newSSELineSplitter().split)
			if strings.Join(got, "\x00") != strings.Join(want, "\x00") || len(got) != len(want) {
				t.Fatalf("chunk %d over %q:\n got %q\nwant %q", chunk, input.String(), got, want)
			}
		}
	}
}

// chunked hands the text out in reads of at most size bytes, as a connection delivers a stream.
func sseChunked(text string, size int) io.Reader {
	return &sseLineChunkReader{data: []byte(text), size: size}
}

type sseLineChunkReader struct {
	data []byte
	size int
}

func (r *sseLineChunkReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, io.EOF
	}
	n := min(len(p), r.size, len(r.data))
	copy(p, r.data[:n])
	r.data = r.data[n:]
	return n, nil
}

// A line that arrives in many reads is searched for its ending once, not from its start after every read: decoding a 16 MB line delivered in 4 KB reads takes about as long as decoding it delivered at once; searching it again from its start after every read took 24 s against 46 ms.
func TestSSELineSplitterDoesNotRescanALongLineAfterEveryRead(t *testing.T) {
	line := "data: " + strings.Repeat("x", 16<<20) + "\n\n"
	best := func(chunk int, split bufio.SplitFunc) time.Duration {
		shortest := time.Duration(1 << 62)
		for range 3 {
			start := time.Now()
			if lines := scanSSELines(t, sseChunked(line, chunk), split); len(lines) != 2 {
				t.Fatalf("%d lines", len(lines))
			}
			shortest = min(shortest, time.Since(start))
		}
		return shortest
	}
	whole := best(len(line), newSSELineSplitter().split)
	pieces := best(4<<10, newSSELineSplitter().split)
	if pieces > 10*whole+60*time.Millisecond {
		t.Fatalf("a 16 MB line in 4 KB reads takes %v, delivered at once %v", pieces, whole)
	}
}

func BenchmarkSSELongLine(b *testing.B) {
	for _, chunk := range []int{4 << 10, 64 << 10} {
		b.Run(sizeName(chunk), func(b *testing.B) {
			line := "data: " + strings.Repeat("x", 16<<20) + "\n\n"
			b.SetBytes(int64(len(line)))
			b.ReportAllocs()
			for b.Loop() {
				decoder := newSSEDecoder(sseChunked(line, chunk))
				if !decoder.Next() {
					b.Fatal("no event")
				}
			}
		})
	}
}

func sizeName(n int) string {
	return map[int]string{4 << 10: "reads-4KB", 64 << 10: "reads-64KB"}[n]
}
