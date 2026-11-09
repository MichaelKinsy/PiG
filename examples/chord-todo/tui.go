package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"sync"

	"github.com/MichaelKinsy/PiG/chord/nextnode/tui"
)

// scripted is a terminal for the steps: a size, no keys, and every frame the client wrote kept.
type scripted struct {
	input   *io.PipeReader
	typed   *io.PipeWriter
	mu      sync.Mutex
	written bytes.Buffer
}

func newScripted() *scripted {
	input, typed := io.Pipe()
	return &scripted{input: input, typed: typed}
}

func (t *scripted) Input() io.Reader  { return keys{t.input} }
func (t *scripted) Output() io.Writer { return writer{t} }

// keys is the terminal's input: no keys typed, not a TTY.
type keys struct{ *io.PipeReader }

func (keys) IsTTY() bool           { return false }
func (keys) SetRawMode(bool) error { return nil }

// writer is the terminal's output: a size, and every frame kept.
type writer struct{ t *scripted }

func (writer) Columns() int                  { return 60 }
func (writer) Rows() int                     { return 12 }
func (writer) OnResize(func()) (stop func()) { return func() {} }

func (w writer) Write(p []byte) (int, error) {
	w.t.mu.Lock()
	defer w.t.mu.Unlock()
	return w.t.written.Write(p)
}

var colours = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// frames is everything the client wrote so far.
func (t *scripted) frames() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.written.String()
}

// screen is the terminal's last frame as text (its colours stripped), one string per line.
func (t *scripted) screen() []string {
	all := t.frames()
	if at := strings.LastIndex(all, "\x1b[2J\x1b[H"); at >= 0 {
		all = all[at+len("\x1b[2J\x1b[H"):]
	}
	return strings.Split(strings.TrimSpace(colours.ReplaceAllString(all, "")), "\r\n")
}

// showsScreen is whether the last frame, as text, has text in it.
func (t *scripted) showsScreen(text string) bool {
	return strings.Contains(strings.Join(t.screen(), "\n"), text)
}

// terminalNode is the Node the terminal's sandbox runs on: `CHORD_TUI_NODE`, else `node`.
func terminalNode() string {
	if node := os.Getenv("CHORD_TUI_NODE"); node != "" {
		return node
	}
	return "node"
}

// terminalStep is chapter V (Node 26.5.1 or later): the launcher signs in with the owner token file, downloads the terminal's base
// bundle and the plugins' terminal scripts and runs them in its sandbox, here on a scripted terminal.
func (s *session) terminalStep() error {
	ctx := s.ctx
	if err := tui.CheckSandboxNode(terminalNode()); err != nil {
		_, _ = fmt.Fprintln(s.out, "5. (the terminal client needs Node 26.5.1 or later for its sandbox)")
		return nil
	}
	s.terminal = newScripted()
	running, err := tui.StartTerminal(ctx, tui.TerminalOptions{
		URL:         s.origin,
		Credentials: tui.TerminalCredentials{Kind: tui.CredentialsFile, Path: s.app.OwnerTokenFile},
		Input:       s.terminal.Input(),
		Output:      s.terminal.Output(),
		Colors:      "truecolor",
		Node:        terminalNode(),
		// The machine's languages (its system locale by default), here fixed for the book's output.
		Locales: []string{"en-US"},
	})
	if err != nil {
		return err
	}
	s.running = running
	if err := until(func() (bool, error) { return s.terminal.showsScreen("butter"), nil }); err != nil {
		return fmt.Errorf("the terminal shows no butter: %w\n%q", err, s.terminal.frames())
	}
	_, _ = fmt.Fprintln(s.out, "5. the terminal shows:")
	for _, line := range s.terminal.screen() {
		_, _ = fmt.Fprintln(s.out, "  ", line)
	}
	return nil
}
