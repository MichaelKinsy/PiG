package codemode

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// The remote side of the protocol (packages/codemode/src/runtime/remote.ts: serveCodemodeRemote) against a scripted VM.

// serveChannel is the VM of a served execution: it records what the server posts and how often it stops.
type serveChannel struct {
	mu      sync.Mutex
	posted  []callResult
	stopped int
	// arrived receives a signal for each post.
	arrived chan struct{}
}

func (c *serveChannel) start() {}

func (c *serveChannel) post(r callResult) {
	c.mu.Lock()
	c.posted = append(c.posted, r)
	c.mu.Unlock()
	c.arrived <- struct{}{}
}

func (c *serveChannel) stop() {
	c.mu.Lock()
	c.stopped++
	c.mu.Unlock()
}

func (c *serveChannel) state() ([]callResult, int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]callResult(nil), c.posted...), c.stopped
}

// served is one run of serveRemote with the exchanges it made.
type served struct {
	channel   *serveChannel
	budget    int
	data      VmStartData
	events    vmEvents
	mu        sync.Mutex
	exchanges [][]RemoteMessage
	returned  chan struct{}
}

// wait returns once serveRemote has returned.
func (s *served) wait(t *testing.T) {
	t.Helper()
	select {
	case <-s.returned:
	case <-time.After(10 * time.Second):
		t.Fatal("serveRemote did not return")
	}
}

func (s *served) batches() [][]RemoteMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([][]RemoteMessage(nil), s.exchanges...)
}

// serveWith starts serveRemote. script runs inside the launcher, as a VM that reports before launch returns. answer
// decides each exchange's reply; nil answers null at once. Serving ends when the test calls wait.
func serveWith(t *testing.T, ctx context.Context, request RemoteRequest, options ServeOptions,
	script func(events vmEvents), answer func(batch []RemoteMessage, reply func(*RemoteReply, error))) *served {
	t.Helper()
	s := &served{channel: &serveChannel{arrived: make(chan struct{}, 64)}, returned: make(chan struct{})}
	launch := func(budget int) vmLauncher {
		s.budget = budget
		return func(_ context.Context, data VmStartData, events vmEvents) (vmChannel, error) {
			s.data, s.events = data, events
			if script != nil {
				script(events)
			}
			return s.channel, nil
		}
	}
	exchange := func(_ context.Context, messages []RemoteMessage, reply func(*RemoteReply, error)) {
		s.mu.Lock()
		s.exchanges = append(s.exchanges, messages)
		s.mu.Unlock()
		if answer == nil {
			reply(nil, nil)
			return
		}
		answer(messages, reply)
	}
	go func() {
		defer close(s.returned)
		serveRemote(ctx, request, exchange, options, launch)
	}()
	return s
}

func outputOf(text string) vmMessage {
	return vmMessage{kind: vmMessageOutput, item: OutputItem{Type: OutputItemText, Text: text, Console: true}}
}

func doneVM(value string) vmMessage {
	return vmMessage{kind: vmMessageDone, ok: true, value: value, valueDefined: true, writes: "[]", writesDefined: true}
}

// remote.ts: output is buffered and sent with the next call or the terminal message, at most 1000 items per exchange.
// portable.test.ts "batches output instead of making one exchange per line" (5000 lines, at most 6 exchanges).
func TestServedOutputIsBatchedByItemCount(t *testing.T) {
	s := serveWith(t, t.Context(), RemoteRequest{}, ServeOptions{}, func(events vmEvents) {
		for i := range 5000 {
			events.message(outputOf(strconv.Itoa(i)))
		}
		events.message(doneVM("1"))
	}, nil)
	s.wait(t)
	batches := s.batches()
	if len(batches) != 6 {
		t.Fatalf("%d exchanges, want 5 flushes of 1000 items and the terminal one", len(batches))
	}
	next := 0
	for i, batch := range batches {
		for _, m := range batch {
			if m.Type == MessageOutput {
				if got := m.Item.Text; got != strconv.Itoa(next) {
					t.Fatalf("exchange %d: item %q out of order, want %d", i, got, next)
				}
				next++
			}
		}
	}
	last := batches[5]
	if next != 5000 || len(last) != 1 || last[0].Type != MessageDone {
		t.Fatalf("%d items delivered, terminal batch %+v, want 5000 and [done]", next, last)
	}
	for i, batch := range batches[:5] {
		if len(batch) != 1000 {
			t.Errorf("exchange %d carries %d items, want 1000", i, len(batch))
		}
	}
}

// remote.ts: the buffer is also flushed once it holds 1<<20 characters (image data counts as well as text).
func TestServedOutputIsFlushedAtTheCharacterLimit(t *testing.T) {
	s := serveWith(t, t.Context(), RemoteRequest{}, ServeOptions{}, func(events vmEvents) {
		events.message(outputOf(strings.Repeat("x", 1<<19)))
		events.message(vmMessage{kind: vmMessageOutput, item: OutputItem{Type: OutputItemImage, Data: strings.Repeat("y", 1<<19), MimeType: "image/png"}})
		events.message(outputOf("tail"))
		events.message(doneVM("1"))
	}, nil)
	s.wait(t)
	batches := s.batches()
	if len(batches) != 2 || len(batches[0]) != 2 || len(batches[1]) != 2 || batches[1][0].Item.Text != "tail" {
		t.Fatalf("batches = %d (%d items, then %d), want the two big items flushed together before the tail", len(batches), len(batches[0]), len(batches[1]))
	}
}

// remote.ts: a call is sent with the output buffered before it, one call per exchange, and the reply is posted to the VM.
func TestAServedCallCarriesTheBufferedOutputAndItsResultIsPosted(t *testing.T) {
	payload := `[1]`
	s := serveWith(t, t.Context(), RemoteRequest{}, ServeOptions{}, func(events vmEvents) {
		events.message(outputOf("before"))
		events.message(vmMessage{kind: vmMessageCall, id: 9, isTool: true, name: "echo", args: "[1]", argsDefined: true})
	}, func(batch []RemoteMessage, reply func(*RemoteReply, error)) {
		if batch[len(batch)-1].Type == MessageCall {
			reply(&RemoteReply{HostToWorkerMessage{Type: MessageResult, ID: 9, Ok: true, Payload: &payload}}, nil)
			return
		}
		reply(nil, nil)
	})
	waitForPost(t, s.channel)
	s.events.message(doneVM("2"))
	s.wait(t)
	batches := s.batches()
	if len(batches) != 2 || len(batches[0]) != 2 || batches[0][0].Item.Text != "before" || batches[0][1].Type != MessageCall || batches[0][1].ID != 9 ||
		batches[0][1].Target != "tool" || batches[0][1].Name != "echo" || *batches[0][1].Args != "[1]" {
		t.Fatalf("batches = %+v, want [output, call] then [done]", batches)
	}
	posted, stopped := s.channel.state()
	if len(posted) != 1 || posted[0] != (callResult{id: 9, ok: true, payload: "[1]", defined: true}) || stopped != 1 {
		t.Fatalf("posted = %+v, stopped %d, want the result of call 9 and one stop", posted, stopped)
	}
}

func waitForPost(t *testing.T, c *serveChannel) {
	t.Helper()
	select {
	case <-c.arrived:
	case <-time.After(10 * time.Second):
		t.Fatal("the reply was never posted")
	}
}

// remote.ts: a stop reply ends the execution at once: the VM is stopped, the buffered output is dropped and no terminal
// message is sent.
func TestAStopReplyEndsTheServerWithoutATerminalMessage(t *testing.T) {
	s := serveWith(t, t.Context(), RemoteRequest{}, ServeOptions{}, func(events vmEvents) {
		events.message(outputOf("buffered"))
		events.message(vmMessage{kind: vmMessageCall, id: 1, isTool: true, name: "hang"})
	}, func(_ []RemoteMessage, reply func(*RemoteReply, error)) {
		reply(&RemoteReply{HostToWorkerMessage{Type: MessageStop}}, nil)
	})
	s.wait(t)
	// Anything the VM reports afterwards is ignored.
	s.events.message(doneVM("late"))
	if batches := s.batches(); len(batches) != 1 {
		t.Fatalf("%d exchanges, want only the call's", len(batches))
	}
	if _, stopped := s.channel.state(); stopped != 1 {
		t.Fatalf("stopped %d times, want once", stopped)
	}
}

// remote.ts: a rejected exchange (the RPC failed) ends the execution like a stop; a rejected terminal exchange is the
// same as a null reply.
func TestARejectedExchangeEndsTheServer(t *testing.T) {
	rpcFailed := errors.New("rpc failed")
	t.Run("call", func(t *testing.T) {
		s := serveWith(t, t.Context(), RemoteRequest{}, ServeOptions{}, func(events vmEvents) {
			events.message(vmMessage{kind: vmMessageCall, id: 1, isTool: true, name: "x"})
		}, func(_ []RemoteMessage, reply func(*RemoteReply, error)) { reply(nil, rpcFailed) })
		s.wait(t)
		if _, stopped := s.channel.state(); stopped != 1 || len(s.batches()) != 1 {
			t.Fatalf("stopped %d times after %d exchanges, want 1 and 1", stopped, len(s.batches()))
		}
	})
	t.Run("terminal", func(t *testing.T) {
		s := serveWith(t, t.Context(), RemoteRequest{}, ServeOptions{}, func(events vmEvents) {
			events.message(doneVM("1"))
		}, func(_ []RemoteMessage, reply func(*RemoteReply, error)) { reply(nil, rpcFailed) })
		s.wait(t)
		if _, stopped := s.channel.state(); stopped != 1 {
			t.Fatalf("stopped %d times, want once", stopped)
		}
	})
}

// remote.ts: the server returns only after the terminal message was delivered (its reply settled), then stops the VM.
func TestTheServerReturnsAfterTheTerminalReply(t *testing.T) {
	release := make(chan func(*RemoteReply, error), 1)
	s := serveWith(t, t.Context(), RemoteRequest{}, ServeOptions{}, func(events vmEvents) {
		events.message(doneVM("1"))
	}, func(_ []RemoteMessage, reply func(*RemoteReply, error)) { release <- reply })
	reply := <-release
	select {
	case <-s.returned:
		t.Fatal("returned before the terminal exchange was answered")
	default:
	}
	if _, stopped := s.channel.state(); stopped != 0 {
		t.Fatal("the VM was stopped before the terminal exchange was answered")
	}
	reply(nil, nil)
	s.wait(t)
	if _, stopped := s.channel.state(); stopped != 1 {
		t.Fatalf("stopped %d times, want once", stopped)
	}
}

// The client going away (its context ends) ends the server, also while a call waits for its reply.
func TestTheServerEndsWhenItsContextEnds(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	asked := make(chan struct{})
	s := serveWith(t, ctx, RemoteRequest{}, ServeOptions{}, func(events vmEvents) {
		events.message(vmMessage{kind: vmMessageCall, id: 1, isTool: true, name: "hang"})
	}, func(_ []RemoteMessage, _ func(*RemoteReply, error)) { close(asked) })
	<-asked
	cancel()
	s.wait(t)
	if _, stopped := s.channel.state(); stopped != 1 || len(s.batches()) != 1 {
		t.Fatalf("stopped %d times after %d exchanges, want 1 and 1", stopped, len(s.batches()))
	}

	done, cancelNow := context.WithCancel(t.Context())
	cancelNow()
	started := false
	serveRemote(done, RemoteRequest{}, nil, ServeOptions{}, func(int) vmLauncher {
		started = true
		return nil
	})
	if started {
		t.Fatal("started a VM for a client that is already gone")
	}
}

// remote.ts: a launcher that fails, and a failure the launcher reports, are sent as a failure message.
func TestLauncherFailuresAreReportedAsFailureMessages(t *testing.T) {
	t.Run("launch error", func(t *testing.T) {
		exchanges := make(chan []RemoteMessage, 1)
		serveRemote(t.Context(), RemoteRequest{}, func(_ context.Context, messages []RemoteMessage, reply func(*RemoteReply, error)) {
			exchanges <- messages
			reply(nil, nil)
		}, ServeOptions{}, func(int) vmLauncher {
			return func(context.Context, VmStartData, vmEvents) (vmChannel, error) {
				return nil, errors.New("Failed to load QuickJS: nope")
			}
		})
		batch := <-exchanges
		if len(batch) != 1 || batch[0].Type != MessageFailure || *batch[0].Failure != (Error{Kind: ErrorSandbox, Message: "Failed to load QuickJS: nope"}) {
			t.Fatalf("batch = %+v", batch)
		}
	})
	t.Run("failure event", func(t *testing.T) {
		timeout := &Error{Kind: ErrorTimeout, Message: "Execution exceeded its interrupt budget of 1000"}
		s := serveWith(t, t.Context(), RemoteRequest{}, ServeOptions{}, func(events vmEvents) {
			events.message(outputOf("partial"))
			events.failure(timeout)
		}, nil)
		s.wait(t)
		batches := s.batches()
		if len(batches) != 1 || len(batches[0]) != 2 || batches[0][0].Type != MessageOutput || batches[0][1].Type != MessageFailure || *batches[0][1].Failure != *timeout {
			t.Fatalf("batches = %+v, want [output, failure]", batches)
		}
	})
}

// remote.ts: the VM runs with the request's budget and memory, capped at the remote's limits; a request that names none gets the
// remote's. portable.test.ts "caps the interrupt budget and memory at the remote's limits".
func TestTheRequestIsCappedAtTheRemotesLimits(t *testing.T) {
	for _, tc := range []struct {
		name    string
		request RemoteRequest
		options ServeOptions
		budget  int
		memory  uint32
	}{
		{"remote defaults", RemoteRequest{}, ServeOptions{}, DefaultInterruptBudget, 0},
		{"request below the caps", RemoteRequest{InterruptBudget: 500, Data: VmStartData{MemoryLimitBytes: 4 << 20}}, ServeOptions{MaxInterruptBudget: 1_000, MaxMemoryLimitBytes: 8 << 20}, 500, 4 << 20},
		{"request above the caps", RemoteRequest{InterruptBudget: 1_000_000_000, Data: VmStartData{MemoryLimitBytes: 1 << 30}}, ServeOptions{MaxInterruptBudget: 1_000, MaxMemoryLimitBytes: 8 << 20}, 1_000, 8 << 20},
		{"request names none", RemoteRequest{}, ServeOptions{MaxInterruptBudget: 1_000, MaxMemoryLimitBytes: 8 << 20}, 1_000, 8 << 20},
		{"remote has no memory cap", RemoteRequest{Data: VmStartData{MemoryLimitBytes: 1 << 30}}, ServeOptions{}, DefaultInterruptBudget, 1 << 30},
		{"budget above the default", RemoteRequest{InterruptBudget: 2 * DefaultInterruptBudget}, ServeOptions{}, DefaultInterruptBudget, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := serveWith(t, t.Context(), tc.request, tc.options, func(events vmEvents) { events.message(doneVM("1")) }, nil)
			s.wait(t)
			if s.budget != tc.budget || s.data.MemoryLimitBytes != tc.memory {
				t.Fatalf("VM started with budget %d and memory %d, want %d and %d", s.budget, s.data.MemoryLimitBytes, tc.budget, tc.memory)
			}
		})
	}
}

// remote.ts: the request's start data reaches the VM unchanged.
func TestTheStartDataReachesTheVM(t *testing.T) {
	request := RemoteRequest{Data: VmStartData{Code: "return 1", Store: map[string]string{"k": "1"},
		Tools:   []VmStartTool{{Name: "a-b", JSName: "a_b", Description: "d"}},
		Globals: []VmStartGlobal{{Name: "g", Spread: true}}}}
	s := serveWith(t, t.Context(), request, ServeOptions{}, func(events vmEvents) { events.message(doneVM("1")) }, nil)
	s.wait(t)
	d := s.data
	if d.Code != "return 1" || d.Store["k"] != "1" || len(d.Tools) != 1 || d.Tools[0] != (VmStartTool{"a-b", "a_b", "d"}) || len(d.Globals) != 1 || d.Globals[0] != (VmStartGlobal{"g", true}) {
		t.Fatalf("data = %+v", d)
	}
}

// remote.ts: once the outcome is decided the server reports nothing more: a call after the terminal message is not sent.
func TestNothingIsSentAfterTheOutcomeIsDecided(t *testing.T) {
	s := serveWith(t, t.Context(), RemoteRequest{}, ServeOptions{}, func(events vmEvents) {
		events.message(doneVM("1"))
		events.message(vmMessage{kind: vmMessageCall, id: 1, isTool: true, name: "late"})
		events.message(outputOf("late"))
		events.failure(&Error{Kind: ErrorSandbox, Message: "late"})
	}, nil)
	s.wait(t)
	batches := s.batches()
	if len(batches) != 1 || len(batches[0]) != 1 || batches[0][0].Type != MessageDone {
		t.Fatalf("batches = %+v, want only [done]", batches)
	}
}

// A request that leaves out the tool and global lists (a JSON `null`) starts the VM with empty lists, which the prelude parses as arrays.
func TestARequestWithoutToolAndGlobalListsStartsTheVMWithEmptyLists(t *testing.T) {
	s := serveWith(t, t.Context(), RemoteRequest{Data: VmStartData{Code: "return 1"}}, ServeOptions{}, func(events vmEvents) { events.message(doneVM("1")) }, nil)
	s.wait(t)
	if s.data.Tools == nil || s.data.Globals == nil {
		t.Fatalf("tools = %#v, globals = %#v, want empty non-nil lists", s.data.Tools, s.data.Globals)
	}
}
