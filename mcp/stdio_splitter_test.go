package mcp

import (
	"bytes"
	"fmt"
	"io"
	"math/rand/v2"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// chunkReader returns at most size bytes per Read, splitting a stream the way
// a pipe delivers a large message.
type chunkReader struct {
	data  []byte
	sizes func() int
}

func (r *chunkReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, io.EOF
	}
	n := min(r.sizes(), len(p), len(r.data))
	copy(p, r.data[:n])
	r.data = r.data[n:]
	return n, nil
}

func fixedChunks(n int) func() int { return func() int { return n } }

func largeStdioLine(size int) []byte {
	prefix := `{"jsonrpc":"2.0","method":"notifications/message","params":{"data":"`
	suffix := "\"}}\n"
	line := make([]byte, 0, size+len(prefix)+len(suffix))
	line = append(line, prefix...)
	line = append(line, bytes.Repeat([]byte("x"), size)...)
	return append(line, suffix...)
}

// timeStdioFrame is the time the line splitter takes to frame the message in line when it arrives in 4 KiB reads, as
// readStdout hands them over. It times the splitter alone: parsing the message and allocating its copies cost more than
// linear on a Windows host (page commits), and say nothing about whether the splitter rescans. Each measurement covers
// about 64 MiB of input, so a 0.5 ms Windows clock tick and per-run noise do not decide the ratio.
func timeStdioFrame(t *testing.T, line []byte) time.Duration {
	t.Helper()
	reps := max(1, (64<<20)/len(line))
	best := time.Duration(1<<63 - 1)
	for range 3 {
		framed := 0
		start := time.Now()
		for range reps {
			var lines lineSplitter
			for rest := line; len(rest) > 0; {
				n := min(4096, len(rest))
				lines.write(rest[:n])
				rest = rest[n:]
				for {
					if _, ok := lines.next(); !ok {
						break
					}
					framed++
				}
			}
		}
		elapsed := time.Since(start) / time.Duration(reps)
		if framed != reps {
			t.Fatalf("framed %d messages in %d runs", framed, reps)
		}
		best = min(best, elapsed)
	}
	return best
}

// The transport delivers a message spanning many reads whole.
func TestStdioTransportDeliversALargeMessageSpanningManyReads(t *testing.T) {
	transport := &StdioTransport{options: StdioTransportOptions{MaxMessageBytes: 64 << 20}}
	recorded := record(&transport.TransportEvents)
	transport.readStdout(&chunkReader{data: largeStdioLine(4 << 20), sizes: fixedChunks(4096)})
	if len(recorded.messages) != 1 || len(recorded.errors) != 0 {
		t.Fatalf("messages = %d, errors = %q", len(recorded.messages), recorded.errors)
	}
}

// A message spanning many reads must cost time linear in its size: the
// splitter searches only bytes it has not searched before.
func TestStdioSplitterLargeMessageScalesLinearly(t *testing.T) {
	small := timeStdioFrame(t, largeStdioLine(400<<10))
	large := timeStdioFrame(t, largeStdioLine(4<<20))
	ratio := float64(large) / float64(small)
	t.Logf("400KiB: %v, 4MiB: %v, ratio %.1fx", small, large, ratio)
	testenv.TimingGateFatal(t, ratio > 20, "10x message cost %.1fx time (quadratic rescan)", ratio)
}

type stdioOutcome struct {
	messages  []string
	errors    []string
	remainder string
}

func runStdioChunks(stream []byte, sizes func() int, maxBytes int) stdioOutcome {
	transport := &StdioTransport{options: StdioTransportOptions{MaxMessageBytes: maxBytes}}
	recorded := record(&transport.TransportEvents)
	transport.readStdout(&chunkReader{data: stream, sizes: sizes})
	var out stdioOutcome
	for _, m := range recorded.messages {
		out.messages = append(out.messages, m.Method+"|"+string(m.Params))
	}
	out.errors = recorded.errors
	out.remainder = string(transport.stdoutRemainder)
	return out
}

// Random chunkings, including splits inside multi-byte UTF-8 and between
// "\r" and "\n", frame the same messages, errors and remainder as one read.
func TestStdioSplitterChunkingEquivalence(t *testing.T) {
	parts := []string{
		`{"jsonrpc":"2.0","method":"a","params":{"s":"héllo 世界 🙂"}}` + "\r\n",
		"\n",
		"\r\n",
		" \t\uFEFF\n",
		`{"jsonrpc":"2.0","method":"b","params":[1,2,3]}` + "\n",
		"{not json}\n",
		`{"jsonrpc":"2.0"}` + "\n",
		`{"jsonrpc":"2.0","method":"big","params":{"d":"` + strings.Repeat("é", 300) + `"}}` + "\n",
		`{"jsonrpc":"2.0","method":"c"}` + "\r\r\n",
		"\u0085\n",
	}
	rng := rand.New(rand.NewPCG(1, 1))
	for iteration := range 300 {
		var stream strings.Builder
		for range 1 + rng.IntN(12) {
			stream.WriteString(parts[rng.IntN(len(parts))])
		}
		if rng.IntN(2) == 0 {
			stream.WriteString(`{"jsonrpc":"2.0","method":"partial`)
		}
		data := []byte(stream.String())
		var sizes []int
		for range len(data) {
			sizes = append(sizes, 1+rng.IntN(17))
		}
		replay := func() func() int {
			i := 0
			return func() int { i++; return sizes[i-1] }
		}
		whole := runStdioChunks(data, fixedChunks(len(data)+1), 64<<20)
		split := runStdioChunks(data, replay(), 64<<20)
		if !sameOutcome(whole, split) {
			t.Fatalf("iteration %d: whole %+v, split %+v", iteration, whole, split)
		}
		// Under a size limit framing depends on read boundaries (an over-limit
		// partial line is dropped per read), so compare with the rescanning
		// algorithm on the same reads.
		for _, maxBytes := range []int{64 << 20, 200} {
			got := runStdioChunks(data, replay(), maxBytes)
			want := rescanStdioChunks(data, replay(), maxBytes)
			if !sameOutcome(got, want) {
				t.Fatalf("iteration %d max %d: got %+v, rescan %+v", iteration, maxBytes, got, want)
			}
		}
	}
}

func TestSSESplitterChunkingEquivalence(t *testing.T) {
	parts := []string{
		"data: héllo 世界\r\n", "data: two\n", "\n", "\r\n", ": comment\n", "event: e\n",
		"id: 7\n", "retry: 10\n", "data:" + strings.Repeat("é", 200) + "\n", "field\n",
	}
	consume := func(data []byte, sizes func() int, maxBytes int) string {
		var events []string
		err := ConsumeSSEStream(&chunkReader{data: data, sizes: sizes}, ConsumeSSEOptions{
			MaxEventBytes: maxBytes,
			OnEvent:       func(e SSEEvent) { events = append(events, e.Event+"|"+e.ID+"|"+e.Data) },
		})
		if err != nil {
			events = append(events, "error:"+err.Error())
		}
		return strings.Join(events, "\x00")
	}
	rng := rand.New(rand.NewPCG(2, 2))
	for iteration := range 300 {
		var stream strings.Builder
		for range 1 + rng.IntN(14) {
			stream.WriteString(parts[rng.IntN(len(parts))])
		}
		if rng.IntN(2) == 0 {
			stream.WriteString("data: tail")
		}
		data := []byte(stream.String())
		for _, maxBytes := range []int{1 << 20, 300} {
			whole := consume(data, fixedChunks(len(data)+1), maxBytes)
			split := consume(data, func() int { return 1 + rng.IntN(9) }, maxBytes)
			if whole != split {
				t.Fatalf("iteration %d max %d: whole %q, split %q", iteration, maxBytes, whole, split)
			}
		}
	}
}

func sameOutcome(a, b stdioOutcome) bool {
	return strings.Join(a.messages, "\x00") == strings.Join(b.messages, "\x00") &&
		strings.Join(a.errors, "\x00") == strings.Join(b.errors, "\x00") &&
		a.remainder == b.remainder
}

// rescanStdioChunks is the previous splitter, which searched the whole pending
// buffer from offset 0 on every read.
func rescanStdioChunks(stream []byte, sizes func() int, maxBytes int) stdioOutcome {
	transport := &StdioTransport{}
	recorded := record(&transport.TransportEvents)
	r := &chunkReader{data: stream, sizes: sizes}
	buf := make([]byte, stdoutReadChunkBytes)
	var pending []byte
	for {
		n, err := r.Read(buf)
		if n > 0 {
			pending = append(pending, buf[:n]...)
		drain:
			for {
				newline := bytes.IndexByte(pending, '\n')
				if newline < 0 {
					if len(pending) > maxBytes {
						transport.EmitError(fmt.Errorf("MCP stdio message exceeds %d bytes", maxBytes))
						pending = nil
					}
					break drain
				}
				line := pending[:newline]
				pending = pending[newline+1:]
				if len(line) > maxBytes {
					transport.EmitError(fmt.Errorf("MCP stdio message exceeds %d bytes", maxBytes))
					continue
				}
				text := strings.TrimSuffix(string(line), "\r")
				if jsstring.Trim(text) == "" {
					continue
				}
				message, err := parseWireMessage([]byte(text))
				if err != nil {
					transport.EmitError(err)
					continue
				}
				transport.EmitMessage(message)
			}
		}
		if err != nil {
			break
		}
	}
	var out stdioOutcome
	for _, m := range recorded.messages {
		out.messages = append(out.messages, m.Method+"|"+string(m.Params))
	}
	out.errors = recorded.errors
	out.remainder = string(pending)
	return out
}
