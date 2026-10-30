package host

import (
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/data"
)

// Ports packages/chord/test/next/host/unbound.test.ts: the index (`describe("the index")`). The cases of "references waiting
// in the index" and "holds waiting in the index" build a whole host (createHost, host.hold, memoryLinkPair, a RawPeer) and
// belong to the host core and the remote lane.

// recordingRequest is a request recording its wakes and ends (upstream's `request()`).
type recordingRequest struct {
	who data.Principal
	log []string
}

func (r *recordingRequest) principal() data.Principal { return r.who }
func (r *recordingRequest) wake()                     { r.log = append(r.log, "wake") }
func (r *recordingRequest) end(err *data.ChordError)  { r.log = append(r.log, "end "+err.Message) }

func unboundRequestOf(principal data.Principal) *recordingRequest {
	return &recordingRequest{who: principal}
}

func unboundGuest(kind string) data.Principal { return delta.JsonObjectOf("kind", kind) }

func unboundLogsOf(requests ...*recordingRequest) [][]string {
	logs := make([][]string, len(requests))
	for i, request := range requests {
		logs[i] = request.log
	}
	return logs
}

func TestTheIndexWakesEveryRequestAtThePublicationOutOfTheIndexFirstADeletedOneIsNotWoken(t *testing.T) {
	index := newUnboundIndex()
	a, b, c := unboundRequestOf(nil), unboundRequestOf(nil), unboundRequestOf(nil)
	index.add("app.a", a)
	index.add("app.a", b)
	index.add("app.b", c)
	index.delete("app.a", b)
	index.wakeAll()
	if got, want := unboundLogsOf(a, b, c), [][]string{{"wake"}, nil, {"wake"}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("logs = %v, want %v", got, want)
	}
	// Out of it: a second publication wakes nobody.
	index.wakeAll()
	if !reflect.DeepEqual(a.log, []string{"wake"}) {
		t.Fatalf("a.log = %v, want [wake]", a.log)
	}
}

func TestTheIndexEndsTheRequestsOfARevokedPrincipalOnlyAndEveryRequestAtTheHostsClose(t *testing.T) {
	index := newUnboundIndex()
	guest := unboundRequestOf(unboundGuest("guest"))
	owner := unboundRequestOf(unboundGuest("owner"))
	own := unboundRequestOf(nil)
	for _, each := range []*recordingRequest{guest, owner, own} {
		index.add("app.a", each)
	}
	revoked := data.NewChordError(data.ChordErrorInit{Code: data.CodeForbidden, Reason: data.ReasonRevoked, Message: "revoked"}, nil)
	index.revoke(func(principal data.Principal) bool { return principal.Value("kind") == "guest" }, revoked)
	if got, want := unboundLogsOf(guest, owner, own), [][]string{{"end revoked"}, nil, nil}; !reflect.DeepEqual(got, want) {
		t.Fatalf("logs = %v, want %v", got, want)
	}
	index.close(data.NewChordError(data.ChordErrorInit{Code: data.CodeReleased, Reason: data.ReasonClosed, Message: "closed"}, nil))
	if got, want := unboundLogsOf(owner, own), [][]string{{"end closed"}, {"end closed"}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("logs = %v, want %v", got, want)
	}
}

// Own test, mirroring the wake order Pi's `Map` of names gives (insertion order of the names, the requests of a name in the
// order they were added): a name that left the index and came back is woken last.
func TestTheIndexWakesInInsertionOrderOfNamesAndRequests(t *testing.T) {
	index := newUnboundIndex()
	var order []string
	named := func(name string) *funcRequest {
		return &funcRequest{wakeFn: func() { order = append(order, name) }}
	}
	a1, b1, a2, c1 := named("a1"), named("b1"), named("a2"), named("c1")
	index.add("app.a", a1)
	index.add("app.b", b1)
	index.add("app.a", a2)
	index.add("app.c", c1)
	// app.a leaves and comes back: it is the last name now.
	index.delete("app.a", a1)
	index.delete("app.a", a2)
	index.add("app.a", a1)
	index.wakeAll()
	if want := []string{"b1", "c1", "a1"}; !reflect.DeepEqual(order, want) {
		t.Fatalf("wake order = %v, want %v", order, want)
	}
}

// Own test, mirroring `revoke`: a request an earlier end removed from the index is not visited again, though it was in
// the index under two names when the walk began.
func TestTheIndexRevokeSkipsARequestAnEarlierEndRemoved(t *testing.T) {
	index := newUnboundIndex()
	var ends []string
	var shared *funcRequest
	shared = &funcRequest{who: unboundGuest("guest"), endFn: func(err *data.ChordError) {
		ends = append(ends, err.Message)
		index.delete("app.a", shared)
		index.delete("app.b", shared)
	}}
	index.add("app.a", shared)
	index.add("app.b", shared)
	index.revoke(func(data.Principal) bool { return true }, data.NewChordError(data.ChordErrorInit{Code: data.CodeForbidden, Reason: data.ReasonRevoked, Message: "revoked"}, nil))
	if want := []string{"revoked"}; !reflect.DeepEqual(ends, want) {
		t.Fatalf("ends = %v, want %v", ends, want)
	}
}

// funcRequest is a request whose wake and end are functions of the test.
type funcRequest struct {
	who    data.Principal
	wakeFn func()
	endFn  func(*data.ChordError)
}

func (r *funcRequest) principal() data.Principal { return r.who }
func (r *funcRequest) wake() {
	if r.wakeFn != nil {
		r.wakeFn()
	}
}
func (r *funcRequest) end(err *data.ChordError) {
	if r.endFn != nil {
		r.endFn(err)
	}
}
