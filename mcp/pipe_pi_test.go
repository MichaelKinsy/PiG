package mcp_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os/exec"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/env"
	"github.com/MichaelKinsy/PiG/internal/testenv"
	"github.com/MichaelKinsy/PiG/mcp"
)

// Ports packages/mcp/test/pipe.test.ts (Pi c158fa2ee). Go mechanic: Pi's `exited` promise is [mcp.McpPipe.Exited] plus ExitError, and the
// handlers take bytes of a slice the pipe may reuse.

// fakePipe is Pi's in-memory program: it exits only when told to, or when killed.
type fakePipe struct {
	mu      sync.Mutex
	calls   []string
	written []string
	exited  chan struct{}
	// stdinClosed receives a value for each CloseStdin call.
	stdinClosed chan struct{}
	once        sync.Once
	failure     error
}

func (p *fakePipe) Write(data any) {
	p.mu.Lock()
	defer p.mu.Unlock()
	switch data := data.(type) {
	case string:
		p.written = append(p.written, data)
	case []byte:
		p.written = append(p.written, string(data))
	}
}

func (p *fakePipe) CloseStdin() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = append(p.calls, "closeStdin")
	if p.stdinClosed != nil {
		p.stdinClosed <- struct{}{}
	}
}

func (p *fakePipe) Close() {
	p.mu.Lock()
	p.calls = append(p.calls, "close")
	p.mu.Unlock()
	p.end(errors.New("aborted"))
}

func (p *fakePipe) Exited() (int, error) {
	<-p.exited
	return 0, p.failure
}

func (p *fakePipe) end(failure error) {
	p.once.Do(func() {
		p.failure = failure
		close(p.exited)
	})
}

func (p *fakePipe) callList() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.calls)
}

type fakeProgram struct {
	transport *mcp.PipeTransport
	pipe      *fakePipe
	handlers  mcp.McpPipeHandlers

	mu       sync.Mutex
	messages []string
	errors   []string
	closed   chan struct{}
}

func (p *fakeProgram) written() []string {
	p.pipe.mu.Lock()
	defer p.pipe.mu.Unlock()
	return slices.Clone(p.pipe.written)
}

func (p *fakeProgram) messageList() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.messages)
}

func (p *fakeProgram) errorList() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.errors)
}

func (p *fakeProgram) stdout(text string) { p.handlers.OnStdout([]byte(text)) }
func (p *fakeProgram) stderr(text string) { p.handlers.OnStderr([]byte(text)) }

// settle waits until the transport closed: its reactions to `exited` have run.
func (p *fakeProgram) settle(t *testing.T) {
	t.Helper()
	select {
	case <-p.closed:
	case <-time.After(5 * time.Second):
		t.Fatal("the transport did not close")
	}
}

func startFake(t *testing.T, options mcp.PipeTransportOptions) *fakeProgram {
	t.Helper()
	program := &fakeProgram{pipe: &fakePipe{exited: make(chan struct{}), stdinClosed: make(chan struct{}, 4)}, closed: make(chan struct{})}
	options.Open = func(handlers mcp.McpPipeHandlers) (mcp.McpPipe, error) {
		program.handlers = handlers
		return program.pipe, nil
	}
	program.transport = mcp.NewPipeTransport(options)
	program.transport.OnMessage(func(message mcp.JSONRPCMessage) {
		data, _ := json.Marshal(message)
		program.mu.Lock()
		program.messages = append(program.messages, string(data))
		program.mu.Unlock()
	})
	program.transport.OnError(func(err error) {
		program.mu.Lock()
		program.errors = append(program.errors, err.Error())
		program.mu.Unlock()
	})
	program.transport.OnClose(func() { close(program.closed) })
	if err := program.transport.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { program.pipe.end(nil) })
	return program
}

func ping(id float64) mcp.JSONRPCMessage {
	return mcp.NewRequest(mcp.NumberID(id), "ping", nil)
}

func pingJSON(id string) string { return `{"jsonrpc":"2.0","id":` + id + `,"method":"ping"}` }

func wantList(t *testing.T, what string, got, want []string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s = %q, want %q", what, got, want)
	}
}

// Pi packages/mcp/test/pipe.test.ts:86
func TestPipeTransportJoinsMessagesSplitAcrossChunksAndSplitsSeveralMessagesInOneChunk(t *testing.T) {
	program := startFake(t, mcp.PipeTransportOptions{})
	first, second, third := pingJSON("1"), pingJSON("2"), pingJSON("3")
	program.stdout(first[:10])
	program.stdout(first[10:] + "\n" + second)
	program.stdout("\n" + third + "\n")
	wantList(t, "messages", program.messageList(), []string{first, second, third})
	wantList(t, "errors", program.errorList(), nil)
}

// Pi packages/mcp/test/pipe.test.ts:96
func TestPipeTransportStripsATrailingCarriageReturnAndSkipsBlankLines(t *testing.T) {
	program := startFake(t, mcp.PipeTransportOptions{})
	program.stdout("\n\r\n   \n" + pingJSON("1") + "\r\n\n")
	wantList(t, "messages", program.messageList(), []string{pingJSON("1")})
	wantList(t, "errors", program.errorList(), nil)
}

// Pi packages/mcp/test/pipe.test.ts:103
func TestPipeTransportReportsInvalidMessagesAndKeepsReading(t *testing.T) {
	program := startFake(t, mcp.PipeTransportOptions{})
	program.stdout("not json\n{\"jsonrpc\":\"1.0\"}\n" + pingJSON("1") + "\n")
	wantList(t, "messages", program.messageList(), []string{pingJSON("1")})
	if got := program.errorList(); len(got) != 2 {
		t.Fatalf("errors = %q, want 2", got)
	}
}

// Pi packages/mcp/test/pipe.test.ts:110
func TestPipeTransportDropsMessagesOverMaxMessageBytesCompleteOrNot(t *testing.T) {
	program := startFake(t, mcp.PipeTransportOptions{MaxMessageBytes: 48})
	program.stdout(`{"jsonrpc":"2.0","method":"` + strings.Repeat("x", 48) + `"}` + "\n" + pingJSON("1") + "\n")
	program.stdout(strings.Repeat("y", 49))
	program.stdout("\n" + pingJSON("2") + "\n")
	wantList(t, "messages", program.messageList(), []string{pingJSON("1"), pingJSON("2")})
	wantList(t, "errors", program.errorList(), []string{"MCP stdio message exceeds 48 bytes", "MCP stdio message exceeds 48 bytes"})
}

// Pi packages/mcp/test/pipe.test.ts:122
func TestPipeTransportKeepsABoundedStderrTailAndPassesEveryChunkToOnStderr(t *testing.T) {
	var chunks []string
	program := startFake(t, mcp.PipeTransportOptions{MaxStderrBytes: 8, OnStderr: func(chunk string) { chunks = append(chunks, chunk) }})
	program.stderr("starting\n")
	program.stderr("ready\n")
	wantList(t, "chunks", chunks, []string{"starting\n", "ready\n"})
	if got := program.transport.Stderr(); got != "g\nready\n" {
		t.Fatalf("stderr = %q", got)
	}
}

// Pi packages/mcp/test/pipe.test.ts:133
func TestPipeTransportWritesEachMessageAsOneLine(t *testing.T) {
	program := startFake(t, mcp.PipeTransportOptions{})
	if err := program.transport.Send(t.Context(), ping(1)); err != nil {
		t.Fatal(err)
	}
	wantList(t, "written", program.written(), []string{pingJSON("1") + "\n"})
}

// Pi packages/mcp/test/pipe.test.ts:139
func TestPipeTransportReportsAnIncompleteMessageWhenTheProgramExitsThenCloses(t *testing.T) {
	program := startFake(t, mcp.PipeTransportOptions{})
	program.stdout(`{"jsonrpc":"2.0"`)
	program.pipe.end(nil)
	program.settle(t)
	wantList(t, "errors", program.errorList(), []string{"MCP stdio server closed with an incomplete JSON-RPC message"})
}

// Pi packages/mcp/test/pipe.test.ts:150
func TestPipeTransportReportsAFailedExitItDidNotCauseThenCloses(t *testing.T) {
	program := startFake(t, mcp.PipeTransportOptions{})
	program.pipe.end(errors.New("connection lost"))
	program.settle(t)
	wantList(t, "errors", program.errorList(), []string{"connection lost"})
}

// Like Pi's pipe send, which takes no signal, Send writes the line even when its context has ended: a line is written at once.
func TestPipeTransportSendWritesEvenWhenItsContextHasEnded(t *testing.T) {
	program := startFake(t, mcp.PipeTransportOptions{})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := program.transport.Send(ctx, ping(1)); err != nil {
		t.Fatal(err)
	}
	wantList(t, "written", program.written(), []string{pingJSON("1") + "\n"})
}

// Pi packages/mcp/test/pipe.test.ts:158
func TestPipeTransportRejectsSendAfterCloseAndAfterTheProgramEnded(t *testing.T) {
	closedByUs := startFake(t, mcp.PipeTransportOptions{})
	closedByUs.pipe.end(nil)
	if err := closedByUs.transport.Close(); err != nil {
		t.Fatal(err)
	}
	if err := closedByUs.transport.Send(t.Context(), ping(1)); !errors.As(err, new(*mcp.McpConnectionClosedError)) {
		t.Fatalf("send after close = %v", err)
	}

	ended := startFake(t, mcp.PipeTransportOptions{})
	ended.pipe.end(nil)
	ended.settle(t)
	if err := ended.transport.Send(t.Context(), ping(1)); !errors.As(err, new(*mcp.McpConnectionClosedError)) {
		t.Fatalf("send after exit = %v", err)
	}
	wantList(t, "written", ended.written(), nil)
}

// Pi packages/mcp/test/pipe.test.ts:171
func TestPipeTransportClosesStdinAndDoesNotKillAProgramThatExitsInTime(t *testing.T) {
	program := startFake(t, mcp.PipeTransportOptions{})
	closing := make(chan error, 1)
	go func() { closing <- program.transport.Close() }()
	<-program.pipe.stdinClosed
	wantList(t, "calls", program.pipe.callList(), []string{"closeStdin"})
	if err := program.transport.Send(t.Context(), ping(1)); !errors.As(err, new(*mcp.McpConnectionClosedError)) {
		t.Fatalf("send while closing = %v", err)
	}
	wantList(t, "written", program.written(), nil)
	program.pipe.end(nil)
	if err := <-closing; err != nil {
		t.Fatal(err)
	}
	program.settle(t)
	wantList(t, "calls", program.pipe.callList(), []string{"closeStdin"})
	wantList(t, "errors", program.errorList(), nil)
}

// Pi packages/mcp/test/pipe.test.ts:184
func TestPipeTransportShutsDownOnceWhenClosedTwice(t *testing.T) {
	program := startFake(t, mcp.PipeTransportOptions{})
	done := make(chan struct{}, 2)
	for range 2 {
		go func() { _ = program.transport.Close(); done <- struct{}{} }()
	}
	// The program ends only after the first Close took the shutdown: Pi's close returns without closing stdin once the program has ended.
	<-program.pipe.stdinClosed
	program.pipe.end(nil)
	<-done
	<-done
	if err := program.transport.Close(); err != nil {
		t.Fatal(err)
	}
	program.settle(t)
	wantList(t, "calls", program.pipe.callList(), []string{"closeStdin"})
}

// Pi packages/mcp/test/pipe.test.ts:195
func TestPipeTransportKillsAProgramOnlyAfterCloseTimeoutMs(t *testing.T) {
	program := startFake(t, mcp.PipeTransportOptions{CloseTimeoutMs: 400})
	closing := make(chan error, 1)
	start := time.Now()
	go func() { closing <- program.transport.Close() }()
	<-program.pipe.stdinClosed
	wantList(t, "calls before the timeout", program.pipe.callList(), []string{"closeStdin"})
	if err := <-closing; err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed < 400*time.Millisecond {
		t.Fatalf("killed after %v, before closeTimeoutMs", elapsed)
	}
	program.settle(t)
	wantList(t, "calls", program.pipe.callList(), []string{"closeStdin", "close"})
	// The failure of `exited` that killing caused is not an error.
	wantList(t, "errors", program.errorList(), nil)
}

// Pi packages/mcp/test/pipe.test.ts:209
func TestPipeTransportRejectsStartWhenOpenFails(t *testing.T) {
	transport := mcp.NewPipeTransport(mcp.PipeTransportOptions{Open: func(mcp.McpPipeHandlers) (mcp.McpPipe, error) {
		return nil, errors.New("not_supported")
	}})
	if err := transport.Start(); err == nil || err.Error() != "not_supported" {
		t.Fatalf("start = %v", err)
	}
}

// Pi packages/mcp/test/pipe.test.ts:214
func TestPipeTransportKillsAProgramThatFinishedStartingAfterClose(t *testing.T) {
	pipe := &fakePipe{exited: make(chan struct{})}
	release, opening := make(chan struct{}), make(chan struct{})
	transport := mcp.NewPipeTransport(mcp.PipeTransportOptions{Open: func(mcp.McpPipeHandlers) (mcp.McpPipe, error) {
		close(opening)
		<-release
		return pipe, nil
	}})
	starting := make(chan error, 1)
	go func() { starting <- transport.Start() }()
	// Close runs while the program is starting; it finds nothing to shut down.
	<-opening
	if err := transport.Close(); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-starting; !errors.As(err, new(*mcp.McpConnectionClosedError)) {
		t.Fatalf("start = %v", err)
	}
	wantList(t, "calls", pipe.callList(), []string{"close"})
}

// execPipe runs a program with os/exec, standing in for pi-env's openProcess.
type execPipe struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	exited chan struct{}
}

func (p *execPipe) Write(data any) {
	switch data := data.(type) {
	case string:
		_, _ = io.WriteString(p.stdin, data)
	case []byte:
		_, _ = p.stdin.Write(data)
	}
}
func (p *execPipe) CloseStdin() { _ = p.stdin.Close() }
func (p *execPipe) Close()      { _ = p.cmd.Process.Kill() }
func (p *execPipe) Exited() (int, error) {
	<-p.exited
	return p.cmd.ProcessState.ExitCode(), nil
}

func spawnPipe(command string, args []string, env map[string]string, handlers mcp.McpPipeHandlers) (mcp.McpPipe, error) {
	cmd := exec.Command(command, args...)
	cmd.Env = cmd.Environ()
	for key, value := range env {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
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
	pipe := &execPipe{cmd: cmd, stdin: stdin, exited: make(chan struct{})}
	var readers sync.WaitGroup
	pump := func(r io.Reader, handle func([]byte)) {
		defer readers.Done()
		buf := make([]byte, 4096)
		for {
			n, err := r.Read(buf)
			if n > 0 {
				handle(buf[:n])
			}
			if err != nil {
				return
			}
		}
	}
	readers.Add(2)
	go pump(stdout, handlers.OnStdout)
	go pump(stderr, handlers.OnStderr)
	go func() {
		readers.Wait()
		_ = cmd.Wait()
		close(pipe.exited)
	}()
	return pipe, nil
}

// Pi packages/mcp/test/pipe.test.ts:251
func TestPipeTransportWithARealProgramRunsInitializeToolsListAndToolsCall(t *testing.T) {
	command, args, env := fixtureStdioOptions("stdio-server")
	transport := mcp.NewPipeTransport(mcp.PipeTransportOptions{Open: func(handlers mcp.McpPipeHandlers) (mcp.McpPipe, error) {
		return spawnPipe(command, args, env, handlers)
	}})
	client := mcp.NewClient(mcp.ClientOptions{Name: "pipe-test", Version: "1.0.0"})
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

// Pi's README: `openProcess()` of pi-env returns an McpPipe without an adapter. PiG's env.RemoteProcess is one as it is, and its ProcessOptions take
// the handlers of McpPipeHandlers.
var _ mcp.McpPipe = (*env.RemoteProcess)(nil)

// Close ends every goroutine the transport started, whether the program exits on its own or is killed after closeTimeoutMs.
func TestPipeTransportLeavesNoGoroutineBehindAfterClose(t *testing.T) {
	baseline := testenv.BaselineGoroutines()
	exits := startFake(t, mcp.PipeTransportOptions{})
	closing := make(chan error, 1)
	go func() { closing <- exits.transport.Close() }()
	<-exits.pipe.stdinClosed
	exits.pipe.end(nil)
	if err := <-closing; err != nil {
		t.Fatal(err)
	}
	exits.settle(t)
	killed := startFake(t, mcp.PipeTransportOptions{CloseTimeoutMs: 20})
	if err := killed.transport.Close(); err != nil {
		t.Fatal(err)
	}
	killed.settle(t)
	baseline.RequireEnded(t, 10*time.Second)
}

// stuckPipe never reports its exit, even after Close.
type stuckPipe struct{ fakePipe }

func (p *stuckPipe) Close() {}

// Go mechanic: Close bounds the wait for the exit after the kill by the close timeout, where Pi's close awaits `exited` with no bound.
func TestPipeTransportCloseFailsWhenTheProgramDoesNotExitAfterTheKill(t *testing.T) {
	pipe := &stuckPipe{fakePipe{exited: make(chan struct{}), stdinClosed: make(chan struct{}, 4)}}
	transport := mcp.NewPipeTransport(mcp.PipeTransportOptions{
		CloseTimeoutMs: 20,
		Open:           func(mcp.McpPipeHandlers) (mcp.McpPipe, error) { return pipe, nil },
	})
	if err := transport.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pipe.end(nil) })
	done := make(chan error, 1)
	go func() { done <- transport.Close() }()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "did not exit") {
			t.Fatalf("Close = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not return")
	}
}

// McpPipe's write, closeStdin, close and exited, through the interface, on a real program: Pi packages/mcp/test/pipe.test.ts:242-245 (spawnPipe's
// pipe). The program answers what is written, ends when its stdin ends, and a program that is still running ends when it is closed.
func TestMcpPipeWritesClosesStdinAndKillsAProgram(t *testing.T) {
	command, args, env := fixtureStdioOptions("stdio-server")
	var mu sync.Mutex
	var stdout strings.Builder
	handlers := mcp.McpPipeHandlers{
		OnStdout: func(bytes []byte) {
			mu.Lock()
			defer mu.Unlock()
			stdout.Write(bytes)
		},
		OnStderr: func([]byte) {},
	}
	seen := func(text string) bool {
		mu.Lock()
		defer mu.Unlock()
		return strings.Contains(stdout.String(), text)
	}
	pipe, err := spawnPipe(command, args, env, handlers)
	if err != nil {
		t.Fatal(err)
	}
	// A string and bytes are both written, in order, to the program's stdin.
	pipe.Write(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"pipe-test","version":"1"}}}` + "\n")
	waitFor(t, func() bool { return seen(`"stdio-fixture"`) })
	pipe.Write([]byte(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}` + "\n"))
	waitFor(t, func() bool { return seen(`"echo"`) })
	pipe.CloseStdin()
	code, err := pipe.Exited()
	if err != nil || code != 0 {
		t.Fatalf("exit after closing stdin = %d, %v", code, err)
	}
	// Exited may be called again, from any goroutine.
	if again, err := pipe.Exited(); err != nil || again != code {
		t.Fatalf("second exit = %d, %v", again, err)
	}

	running, err := spawnPipe(command, args, env, handlers)
	if err != nil {
		t.Fatal(err)
	}
	running.Close()
	ended := make(chan struct{})
	go func() {
		_, _ = running.Exited()
		close(ended)
	}()
	select {
	case <-ended:
	case <-time.After(10 * time.Second):
		t.Fatal("the program still ran after Close")
	}
}

// The transport keeps the options it was given, as Pi's frozen copy does (pipe.test.ts:31 passes them): changing a returned copy changes nothing.
func TestPipeTransportKeepsACopyOfItsOptions(t *testing.T) {
	var stderr []string
	transport := mcp.NewPipeTransport(mcp.PipeTransportOptions{MaxMessageBytes: 48, MaxStderrBytes: 8, CloseTimeoutMs: 1_000, OnStderr: func(chunk string) { stderr = append(stderr, chunk) }})
	options := transport.Options()
	if options.MaxMessageBytes != 48 || options.MaxStderrBytes != 8 || options.CloseTimeoutMs != 1_000 || options.OnStderr == nil {
		t.Fatalf("options = %+v", options)
	}
	options.MaxMessageBytes = 1
	if got := transport.Options().MaxMessageBytes; got != 48 {
		t.Fatalf("MaxMessageBytes after changing a copy = %d", got)
	}
	options.OnStderr("x")
	wantList(t, "stderr", stderr, []string{"x"})
}
