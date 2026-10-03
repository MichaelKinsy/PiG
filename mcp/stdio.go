package mcp

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/internal/crossspawn"
	"github.com/MichaelKinsy/PiG/internal/jsonparse"
	"github.com/MichaelKinsy/PiG/internal/nodespawn"
)

// Ports packages/mcp/src/transports/stdio.ts.

const (
	defaultMaxStderrBytes  = 64 * 1024
	defaultCloseTimeoutMs  = 2_000
	stdinCloseGraceMs      = 500 // how long a server gets to exit on its own after stdin closes, before SIGTERM
	stdoutReadChunkBytes   = 64 * 1024
	forcedPipeCloseTimeout = time.Second
)

// StdioTransportOptions configure a [StdioTransport].
type StdioTransportOptions struct {
	Command string
	Args    []string
	Cwd     string
	// Env is added to the inherited environment, or replaces it when
	// InheritEnv is false. Go mechanic: a map has no insertion order, so
	// entries are applied in sorted key order.
	Env map[string]string
	// InheritEnv is true by default.
	InheritEnv *bool
	// Stderr is "pipe" (the default) or "inherit".
	Stderr          string
	OnStderr        func(chunk string)
	MaxMessageBytes int
	MaxStderrBytes  int
	// CloseTimeoutMs is the time to wait for the server to exit after SIGTERM
	// before sending SIGKILL. Default: 2000.
	CloseTimeoutMs int
}

// StdioTransport talks to a server process over newline-delimited JSON on its
// stdin and stdout. The process runs in its own process group, and closing the
// transport shuts down the whole group per the spec: close stdin, then
// SIGTERM, then SIGKILL.
type StdioTransport struct {
	TransportEvents
	options StdioTransportOptions

	mu          sync.Mutex
	cmd         *exec.Cmd
	stdin       io.WriteCloser
	stdinClosed bool
	stdout      io.ReadCloser
	stderrPipe  io.ReadCloser
	stderr      []byte
	started     bool
	closed      bool
	exited      chan struct{}
	writeMu     sync.Mutex

	stdoutRemainder []byte
}

// NewStdioTransport returns a transport that has not started its process.
func NewStdioTransport(options StdioTransportOptions) *StdioTransport {
	options.Args = slices.Clone(options.Args)
	return &StdioTransport{options: options}
}

// Options returns the transport's options.
func (t *StdioTransport) Options() StdioTransportOptions { return t.options }

// PID is the process id of the running server, or 0.
func (t *StdioTransport) PID() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.cmd == nil || t.cmd.Process == nil {
		return 0
	}
	return t.cmd.Process.Pid
}

// Stderr is the tail of the server's stderr.
func (t *StdioTransport) Stderr() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return strings.ToValidUTF8(string(t.stderr), "\uFFFD")
}

func (t *StdioTransport) environment() []nodespawn.EnvProperty {
	var properties []nodespawn.EnvProperty
	if t.options.InheritEnv == nil || *t.options.InheritEnv {
		properties = nodespawn.EnvProperties(nodespawn.ProcessEnv())
	}
	keys := make([]string, 0, len(t.options.Env))
	for key := range t.options.Env {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for _, key := range keys {
		properties = append(properties, nodespawn.EnvProperty{Name: key, Value: t.options.Env[key]})
	}
	return properties
}

// Start spawns the server.
func (t *StdioTransport) Start() error {
	t.mu.Lock()
	if t.started {
		t.mu.Unlock()
		return errors.New("MCP stdio transport already started")
	}
	if t.closed {
		t.mu.Unlock()
		return NewConnectionClosedError()
	}
	t.started = true
	t.mu.Unlock()

	cmd := crossspawn.Command(bgContext(), t.options.Cwd, t.options.Command, t.options.Args...)
	// The server runs until Close ends it, so no context cancels it, and
	// nodespawn.Start takes no command with a Cancel.
	cmd.Cancel = nil
	setStdioProcAttr(cmd)
	nodespawn.SetEnvProperties(cmd, t.environment())
	nodespawn.SetProgram(cmd)
	nodespawn.HideWindow(cmd, nodespawn.Pipe, nodespawn.Pipe, stdioStderrMode(t.options.Stderr))
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	var stderrPipe io.ReadCloser
	if t.options.Stderr == "inherit" {
		cmd.Stderr = os.Stderr
	} else if stderrPipe, err = cmd.StderrPipe(); err != nil {
		return err
	}
	if err := nodespawn.Start(cmd); err != nil {
		// cmd.Start closes the parent's ends of the pipes when it fails, but
		// nodespawn.Start can fail without calling it.
		_ = stdin.Close()
		_ = stdout.Close()
		if stderrPipe != nil {
			_ = stderrPipe.Close()
		}
		return spawnFailure(t.options.Command, err)
	}
	exited := make(chan struct{})
	t.mu.Lock()
	t.cmd, t.stdin, t.stdout, t.stderrPipe, t.exited = cmd, stdin, stdout, stderrPipe, exited
	t.mu.Unlock()
	pid := cmd.Process.Pid
	trackProcessGroup(pid)

	var readers sync.WaitGroup
	readers.Go(func() {
		t.readStdout(stdout)
	})
	if stderrPipe != nil {
		readers.Go(func() {
			t.readStderr(stderrPipe)
		})
	}
	go func() {
		// Like Node's `close` event: the process exited and its stdio closed.
		readers.Wait()
		_ = cmd.Wait()
		untrackProcessGroup(pid)
		t.finishStdout()
		t.mu.Lock()
		t.cmd = nil
		t.mu.Unlock()
		close(exited)
		t.EmitClose()
	}()
	return nil
}

// nodeSpawnError is the error Node's spawn emits for a program that does not
// start: `spawn <file> ENOENT`.
type nodeSpawnError struct {
	file  string
	cause error
}

func (e *nodeSpawnError) Error() string { return "spawn " + e.file + " ENOENT" }
func (e *nodeSpawnError) Unwrap() error { return e.cause }

// spawnFailure gives a start error the message Node's spawn error has (stdio.ts
// rejects the start with the child's `error` event). nodespawn errors already
// carry it; os/exec reports a missing program with its own text.
func spawnFailure(file string, err error) error {
	if errors.Is(err, exec.ErrNotFound) {
		return &nodeSpawnError{file: file, cause: errors.Join(err, fs.ErrNotExist)}
	}
	return err
}

func stdioStderrMode(mode string) nodespawn.Stdio {
	if mode == "inherit" {
		return nodespawn.Inherit
	}
	return nodespawn.Pipe
}

// Send writes one message as a line on the server's stdin.
func (t *StdioTransport) Send(message JSONRPCMessage) error {
	return t.SendOrdered(message, nil)
}

// SendOrdered is Send that calls placed, if not nil, once the write holds its place: a later Send writes after it.
func (t *StdioTransport) SendOrdered(message JSONRPCMessage, placed func()) error {
	t.mu.Lock()
	stdin, ok := t.stdin, t.started && !t.closed && !t.stdinClosed && t.stdin != nil && t.cmd != nil
	t.mu.Unlock()
	if !ok {
		return NewConnectionClosedError()
	}
	payload, err := json.Marshal(message)
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	t.writeMu.Lock()
	defer t.writeMu.Unlock()
	if placed != nil {
		placed()
	}
	_, err = stdin.Write(payload)
	return err
}

// Close shuts the server down: it closes stdin and lets the server exit, then
// sends SIGTERM to its process group, then SIGKILL. It returns after the
// server exited and the transport notified its close listeners.
func (t *StdioTransport) Close() error {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil
	}
	t.closed = true
	cmd, exited := t.cmd, t.exited
	t.mu.Unlock()
	if cmd == nil {
		t.EmitClose()
		return nil
	}
	closeTimeout := time.Duration(t.options.CloseTimeoutMs) * time.Millisecond
	if t.options.CloseTimeoutMs == 0 {
		closeTimeout = defaultCloseTimeoutMs * time.Millisecond
	}
	select {
	case <-exited:
		t.closeStdin()
		return nil
	default:
	}
	grace := min(stdinCloseGraceMs*time.Millisecond, closeTimeout)
	termTimer := time.AfterFunc(grace, func() { killProcessTree(cmd, exited, false) })
	killTimer := time.AfterFunc(grace+closeTimeout, func() {
		killProcessTree(cmd, exited, true)
		// A descendant that inherited the pipes must not hold Close forever.
		time.AfterFunc(forcedPipeCloseTimeout, t.closePipes)
	})
	defer termTimer.Stop()
	defer killTimer.Stop()
	t.closeStdin()
	<-exited
	// Children of a server that ignored stdin closing would otherwise outlive it.
	killProcessTree(cmd, exited, false)
	return nil
}

func (t *StdioTransport) closeStdin() {
	t.mu.Lock()
	stdin := t.stdin
	already := t.stdinClosed
	t.stdinClosed = true
	t.mu.Unlock()
	if stdin != nil && !already {
		_ = stdin.Close()
	}
}

func (t *StdioTransport) closePipes() {
	t.mu.Lock()
	stdout, stderr := t.stdout, t.stderrPipe
	t.mu.Unlock()
	if stdout != nil {
		_ = stdout.Close()
	}
	if stderr != nil {
		_ = stderr.Close()
	}
}

// readStdout frames stdout into lines. A chunk that leaves a partial line
// longer than the limit is dropped with an error.
func (t *StdioTransport) readStdout(r io.Reader) {
	buf := make([]byte, stdoutReadChunkBytes)
	var pending []byte
	maxBytes := t.options.MaxMessageBytes
	if maxBytes == 0 {
		maxBytes = DefaultMaxMessageBytes
	}
	for {
		n, err := r.Read(buf)
		if n > 0 {
			pending = append(pending, buf[:n]...)
			pending = t.drainLines(pending, maxBytes)
		}
		if err != nil {
			t.mu.Lock()
			t.stdoutRemainder = pending
			t.mu.Unlock()
			return
		}
	}
}

func (t *StdioTransport) drainLines(pending []byte, maxBytes int) []byte {
	for {
		newline := bytes.IndexByte(pending, '\n')
		if newline < 0 {
			if len(pending) > maxBytes {
				t.EmitError(fmt.Errorf("MCP stdio message exceeds %d bytes", maxBytes))
				return nil
			}
			return pending
		}
		line := pending[:newline]
		pending = pending[newline+1:]
		if len(line) > maxBytes {
			t.EmitError(fmt.Errorf("MCP stdio message exceeds %d bytes", maxBytes))
			continue
		}
		text := strings.TrimSuffix(string(line), "\r")
		if strings.TrimSpace(text) == "" {
			continue
		}
		message, err := parseWireMessage([]byte(text))
		if err != nil {
			t.EmitError(err)
			continue
		}
		t.EmitMessage(message)
	}
}

// parseWireMessage parses JSON text and validates it as a JSON-RPC message.
// A syntax error carries the message JSON.parse throws.
func parseWireMessage(text []byte) (JSONRPCMessage, error) {
	if !json.Valid(text) {
		if err := jsonparse.Validate(text); err != nil {
			return JSONRPCMessage{}, err
		}
	}
	return ParseJSONRPCMessage(text)
}

func (t *StdioTransport) finishStdout() {
	t.mu.Lock()
	rest := t.stdoutRemainder
	t.stdoutRemainder = nil
	t.mu.Unlock()
	if strings.TrimSpace(string(rest)) != "" {
		t.EmitError(errors.New("MCP stdio server closed with an incomplete JSON-RPC message"))
	}
}

func (t *StdioTransport) readStderr(r io.Reader) {
	buf := make([]byte, 4096)
	maxBytes := t.options.MaxStderrBytes
	if maxBytes == 0 {
		maxBytes = defaultMaxStderrBytes
	}
	for {
		n, err := r.Read(buf)
		if n > 0 {
			chunk := append([]byte(nil), buf[:n]...)
			t.mu.Lock()
			t.stderr = append(t.stderr, chunk...)
			if len(t.stderr) > maxBytes {
				t.stderr = t.stderr[len(t.stderr)-maxBytes:]
			}
			t.mu.Unlock()
			if t.options.OnStderr != nil {
				t.options.OnStderr(strings.ToValidUTF8(string(chunk), "\uFFFD"))
			}
		}
		if err != nil {
			return
		}
	}
}
