package session_test

// pi: packages/durable/src/session/transaction.ts

import (
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/session"
	"github.com/MichaelKinsy/PiG/durable/session/sessiontest"
	"github.com/MichaelKinsy/PiG/internal/entered"
	"github.com/MichaelKinsy/PiG/internal/goroutineid"
	"github.com/MichaelKinsy/PiG/schema"
)

// Pi 473515f9c, packages/durable/src/session/transaction.ts signal(): a delivery awaits the read of the task and then decides
// and stages without yielding. The calls of a Promise.all share one read of the task, and the continuations of one awaited
// read run in the order the calls registered, so deliveries started together decide in the order they started. In Go the
// goroutines of those calls are readied together when the read returns and the runtime runs them in no fixed order: the
// transaction orders them by the ticket each took when it started.

var turnsToken = durable.DefineSignal(durable.SignalDefinition[turnsAnswer]{
	Name:    "test.turns",
	Version: 1,
	Schema:  schema.Obj(schema.Properties{schema.Prop("text", schema.Str())}),
})

type turnsAnswer struct {
	Text string `json:"text"`
}

// signalCall is one delivery of a commit under test. It runs on its own goroutine and parks in the transaction's
// afterSignalRead hook once it read the task and took its place in line, until the test lets it go on: the test decides
// which goroutine the runtime readies first.
type signalCall struct {
	key, text string
	// giveUp makes the call panic where it is let go on, as a delivery that fails before it waited for its turn.
	giveUp bool

	parked, release, done chan struct{}
	goID                  uint64

	result   durable.SignalResult
	err      error
	panicked any
}

func newSignalCall(key, text string) *signalCall {
	return &signalCall{key: key, text: text, parked: make(chan struct{}), release: make(chan struct{}), done: make(chan struct{})}
}

// holdSignals parks the deliveries of tx named in calls once they read the task.
func holdSignals(tx *session.Transaction, calls ...*signalCall) {
	byText := make(map[string]*signalCall, len(calls))
	for _, call := range calls {
		byText[call.text] = call
	}
	session.SetAfterSignalRead(tx, func(_ durable.TaskId, _ string, value durable.JsonValue) {
		object, _ := value.(*delta.JsonObject)
		text, _ := object.Value("text").(string)
		call := byText[text]
		close(call.parked)
		<-call.release
		if call.giveUp {
			panic("the delivery gave up")
		}
	})
}

// start begins the delivery and returns once it holds its ticket and read the task, so deliveries start in call order.
func (call *signalCall) start(tx *session.Transaction, taskId durable.TaskId) {
	ids := make(chan uint64, 1)
	go func() {
		ids <- goroutineid.Current()
		defer close(call.done)
		defer func() { call.panicked = recover() }()
		call.result, call.err = tx.SignalErased(taskId, turnsToken, call.key, delta.JsonObjectOf("text", call.text))
	}()
	call.goID = <-ids
	<-call.parked
}

// let lets the delivery go on and returns once it ran as far as it goes: it returned, or it waits for its turn.
func (call *signalCall) let() {
	close(call.release)
	entered.Await(call.goID, call.done)
}

// returned reports whether the delivery returned, once it ran as far as it goes.
func (call *signalCall) returned() bool {
	entered.Await(call.goID, call.done)
	select {
	case <-call.done:
		return true
	default:
		return false
	}
}

// storedSignalText is the text stored under key in the task's signal memo, or "" when the key holds none.
func storedSignalText(t *testing.T, harness sessiontest.Harness, taskId durable.TaskId, key string) string {
	t.Helper()
	text := ""
	commitWith(t, harness.Session, func(tx *session.Transaction) error {
		memo, ok := readTask(t, tx, taskId).Memos["pi.signal.test.turns/"+key].(*delta.JsonObject)
		if !ok {
			return nil
		}
		stored, _ := memo.Get("value")
		answer, _ := stored.(*delta.JsonObject)
		text, _ = answer.Value("text").(string)
		return nil
	})
	return text
}

func TestSignalDeliveriesStartedTogetherDecideInStartOrder(t *testing.T) {
	t.Run("whichever delivery the runtime lets run first", func(t *testing.T) {
		harness := open()
		taskId := createWorkTask(t, harness, createConversation(t, harness), false)
		first, second, third := newSignalCall("a", "first"), newSignalCall("a", "second"), newSignalCall("b", "bee")
		commitWith(t, harness.Session, func(tx *session.Transaction) error {
			holdSignals(tx, first, second, third)
			first.start(tx, taskId)
			second.start(tx, taskId)
			third.start(tx, taskId)
			// The runtime readies them last first: each runs on to its decision or to the wait for its turn.
			third.let()
			second.let()
			first.let()
			<-first.done
			<-second.done
			<-third.done
			return nil
		})
		for _, call := range []*signalCall{first, second, third} {
			if call.err != nil || call.panicked != nil {
				t.Fatalf("delivery %q: %v, panic %v", call.text, call.err, call.panicked)
			}
		}
		got := []durable.SignalResult{first.result, second.result, third.result}
		if want := []durable.SignalResult{durable.SignalDelivered, durable.SignalDuplicate, durable.SignalDelivered}; !equal(got, want) {
			t.Fatalf("results %v, want %v", got, want)
		}
		if got := storedSignalText(t, harness, taskId, "a"); got != "first" {
			t.Fatalf("key a holds %q, want the first delivery's value", got)
		}
		if got := storedSignalText(t, harness, taskId, "b"); got != "bee" {
			t.Fatalf("key b holds %q", got)
		}
	})

	t.Run("a delivery that fails before its turn passes the turn on, in order", func(t *testing.T) {
		harness := open()
		taskId := createWorkTask(t, harness, createConversation(t, harness), false)
		failing, second, third := newSignalCall("a", "lost"), newSignalCall("b", "second"), newSignalCall("b", "third")
		failing.giveUp = true
		commitWith(t, harness.Session, func(tx *session.Transaction) error {
			holdSignals(tx, failing, second, third)
			failing.start(tx, taskId)
			second.start(tx, taskId)
			third.start(tx, taskId)
			third.let()
			second.let()
			failing.let()
			<-failing.done
			<-second.done
			<-third.done
			return nil
		})
		if failing.panicked == nil {
			t.Fatal("the failing delivery did not panic")
		}
		for _, call := range []*signalCall{second, third} {
			if call.err != nil || call.panicked != nil {
				t.Fatalf("delivery %q: %v, panic %v", call.text, call.err, call.panicked)
			}
		}
		got := []durable.SignalResult{second.result, third.result}
		if want := []durable.SignalResult{durable.SignalDelivered, durable.SignalDuplicate}; !equal(got, want) {
			t.Fatalf("results %v, want %v", got, want)
		}
		if got := storedSignalText(t, harness, taskId, "a"); got != "" {
			t.Fatalf("key a holds %q, want none: its delivery failed", got)
		}
		if got := storedSignalText(t, harness, taskId, "b"); got != "second" {
			t.Fatalf("key b holds %q, want the second delivery's value", got)
		}
	})

	t.Run("a delivery to another task does not wait for one to this task", func(t *testing.T) {
		harness := open()
		conversationId := createConversation(t, harness)
		held := createWorkTask(t, harness, conversationId, false)
		other := createWorkTask(t, harness, conversationId, false)
		behind, ahead := newSignalCall("a", "held"), newSignalCall("a", "other")
		commitWith(t, harness.Session, func(tx *session.Transaction) error {
			holdSignals(tx, behind, ahead)
			behind.start(tx, held)
			ahead.start(tx, other)
			ahead.let()
			if !ahead.returned() {
				t.Error("a delivery to another task waited for the one held")
			}
			behind.let()
			<-behind.done
			<-ahead.done
			return nil
		})
		if behind.result != durable.SignalDelivered || ahead.result != durable.SignalDelivered {
			t.Fatalf("results %v, %v, want both delivered", behind.result, ahead.result)
		}
	})
}
