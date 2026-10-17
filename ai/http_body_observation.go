package ai

import (
	"crypto/tls"
	"io"
	"net"
	"sync"
	"sync/atomic"

	"github.com/MichaelKinsy/PiG/internal/goroutineid"
)

// bodyReadOwner identifies one HTTP/1 response's active body read. A network read announces suspension before it blocks; a buffered body read completes without that announcement.
type bodyReadOwner struct {
	mu      sync.Mutex
	current *bodyReadOperation
}

// bodyReadOperation is the read a body's read coroutine has in progress (observedBodyReadiness.reads). Only the coroutine's own goroutine may announce a network wait: the connection is also read by goroutines that net/http owns (the drain of a body closed early, the peek for the next response), and a yield from one of those would resume the caller's next on a goroutine that is not the coroutine.
type bodyReadOperation struct {
	// yield suspends the read coroutine and hands a signal to the goroutine that resumed it.
	yield func(bodyReadSignal) bool
	// owner is the goroutine the read coroutine runs on, set before the first read starts.
	owner uint64
	// announced records that this read already reported its network wait; reset for every read.
	announced atomic.Bool
}

// pending is the connection's report that a read is about to wait for the network. It suspends the read at its first network wait, so the goroutine that started the read learns it is pending without waiting for the network; that goroutine then blocks in the read. A report from any goroutine but the coroutine's own is not this read's wait and is ignored: the connection read it belongs to is not the read the consumer is waiting for.
func (operation *bodyReadOperation) pending() {
	if operation.announced.Load() || operation.owner != goroutineid.Current() {
		return
	}
	operation.announce()
}

// announce reports the wait from the coroutine's own goroutine.
func (operation *bodyReadOperation) announce() {
	if operation.announced.Swap(true) {
		return
	}
	operation.yield(bodyReadSignal{pending: true})
}

type bodyReadSignal struct {
	pending bool
	data    []byte
	err     error
}

func (owner *bodyReadOwner) begin(operation *bodyReadOperation) {
	owner.mu.Lock()
	defer owner.mu.Unlock()
	owner.current = operation
}

func (owner *bodyReadOwner) clear(operation *bodyReadOperation) {
	owner.mu.Lock()
	if owner.current == operation {
		owner.current = nil
	}
	owner.mu.Unlock()
}

func (owner *bodyReadOwner) pending() {
	owner.mu.Lock()
	operation := owner.current
	owner.mu.Unlock()
	if operation != nil {
		operation.pending()
	}
}

func httpBodyReadOwner(conn net.Conn) *bodyReadOwner {
	var owner *bodyReadOwner
	for {
		switch current := conn.(type) {
		case *idleTimeoutConn:
			owner = &current.bodyRead
			conn = current.Conn
		case *tls.Conn:
			conn = current.NetConn()
		case *proxyBufferedConn:
			conn = current.Conn
		default:
			return owner
		}
	}
}

type observedResponseBody struct {
	io.ReadCloser
	owner *bodyReadOwner
	mu    sync.Mutex
	read  *bodyReadOperation
	// alwaysPending announces every read as waiting for input, for bodies whose buffering is unknowable.
	alwaysPending bool
}

func (body *observedResponseBody) begin(operation *bodyReadOperation) {
	body.mu.Lock()
	if body.read != nil {
		body.mu.Unlock()
		panic("HTTP body has overlapping reads")
	}
	body.read = operation
	body.owner.begin(operation)
	body.mu.Unlock()
	if body.alwaysPending {
		operation.announce()
	}
}

func (body *observedResponseBody) clear(operation *bodyReadOperation) {
	body.owner.clear(operation)
	body.mu.Lock()
	if body.read == operation {
		body.read = nil
	}
	body.mu.Unlock()
}

func (body *observedResponseBody) retire() {
	body.mu.Lock()
	operation := body.read
	body.read = nil
	body.mu.Unlock()
	if operation != nil {
		body.owner.clear(operation)
	}
}

func (body *observedResponseBody) Close() error {
	body.retire()
	return body.ReadCloser.Close()
}
