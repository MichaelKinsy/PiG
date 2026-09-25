package rpcclient

import (
	"encoding/json"
	"io"
	"runtime"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// waitForRPCReaders waits for the client's stream readers to join. The wait
// synchronizes on Wait itself; testbudget only bounds how long a genuine hang
// takes to fail, since a loaded host can delay the child's exit and pipe
// close by seconds.
func waitForRPCReaders(t *testing.T, client *RpcClient) {
	t.Helper()
	done := make(chan struct{})
	go func() { client.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(testbudget.Wait(t)):
		t.Fatal("RPC stream readers did not join")
	}
}

func TestRPCStopFromListenerIsReentrantAndReadersJoin(t *testing.T) {
	client, _ := childClient(t, "emit")
	stopped := make(chan struct{})
	var once sync.Once
	client.OnEvent(func(JsonAgentSessionEvent) { once.Do(func() { client.Stop(); close(stopped) }) })
	// The callback may stop the child during Start's settle period. It runs
	// once the child's first record arrives, which on a loaded host can take
	// seconds after the spawn; only a deadlocked Stop never closes stopped.
	_ = client.Start()
	select {
	case <-stopped:
	case <-time.After(testbudget.Wait(t)):
		t.Fatal("Stop deadlocked inside event callback")
	}
	waitForRPCReaders(t, client)
}

func TestRPCRestartDoesNotRetargetOldRequestWrite(t *testing.T) {
	client, logPath := childClient(t, "canned")
	if err := client.Start(); err != nil {
		t.Fatal(err)
	}
	request, id, err := client.registerRequest()
	if err != nil {
		t.Fatal(err)
	}
	payload, err := command("clone").marshal(id)
	if err != nil {
		t.Fatal(err)
	}
	client.Stop()
	waitForRPCReaders(t, client)
	select {
	case <-request.reject:
	default:
		t.Fatal("stopped request was not rejected")
	}
	if err := client.Start(); err != nil {
		t.Fatal(err)
	}
	if err := client.writeCommand(request.process, payload); err == nil {
		t.Fatal("old process stdin remained writable after stop")
	}
	if _, err := client.GetState(); err != nil {
		t.Fatalf("old stdin failure poisoned replacement: %v", err)
	}
	commands := loggedCommands(t, logPath)
	if len(commands) != 1 || strings.Contains(commands[0], "clone") {
		t.Fatalf("old request reached replacement: %v", commands)
	}
	client.Stop()
	waitForRPCReaders(t, client)
}

type blockedRPCWrite struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (w *blockedRPCWrite) Write([]byte) (int, error) {
	close(w.started)
	<-w.release
	return 0, io.ErrClosedPipe
}

func (w *blockedRPCWrite) Close() error {
	w.once.Do(func() { close(w.release) })
	return nil
}

func TestRPCRequestTimeoutIncludesBlockedWrite(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		writer := &blockedRPCWrite{started: make(chan struct{}), release: make(chan struct{})}
		client := NewRpcClient(RpcClientOptions{})
		proc := newAgentProcess(nil, writer, nil, nil)
		client.process = proc
		client.startWriter(proc)
		done := make(chan error, 1)
		go func() {
			_, err := client.send(rpcCommand{typ: "get_state"})
			done <- err
		}()
		synctest.Wait()
		select {
		case <-writer.started:
		default:
			t.Fatal("request did not start its stdin write")
		}
		time.Sleep(requestTimeout + time.Second)
		synctest.Wait()
		select {
		case err := <-done:
			if err == nil || !strings.HasPrefix(err.Error(), "Timeout waiting for response to get_state.") {
				t.Fatalf("request error = %v", err)
			}
		default:
			t.Error("request still blocked after its response deadline")
		}
		proc.closeStdin()
		synctest.Wait()
		client.writers.Wait()
	})
}

type delayedRPCWrite struct {
	client      *RpcClient
	release     chan struct{}
	first       sync.Once
	releaseOnce sync.Once
	mu          sync.Mutex
	closed      bool
	records     []string
}

func (w *delayedRPCWrite) Write(payload []byte) (int, error) {
	w.first.Do(func() { <-w.release })
	var command struct {
		Type string `json:"type"`
		ID   string `json:"id"`
	}
	if err := json.Unmarshal(payload, &command); err != nil {
		return 0, err
	}
	w.mu.Lock()
	w.records = append(w.records, command.Type)
	w.mu.Unlock()
	response, err := json.Marshal(map[string]any{
		"type": "response", "id": command.ID, "command": command.Type, "success": true,
	})
	if err != nil {
		return 0, err
	}
	w.client.handleLine(response)
	return len(payload), nil
}

func (w *delayedRPCWrite) resume() {
	w.releaseOnce.Do(func() { close(w.release) })
}

func (w *delayedRPCWrite) Close() error {
	w.mu.Lock()
	w.closed = true
	w.mu.Unlock()
	w.resume()
	return nil
}

// Upstream times out only the request. A stream write that drains later leaves
// the same child transport available for subsequent commands.
func TestRPCTimedOutWriteKeepsSameChildUsable(t *testing.T) {
	writer := &delayedRPCWrite{release: make(chan struct{})}
	client := NewRpcClient(RpcClientOptions{})
	writer.client = client
	proc := newAgentProcess(nil, writer, nil, nil)
	client.process = proc
	client.startWriter(proc)
	t.Cleanup(func() {
		proc.closeStdin()
		client.writers.Wait()
	})

	_, err := client.sendWithTimeout(rpcCommand{typ: "prompt"}, 50*time.Millisecond)
	if err == nil || !strings.HasPrefix(err.Error(), "Timeout waiting for response to prompt.") {
		t.Fatalf("first request error = %v", err)
	}
	writer.resume()
	if _, err := client.GetState(); err != nil {
		t.Fatalf("next request on the same child failed after a timed-out write: %v", err)
	}
	writer.mu.Lock()
	closed, records := writer.closed, append([]string(nil), writer.records...)
	writer.mu.Unlock()
	if closed {
		t.Fatal("request timeout closed the child's stdin")
	}
	if len(records) != 2 || records[0] != "prompt" || records[1] != "get_state" {
		t.Fatalf("records = %v, want [prompt get_state]", records)
	}
}

// Upstream calls stdin.write even while an earlier record is backpressured.
// Each request may time out, but every record remains in the process FIFO.
func TestRPCQueuedTimeoutRetainsRecord(t *testing.T) {
	writer := &delayedRPCWrite{release: make(chan struct{})}
	client := NewRpcClient(RpcClientOptions{})
	writer.client = client
	proc := newAgentProcess(nil, writer, nil, nil)
	client.process = proc
	client.startWriter(proc)
	t.Cleanup(func() {
		proc.closeStdin()
		client.writers.Wait()
	})

	for _, typ := range []string{"prompt", "abort"} {
		if _, err := client.sendWithTimeout(rpcCommand{typ: typ}, 10*time.Millisecond); err == nil {
			t.Fatalf("%s did not time out", typ)
		}
	}
	writer.resume()
	if _, err := client.GetState(); err != nil {
		t.Fatal(err)
	}
	writer.mu.Lock()
	records := append([]string(nil), writer.records...)
	writer.mu.Unlock()
	want := []string{"prompt", "abort", "get_state"}
	if len(records) != len(want) {
		t.Fatalf("records = %v, want %v", records, want)
	}
	for i := range want {
		if records[i] != want[i] {
			t.Fatalf("records = %v, want %v", records, want)
		}
	}
}

func TestRPCBlockedChildWriteCannotOutliveRestart(t *testing.T) {
	client, logPath := childClient(t, "not-reading")
	if err := client.Start(); err != nil {
		t.Fatal(err)
	}
	message := strings.Repeat("x", 8<<20)
	proc := client.process
	started := time.Now()
	_, err := client.sendWithTimeout(command("prompt", field("message", message)), 200*time.Millisecond)
	if err == nil || !strings.HasPrefix(err.Error(), "Timeout waiting for response to prompt.") {
		t.Fatalf("blocked send error = %v", err)
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("blocked send exceeded bounded timeout: %v", elapsed)
	}
	if proc.stdinClosed.Load() {
		t.Fatal("request timeout closed the old process stdin")
	}
	_, err = client.sendWithTimeout(command("abort"), 100*time.Millisecond)
	if err == nil || !strings.HasPrefix(err.Error(), "Timeout waiting for response to abort.") {
		t.Fatalf("queued send error = %v", err)
	}
	client.Stop()
	waitForRPCReaders(t, client)
	if !proc.stdinClosed.Load() {
		t.Fatal("Stop left the old process stdin open")
	}

	client.options.Env[childModeEnv] = "canned"
	if err := client.Start(); err != nil {
		t.Fatal(err)
	}
	if _, err := client.GetState(); err != nil {
		t.Fatalf("replacement process failed: %v", err)
	}
	if got := loggedCommands(t, logPath); len(got) != 1 || !strings.Contains(got[0], `"type":"get_state"`) {
		t.Fatalf("replacement commands = %v", got)
	}
	client.Stop()
	waitForRPCReaders(t, client)
}

type serialRPCWrite struct {
	client      *RpcClient
	started     chan struct{}
	release     chan struct{}
	startedOnce sync.Once
	releaseOnce sync.Once
	mu          sync.Mutex
	active      int
	maxActive   int
	records     [][]byte
}

func (w *serialRPCWrite) Write(payload []byte) (int, error) {
	w.mu.Lock()
	w.active++
	w.maxActive = max(w.maxActive, w.active)
	w.mu.Unlock()
	w.startedOnce.Do(func() { close(w.started) })
	<-w.release

	var command struct {
		Type string `json:"type"`
		ID   string `json:"id"`
	}
	if err := json.Unmarshal(payload, &command); err != nil {
		return 0, err
	}
	response, err := json.Marshal(map[string]any{
		"type": "response", "id": command.ID, "command": command.Type, "success": true,
	})
	if err != nil {
		return 0, err
	}
	w.client.handleLine(response)
	w.mu.Lock()
	w.active--
	w.records = append(w.records, append([]byte(nil), payload...))
	w.mu.Unlock()
	return len(payload), nil
}

func (w *serialRPCWrite) Close() error {
	w.releaseOnce.Do(func() { close(w.release) })
	return nil
}

func TestRPCConcurrentWritesRemainWholeJSONLRecords(t *testing.T) {
	writer := &serialRPCWrite{started: make(chan struct{}), release: make(chan struct{})}
	client := NewRpcClient(RpcClientOptions{})
	writer.client = client
	proc := newAgentProcess(nil, writer, nil, nil)
	client.process = proc
	client.startWriter(proc)
	const requestCount = 8
	errs := make(chan error, requestCount)
	var sends sync.WaitGroup
	for range requestCount {
		sends.Go(func() {
			_, err := client.GetState()
			errs <- err
		})
	}
	select {
	case <-writer.started:
	case <-time.After(5 * time.Second):
		t.Fatal("no command reached stdin")
	}
	for range 100 {
		runtime.Gosched()
	}
	_ = writer.Close()
	sends.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	proc.closeStdin()
	client.writers.Wait()

	writer.mu.Lock()
	defer writer.mu.Unlock()
	if writer.maxActive != 1 {
		t.Fatalf("maximum concurrent stdin writes = %d, want 1", writer.maxActive)
	}
	if len(writer.records) != requestCount {
		t.Fatalf("recorded %d commands, want %d", len(writer.records), requestCount)
	}
	ids := make(map[string]bool, requestCount)
	for _, payload := range writer.records {
		var record struct {
			Type string `json:"type"`
			ID   string `json:"id"`
		}
		if err := json.Unmarshal(payload, &record); err != nil {
			t.Fatalf("interleaved JSONL record %q: %v", payload, err)
		}
		if record.Type != "get_state" || record.ID == "" || ids[record.ID] {
			t.Fatalf("invalid JSONL record %q", payload)
		}
		ids[record.ID] = true
	}
}

func TestCollectorRegistrationIsReadyBeforePublication(t *testing.T) {
	client := NewRpcClient(RpcClientOptions{})
	for range 100 {
		collected := make(chan eventCollector, 1)
		go func() { collected <- client.startCollecting(true) }()
		deadline := time.Now().Add(5 * time.Second)
		for {
			client.mu.Lock()
			published := len(client.eventListeners) != 0
			client.mu.Unlock()
			if published {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("collector did not register")
			}
			runtime.Gosched()
		}
		// The reader can deliver immediately after OnEvent publishes a callback,
		// without waiting for startCollecting to return to the subscribing caller.
		client.handleLine([]byte(`{"type":"agent_settled"}`))
		collector := <-collected
		events, err := client.finishCollecting(collector, time.Second, "collector not initialized")
		if err != nil || len(events) != 1 {
			t.Fatalf("immediate settled: events=%v error=%v", events, err)
		}
	}
}

func TestRPCListenerPanicIsConfinedToItsLine(t *testing.T) {
	client := NewRpcClient(RpcClientOptions{})
	remove := client.OnEvent(func(JsonAgentSessionEvent) { panic("listener failed") })
	delivered := 0
	client.OnEvent(func(JsonAgentSessionEvent) { delivered++ })
	func() {
		defer func() {
			if cause := recover(); cause != nil {
				t.Errorf("listener panic escaped handleLine: %v", cause)
			}
		}()
		client.handleLine([]byte(`{"type":"first"}`))
	}()
	if delivered != 0 {
		t.Fatal("upstream aborts the current listener loop after a throw")
	}
	remove()
	client.handleLine([]byte(`{"type":"second"}`))
	if delivered != 1 {
		t.Fatal("next line did not resume dispatch")
	}
}
