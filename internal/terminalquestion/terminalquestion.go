// Package terminalquestion reads one answer from a terminal, cancelable.
package terminalquestion

// Ports packages/coding-agent/src/utils/terminal-question.ts

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
)

// ErrInterrupted is the AbortError of a question the user ended with Ctrl+C or end of input.
var ErrInterrupted = errors.New("The operation was aborted")

type answer struct {
	line string
	err  error
}

// Reader reads lines from one input. A question the caller cancels leaves its read in flight; the next question takes over that read instead of
// starting a second one, so no input is lost to an abandoned read.
type Reader struct {
	input   io.Reader
	mu      sync.Mutex
	pending chan answer
}

// NewReader reads lines from input one byte at a time, so it consumes nothing past the line a question returns.
func NewReader(input io.Reader) *Reader { return &Reader{input: input} }

var (
	stdinOnce   sync.Once
	stdinReader *Reader
)

// Stdin is the Reader of the process's standard input.
func Stdin() *Reader {
	stdinOnce.Do(func() { stdinReader = NewReader(os.Stdin) })
	return stdinReader
}

// Question writes message to output and reads one line from the input, without its line ending. It fails with the cause of ctx when ctx ends, and with
// ErrInterrupted on Ctrl+C or end of input; onInterrupt, which may be nil, runs for those two, not when the caller cancels just this prompt.
func (r *Reader) Question(ctx context.Context, message string, output io.Writer, onInterrupt func()) (string, error) {
	if ctx.Err() != nil {
		return "", context.Cause(ctx)
	}
	if _, err := io.WriteString(output, message); err != nil {
		return "", err
	}
	r.mu.Lock()
	if r.pending == nil {
		pending := make(chan answer, 1)
		r.pending = pending
		go func() { pending <- r.readLine() }()
	}
	pending := r.pending
	r.mu.Unlock()
	select {
	case <-ctx.Done():
		return "", context.Cause(ctx)
	case got := <-pending:
		r.mu.Lock()
		r.pending = nil
		r.mu.Unlock()
		if got.err != nil {
			if onInterrupt != nil {
				onInterrupt()
			}
			return "", ErrInterrupted
		}
		return got.line, nil
	}
}

// readLine reads up to the next line feed, or end of input. A line that has text before the end of input is an answer; Ctrl+C anywhere in the line, which
// a terminal in raw mode delivers as a byte, is an interrupt.
func (r *Reader) readLine() answer {
	var line []byte
	buf := make([]byte, 1)
	for {
		n, err := r.input.Read(buf)
		if n == 1 {
			switch buf[0] {
			case 0x03:
				return answer{err: ErrInterrupted}
			case '\n':
				return answer{line: strings.TrimSuffix(string(line), "\r")}
			}
			line = append(line, buf[0])
		}
		if err != nil {
			if len(line) > 0 {
				return answer{line: string(bytes.TrimSuffix(line, []byte("\r")))}
			}
			return answer{err: ErrInterrupted}
		}
	}
}
