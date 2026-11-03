package mcp_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/mcp"
)

// Ports packages/mcp/test/pipe.test.ts (c158fa2ee).
//
// Go mechanics: `exited` is a channel that receives the program's end (nil, or the error a rejected promise carries),
// `write` returns its error, and vitest's fake timers are replaced by real closeTimeoutMs values of tens of milliseconds
// with the assertions placed well inside and well outside them.

// fakePipe is an in-memory program that exits only when told to, or when killed.
type fakePipe struct {
	mu      sync.Mutex
	calls   []string
	written []string
	exited  chan error
}

func (p *fakePipe) record(call string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = append(p.calls, call)
}

func (p *fakePipe) Write(data []byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.written = append(p.written, string(data))
	return nil
}

func (p *fakePipe) CloseStdin() { p.record("closeStdin") }

func (p *fakePipe) Close() {
	p.record("close")
	p.exited <- errors.New("aborted")
}

func (p *fakePipe) Exited() <-chan error { return p.exited }

func (p *fakePipe) callList() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.calls)
}

func (p *fakePipe) writtenList() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.written)
}

// fakeProgram is a PipeTransport over a fakePipe, with what it reported.
type fakeProgram struct {
	transport *mcp.PipeTransport
	pipe      *fakePipe
	handlers  mcp.McpPipeHandlers

	mu       sync.Mutex
	messages []string
	errors   []string
	closed   bool
}

func (f *fakeProgram) messageList() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.messages)
}

func (f *fakeProgram) errorList() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.errors)
}

func (f *fakeProgram) isClosed() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closed
}

func (f *fakeProgram) stdout(text string) { f.handlers.OnStdout([]byte(text)) }
func (f *fakeProgram) stderr(text string) { f.handlers.OnStderr([]byte(text)) }
func (f *fakeProgram) exit()              { f.pipe.exited <- nil }
func (f *fakeProgram) fail(err error)     { f.pipe.exited <- err }

// startFake starts a PipeTransport over an in-memory program that exits only when told to, or when killed.
func startFake(t *testing.T, options mcp.PipeTransportOptions) *fakeProgram {
	t.Helper()
	program := &fakeProgram{pipe: &fakePipe{exited: make(chan error, 2)}}
	options.Open = func(handlers mcp.McpPipeHandlers) (mcp.McpPipe, error) {
		program.handlers = handlers
		return program.pipe, nil
	}
	program.transport = mcp.NewPipeTransport(options)
	program.transport.OnMessage(func(message mcp.JSONRPCMessage) {
		wire, _ := json.Marshal(message)
		program.mu.Lock()
		program.messages = append(program.messages, string(wire))
		program.mu.Unlock()
	})
	program.transport.OnError(func(err error) {
		program.mu.Lock()
		program.errors = append(program.errors, err.Error())
		program.mu.Unlock()
	})
	program.transport.OnClose(func() {
		program.mu.Lock()
		program.closed = true
		program.mu.Unlock()
	})
	if err := program.transport.Start(); err != nil {
		t.Fatal(err)
	}
	// A program that nobody ended would leave the transport's exit watcher blocked.
	t.Cleanup(func() {
		program.exit()
		_ = program.transport.Close()
	})
	return program
}

func pingMessage(id float64) mcp.JSONRPCMessage {
	return mcp.NewRequest(mcp.NumberID(id), "ping", nil)
}

func pingWire(id float64) string {
	wire, err := json.Marshal(pingMessage(id))
	if err != nil {
		panic(err)
	}
	return string(wire)
}

func isConnectionClosed(err error) bool {
	var closed *mcp.McpConnectionClosedError
	return errors.As(err, &closed)
}

func TestPipeTransportFramingJoinsMessagesSplitAcrossChunksAndSplitsSeveralMessagesInOneChunk(t *testing.T) {
	program := startFake(t, mcp.PipeTransportOptions{})
	first, second, third := pingWire(1), pingWire(2), pingWire(3)
	program.stdout(first[:10])
	program.stdout(first[10:] + "\n" + second)
	program.stdout("\n" + third + "\n")
	if got, want := program.messageList(), []string{first, second, third}; !slices.Equal(got, want) {
		t.Fatalf("messages = %q, want %q", got, want)
	}
	if got := program.errorList(); len(got) != 0 {
		t.Fatalf("errors = %q", got)
	}
}

// Equivalent mutants (not trimming \r) survive: JSON.parse and encoding/json accept a trailing \r as whitespace.
func TestPipeTransportFramingStripsATrailingCarriageReturnAndSkipsBlankLines(t *testing.T) {
	program := startFake(t, mcp.PipeTransportOptions{})
	program.stdout("\n\r\n   \n" + pingWire(1) + "\r\n\n")
	if got, want := program.messageList(), []string{pingWire(1)}; !slices.Equal(got, want) {
		t.Fatalf("messages = %q, want %q", got, want)
	}
	if got := program.errorList(); len(got) != 0 {
		t.Fatalf("errors = %q", got)
	}
}

// mutation-checked: stopping at the first invalid line fails it
func TestPipeTransportFramingReportsInvalidMessagesAndKeepsReading(t *testing.T) {
	program := startFake(t, mcp.PipeTransportOptions{})
	program.stdout("not json\n{\"jsonrpc\":\"1.0\"}\n" + pingWire(1) + "\n")
	if got, want := program.messageList(), []string{pingWire(1)}; !slices.Equal(got, want) {
		t.Fatalf("messages = %q, want %q", got, want)
	}
	if got := program.errorList(); len(got) != 2 {
		t.Fatalf("errors = %q, want 2", got)
	}
}

// The pending-bytes limit is not separately pinned: a complete over-limit line reports the same error (as in Pi's test).
func TestPipeTransportFramingDropsMessagesOverMaxMessageBytesCompleteOrNot(t *testing.T) {
	program := startFake(t, mcp.PipeTransportOptions{MaxMessageBytes: 48})
	long := `{"jsonrpc":"2.0","method":"` + strings.Repeat("x", 48) + `"}`
	program.stdout(long + "\n" + pingWire(1) + "\n")
	program.stdout(strings.Repeat("y", 49))
	program.stdout("\n" + pingWire(2) + "\n")
	if got, want := program.messageList(), []string{pingWire(1), pingWire(2)}; !slices.Equal(got, want) {
		t.Fatalf("messages = %q, want %q", got, want)
	}
	over := "MCP stdio message exceeds 48 bytes"
	if got, want := program.errorList(), []string{over, over}; !slices.Equal(got, want) {
		t.Fatalf("errors = %q, want %q", got, want)
	}
}

// mutation-checked: keeping the head instead of the tail of stderr, or skipping onStderr, fails it
func TestPipeTransportFramingKeepsABoundedStderrTailAndPassesEveryChunkToOnStderr(t *testing.T) {
	var chunks []string
	program := startFake(t, mcp.PipeTransportOptions{MaxStderrBytes: 8, OnStderr: func(chunk string) { chunks = append(chunks, chunk) }})
	program.stderr("starting\n")
	program.stderr("ready\n")
	if want := []string{"starting\n", "ready\n"}; !slices.Equal(chunks, want) {
		t.Fatalf("chunks = %q, want %q", chunks, want)
	}
	if got := program.transport.Stderr(); got != "g\nready\n" {
		t.Fatalf("stderr = %q", got)
	}
}

// mutation-checked: omitting the newline after the message fails it
func TestPipeTransportLifecycleWritesEachMessageAsOneLine(t *testing.T) {
	program := startFake(t, mcp.PipeTransportOptions{})
	if err := program.transport.Send(context.Background(), pingMessage(1)); err != nil {
		t.Fatal(err)
	}
	if got, want := program.pipe.writtenList(), []string{pingWire(1) + "\n"}; !slices.Equal(got, want) {
		t.Fatalf("written = %q, want %q", got, want)
	}
}

// mutation-checked: skipping lines.end on exit, or not emitting close, fails it
func TestPipeTransportLifecycleReportsAnIncompleteMessageWhenTheProgramExitsThenCloses(t *testing.T) {
	program := startFake(t, mcp.PipeTransportOptions{})
	program.stdout(`{"jsonrpc":"2.0"`)
	program.exit()
	waitFor(t, program.isClosed)
	if got, want := program.errorList(), []string{"MCP stdio server closed with an incomplete JSON-RPC message"}; !slices.Equal(got, want) {
		t.Fatalf("errors = %q, want %q", got, want)
	}
}

// mutation-checked: dropping the error of a rejected exit, or emitting it when close caused it, fails it
func TestPipeTransportLifecycleReportsAFailedExitItDidNotCauseThenCloses(t *testing.T) {
	program := startFake(t, mcp.PipeTransportOptions{})
	program.fail(errors.New("connection lost"))
	waitFor(t, program.isClosed)
	if got, want := program.errorList(), []string{"connection lost"}; !slices.Equal(got, want) {
		t.Fatalf("errors = %q, want %q", got, want)
	}
}

// mutation-checked: dropping the closed or the ended check in send fails it
func TestPipeTransportLifecycleRejectsSendAfterCloseAndAfterTheProgramEnded(t *testing.T) {
	closedByUs := startFake(t, mcp.PipeTransportOptions{})
	closedByUs.exit()
	if err := closedByUs.transport.Close(); err != nil {
		t.Fatal(err)
	}
	if err := closedByUs.transport.Send(context.Background(), pingMessage(1)); !isConnectionClosed(err) {
		t.Fatalf("send after close = %v, want McpConnectionClosedError", err)
	}

	ended := startFake(t, mcp.PipeTransportOptions{})
	ended.exit()
	waitFor(t, ended.isClosed)
	if err := ended.transport.Send(context.Background(), pingMessage(1)); !isConnectionClosed(err) {
		t.Fatalf("send after exit = %v, want McpConnectionClosedError", err)
	}
	if got := ended.pipe.writtenList(); len(got) != 0 {
		t.Fatalf("written = %q", got)
	}
}

// mutation-checked: killing the program without waiting for closeTimeoutMs fails it
func TestPipeTransportLifecycleClosesStdinAndDoesNotKillAProgramThatExitsInTime(t *testing.T) {
	program := startFake(t, mcp.PipeTransportOptions{})
	closing := make(chan error, 1)
	go func() { closing <- program.transport.Close() }()
	waitFor(t, func() bool { return slices.Equal(program.pipe.callList(), []string{"closeStdin"}) })
	if err := program.transport.Send(context.Background(), pingMessage(1)); !isConnectionClosed(err) {
		t.Fatalf("send while closing = %v, want McpConnectionClosedError", err)
	}
	if got := program.pipe.writtenList(); len(got) != 0 {
		t.Fatalf("written = %q", got)
	}
	program.exit()
	if err := <-closing; err != nil {
		t.Fatal(err)
	}
	if got := program.pipe.callList(); !slices.Equal(got, []string{"closeStdin"}) {
		t.Fatalf("calls = %q", got)
	}
	if !program.isClosed() {
		t.Fatal("transport did not close")
	}
	if got := program.errorList(); len(got) != 0 {
		t.Fatalf("errors = %q", got)
	}
}

// mutation-checked: shutting down once per close call fails it
func TestPipeTransportLifecycleShutsDownOnceWhenClosedTwice(t *testing.T) {
	program := startFake(t, mcp.PipeTransportOptions{})
	results := make(chan error, 2)
	go func() { results <- program.transport.Close() }()
	go func() { results <- program.transport.Close() }()
	waitFor(t, func() bool { return len(program.pipe.callList()) > 0 })
	program.exit()
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	if err := program.transport.Close(); err != nil {
		t.Fatal(err)
	}
	if got := program.pipe.callList(); !slices.Equal(got, []string{"closeStdin"}) {
		t.Fatalf("calls = %q", got)
	}
	if !program.isClosed() {
		t.Fatal("transport did not close")
	}
}

// mutation-checked: killing at once, never killing, or reporting the rejection the kill caused as an error fails it
func TestPipeTransportLifecycleKillsAProgramOnlyAfterCloseTimeoutMs(t *testing.T) {
	const timeout = 400 * time.Millisecond
	program := startFake(t, mcp.PipeTransportOptions{CloseTimeoutMs: int(timeout / time.Millisecond)})
	start := time.Now()
	closing := make(chan error, 1)
	go func() { closing <- program.transport.Close() }()
	waitFor(t, func() bool { return len(program.pipe.callList()) > 0 })
	time.Sleep(timeout / 4)
	if got := program.pipe.callList(); !slices.Equal(got, []string{"closeStdin"}) {
		t.Fatalf("calls before the timeout = %q", got)
	}
	if err := <-closing; err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed < timeout {
		t.Fatalf("closed after %v, before closeTimeoutMs", elapsed)
	}
	if got := program.pipe.callList(); !slices.Equal(got, []string{"closeStdin", "close"}) {
		t.Fatalf("calls = %q", got)
	}
	if !program.isClosed() {
		t.Fatal("transport did not close")
	}
	// The rejection of `exited` that killing caused is not an error.
	if got := program.errorList(); len(got) != 0 {
		t.Fatalf("errors = %q", got)
	}
}

// mutation-checked: returning nil from start when open fails fails it
func TestPipeTransportLifecycleRejectsStartWhenOpenRejects(t *testing.T) {
	transport := mcp.NewPipeTransport(mcp.PipeTransportOptions{
		Open: func(mcp.McpPipeHandlers) (mcp.McpPipe, error) { return nil, errors.New("not_supported") },
	})
	if err := transport.Start(); err == nil || err.Error() != "not_supported" {
		t.Fatalf("start = %v, want not_supported", err)
	}
}

// mutation-checked: keeping a program that finished starting after close fails it
func TestPipeTransportLifecycleKillsAProgramThatFinishedStartingAfterClose(t *testing.T) {
	release := make(chan struct{})
	entered := make(chan struct{})
	pipe := &fakePipe{exited: make(chan error, 1)}
	transport := mcp.NewPipeTransport(mcp.PipeTransportOptions{
		Open: func(mcp.McpPipeHandlers) (mcp.McpPipe, error) {
			close(entered)
			<-release
			return pipe, nil
		},
	})
	starting := make(chan error, 1)
	go func() { starting <- transport.Start() }()
	<-entered
	if err := transport.Close(); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-starting; !isConnectionClosed(err) {
		t.Fatalf("start = %v, want McpConnectionClosedError", err)
	}
	if got := pipe.callList(); !slices.Equal(got, []string{"close"}) {
		t.Fatalf("calls = %q, want one close", got)
	}
}

// commandPipe runs a program with os/exec, standing in for pi-env's openProcess.
type commandPipe struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	exited chan error
}

func (p *commandPipe) Write(data []byte) error {
	// Writes racing the program's exit fail with EPIPE; Exited reports the end.
	_, _ = p.stdin.Write(data)
	return nil
}
func (p *commandPipe) CloseStdin()          { _ = p.stdin.Close() }
func (p *commandPipe) Close()               { _ = p.cmd.Process.Kill() }
func (p *commandPipe) Exited() <-chan error { return p.exited }

func spawnPipe(command string, args, env []string, handlers mcp.McpPipeHandlers) (mcp.McpPipe, error) {
	cmd := exec.Command(command, args...)
	cmd.Env = env
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	pump := func(r io.Reader, to func([]byte)) *sync.WaitGroup {
		var done sync.WaitGroup
		done.Go(func() {
			buf := make([]byte, 4096)
			for {
				n, err := r.Read(buf)
				if n > 0 {
					to(buf[:n])
				}
				if err != nil {
					return
				}
			}
		})
		return &done
	}
	outDone, errDone := pump(stdout, handlers.OnStdout), pump(stderr, handlers.OnStderr)
	exited := make(chan error, 1)
	go func() {
		outDone.Wait()
		errDone.Wait()
		_ = cmd.Wait()
		exited <- nil
	}()
	return &commandPipe{cmd: cmd, stdin: stdin, exited: exited}, nil
}

func TestPipeTransportWithARealProgramRunsInitializeToolsListAndToolsCall(t *testing.T) {
	command, args, fixtureEnvironment := fixtureStdioOptions("stdio-server")
	env := os.Environ()
	for key, value := range fixtureEnvironment {
		env = append(env, key+"="+value)
	}
	transport := mcp.NewPipeTransport(mcp.PipeTransportOptions{
		Open: func(handlers mcp.McpPipeHandlers) (mcp.McpPipe, error) {
			return spawnPipe(command, args, env, handlers)
		},
	})
	client := mcp.NewClient(mcp.ClientOptions{Implementation: mcp.Implementation{Name: "pipe-test", Version: "1.0.0"}})
	result, err := client.Connect(t.Context(), transport)
	if err != nil {
		t.Fatal(err)
	}
	jsonEqual(t, result.ServerInfo, `{"name":"stdio-fixture","version":"1.0.0"}`)
	tools, err := client.ListTools(t.Context(), mcp.RequestOptions{})
	if err != nil {
		t.Fatal(err)
	}
	jsonEqual(t, tools, `[{"name":"echo","inputSchema":{"type":"object"}}]`)
	called, err := client.CallTool(t.Context(), "echo", map[string]any{"text": "hello"}, mcp.RequestOptions{})
	if err != nil {
		t.Fatal(err)
	}
	jsonEqual(t, called, `{"content":[{"type":"text","text":"hello"}]}`)
	waitFor(t, func() bool { return strings.Contains(transport.Stderr(), "stdio fixture ready") })
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	if client.ConnectionState() != mcp.ClientStateClosed {
		t.Fatalf("state = %s", client.ConnectionState())
	}
}
