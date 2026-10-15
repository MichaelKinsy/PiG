package ai

import (
	"io"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/goroutineid"
)

// recordedRead is a read, owned by the calling goroutine, whose network-wait announcements are counted instead of suspending a read coroutine.
func recordedRead(announced *int) *bodyReadOperation {
	return &bodyReadOperation{owner: goroutineid.Current(), yield: func(signal bodyReadSignal) bool {
		if signal.pending {
			*announced++
		}
		return true
	}}
}

func TestHTTPBodyReadOwnershipTransferKeepsNewRequest(t *testing.T) {
	owner := &bodyReadOwner{}
	var oldAnnounced, newAnnounced int
	oldRead, newRead := recordedRead(&oldAnnounced), recordedRead(&newAnnounced)
	owner.begin(oldRead)
	// net/http can publish the next request's connection before the prior body worker finishes returning from EOF.
	owner.begin(newRead)
	owner.clear(oldRead)
	owner.pending()
	if newAnnounced != 1 || oldAnnounced != 0 {
		t.Fatalf("new request announced %d times, old %d; want the new request's read only", newAnnounced, oldAnnounced)
	}
	owner.clear(newRead)
}

func TestHTTPBodyReadRetireDoesNotClearReusedConnection(t *testing.T) {
	owner := &bodyReadOwner{}
	oldBody := &observedResponseBody{ReadCloser: io.NopCloser(strings.NewReader("old")), owner: owner}
	newBody := &observedResponseBody{ReadCloser: io.NopCloser(strings.NewReader("new")), owner: owner}
	var oldAnnounced, newAnnounced int
	oldRead, newRead := recordedRead(&oldAnnounced), recordedRead(&newAnnounced)
	oldBody.begin(oldRead)
	newBody.begin(newRead)
	oldBody.retire()
	oldBody.clear(oldRead)
	owner.pending()
	if newAnnounced != 1 || oldAnnounced != 0 {
		t.Fatalf("old body retired the new request: new announced %d times, old %d", newAnnounced, oldAnnounced)
	}
	newBody.clear(newRead)
}

// A read that finished without waiting is no longer its connection's read: a later connection read does not reclassify it as pending.
func TestHTTPBodyReadCompletionDoesNotInventPending(t *testing.T) {
	owner := &bodyReadOwner{}
	body := &observedResponseBody{ReadCloser: io.NopCloser(strings.NewReader("ready")), owner: owner}
	var announced int
	operation := recordedRead(&announced)
	body.begin(operation)
	var buffer bodyReadBuffer
	if _, err := buffer.read(body); err != nil {
		t.Fatal(err)
	}
	body.clear(operation)
	owner.pending()
	if announced != 0 {
		t.Fatalf("a buffered read was announced pending %d times", announced)
	}
}

// One read announces its network wait once, however many connection reads it takes.
func TestHTTPBodyReadAnnouncesItsWaitOnce(t *testing.T) {
	owner := &bodyReadOwner{}
	var announced int
	operation := recordedRead(&announced)
	owner.begin(operation)
	owner.pending()
	owner.pending()
	owner.clear(operation)
	if announced != 1 {
		t.Fatalf("one read announced its network wait %d times, want 1", announced)
	}
}

// Only the read coroutine's own goroutine announces a read's network wait. net/http reads the connection on goroutines of its own (the drain of a body closed early, the peek for the next response); the connection reports those reads to the same owner, and an announcement from one of them must neither suspend the coroutine from the wrong goroutine nor count as the consumer's read.
func TestHTTPBodyReadIgnoresAnnouncementFromAnotherGoroutine(t *testing.T) {
	owner := &bodyReadOwner{}
	var announced int
	operation := recordedRead(&announced)
	owner.begin(operation)
	done := make(chan struct{})
	go func() {
		defer close(done)
		owner.pending()
	}()
	<-done
	if announced != 0 {
		t.Fatalf("a goroutine that does not own the read announced its wait %d times", announced)
	}
	owner.pending()
	owner.clear(operation)
	if announced != 1 {
		t.Fatalf("the owning goroutine announced %d times after the other goroutine's report, want 1", announced)
	}
}

// foreignAnnouncingBody reports a connection read from another goroutine, as net/http's drain does, in the middle of the read that serves the consumer.
type foreignAnnouncingBody struct {
	owner *bodyReadOwner
	sent  bool
}

func (body *foreignAnnouncingBody) Read(buffer []byte) (int, error) {
	if body.sent {
		return 0, io.EOF
	}
	body.sent = true
	done := make(chan struct{})
	go func() {
		defer close(done)
		body.owner.pending()
	}()
	<-done
	return copy(buffer, "served"), nil
}

func (*foreignAnnouncingBody) Close() error { return nil }

// A read the transport serves without waiting stays ready when another goroutine reports a connection read meanwhile: that goroutine must not resume the read's caller as if the read were pending. Before the owner check the report yielded from the foreign goroutine, begin returned pending, and the read never delivered.
func TestObservedBodyReadinessReadStaysReadyWhenAnotherGoroutineReadsTheConnection(t *testing.T) {
	owner := &bodyReadOwner{}
	readiness := newObservedBodyReadiness(&observedResponseBody{ReadCloser: &foreignAnnouncingBody{owner: owner}, owner: owner})
	result, ready := readiness.begin(func(bodyReadSignal) { t.Error("a ready read must not call deliver") })
	if !ready || string(result.data) != "served" || result.err != nil {
		t.Fatalf("read = %q err=%v ready=%t, want the served bytes ready", result.data, result.err, ready)
	}
	if err := readiness.Close(); err != nil {
		t.Fatal(err)
	}
}
