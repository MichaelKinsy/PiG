// Package rpcclient is a Go client for `pig --mode rpc`: it spawns the agent
// in RPC mode and provides a typed API for all operations.
//
// Upstream reference:
//
//	.upstream/current/packages/coding-agent/src/modes/rpc/rpc-client.ts
//
// Upstream's Promise-returning methods are blocking calls here. Responses and
// events arrive on one stdout reader goroutine; event listeners run on it in
// registration order, as they run on Node's event loop upstream.
package rpcclient

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Upstream's fixed waits: the start settle delay, the per-request response
// timeout, the SIGTERM grace period in stop, and the default wait/collect
// timeout.
const (
	startSettleDelay = 100 * time.Millisecond
	requestTimeout   = 30 * time.Second
	stopGracePeriod  = time.Second
	DefaultTimeout   = 60 * time.Second
)

type pendingRequest struct {
	process *agentProcess
	resolve chan RpcResponse
	reject  chan error
}

type commandWrite struct {
	payload []byte
	result  chan error
}

type listenerEntry struct {
	id       uint64
	listener RpcEventListener
}

// agentProcess is one spawned agent: its command, stdin, and exit state.
// exited closes once the process has been reaped and exitErr is set.
type agentProcess struct {
	cmd            *exec.Cmd
	stdin          io.WriteCloser
	stdout         io.ReadCloser
	stderr         io.ReadCloser
	stop           sync.Once
	stdinClose     sync.Once
	stdinClosed    atomic.Bool
	writeMu        sync.Mutex
	writeQueue     []commandWrite
	writeWake      chan struct{}
	writingStopped bool
	stopWriting    chan struct{}
	exited         chan struct{}
	exitErr        error
	stopReading    atomic.Bool
}

// RpcClient mirrors upstream RpcClient.
type RpcClient struct {
	mu              sync.Mutex
	process         *agentProcess
	eventListeners  []listenerEntry
	nextListenerID  uint64
	pendingRequests map[string]pendingRequest
	requestID       int
	stderr          strings.Builder
	exitError       error
	options         RpcClientOptions
	readers         sync.WaitGroup
	writers         sync.WaitGroup
}

// NewRpcClient mirrors the upstream constructor.
func NewRpcClient(options RpcClientOptions) *RpcClient {
	return &RpcClient{options: options, pendingRequests: make(map[string]pendingRequest)}
}

// Start spawns the agent in RPC mode, waits upstream's 100ms settle delay,
// and reports an exit that happened during it.
func (c *RpcClient) Start() error {
	c.mu.Lock()
	if c.process != nil {
		c.mu.Unlock()
		return errors.New("Client already started")
	}
	c.exitError = nil
	proc, stdout, stderr, err := c.spawn()
	if err != nil {
		processErr := fmt.Errorf("Agent process error: %s. Stderr: %s", err.Error(), c.stderr.String())
		c.exitError = processErr
		c.mu.Unlock()
		return processErr
	}
	c.process = proc
	c.startWriter(proc)
	c.readers.Add(2)
	go func() { defer c.readers.Done(); c.collectStderr(proc, stderr) }()
	go func() { defer c.readers.Done(); c.readStdout(proc, stdout) }()
	go c.awaitExit(proc)
	c.mu.Unlock()

	time.Sleep(startSettleDelay)
	select {
	case <-proc.exited:
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.process != proc {
			return proc.exitErr
		}
		if c.exitError == nil {
			c.exitError = proc.exitErr
		}
		return c.exitError
	default:
		return nil
	}
}

func (c *RpcClient) spawn() (*agentProcess, io.ReadCloser, io.ReadCloser, error) {
	cliPath := c.options.CliPath
	if cliPath == "" {
		cliPath = "pig"
	}
	args := []string{"--mode", "rpc"}
	if c.options.Provider != "" {
		args = append(args, "--provider", c.options.Provider)
	}
	if c.options.Model != "" {
		args = append(args, "--model", c.options.Model)
	}
	args = append(args, c.options.Args...)

	cmd := exec.Command(cliPath, args...)
	cmd.Dir = c.options.Cwd
	cmd.Env = os.Environ()
	for _, key := range slices.Sorted(maps.Keys(c.options.Env)) {
		cmd.Env = append(cmd.Env, key+"="+c.options.Env[key])
	}
	// Own both ends until Start succeeds, including failures while opening a
	// later pipe. StdinPipe would hide the child's read end on those failures.
	var owned []io.Closer
	started := false
	defer func() {
		if !started {
			for _, descriptor := range owned {
				_ = descriptor.Close()
			}
		}
	}()
	openPipe := func() (*os.File, *os.File, error) {
		read, write, err := os.Pipe()
		if err == nil {
			owned = append(owned, read, write)
		}
		return read, write, err
	}
	stdinR, stdin, err := openPipe()
	if err != nil {
		return nil, nil, nil, err
	}
	// os.Pipe keeps the read ends ours, so Wait reaps the process as soon as
	// it exits instead of waiting for, or closing, the output streams.
	stdoutR, stdoutW, err := openPipe()
	if err != nil {
		return nil, nil, nil, err
	}
	stderrR, stderrW, err := openPipe()
	if err != nil {
		return nil, nil, nil, err
	}
	cmd.Stdin = stdinR
	cmd.Stdout, cmd.Stderr = stdoutW, stderrW
	startErr := cmd.Start()
	_ = stdinR.Close()
	_ = stdoutW.Close()
	_ = stderrW.Close()
	if startErr != nil {
		return nil, nil, nil, startErr
	}
	started = true
	return newAgentProcess(cmd, stdin, stdoutR, stderrR), stdoutR, stderrR, nil
}

func newAgentProcess(cmd *exec.Cmd, stdin io.WriteCloser, stdout, stderr io.ReadCloser) *agentProcess {
	return &agentProcess{
		cmd:         cmd,
		stdin:       stdin,
		stdout:      stdout,
		stderr:      stderr,
		writeWake:   make(chan struct{}, 1),
		stopWriting: make(chan struct{}),
		exited:      make(chan struct{}),
	}
}

func (c *RpcClient) startWriter(proc *agentProcess) {
	c.writers.Go(func() {
		for {
			request, ok, stopped := proc.nextWrite()
			if stopped {
				return
			}
			if ok {
				request.result <- c.writeCommand(proc, request.payload)
				continue
			}
			select {
			case <-proc.writeWake:
			case <-proc.stopWriting:
				return
			}
		}
	})
}

func (proc *agentProcess) enqueueWrite(request commandWrite) bool {
	proc.writeMu.Lock()
	defer proc.writeMu.Unlock()
	if proc.writingStopped {
		return false
	}
	proc.writeQueue = append(proc.writeQueue, request)
	select {
	case proc.writeWake <- struct{}{}:
	default:
	}
	return true
}

func (proc *agentProcess) nextWrite() (commandWrite, bool, bool) {
	proc.writeMu.Lock()
	defer proc.writeMu.Unlock()
	if proc.writingStopped {
		return commandWrite{}, false, true
	}
	if len(proc.writeQueue) == 0 {
		return commandWrite{}, false, false
	}
	request := proc.writeQueue[0]
	proc.writeQueue = proc.writeQueue[1:]
	if len(proc.writeQueue) == 0 {
		proc.writeQueue = nil
	}
	return request, true, false
}

func (proc *agentProcess) closeStdin() {
	proc.stdinClose.Do(func() {
		proc.stdinClosed.Store(true)
		proc.writeMu.Lock()
		proc.writingStopped = true
		queued := proc.writeQueue
		proc.writeQueue = nil
		proc.writeMu.Unlock()
		close(proc.stopWriting)
		_ = proc.stdin.Close()
		for _, request := range queued {
			request.result <- io.ErrClosedPipe
		}
	})
}

// collectStderr accumulates stderr for error messages and mirrors it to this
// process's stderr, as upstream does.
func (c *RpcClient) collectStderr(proc *agentProcess, stderr io.ReadCloser) {
	defer func() { _ = stderr.Close() }()
	buf := make([]byte, 4096)
	for {
		n, err := stderr.Read(buf)
		if n > 0 {
			c.mu.Lock()
			current := c.process == proc
			if current {
				c.stderr.Write(buf[:n])
			}
			c.mu.Unlock()
			if current {
				_, _ = os.Stderr.Write(buf[:n])
			}
		}
		if err != nil {
			return
		}
	}
}

// readStdout mirrors upstream attachJsonlLineReader: records split on LF only,
// one trailing CR is stripped, and a final unterminated record is delivered
// at end of stream.
func (c *RpcClient) readStdout(proc *agentProcess, stdout io.ReadCloser) {
	defer func() { _ = stdout.Close() }()
	_ = ReadJSONLLines(stdout, func(line []byte) bool {
		if proc.stopReading.Load() {
			// Detach callbacks but keep draining stdout until the shutdown owner closes it after exit.
			return true
		}
		c.handleLine(line)
		return true
	})
}

// awaitExit mirrors upstream's exit handler: once the process exits, the exit
// error is recorded and every pending request is rejected with it. Like Node's
// "exit" event, it does not wait for the output streams to drain.
func (c *RpcClient) awaitExit(proc *agentProcess) {
	_ = proc.cmd.Wait() // a non-zero or signalled exit is reported through ProcessState
	c.mu.Lock()
	proc.exitErr = c.createProcessExitError(proc.cmd.ProcessState)
	var pending []pendingRequest
	if c.process == proc {
		c.exitError = proc.exitErr
		pending = c.takePendingRequestsLocked()
	}
	c.mu.Unlock()
	// Reject before closing the write queue: pending callers observe upstream's
	// process-exit error rather than the pipe error used to release queued work.
	for _, request := range pending {
		request.reject <- proc.exitErr
	}
	proc.closeStdin()
	// Signal exit after requests are rejected and the process write queue closes.
	close(proc.exited)
}

// Stop mirrors upstream stop: it detaches stdout callbacks, sends SIGTERM, waits up to one second before killing, reaps the child and closes owned pipes.
// It is safe in an event callback; use Wait outside callbacks to join readers.
func (c *RpcClient) Stop() {
	c.mu.Lock()
	proc := c.process
	c.mu.Unlock()
	if proc == nil {
		return
	}
	proc.stop.Do(func() {
		proc.stopReading.Store(true)
		terminateProcess(proc.cmd.Process)
		select {
		case <-proc.exited:
		case <-time.After(stopGracePeriod):
			_ = proc.cmd.Process.Kill()
			<-proc.exited
		}
		proc.closeStdin()
		_ = proc.stdout.Close()
		_ = proc.stderr.Close()
	})
	c.mu.Lock()
	if c.process == proc {
		c.process = nil
		clear(c.pendingRequests)
	}
	c.mu.Unlock()
}

// Wait joins all owned stream readers and writers after Stop. Call it outside
// event callbacks and before starting another lifecycle; Stop alone is reentrant.
func (c *RpcClient) Wait() {
	c.readers.Wait()
	c.writers.Wait()
}

// OnEvent mirrors upstream onEvent and returns the unsubscribe function.
func (c *RpcClient) OnEvent(listener RpcEventListener) func() {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.onEventLocked(listener)
}

func (c *RpcClient) onEventLocked(listener RpcEventListener) func() {
	c.nextListenerID++
	id := c.nextListenerID
	c.eventListeners = append(c.eventListeners, listenerEntry{id: id, listener: listener})
	return func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		if index := slices.IndexFunc(c.eventListeners, func(e listenerEntry) bool { return e.id == id }); index != -1 {
			c.eventListeners = slices.Delete(c.eventListeners, index, index+1)
		}
	}
}

// GetStderr returns the collected stderr output.
func (c *RpcClient) GetStderr() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stderr.String()
}

// handleLine mirrors upstream handleLine: a response whose id matches a
// pending request resolves it; any other JSON record goes to the listeners;
// non-JSON lines are ignored.
func (c *RpcClient) handleLine(line []byte) {
	// Upstream's catch encloses both parsing and the listener loop: a throwing
	// listener ends this line's dispatch, without terminating the reader.
	defer func() { _ = recover() }() // upstream: coding-agent/src/modes/rpc/rpc-client.ts:eventListeners
	var value any
	if json.Unmarshal(line, &value) != nil || value == nil {
		// Upstream's JSON.parse throws, or reading .type on null throws; the
		// catch ignores the line.
		return
	}
	object, _ := value.(map[string]any)
	eventType, _ := object["type"].(string)
	if id, ok := object["id"].(string); ok && id != "" && eventType == "response" {
		c.mu.Lock()
		pending, found := c.pendingRequests[id]
		delete(c.pendingRequests, id)
		c.mu.Unlock()
		if found {
			// Upstream resolves with the parsed object as is; decode what fits.
			var response RpcResponse
			_ = json.Unmarshal(line, &response)
			pending.resolve <- response
			return
		}
	}
	c.emitEvent(JsonAgentSessionEvent{Type: eventType, Raw: append(json.RawMessage(nil), line...)})
}

// emitEvent walks the live listener list by index, as upstream's for...of over
// the array does, so a listener that unsubscribes itself shifts the next one
// into its slot.
func (c *RpcClient) emitEvent(event JsonAgentSessionEvent) {
	for i := 0; ; i++ {
		c.mu.Lock()
		if i >= len(c.eventListeners) {
			c.mu.Unlock()
			return
		}
		listener := c.eventListeners[i].listener
		c.mu.Unlock()
		listener(event)
	}
}

func (c *RpcClient) createProcessExitError(state *os.ProcessState) error {
	code, signal := exitCodeAndSignal(state)
	return fmt.Errorf("Agent process exited (code=%s signal=%s). Stderr: %s", code, signal, c.stderr.String())
}

func (c *RpcClient) takePendingRequestsLocked() []pendingRequest {
	pending := slices.Collect(maps.Values(c.pendingRequests))
	clear(c.pendingRequests)
	return pending
}

// send mirrors upstream send: it assigns the next req_N id, queues one
// serialized JSONL command, and bounds the caller's wait for writing and the
// response by the same 30s deadline. The process-owned writer may finish a
// timed-out record later, as upstream's buffered stream does.
func (c *RpcClient) send(command rpcCommand) (RpcResponse, error) {
	return c.sendWithTimeout(command, requestTimeout)
}

func (c *RpcClient) sendWithTimeout(command rpcCommand, timeout time.Duration) (RpcResponse, error) {
	request, id, err := c.registerRequest()
	if err != nil {
		return RpcResponse{}, err
	}
	payload, err := command.marshal(id)
	if err != nil {
		c.mu.Lock()
		delete(c.pendingRequests, id)
		c.mu.Unlock()
		return RpcResponse{}, err
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	writeResult := make(chan error, 1)
	if !request.process.enqueueWrite(commandWrite{payload: payload, result: writeResult}) {
		c.mu.Lock()
		delete(c.pendingRequests, id)
		writeErr := request.process.exitErr
		if writeErr == nil {
			writeErr = fmt.Errorf("Agent process stdin is not writable. Stderr: %s", c.stderr.String())
		}
		if c.process == request.process {
			if c.exitError == nil {
				c.exitError = writeErr
			}
			writeErr = c.exitError
		}
		c.mu.Unlock()
		return RpcResponse{}, writeErr
	}
	select {
	case response := <-request.resolve:
		return response, nil
	case err := <-request.reject:
		return RpcResponse{}, err
	case writeErr := <-writeResult:
		if writeErr != nil {
			return RpcResponse{}, requestWriteError(request, writeErr)
		}
	case <-timer.C:
		select {
		case response := <-request.resolve:
			return response, nil
		case err := <-request.reject:
			return RpcResponse{}, err
		case writeErr := <-writeResult:
			if writeErr != nil {
				return RpcResponse{}, requestWriteError(request, writeErr)
			}
			return RpcResponse{}, c.timeoutRequest(id, command.typ)
		default:
			return RpcResponse{}, c.timeoutRequest(id, command.typ)
		}
	}
	select {
	case response := <-request.resolve:
		return response, nil
	case err := <-request.reject:
		return RpcResponse{}, err
	case <-timer.C:
		select {
		case response := <-request.resolve:
			return response, nil
		case err := <-request.reject:
			return RpcResponse{}, err
		default:
			return RpcResponse{}, c.timeoutRequest(id, command.typ)
		}
	}
}

func requestWriteError(request pendingRequest, writeErr error) error {
	select {
	case err := <-request.reject:
		return err
	default:
		return writeErr
	}
}

func (c *RpcClient) timeoutRequest(id, commandType string) error {
	c.mu.Lock()
	delete(c.pendingRequests, id)
	stderr := c.stderr.String()
	c.mu.Unlock()
	return fmt.Errorf("Timeout waiting for response to %s. Stderr: %s", commandType, stderr)
}

// registerRequest performs send's preconditions and registers the pending
// request under its new id.
func (c *RpcClient) registerRequest() (pendingRequest, string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	proc := c.process
	if proc == nil {
		return pendingRequest{}, "", errors.New("Client not started")
	}
	if c.exitError != nil {
		return pendingRequest{}, "", c.exitError
	}
	select {
	case <-proc.exited:
		c.exitError = proc.exitErr
		return pendingRequest{}, "", c.exitError
	default:
	}
	if proc.stdinClosed.Load() {
		c.exitError = fmt.Errorf("Agent process stdin is not writable. Stderr: %s", c.stderr.String())
		return pendingRequest{}, "", c.exitError
	}
	c.requestID++
	id := fmt.Sprintf("req_%d", c.requestID)
	request := pendingRequest{process: proc, resolve: make(chan RpcResponse, 1), reject: make(chan error, 1)}
	c.pendingRequests[id] = request
	return request, id, nil
}

// writeCommand writes one JSONL record. A write failure is upstream's stdin
// "error" event: stdin is marked closed, the exit error (or a new stdin error)
// is recorded, and every pending request, this one included, is rejected.
func (c *RpcClient) writeCommand(proc *agentProcess, payload []byte) error {
	_, err := proc.stdin.Write(payload)
	if err == nil {
		return nil
	}
	proc.stdinClosed.Store(true)
	c.mu.Lock()
	if c.process != proc {
		c.mu.Unlock()
		return err
	}
	if c.exitError == nil {
		c.exitError = fmt.Errorf("Agent process stdin error: %s. Stderr: %s", err.Error(), c.stderr.String())
	}
	stdinErr := c.exitError
	pending := c.takePendingRequestsLocked()
	c.mu.Unlock()
	for _, request := range pending {
		request.reject <- stdinErr
	}
	return err
}

// getData mirrors upstream getData: a failed response becomes its error, and
// a successful one decodes its data into T.
func getData[T any](response RpcResponse) (T, error) {
	var data T
	if !response.Success {
		return data, errors.New(response.Error)
	}
	if len(response.Data) == 0 {
		return data, nil
	}
	err := json.Unmarshal(response.Data, &data)
	return data, err
}

// eventCollector is a listener that records events until agent_settled.
type eventCollector struct {
	unsubscribe func()
	settled     chan []JsonAgentSessionEvent
}

func (c *RpcClient) startCollecting(keep bool) eventCollector {
	// Registration and closure initialization are one publication boundary.
	// Node cannot dispatch during onEvent's synchronous call; Go's reader can.
	c.mu.Lock()
	defer c.mu.Unlock()
	collector := eventCollector{settled: make(chan []JsonAgentSessionEvent, 1)}
	var events []JsonAgentSessionEvent
	var once sync.Once
	collector.unsubscribe = c.onEventLocked(func(event JsonAgentSessionEvent) {
		if keep {
			events = append(events, event)
		}
		if event.Type == "agent_settled" {
			once.Do(func() {
				collector.unsubscribe()
				collector.settled <- events
			})
		}
	})
	return collector
}

func (c *RpcClient) finishCollecting(collector eventCollector, timeout time.Duration, message string) ([]JsonAgentSessionEvent, error) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case events := <-collector.settled:
		return events, nil
	case <-timer.C:
		collector.unsubscribe()
		return nil, fmt.Errorf("%s. Stderr: %s", message, c.GetStderr())
	}
}

func (c *RpcClient) awaitSettled(timeout time.Duration, keep bool, message string) ([]JsonAgentSessionEvent, error) {
	return c.finishCollecting(c.startCollecting(keep), timeout, message)
}
