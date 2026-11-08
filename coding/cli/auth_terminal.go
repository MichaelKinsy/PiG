package cli

// Ports packages/coding-agent/src/cli/auth-terminal.ts

import (
	"context"
	"fmt"
	"io"
	"os"

	"golang.org/x/term"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/terminalquestion"
	"github.com/MichaelKinsy/PiG/tui"
)

// AuthOutput is the output shared by the terminal authentication commands.
type AuthOutput interface {
	Write(line string)
	Error(line string)
}

// LoginTerminal is line-based terminal I/O for `pig auth login`. Prompts fail with the cause of ctx when it ends.
type LoginTerminal interface {
	AuthOutput
	// Interactive reports whether stdin and stdout are a terminal; login prompts need one.
	Interactive() bool
	Question(ctx context.Context, message string) (string, error)
	// Secret reads a line without echoing it.
	Secret(ctx context.Context, message string) (string, error)
	OpenURL(url string)
}

// processLoginTerminal is terminal I/O on stdin and stdout.
type processLoginTerminal struct {
	reader      *terminalquestion.Reader
	output      io.Writer
	onInterrupt func()
}

// CreateProcessLoginTerminal is terminal I/O on stdin and stdout. onInterrupt runs on Ctrl+C or end of input in a question, or Ctrl+C in a secret.
func CreateProcessLoginTerminal(onInterrupt func()) LoginTerminal {
	return processLoginTerminal{reader: terminalquestion.Stdin(), output: os.Stdout, onInterrupt: onInterrupt}
}

func (processLoginTerminal) Interactive() bool {
	return term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd()))
}
func (t processLoginTerminal) Write(line string) { fmt.Fprintln(t.output, line) }
func (processLoginTerminal) Error(line string)   { fmt.Fprintln(os.Stderr, line) }
func (t processLoginTerminal) Question(ctx context.Context, message string) (string, error) {
	return t.reader.Question(ctx, message, t.output, t.onInterrupt)
}
func (t processLoginTerminal) Secret(ctx context.Context, message string) (string, error) {
	return readSecret(ctx, os.Stdin, t.output, message, t.onInterrupt)
}
func (processLoginTerminal) OpenURL(url string) { codingagent.OpenBrowser(url) }

// readSecret reads a line from input, a terminal switched to raw mode, without echoing it. A Ctrl+C runs onInterrupt, which is expected to end ctx.
func readSecret(ctx context.Context, input *os.File, output io.Writer, message string, onInterrupt func()) (string, error) {
	if ctx.Err() != nil {
		return "", errLoginCancelled
	}
	fd := int(input.Fd())
	if _, err := io.WriteString(output, message); err != nil {
		return "", err
	}
	// A stop and continue interrupts the termios ioctl; MakeRaw retries it, as Pi's Node does.
	state, err := tui.MakeRaw(fd)
	if err != nil {
		return "", err
	}
	type result struct {
		value string
		err   error
	}
	done := make(chan result, 1)
	stop := make(chan struct{})
	finish := func() {
		_ = tui.RestoreCooked(fd, state)
		_, _ = io.WriteString(output, "\n")
	}
	go func() {
		var value []rune
		buf := make([]byte, 4096)
		for {
			n, err := input.Read(buf)
			if n > 0 {
				chunk := string(buf[:n])
				// Ignore escape sequences such as arrow keys.
				if chunk[0] == 0x1b {
					continue
				}
				for _, char := range chunk {
					switch {
					case char == '\r' || char == '\n':
						done <- result{value: string(value)}
						return
					case char == 0x03:
						onInterrupt()
					case char == 0x7f || char == '\b':
						if len(value) > 0 {
							value = value[:len(value)-1]
						}
					case char >= ' ':
						value = append(value, char)
					}
				}
			}
			if err != nil {
				done <- result{err: err}
				return
			}
			select {
			case <-stop:
				return
			default:
			}
		}
	}()
	select {
	case got := <-done:
		finish()
		return got.value, got.err
	case <-ctx.Done():
		close(stop)
		finish()
		return "", errLoginCancelled
	}
}
