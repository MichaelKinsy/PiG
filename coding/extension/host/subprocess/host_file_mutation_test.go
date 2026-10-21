package subprocess

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// The file mutation queue an extension process asks for is the host's own (tools.WithFileMutationQueue, the queue the built-in edit and write tools use): these
// tests drive the host's handlers and read the queue through that function.

type enterOutcome struct {
	ticket string
	err    error
}

// enter runs the host's handler for fileMutation.enter on its own goroutine. initiated is closed when the handler reports the call initiated (the call lane advances).
func enter(t *testing.T, h *Host, conn *Conn, ctx context.Context, path string) (outcome <-chan enterOutcome, initiated <-chan struct{}) {
	t.Helper()
	marked := make(chan struct{})
	done := make(chan enterOutcome, 1)
	args, err := json.Marshal(FileMutationEnterArgs{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	callCtx := extension.WithCallInitiation(ctx, func() {
		select {
		case <-marked:
		default:
			close(marked)
		}
	})
	go func() {
		result, err := h.handleFileMutationEnter(callCtx, conn, &CallPayload{Method: CallFileMutationEnter, Args: args})
		if err != nil {
			done <- enterOutcome{err: err}
			return
		}
		var reply FileMutationEnterResult
		if err := json.Unmarshal(result.Result, &reply); err != nil {
			done <- enterOutcome{err: err}
			return
		}
		done <- enterOutcome{ticket: reply.Ticket}
	}()
	return done, marked
}

func exit(t *testing.T, h *Host, conn *Conn, ticket string) error {
	t.Helper()
	args, err := json.Marshal(FileMutationExitArgs{Ticket: ticket})
	if err != nil {
		t.Fatal(err)
	}
	_, err = h.handleFileMutationExit(conn, &CallPayload{Method: CallFileMutationExit, Args: args})
	return err
}

func awaitEnter(t *testing.T, outcome <-chan enterOutcome) enterOutcome {
	t.Helper()
	select {
	case got := <-outcome:
		return got
	case <-time.After(10 * time.Second):
		t.Fatal("fileMutation.enter never answered")
		return enterOutcome{}
	}
}

// queueFree reports whether a host mutation of path runs at once.
func queueFree(path string) bool {
	ran := make(chan struct{})
	go func() {
		_, _ = tools.WithFileMutationQueue(path, func() (struct{}, error) {
			close(ran)
			return struct{}{}, nil
		})
	}()
	select {
	case <-ran:
		return true
	case <-time.After(100 * time.Millisecond):
		return false
	}
}

// hostHold holds path in the host's queue (as a built-in edit or write does) until the returned release runs.
func hostHold(t *testing.T, path string) (release func()) {
	t.Helper()
	inside, done, gate := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		_, _ = tools.WithFileMutationQueue(path, func() (struct{}, error) {
			close(inside)
			<-gate
			return struct{}{}, nil
		})
	}()
	<-inside
	var once bool
	return func() {
		if !once {
			once = true
			close(gate)
			<-done
		}
	}
}

// A call with the file free is admitted at once and holds the host's queue until its exit.
func TestFileMutationEnterHoldsTheHostQueueUntilExit(t *testing.T) {
	h := NewHost(t.TempDir())
	defer h.Shutdown("test done")
	conn := &Conn{}
	file := filepath.Join(t.TempDir(), "a.txt")
	outcome, initiated := enter(t, h, conn, t.Context(), file)
	got := awaitEnter(t, outcome)
	if got.err != nil || got.ticket == "" {
		t.Fatalf("enter = %+v", got)
	}
	select {
	case <-initiated:
	default:
		t.Fatal("the call did not report initiation")
	}
	if queueFree(file) {
		t.Fatal("a host mutation ran while an extension held the file")
	}
	if err := exit(t, h, conn, got.ticket); err != nil {
		t.Fatal(err)
	}
	if !queueFree(file) {
		t.Fatal("the file stayed held after the extension's exit")
	}
	if err := exit(t, h, conn, got.ticket); err == nil {
		t.Fatal("a ticket was released twice")
	}
}

// The call takes its place when the host reads it and reports the call initiated before it waits: a call held behind a long host mutation does not hold up the
// calls that follow it in its lane. The extension is admitted when the host mutation ends, and a later host mutation waits behind it (FIFO with the built-in tools).
func TestFileMutationEnterRegistersInCallOrderAndInitiatesBeforeItWaits(t *testing.T) {
	h := NewHost(t.TempDir())
	defer h.Shutdown("test done")
	conn := &Conn{}
	file := filepath.Join(t.TempDir(), "a.txt")
	release := hostHold(t, file)
	defer release()

	outcome, initiated := enter(t, h, conn, t.Context(), file)
	select {
	case <-initiated:
	case <-time.After(10 * time.Second):
		t.Fatal("the call reported no initiation while it waited for the host mutation")
	}
	select {
	case got := <-outcome:
		t.Fatalf("the extension was admitted while a host mutation held the file: %+v", got)
	case <-time.After(100 * time.Millisecond):
	}
	// A host mutation that registers after the call waits behind the extension.
	later := make(chan struct{})
	go func() {
		_, _ = tools.WithFileMutationQueue(file, func() (struct{}, error) {
			close(later)
			return struct{}{}, nil
		})
	}()
	time.Sleep(50 * time.Millisecond)
	release()
	got := awaitEnter(t, outcome)
	if got.err != nil {
		t.Fatal(got.err)
	}
	select {
	case <-later:
		t.Fatal("a host mutation registered after the extension's call ran before the extension exited")
	case <-time.After(100 * time.Millisecond):
	}
	if err := exit(t, h, conn, got.ticket); err != nil {
		t.Fatal(err)
	}
	select {
	case <-later:
	case <-time.After(10 * time.Second):
		t.Fatal("the host mutation never ran after the extension exited")
	}
}

// Closing the connection (the extension exited, was replaced, or the host shut down) releases every turn it held, so a crashed extension cannot keep a file blocked.
func TestFileMutationTurnsAreReleasedWhenTheConnectionCloses(t *testing.T) {
	h := NewHost(t.TempDir())
	defer h.Shutdown("test done")
	conn := &Conn{}
	dir := t.TempDir()
	first, second := filepath.Join(dir, "a.txt"), filepath.Join(dir, "b.txt")
	var tickets []string
	for _, file := range []string{first, second} {
		outcome, _ := enter(t, h, conn, t.Context(), file)
		got := awaitEnter(t, outcome)
		if got.err != nil {
			t.Fatal(got.err)
		}
		tickets = append(tickets, got.ticket)
	}
	if queueFree(first) || queueFree(second) {
		t.Fatal("a file was free while its extension held it")
	}
	conn.turns().leave(conn)
	if !queueFree(first) || !queueFree(second) {
		t.Fatal("a turn survived its connection")
	}
	// A call that was waiting when the connection closed takes no turn: it fails and leaves the queue as it found it.
	outcome, _ := enter(t, h, conn, t.Context(), first)
	if got := awaitEnter(t, outcome); got.err == nil {
		t.Fatalf("a closed connection was admitted: %+v", got)
	}
	if !queueFree(first) {
		t.Fatal("a closed connection's call kept the file")
	}
	if err := exit(t, h, conn, tickets[0]); err == nil {
		t.Fatal("a ticket the closed connection held was still redeemable")
	}
}

// A ticket belongs to the connection that was admitted: another extension cannot release it.
func TestFileMutationExitOfAnotherConnectionsTicketFails(t *testing.T) {
	h := NewHost(t.TempDir())
	defer h.Shutdown("test done")
	owner, other := &Conn{}, &Conn{}
	file := filepath.Join(t.TempDir(), "a.txt")
	outcome, _ := enter(t, h, owner, t.Context(), file)
	got := awaitEnter(t, outcome)
	if got.err != nil {
		t.Fatal(got.err)
	}
	if err := exit(t, h, other, got.ticket); err == nil {
		t.Fatal("a connection released another connection's turn")
	}
	if queueFree(file) {
		t.Fatal("the file was released by the wrong connection")
	}
	if err := exit(t, h, owner, got.ticket); err != nil {
		t.Fatal(err)
	}
}

// A waiting call whose context ends gives up without running ahead of the holder: its place passes on once the holder is done, and a later waiter runs after both.
func TestFileMutationEnterCancelledWhileWaitingTakesNoTurn(t *testing.T) {
	h := NewHost(t.TempDir())
	defer h.Shutdown("test done")
	conn := &Conn{}
	file := filepath.Join(t.TempDir(), "a.txt")
	release := hostHold(t, file)
	defer release()

	ctx, cancel := context.WithCancel(t.Context())
	outcome, initiated := enter(t, h, conn, ctx, file)
	<-initiated
	later := make(chan struct{})
	go func() {
		_, _ = tools.WithFileMutationQueue(file, func() (struct{}, error) {
			close(later)
			return struct{}{}, nil
		})
	}()
	time.Sleep(50 * time.Millisecond)
	cancel()
	if got := awaitEnter(t, outcome); got.err == nil {
		t.Fatalf("a cancelled call was admitted: %+v", got)
	}
	select {
	case <-later:
		t.Fatal("a later waiter ran while the holder still held the file")
	case <-time.After(100 * time.Millisecond):
	}
	release()
	select {
	case <-later:
	case <-time.After(10 * time.Second):
		t.Fatal("the cancelled call's place was never passed on")
	}
	if len(conn.fileMutations.held) != 0 {
		t.Fatalf("a cancelled call left a ticket: %v", conn.fileMutations.held)
	}
}

// A path the host cannot resolve fails the call, as it fails the built-in tools: the task does not run, and nothing is held.
func TestFileMutationEnterFailsForAnUnresolvablePath(t *testing.T) {
	h := NewHost(t.TempDir())
	defer h.Shutdown("test done")
	conn := &Conn{}
	dir := t.TempDir()
	loopA, loopB := filepath.Join(dir, "loop-a"), filepath.Join(dir, "loop-b")
	testenv.Symlink(t, loopB, loopA)
	testenv.Symlink(t, loopA, loopB)
	outcome, initiated := enter(t, h, conn, t.Context(), loopA)
	got := awaitEnter(t, outcome)
	if got.err == nil {
		t.Fatalf("an unresolvable path was admitted: %+v", got)
	}
	select {
	case <-initiated:
	default:
		t.Fatal("a failed call must still report initiation, or its lane stalls")
	}
	// A path that does not exist yet is keyed by itself and is admitted.
	missing := filepath.Join(dir, "not-yet.txt")
	outcome, _ = enter(t, h, conn, t.Context(), missing)
	ok := awaitEnter(t, outcome)
	if ok.err != nil {
		t.Fatal(ok.err)
	}
	if err := exit(t, h, conn, ok.ticket); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(missing); err == nil {
		t.Fatal("the queue created the file")
	}
}

// fileMutation.enter waits for the file and starts in lane order, and a factory may use the queue while it loads.
func TestFileMutationCallsAreRoutedLikeThePureHelperCalls(t *testing.T) {
	if !startsAsync(CallFileMutationEnter) {
		t.Error("fileMutation.enter does not start asynchronously: it would hold its call lane until the file is free")
	}
	if startsAsync(CallFileMutationExit) {
		t.Error("fileMutation.exit starts asynchronously: it must release in lane order")
	}
	for _, method := range []string{CallFileMutationEnter, CallFileMutationExit} {
		if !loadingBusCall(method) {
			t.Errorf("%s is not served while the factory loads", method)
		}
	}
}

// The connections of one packed process share their tickets: a ticket one of them was admitted to survives that connection closing while another still runs, and
// any of the process's connections can exit it. The process's last connection to close releases what is still held.
func TestFileMutationTurnsOfOneProcessOutliveAConnectionAndEndWithTheLast(t *testing.T) {
	h := NewHost(t.TempDir())
	defer h.Shutdown("test done")
	var process fileMutationTurns
	first, second := &Conn{}, &Conn{}
	first.shareFileMutations(&process)
	second.shareFileMutations(&process)
	dir := t.TempDir()
	kept, handedOver := filepath.Join(dir, "kept.txt"), filepath.Join(dir, "handed-over.txt")
	enterOn := func(conn *Conn, file string) string {
		t.Helper()
		outcome, _ := enter(t, h, conn, t.Context(), file)
		got := awaitEnter(t, outcome)
		if got.err != nil {
			t.Fatal(got.err)
		}
		return got.ticket
	}
	keptTicket := enterOn(first, kept)
	handedOverTicket := enterOn(first, handedOver)

	first.turns().leave(first)
	if queueFree(kept) || queueFree(handedOver) {
		t.Fatal("a turn was released when one connection of its process closed while another still ran")
	}
	// A call that was in flight on the closed connection takes no turn, though the process still runs.
	outcome, _ := enter(t, h, first, t.Context(), filepath.Join(dir, "late.txt"))
	if got := awaitEnter(t, outcome); got.err == nil {
		t.Fatalf("a closed connection was admitted: %+v", got)
	}
	// The process's other connection gives the turn back.
	if err := exit(t, h, second, handedOverTicket); err != nil {
		t.Fatal(err)
	}
	if !queueFree(handedOver) {
		t.Fatal("the file stayed held after another connection of the process exited its turn")
	}
	if queueFree(kept) {
		t.Fatal("the other file was released by the exit of the first")
	}
	second.turns().leave(second)
	if !queueFree(kept) {
		t.Fatal("a turn survived its process's last connection")
	}
	if err := exit(t, h, second, keptTicket); err == nil {
		t.Fatal("a ticket released with the process's last connection was still redeemable")
	}
}
