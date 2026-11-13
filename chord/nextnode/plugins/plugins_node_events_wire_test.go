package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord/internal/hostevents"
)

// Not upstream's: the Go side of the wire of the events that cross the plugins bridge (plugins_node_events.go): the
// sender of the `event` and `ack` messages and what it does with the Node host's. Each expectation is a link's
// provider's (Pi remote/provider.ts `sendEvents`, `#eventAck`) or consumer's (remote/consumer.ts `#events`), cited where
// it is checked.

// wirePort is a hostevents.Port that records the acknowledgements it gets.
type wirePort struct {
	mu   sync.Mutex
	acks []int
}

func (p *wirePort) Tap(any, int) ([]string, error) { return nil, errors.New("a wirePort taps nothing") }
func (p *wirePort) Untap(int)                      {}
func (p *wirePort) Close()                         {}

func (p *wirePort) Ack(messages int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.acks = append(p.acks, messages)
}

func (p *wirePort) acked() []int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]int{}, p.acks...)
}

func (p *wirePort) total() int {
	sum := 0
	for _, messages := range p.acked() {
		sum += messages
	}
	return sum
}

// wireChannel is a control channel that records what is written to it as JSON text, in order. While gate is not nil a
// write waits for it; entered gets one value when the first write arrived.
type wireChannel struct {
	gate    chan struct{}
	entered chan struct{}
	mu      sync.Mutex
	written []string
}

func gatedChannel() *wireChannel {
	return &wireChannel{gate: make(chan struct{}), entered: make(chan struct{}, 1)}
}

func (c *wireChannel) control(message any) error {
	if c.gate != nil {
		select {
		case c.entered <- struct{}{}:
		default:
		}
		<-c.gate
	}
	text, err := json.Marshal(message)
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.written = append(c.written, string(text))
	return nil
}

func (c *wireChannel) all() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string{}, c.written...)
}

// returnsAtOnce fails the test when work has not returned within ten seconds, after opening the channel's gate so that
// nothing stays blocked.
func returnsAtOnce(t *testing.T, channel *wireChannel, what string, work func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		work()
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		close(channel.gate)
		<-done
		t.Fatal(what)
	}
}

// A flush's message is one control message `{ op: "event", events: [{ hold, member, at, value }] }`, the value as the
// link's JSON part text, and an acknowledgement is `{ op: "ack", events: n }` (the link's `event` and `ack` messages:
// Pi link/message.ts). A Go host's flush hands its message over inside its section, so handing over only queues: one
// goroutine writes the queue in the order it was queued, and `flush` returns once what was queued before it is written.
func TestTheBridgesEventSenderWritesWhatWasQueuedInOrderOnAGoroutineOfItsOwn(t *testing.T) {
	channel := gatedChannel()
	outbox := &eventOutbox{control: channel.control}
	port := &wirePort{}
	returnsAtOnce(t, channel, "queueing a message waited for the channel: the sender wrote on its caller's goroutine", func() {
		outbox.event(port, []hostevents.Entry{{Hold: 3, Member: 1, At: 1700000000001, Value: `{"y":1}`}})
		outbox.ack(2)
		outbox.event(port, []hostevents.Entry{
			{Hold: 3, Member: 0, At: 1700000000002, Value: `"a\"b"`},
			{Hold: 4, Member: 0, At: 1700000000002, Value: `null`},
		})
	})
	<-channel.entered
	ended, cancel := context.WithCancelCause(background)
	stop := errors.New("the caller gave up")
	cancel(stop)
	if err := outbox.flush(ended); !errors.Is(err, stop) {
		t.Fatalf("flush under an ended context = %v while a message queued before it waits to be written, want the context's cause", err)
	}
	close(channel.gate)
	if err := outbox.flush(background); err != nil {
		t.Fatal(err)
	}
	want := []string{
		`{"op":"event","events":[{"hold":3,"member":1,"at":1700000000001,"value":"{\"y\":1}"}]}`,
		`{"op":"ack","events":2}`,
		`{"op":"event","events":[{"hold":3,"member":0,"at":1700000000002,"value":"\"a\\\"b\""},{"hold":4,"member":0,"at":1700000000002,"value":"null"}]}`,
	}
	if got := channel.all(); !reflect.DeepEqual(got, want) {
		t.Fatalf("written after flush = %q, want %q", got, want)
	}
	if err := outbox.flush(background); err != nil {
		t.Fatalf("flush with nothing queued = %v", err)
	}
}

// A wait ends when what was queued before its mark is written, whatever was queued after: the sender is one queue for
// every Go host's events and every acknowledgement, so a wait for "nothing queued" would hold a call's answer for as long
// as any other object emits. One whose mark is written already returns at once, under an ended context too, and what was
// queued after a mark is waited for by a later mark.
func TestAWaitOnTheBridgesEventSenderEndsWhenWhatWasQueuedBeforeItsMarkIsWrittenWhateverIsQueuedAfter(t *testing.T) {
	first, second := make(chan struct{}), make(chan struct{})
	entered := make(chan int, 2)
	writes := 0
	var outbox *eventOutbox
	outbox = &eventOutbox{control: func(any) error {
		writes++
		entered <- writes
		if writes == 1 {
			<-first
			// Queued while the first message is written: the queue is not empty when that write returns.
			outbox.ack(2)
			return nil
		}
		<-second
		return nil
	}}
	outbox.ack(1)
	mark := outbox.mark()
	if n := <-entered; n != 1 {
		t.Fatalf("write %d entered first", n)
	}
	waited := make(chan error, 1)
	go func() { waited <- outbox.wait(mark, background) }()
	eventually(t, 15*time.Second, "the wait to be parked", func() bool {
		outbox.mu.Lock()
		defer outbox.mu.Unlock()
		return outbox.progress != nil
	})
	close(first)
	// The second message is being written, and stays so: the queue never empties while the checks below run.
	<-entered
	select {
	case err := <-waited:
		if err != nil {
			t.Fatalf("wait = %v", err)
		}
	case <-time.After(10 * time.Second):
		close(second)
		t.Fatal("a wait whose mark was written still waits: it waits for what was queued after it")
	}
	ended, cancel := context.WithCancelCause(background)
	stop := errors.New("the caller gave up")
	cancel(stop)
	if err := outbox.wait(mark, ended); err != nil {
		close(second)
		t.Fatalf("wait for a mark that is written = %v under an ended context, want nil at once", err)
	}
	if err := outbox.wait(outbox.mark(), ended); !errors.Is(err, stop) {
		close(second)
		t.Fatalf("wait for the message queued after the first mark = %v while it is being written, want the context's cause", err)
	}
	// That wait left the channel of the next write behind: the write closes it once (a second close panics), and a wait
	// that finds the sender stopped returns.
	close(second)
	if err := outbox.flush(background); err != nil {
		t.Fatal(err)
	}
	outbox.stop()
	if err := outbox.wait(outbox.mark()+1, background); err != nil {
		t.Fatalf("wait on a stopped sender = %v, want nil: nothing more is written", err)
	}
}

// What is queued when the sender stops is dropped, never written: a wait for it ends with the stop, once the write that
// was under way returned, and does not wait for a message that will not be written.
func TestAWaitForAMessageDroppedWhenTheBridgesEventSenderStoppedReturns(t *testing.T) {
	channel := gatedChannel()
	outbox := &eventOutbox{control: channel.control}
	outbox.ack(1)
	outbox.ack(2)
	<-channel.entered
	waited := make(chan error, 1)
	go func() { waited <- outbox.wait(outbox.mark(), background) }()
	eventually(t, 15*time.Second, "the wait to be parked", func() bool {
		outbox.mu.Lock()
		defer outbox.mu.Unlock()
		return outbox.progress != nil
	})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		outbox.stop()
	}()
	eventually(t, 15*time.Second, "the sender to be told to stop", func() bool {
		outbox.mu.Lock()
		defer outbox.mu.Unlock()
		return outbox.stopped
	})
	close(channel.gate)
	select {
	case err := <-waited:
		if err != nil {
			t.Fatalf("wait = %v, want nil", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("a wait for a message that was dropped when the sender stopped still waits")
	}
	<-stopped
	if got, want := channel.all(), []string{`{"op":"ack","events":1}`}; !reflect.DeepEqual(got, want) {
		t.Fatalf("written = %q, want %q: what was queued behind the write under way is dropped", got, want)
	}
}

// An `ack` of the Node host counts the `event` messages it consumed; it names none. Each frees a slot of the port whose
// message was written first: several Go hosts can be bridged into one Node host, each with its own port and window
// (Pi remote/provider.ts:679-692, `#eventAck`: the first tickets). One that counts more than were written finds no
// ticket for the rest and is ignored there (:682-683: `const ticket = this.#tickets.shift(); if (ticket === undefined)
// break;`): no error, and no port is acknowledged what it did not send.
func TestAnAckOfTheNodeHostFreesTheSlotsOfThePortsWhoseMessagesWereWrittenFirst(t *testing.T) {
	channel := &wireChannel{}
	h := &NodeHost{}
	h.events.control = channel.control
	first, second := &wirePort{}, &wirePort{}
	h.control([]byte(`{"event":"ack","events":5}`))
	for _, port := range []*wirePort{first, second, first, first} {
		h.events.event(port, []hostevents.Entry{{Value: "1"}})
	}
	if err := h.events.flush(background); err != nil {
		t.Fatal(err)
	}
	if got := [][]int{first.acked(), second.acked()}; !reflect.DeepEqual(got, [][]int{{}, {}}) {
		t.Fatalf("acknowledged = %v by an ack for five messages when none was written, want nothing", got)
	}
	h.control([]byte(`{"event":"ack","events":2}`))
	if got := [][]int{first.acked(), second.acked()}; !reflect.DeepEqual(got, [][]int{{1}, {1}}) {
		t.Fatalf("acknowledged = %v after an ack of 2, want one message of each port (the two written first)", got)
	}
	h.control([]byte(`{"event":"ack","events":5}`))
	if got := [][]int{first.acked(), second.acked()}; !reflect.DeepEqual(got, [][]int{{1, 2}, {1}}) {
		t.Fatalf("acknowledged = %v after an ack of 5 with two messages written and unacknowledged, want the two of the first port and no more", got)
	}
	h.control([]byte(`{"event":"ack","events":1}`))
	h.control([]byte(`{"event":"ack","events":-3}`))
	if got := [][]int{first.acked(), second.acked()}; !reflect.DeepEqual(got, [][]int{{1, 2}, {1}}) {
		t.Fatalf("acknowledged = %v after acks with nothing unacknowledged, want them ignored", got)
	}
}

// An `event` message of the Node host is acknowledged once, by one `{ op: "ack", events: 1 }` (Go has no inbound turns:
// Pi's consumer sends one ack per turn that counts the turn's messages, remote/consumer.ts:312-320), and never on the
// goroutine that reads the channel: the reader returns while the channel takes nothing.
func TestAnEventMessageOfTheNodeHostIsAcknowledgedThroughTheSenderNeverOnTheChannelsReader(t *testing.T) {
	channel := gatedChannel()
	h := &NodeHost{}
	h.events.control = channel.control
	message := []byte(`{"event":"event","events":[{"hold":1,"member":0,"at":1700000000001,"value":"{\"y\":1}"}]}`)
	returnsAtOnce(t, channel, "the channel's reader waited for the write of an acknowledgement", func() {
		h.control(message)
		h.control(message)
	})
	close(channel.gate)
	if err := h.events.flush(background); err != nil {
		t.Fatal(err)
	}
	if got, want := channel.all(), []string{`{"op":"ack","events":1}`, `{"op":"ack","events":1}`}; !reflect.DeepEqual(got, want) {
		t.Fatalf("written = %q, want %q", got, want)
	}
}

// senderStacks is the stacks of the goroutines that are writing the bridge's events.
func senderStacks() string {
	buf := make([]byte, 1<<22)
	buf = buf[:runtime.Stack(buf, true)]
	if !strings.Contains(string(buf), "(*eventOutbox).drain") {
		return ""
	}
	return string(buf)
}

// The sender is owned by its NodeHost: when the process ended it is joined before Exited closes, whatever was queued,
// so a NodeHost that ended has no goroutine of it left, and a message queued after that is dropped without one.
func TestTheBridgesEventSenderHasReturnedWhenExitedClosesAndWritesNothingAfter(t *testing.T) {
	for _, mode := range []string{"close", "kill"} {
		t.Run(mode, func(t *testing.T) {
			node := evNode(t)
			channel := gatedChannel()
			write := node.events.control
			node.events.control = func(message any) error {
				if err := channel.control(message); err != nil {
					return err
				}
				return write(message)
			}
			port := &wirePort{}
			for hold := range 100 {
				node.events.event(port, []hostevents.Entry{{Hold: 1000 + hold, At: 1700000000001, Value: `{"y":1}`}})
			}
			// The sender is writing its first message, 99 wait behind it.
			<-channel.entered
			ended := make(chan error, 1)
			if mode == "kill" {
				node.kill()
				go func() { <-node.Exited(); ended <- nil }()
			} else {
				go func() { ended <- node.Close(background) }()
			}
			<-node.reaped
			select {
			case <-node.Exited():
				close(channel.gate)
				t.Fatal("Exited closed while the sender of the bridge's events was writing")
			case <-time.After(300 * time.Millisecond):
			}
			close(channel.gate)
			<-ended
			<-node.Exited()
			if stacks := senderStacks(); stacks != "" {
				t.Fatalf("a goroutine of the sender outlived the process:\n%s", stacks)
			}
			written := len(channel.all())
			node.events.event(port, []hostevents.Entry{{Hold: 1, At: 1700000000001, Value: `{"y":2}`}})
			node.events.ack(1)
			if err := node.events.flush(background); err != nil {
				t.Fatal(err)
			}
			if stacks := senderStacks(); stacks != "" || len(channel.all()) != written {
				t.Fatalf("%d messages were written after the process ended, want none and no goroutine:\n%s", len(channel.all())-written, stacks)
			}
		})
	}
}

// Over the channel of a process: the `event` messages the Go side writes are consumed by the Node host whatever their
// holds (a hold it does not know drops its entries: Pi remote/consumer.ts:313, `this.#entries.get(hold)?.event(...)`)
// and acknowledged by count, one `ack` per inbound turn, and each acknowledged message frees a slot of its port: forty
// messages are acknowledged as forty, never as more.
func TestEventMessagesWrittenToTheNodeHostAreAcknowledgedByCountWhateverTheirHolds(t *testing.T) {
	node := evNode(t)
	first, second := &wirePort{}, &wirePort{}
	for hold := range 40 {
		port := first
		if hold%4 == 3 {
			port = second
		}
		node.events.event(port, []hostevents.Entry{{Hold: 1000 + hold, At: 1700000000001, Value: `{"y":1}`}})
	}
	eventually(t, 15*time.Second, "the Node host to acknowledge the forty messages", func() bool {
		return first.total() == 30 && second.total() == 10
	})
	// A round trip after the last ack: an ack that followed would have arrived before its answer.
	if _, err := node.Describe(evTicker, background); err != nil {
		t.Fatal(err)
	}
	if first.total() != 30 || second.total() != 10 {
		t.Fatalf("acknowledged %d and %d messages, want 30 and 10", first.total(), second.total())
	}
}
