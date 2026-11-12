//go:build unix

package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

func startAsyncModesRPC(t *testing.T) *rpcProcess {
	t.Helper()
	home := t.TempDir()
	return startRPCProcess(t, []string{"PIG_HOME=" + home, "PIG_CODING_AGENT_DIR=" + filepath.Join(home, "agent")}, "--no-session")
}

// TestRPCAbortBashAnswersWhileTheBashCommandIsInFlight covers handleCommand's
// bash and abort_bash (rpc-mode.ts:401, handleInputLine:763): input lines are
// handled concurrently, so abort_bash is read and answered while the bash
// command still awaits its child, and the bash response follows with the
// cancelled result. The child announces itself through a FIFO, so abort_bash is
// sent only once bash is provably running.
func TestRPCAbortBashAnswersWhileTheBashCommandIsInFlight(t *testing.T) {
	p := startAsyncModesRPC(t)
	fifo := filepath.Join(t.TempDir(), "started")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	p.sendJSON(map[string]any{"id": "b", "type": "bash", "command": "echo ready > '" + fifo + "'; sleep 30"})
	started := make(chan error, 1)
	go func() {
		f, err := os.Open(fifo) // blocks until the shell opens it for writing
		if err == nil {
			_, err = f.Read(make([]byte, 1))
			_ = f.Close()
		}
		started <- err
	}()
	select {
	case err := <-started:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(testbudget.Wait(t)):
		t.Fatalf("the bash command never started\n%s", p.stderr.String())
	}
	p.sendJSON(map[string]any{"id": "a", "type": "abort_bash"})
	var order []string
	var bashData map[string]any
	p.await("the abort_bash and bash responses", func(record rpcRecord) bool {
		id, _ := record["id"].(string)
		if record["type"] != "response" || (id != "a" && id != "b") {
			return false
		}
		if record["success"] != true {
			t.Fatalf("response %s failed: %v", id, record)
		}
		order = append(order, id)
		if id == "b" {
			bashData, _ = record["data"].(map[string]any)
		}
		return len(order) == 2
	})
	if strings.Join(order, ",") != "a,b" {
		t.Errorf("responses arrived as %v, want abort_bash before the bash it aborts", order)
	}
	if bashData["cancelled"] != true {
		t.Errorf("bash data = %v, want cancelled", bashData)
	}
}

// TestRPCParseErrorAndRejectedHandlerAnswerInOrderAndTheLoopContinues covers
// handleInputLine (rpc-mode.ts:763-811): a line that is not JSON answers a
// parse error without id, a command whose handler throws answers an error with
// its id and type, and every later line is still handled, the responses in the
// order of the lines.
func TestRPCParseErrorAndRejectedHandlerAnswerInOrderAndTheLoopContinues(t *testing.T) {
	p := startAsyncModesRPC(t)
	p.send(`{not json`)
	p.sendJSON(map[string]any{"id": "p", "type": "prompt", "message": 5}) // message.startsWith throws in Pi
	p.sendJSON(map[string]any{"id": "s", "type": "get_state"})
	var got []rpcRecord
	p.await("the three responses", func(record rpcRecord) bool {
		if record["type"] != "response" {
			return false
		}
		got = append(got, record)
		return len(got) == 3
	})
	parse, rejected, state := got[0], got[1], got[2]
	if _, hasID := parse["id"]; hasID || parse["command"] != "parse" || parse["success"] != false || !strings.HasPrefix(parse["error"].(string), "Failed to parse command: ") {
		t.Errorf("first response = %v, want the id-less parse error", parse)
	}
	if rejected["id"] != "p" || rejected["command"] != "prompt" || rejected["success"] != false || !strings.Contains(rejected["error"].(string), "startsWith") {
		t.Errorf("second response = %v, want the prompt error carrying its id", rejected)
	}
	if state["id"] != "s" || state["success"] != true {
		t.Errorf("third response = %v, want get_state to succeed after both failures", state)
	}
}

// TestRPCAgentSubscriberWaitsForStdoutBackpressureOnEveryEvent covers
// agent.subscribe(async () => await waitForRawStdoutBackpressure()) (rpc-mode.ts:361-363):
// the agent loop does not hand an event to the next subscriber, nor start the
// next event, until the wait returns, once per event. Subscriber A is the
// writer before it, C a subscriber after it.
func TestRPCAgentSubscriberWaitsForStdoutBackpressureOnEveryEvent(t *testing.T) {
	streamFn := func(context.Context, *ai.Model, ai.TranscriptContext, ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
		message := &ai.AssistantMessage{Content: []ai.AssistantContentBlock{ai.TextContent{Text: "done"}}, API: "test", Provider: "test", Model: "mock", StopReason: ai.StopReasonStop}
		stream := ai.NewAssistantMessageEventStream()
		_ = stream.Push(ai.StartEvent{Partial: &ai.AssistantMessage{API: "test", Provider: "test", Model: "mock", StopReason: ai.StopReasonPending}})
		_ = stream.Push(ai.DoneEvent{Reason: ai.StopReasonStop, Message: message})
		return stream, nil
	}
	a, err := agent.NewAgent(agent.AgentOptions{Model: &ai.Model{ID: "mock", DisplayName: "mock", Capabilities: ai.ModelCapabilities{ContextWindow: 8192}}, StreamFn: streamFn})
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var seenByA, seenByC, waits int
	counts := func() (int, int, int) { mu.Lock(); defer mu.Unlock(); return seenByA, seenByC, waits }
	a.Subscribe(func(context.Context, agent.AgentEvent) error { mu.Lock(); seenByA++; mu.Unlock(); return nil })
	entered, release := make(chan struct{}, 1), make(chan struct{})
	first := true
	defer subscribeStdoutBackpressure(a, func() {
		mu.Lock()
		waits++
		block := first
		first = false
		mu.Unlock()
		if block {
			entered <- struct{}{}
			<-release
		}
	})()
	a.Subscribe(func(context.Context, agent.AgentEvent) error { mu.Lock(); seenByC++; mu.Unlock(); return nil })

	done := make(chan error, 1)
	go func() { done <- a.Prompt(t.Context(), "hi") }()
	select {
	case <-entered:
	case err := <-done:
		t.Fatalf("the prompt finished (%v) without waiting for stdout backpressure on its first event", err)
	case <-time.After(testbudget.Wait(t)):
		t.Fatal("the backpressure wait was never entered")
	}
	if a1, c1, w1 := counts(); a1 != 1 || c1 != 0 || w1 != 1 {
		t.Fatalf("while the first wait blocks: writer saw %d events, later subscriber %d, waits %d; want 1, 0, 1", a1, c1, w1)
	}
	select {
	case err := <-done:
		t.Fatalf("the prompt finished (%v) while stdout was backpressured", err)
	default:
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	a2, c2, w2 := counts()
	if a2 < 2 || c2 != a2 || w2 != a2 {
		t.Errorf("after release: writer saw %d events, later subscriber %d, waits %d; want every event waited on once", a2, c2, w2)
	}
}
