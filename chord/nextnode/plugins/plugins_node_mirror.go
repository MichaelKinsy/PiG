package plugins

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/documents"
	"github.com/MichaelKinsy/PiG/chord/next/host"
)

// The Node host's copy of a Go object's keyed member (plugins_node_bridged.go, node/proxy.mjs): the documents of the member, sent as
// `mirrorReset` and `mirrorDoc` notifications under the id the proxy chose. No Pi file corresponds to this one: Pi's plugins share
// the one host, so a plugin reads the provider's keyed slot itself (reference.ts ReferenceKeyed.get/watch/at), and here that slot
// is on the other side of the channel from the plugin's.
//
// Async contract: `mirror` registers an observer on the member's slot, reads the slot's documents, and answers with nothing. The
// first notification it sends is `mirrorReset` with the documents read; the commits the observer saw while they were read, and every
// later one, follow it in the order the Go host adopted them (the observer runs inside the commit and only queues; one goroutine sends
// the queue). The proxy makes the object live once it applied that first reset, so no commit is applied before the documents it
// belongs to. A method call's answer waits for the queue to empty (`flush`), so the frames of the commits it made precede its answer and
// the proxy, which applies frames in order before it answers its caller, shows them to the plugin that awaited the call. A Go object that
// ended (its facet reloaded) is told by its slot's `dropped`: the mirror reads the reference's next slot (it retries while the
// reference has none live, and again when that slot ends while it is read) and sends another `mirrorReset` before the commits that
// followed its read. The mirror lives until `unmirror`, the release of its reference or the process's end.

// mirrorSnapshotHook is run once, by tests, after a mirror's documents were read and before they are sent: a commit made there is one
// the snapshot does not have, whose frame the mirror must send after the reset.
var mirrorSnapshotHook atomic.Pointer[func()]

func runMirrorSnapshotHook() {
	if hook := mirrorSnapshotHook.Swap(nil); hook != nil {
		(*hook)()
	}
}

// resyncDelay is how long a mirror waits before it reads a reference again that had no live slot to read.
const resyncDelay = 50 * time.Millisecond

type bridgedMirror struct {
	host   *NodeHost
	id     int
	handle int
	keyed  *host.ReferenceKeyed
	life   context.Context
	cancel context.CancelFunc

	mu      sync.Mutex
	queue   []map[string]any
	started bool
	sending bool
	// idle is closed when the queue is empty and nothing is being sent; nil while it is.
	idle     chan struct{}
	stopSlot func()
	stopped  bool
	// reading is whether a slot's documents are being read: the commits its observer sees are kept in pending, to follow the reset
	// that carries the documents. dropped is whether the slot ended meanwhile.
	reading bool
	pending []map[string]any
	dropped bool
	// resyncing is whether resync runs.
	resyncing bool
}

// observe is the slot's observer: it runs inside the Go host's commit and only queues.
func (m *bridgedMirror) observe(key string, value *documents.JsonObject, dropped bool) {
	m.mu.Lock()
	if dropped {
		if m.reading {
			m.dropped = true
			m.mu.Unlock()
			return
		}
		m.mu.Unlock()
		go m.resync()
		return
	}
	m.mu.Unlock()
	if value == nil {
		m.enqueue(map[string]any{"op": "mirrorDoc", "mirror": m.id, "key": key, "retired": true})
	} else {
		m.enqueue(map[string]any{"op": "mirrorDoc", "mirror": m.id, "key": key, "value": wireEncode(value)})
	}
}

func (m *bridgedMirror) enqueue(message map[string]any) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stopped {
		return
	}
	if m.reading {
		m.pending = append(m.pending, message)
		return
	}
	m.queue = append(m.queue, message)
	m.kick()
}

// kick starts the sender when frames wait and nothing sends them; m.mu is held.
func (m *bridgedMirror) kick() {
	if !m.started || m.sending || len(m.queue) == 0 {
		return
	}
	m.sending = true
	m.idle = make(chan struct{})
	//portlint:allow golifetime ends when the queue is empty
	go m.drain()
}

func (m *bridgedMirror) drain() {
	for {
		m.mu.Lock()
		if len(m.queue) == 0 || m.stopped {
			m.queue, m.sending = nil, false
			close(m.idle)
			m.idle = nil
			m.mu.Unlock()
			return
		}
		message := m.queue[0]
		m.queue = m.queue[1:]
		m.mu.Unlock()
		if port := m.host.port.Load(); port != nil {
			_ = port.control(message)
		}
	}
}

// start lets the queued frames go: the caller runs it once the answer of `mirror` is out.
func (m *bridgedMirror) start() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.started = true
	m.kick()
}

// flush waits until every frame queued so far was sent.
func (m *bridgedMirror) flush(ctx context.Context) error {
	m.mu.Lock()
	idle := m.idle
	m.mu.Unlock()
	if idle == nil {
		return nil
	}
	select {
	case <-idle:
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

// read reads the member's documents and registers the observer first. It returns false when the slot ended while it was read, which
// the caller reads again; else the reset that carries the documents is queued, followed by the commits the read saw.
func (m *bridgedMirror) read(ctx context.Context) (ok bool, err error) {
	m.mu.Lock()
	m.reading, m.dropped, m.pending = true, false, nil
	m.mu.Unlock()
	docs, stop, err := m.keyed.Snapshot(ctx, m.observe)
	if err != nil {
		return false, err
	}
	runMirrorSnapshotHook()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.dropped || m.stopped {
		m.pending = nil
		go stop()
		return false, nil
	}
	m.reading = false
	m.stopSlot = stop
	m.queue = append(m.queue, map[string]any{"op": "mirrorReset", "mirror": m.id, "docs": mirrorDocs(docs)})
	m.queue = append(m.queue, m.pending...)
	m.pending = nil
	m.kick()
	return true, nil
}

// resync reads the reference's keyed member again after its slot ended and sends it as a replacement.
func (m *bridgedMirror) resync() {
	m.mu.Lock()
	if m.stopped || m.resyncing {
		m.mu.Unlock()
		return
	}
	m.resyncing = true
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		m.resyncing = false
		m.mu.Unlock()
	}()
	for {
		ok, err := m.read(m.life)
		if ok {
			return
		}
		m.mu.Lock()
		stopped := m.stopped
		m.mu.Unlock()
		if stopped || m.life.Err() != nil {
			return
		}
		if err == nil {
			// The slot read ended meanwhile: read the next at once.
			continue
		}
		select {
		case <-m.life.Done():
			return
		case <-time.After(resyncDelay):
		}
	}
}

// end stops the mirror: no more frames, its observer removed.
func (m *bridgedMirror) end() {
	m.mu.Lock()
	m.stopped = true
	stop := m.stopSlot
	m.stopSlot = nil
	m.mu.Unlock()
	m.cancel()
	if stop != nil {
		stop()
	}
}

// mirrorDocs is docs as the `docs` of `mirrorReset`.
func mirrorDocs(docs []host.KeyedDocument) []any {
	out := make([]any, len(docs))
	for i, doc := range docs {
		out[i] = delta.JsonObjectOf("key", doc.Key, "value", wireEncode(doc.Value))
	}
	return out
}

// bridgedMirrorOf starts a mirror of keyed member name of the held reference handle under id: the answer is empty, and the returned
// function lets the frames go (the first is the reset with the member's documents), which the caller runs once the answer is out.
func (h *NodeHost) bridgedMirrorOf(ctx context.Context, handle int, name string, id int) (result any, start func(), err error) {
	reference, err := h.bridgedReference(handle)
	if err != nil {
		return nil, nil, err
	}
	keyed := reference.Keyed(name)
	if keyed == nil {
		return nil, nil, &keyedMemberError{name}
	}
	serving := h.serving()
	life, cancel := context.WithCancel(serving.life)
	m := &bridgedMirror{host: h, id: id, handle: handle, keyed: keyed, life: life, cancel: cancel}
	for {
		ok, err := m.read(ctx)
		if err != nil {
			cancel()
			return nil, nil, err
		}
		if ok {
			break
		}
		if ctx.Err() != nil {
			cancel()
			return nil, nil, context.Cause(ctx)
		}
	}
	serving.mu.Lock()
	serving.mirrors[id] = m
	serving.mu.Unlock()
	if ctx.Err() != nil {
		serving.mu.Lock()
		delete(serving.mirrors, id)
		serving.mu.Unlock()
		m.end()
		return nil, nil, context.Cause(ctx)
	}
	return map[string]any{}, m.start, nil
}

type keyedMemberError struct{ name string }

func (e *keyedMemberError) Error() string { return e.name + ": not a keyed member of the reference" }

// endMirrors ends the mirrors for which match is true.
func (served *bridgedServing) endMirrors(match func(*bridgedMirror) bool) {
	served.mu.Lock()
	var ended []*bridgedMirror
	for id, m := range served.mirrors {
		if match(m) {
			ended = append(ended, m)
			delete(served.mirrors, id)
		}
	}
	served.mu.Unlock()
	for _, m := range ended {
		m.end()
	}
}

// flushMirrors waits until the frames queued for the mirrors of reference handle were sent.
func (served *bridgedServing) flushMirrors(ctx context.Context, handle int) error {
	served.mu.Lock()
	var mirrors []*bridgedMirror
	for _, m := range served.mirrors {
		if m.handle == handle {
			mirrors = append(mirrors, m)
		}
	}
	served.mu.Unlock()
	for _, m := range mirrors {
		if err := m.flush(ctx); err != nil {
			return err
		}
	}
	return nil
}
