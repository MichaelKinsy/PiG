package harness

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// TermOpts configures one terminal session.
type TermOpts struct {
	Dir    string   // working directory
	Env    []string // complete child environment (KEY=VALUE); nothing is inherited
	Argv   []string // program and arguments
	Width  int
	Height int
	// Root is a private directory for the launcher, exit code, stderr and PID files.
	Root string
}

// Terminal is one program running in a real pseudo-terminal behind a private tmux server. Output events arrive through tmux control mode, so every wait blocks on an event instead of sleeping.
type Terminal struct {
	opts    TermOpts
	sock    string
	session string
	root    string

	ctl   *exec.Cmd
	stdin io.WriteCloser

	cmdMu   sync.Mutex // serialises control-mode commands
	replies chan reply

	mu      sync.Mutex
	changed chan struct{}
	outputs int
	lastOut time.Time
	closed  bool

	exited   chan struct{}
	waitDone chan struct{}
	pid      int
}

type reply struct {
	lines []string
	err   bool
}

// StartTerminal launches Argv in a fresh tmux server and session.
func StartTerminal(o TermOpts) (*Terminal, error) {
	if o.Width == 0 {
		o.Width = 120
	}
	if o.Height == 0 {
		o.Height = 40
	}
	id := fmt.Sprintf("%d-%d", os.Getpid(), uniqueN())
	t := &Terminal{opts: o, sock: "pig-jny-" + id, session: "pig-jny", root: o.Root,
		changed: make(chan struct{}), exited: make(chan struct{}), waitDone: make(chan struct{}), replies: make(chan reply, 64)}
	if err := os.MkdirAll(t.root, 0o700); err != nil {
		return nil, err
	}
	conf := filepath.Join(t.root, "tmux.conf")
	confText := "set -g remain-on-exit on\nset -g window-size manual\nset -g history-limit 200000\nset -g extended-keys on\n"
	if err := os.WriteFile(conf, []byte(confText), 0o600); err != nil {
		return nil, err
	}
	launcher := filepath.Join(t.root, "launch.sh")
	var sb strings.Builder
	sb.WriteString("#!/bin/sh\n")
	fmt.Fprintf(&sb, "cd %s || exit 97\n", shQuote(o.Dir))
	sb.WriteString("sh -c 'echo $$ > \"$0\"; exec \"$@\"' " + shQuote(filepath.Join(t.root, "pid")) + " env -i")
	for _, kv := range o.Env {
		sb.WriteString(" " + shQuote(kv))
	}
	for _, a := range o.Argv {
		sb.WriteString(" " + shQuote(a))
	}
	sb.WriteString(" 2>" + shQuote(filepath.Join(t.root, "stderr.log")) + "\n")
	sb.WriteString("echo $? > " + shQuote(filepath.Join(t.root, "exit")) + "\n")
	fmt.Fprintf(&sb, "exec tmux -L %s wait-for -S exited\n", shQuote(t.sock))
	if err := os.WriteFile(launcher, []byte(sb.String()), 0o700); err != nil {
		return nil, err
	}
	base := []string{"-L", t.sock, "-f", conf}
	out, err := exec.Command("tmux", append(append([]string(nil), base...), "new-session", "-d", "-s", t.session, "-x", strconv.Itoa(o.Width), "-y", strconv.Itoa(o.Height), "sh", launcher)...).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("tmux new-session: %w: %s", err, out)
	}
	t.ctl = exec.Command("tmux", append(append([]string(nil), base...), "-C", "attach", "-t", t.session)...)
	t.stdin, err = t.ctl.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := t.ctl.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := t.ctl.Start(); err != nil {
		return nil, err
	}
	go t.read(stdout)
	// The first %begin/%end pair answers the attach itself.
	go func() { // exit watcher: blocks on tmux wait-for, an event, not a poll.
		defer close(t.waitDone)
		_ = exec.Command("tmux", "-L", t.sock, "wait-for", "exited").Run()
		close(t.exited)
		t.bump()
	}()
	return t, nil
}

var (
	uniqMu sync.Mutex
	uniq   int
)

func uniqueN() int {
	uniqMu.Lock()
	defer uniqMu.Unlock()
	uniq++
	return uniq
}

func shQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func (t *Terminal) bump() {
	t.mu.Lock()
	old := t.changed
	t.changed = make(chan struct{})
	t.lastOut = time.Now()
	t.mu.Unlock()
	close(old)
}

// Changed returns a channel closed on the next terminal output or exit. Take it before inspecting state.
func (t *Terminal) Changed() <-chan struct{} {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.changed
}

// Outputs returns how many output notifications arrived so far.
func (t *Terminal) Outputs() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.outputs
}

func (t *Terminal) read(r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	var in bool
	var cur reply
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "%begin "):
			in = true
			cur = reply{}
		case in && (strings.HasPrefix(line, "%end ") || strings.HasPrefix(line, "%error ")):
			cur.err = strings.HasPrefix(line, "%error ")
			in = false
			select {
			case t.replies <- cur:
			default:
			}
		case in:
			cur.lines = append(cur.lines, line)
		case strings.HasPrefix(line, "%output ") || strings.HasPrefix(line, "%extended-output "):
			t.mu.Lock()
			t.outputs++
			t.mu.Unlock()
			t.bump()
		case strings.HasPrefix(line, "%exit"):
			return
		}
	}
}

// command runs one tmux command through the control channel and returns its output lines.
func (t *Terminal) command(cmd string) ([]string, error) {
	t.cmdMu.Lock()
	defer t.cmdMu.Unlock()
	// Drop the unsolicited attach reply (and any leftovers) before sending.
	for {
		select {
		case <-t.replies:
			continue
		default:
		}
		break
	}
	if _, err := io.WriteString(t.stdin, cmd+"\n"); err != nil {
		return nil, err
	}
	select {
	case r := <-t.replies:
		if r.err {
			return nil, fmt.Errorf("tmux %q: %s", cmd, strings.Join(r.lines, "\n"))
		}
		return r.lines, nil
	case <-time.After(30 * time.Second):
		return nil, fmt.Errorf("tmux control channel did not answer %q", cmd)
	}
}

// Screen returns the visible pane as plain text with trailing blanks removed from every row.
func (t *Terminal) Screen() (string, error) {
	lines, err := t.command("capture-pane -p -t " + t.session)
	if err != nil {
		return "", err
	}
	return trimScreen(lines), nil
}

// ScreenEscaped returns the visible pane with escape sequences (SGR) preserved.
func (t *Terminal) ScreenEscaped() (string, error) {
	lines, err := t.command("capture-pane -p -e -t " + t.session)
	if err != nil {
		return "", err
	}
	return trimScreen(lines), nil
}

// History returns the retained scrollback plus the visible pane.
func (t *Terminal) History() (string, error) {
	lines, err := t.command("capture-pane -p -J -S - -t " + t.session)
	if err != nil {
		return "", err
	}
	return trimScreen(lines), nil
}

func trimScreen(lines []string) string {
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " ")
	}
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return strings.Join(lines, "\n")
}

// SendText types literal text.
func (t *Terminal) SendText(s string) error {
	if s == "" {
		return nil
	}
	var hex strings.Builder
	for i := 0; i < len(s); i++ {
		fmt.Fprintf(&hex, " %02x", s[i])
	}
	_, err := t.command("send-keys -t " + t.session + " -H" + hex.String())
	return err
}

// SendKeys sends named tmux keys such as Enter, Escape, C-c, Up.
func (t *Terminal) SendKeys(keys ...string) error {
	for _, k := range keys {
		if _, err := t.command("send-keys -t " + t.session + " " + k); err != nil {
			return err
		}
	}
	return nil
}

// SendBytes writes raw bytes to the pane, for protocol input such as mouse reports and bracketed paste.
func (t *Terminal) SendBytes(b []byte) error {
	var hex strings.Builder
	for _, c := range b {
		fmt.Fprintf(&hex, " %02x", c)
	}
	_, err := t.command("send-keys -t " + t.session + " -H" + hex.String())
	return err
}

// Resize changes the terminal size; the program receives SIGWINCH.
func (t *Terminal) Resize(w, h int) error {
	_, err := t.command(fmt.Sprintf("resize-window -t %s -x %d -y %d", t.session, w, h))
	return err
}

// Exited is closed when the program has exited.
func (t *Terminal) Exited() <-chan struct{} { return t.exited }

// ExitCode returns the program's exit status once it has exited.
func (t *Terminal) ExitCode() (int, error) {
	select {
	case <-t.exited:
	default:
		return 0, errors.New("still running")
	}
	b, err := os.ReadFile(filepath.Join(t.root, "exit"))
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(b)))
}

// PID returns the program's process ID (the launcher execs into it).
func (t *Terminal) PID() int {
	if t.pid != 0 {
		return t.pid
	}
	b, err := os.ReadFile(filepath.Join(t.root, "pid"))
	if err != nil {
		return 0
	}
	t.pid, _ = strconv.Atoi(strings.TrimSpace(string(b)))
	return t.pid
}

// Stderr returns what the program wrote to standard error.
func (t *Terminal) Stderr() string {
	b, _ := os.ReadFile(filepath.Join(t.root, "stderr.log"))
	return string(b)
}

// Close ends the session; only this session's private server is touched.
func (t *Terminal) Close() {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return
	}
	t.closed = true
	t.mu.Unlock()
	if pid := t.PID(); pid > 0 {
		select {
		case <-t.exited:
		default:
			_ = signalPID(pid, 9)
		}
	}
	_ = exec.Command("tmux", "-L", t.sock, "kill-session", "-t", t.session).Run()
	_ = t.stdin.Close()
	done := make(chan struct{})
	go func() { _ = t.ctl.Wait(); close(done) }()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	select {
	case <-done:
	case <-ctx.Done():
		_ = t.ctl.Process.Kill()
	}
	select {
	case <-t.exited:
	default:
		// release the watcher blocked in wait-for
		_ = exec.Command("tmux", "-L", t.sock, "wait-for", "-S", "exited").Run()
	}
}
