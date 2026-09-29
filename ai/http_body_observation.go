package ai

import (
	"crypto/tls"
	"io"
	"net"
	"sync"
)

// bodyReadOwner identifies one HTTP/1 response's active body read. A network read announces suspension before it blocks; a buffered body read completes without that announcement.
type bodyReadOwner struct {
	mu      sync.Mutex
	current *bodyReadOperation
}

type bodyReadOperation struct {
	signals           chan bodyReadSignal
	onPendingComplete func(bodyReadSignal)
	once              sync.Once
}

type bodyReadSignal struct {
	pending bool
	data    []byte
	err     error
}

func (operation *bodyReadOperation) pending() {
	operation.once.Do(func() { operation.signals <- bodyReadSignal{pending: true} })
}

func (operation *bodyReadOperation) complete(data []byte, err error) {
	signal := bodyReadSignal{data: data, err: err}
	ready := false
	operation.once.Do(func() {
		ready = true
		operation.signals <- signal
	})
	if !ready {
		if operation.onPendingComplete != nil {
			operation.onPendingComplete(signal)
		} else {
			operation.signals <- signal
		}
	}
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
		operation.pending()
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
