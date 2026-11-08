package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sync"
	"sync/atomic"

	"github.com/MichaelKinsy/PiG/chord"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
)

// The reference RPC, Go to Node (docs/plan/chord-plugins-node-evaluator.md 10.1 and 10.3). No Pi file corresponds to this one:
// Pi's app `get`s a local type its plugins serve in process (`server.get(Index)`, plugins.test.ts's end to end); here the
// plugins' host is in a Node subprocess, so the Go host holds the object through control requests that Node serves over Pi's
// own `host.get` (node/bridge.mjs): the Node host's `get` is the one that waits (`readyTimeout`), reports `unavailable` and
// `aborted`, counts the reference and releases it.
//
// Async contract. Every request but `unwatch` and `release` blocks until its reply and takes the caller's context: an abort
// is sent to the Node host, which aborts the request's own context (its `get` ends `aborted`), and the call returns that
// error; a `get` that was answered when the abort arrived is released and returns the abort. `unwatch` and `release` are
// notifications on the same channel, so they are applied in the order they were sent, after what was sent before them. The
// frames of a watch arrive in the order the Node host's watch delivers them, over the one channel, and each goes to the
// watch's `Advance` in that order on the goroutine that reads the channel; the listener runs on the watch's own delivery,
// one call at a time, folding frames made meanwhile as every chord watch does. When the process ends the reference's state
// is `loading` for good, as a lost link's, and its calls and reads fail with the process's exit.

// getRequest is the `get` control request of bridge.mjs. Handle is the id this side holds the reference by.
type getRequest struct {
	envelope
	Handle   int             `json:"handle"`
	Contract *ContractData   `json:"contract"`
	Params   json.RawMessage `json:"params"`
	// ReadyTimeout is milliseconds.
	ReadyTimeout *int64 `json:"readyTimeout,omitempty"`
	// Hold asks for `host.hold` (NodeHost.Hold): the reference at once, in whatever state its object is.
	Hold bool `json:"hold,omitempty"`
}

// memberRequest is the `value` control request: a state member's value, or a keyed member's document at Key.
type memberRequest struct {
	envelope
	Handle int     `json:"handle"`
	Member string  `json:"member"`
	Key    *string `json:"key,omitempty"`
}

// watchRequest is the `watch` control request: a watch of a state member, or of a keyed member's document at Key, whose
// frames are `frame` events of Watch (the id this side chose, so a frame never arrives for an id it does not know).
type watchRequest struct {
	envelope
	Handle int     `json:"handle"`
	Member string  `json:"member"`
	Key    *string `json:"key,omitempty"`
	Watch  int     `json:"watch"`
}

// callRequest is the `call` control request.
type callRequest struct {
	envelope
	Handle int             `json:"handle"`
	Method string          `json:"method"`
	Args   json.RawMessage `json:"args"`
}

// nodeBridge is what a NodeHost keeps of the references it holds and their watches, by the ids this side chose (a handle for a
// reference, an id for a watch): registered before the request that makes them is sent, so the events that follow its reply
// find them; and, for the other direction, the Go core facets it ships as proxies and what it holds for them
// (plugins_node_bridged.go).
type nodeBridge struct {
	mu         sync.Mutex
	nextHandle int
	references map[int]*NodeReference
	nextWatch  int
	nextPin    int
	watches    map[int]*nodeWatch
	bridged    []BridgedFacet
	served     *bridgedServing
}

// nodeWatch is a watch of a member the Node host serves: its frames wait until the reply of the `watch` request (the
// watch's first value) made the handle, and are delivered to it in order.
type nodeWatch struct {
	mu     sync.Mutex
	handle *chord.Watch[delta.JsonValue]
	early  []func(*chord.Watch[delta.JsonValue])
	// ended is set when the Node host reported that the watch ended (or this side ended it knowing the Node host is told by
	// the reference's release): nothing is left to stop there.
	ended atomic.Bool
}

// deliver runs f on the watch's handle once there is one, in the order frames arrived.
func (w *nodeWatch) deliver(f func(*chord.Watch[delta.JsonValue])) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.handle == nil {
		w.early = append(w.early, f)
		return
	}
	f(w.handle)
}

// bind gives the watch its handle and delivers the frames that arrived before it.
func (w *nodeWatch) bind(handle *chord.Watch[delta.JsonValue]) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.handle = handle
	for _, f := range w.early {
		f(handle)
	}
	w.early = nil
}

func (b *nodeBridge) addWatch(w *nodeWatch) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.nextWatch++
	if b.watches == nil {
		b.watches = map[int]*nodeWatch{}
	}
	b.watches[b.nextWatch] = w
	return b.nextWatch
}

func (b *nodeBridge) removeWatch(id int) *nodeWatch {
	b.mu.Lock()
	defer b.mu.Unlock()
	w := b.watches[id]
	delete(b.watches, id)
	return w
}

func (b *nodeBridge) watch(id int) *nodeWatch {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.watches[id]
}

func (b *nodeBridge) addReference(r *NodeReference) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.nextHandle++
	if b.references == nil {
		b.references = map[int]*NodeReference{}
	}
	b.references[b.nextHandle] = r
	return b.nextHandle
}

func (b *nodeBridge) removeReference(handle int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.references, handle)
}

func (b *nodeBridge) reference(handle int) *NodeReference {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.references[handle]
}

// NodeReference is a reference to an object of a local type that the plugins of a Node host serve: host.Reference's shape for
// the members a local type has (`Member`, `Keyed`, `Call`, `State`, `Release`), held through the Node host's own
// `host.get`. Additive Go API: it is what the app's Go code uses where Pi's app `get`s a local type in process.
//
// Its reads, calls and watches are round trips to the Node host (a state member's `Value` is a read of the Node host's
// current value). Values cross as JSON. After Release, and after the process ended, they fail.
type NodeReference struct {
	node     *NodeHost
	handle   int
	contract *contracts.Contract
	state    map[string]*NodeMember
	keyed    map[string]*NodeKeyed
	methods  map[string]struct{}

	mu sync.Mutex
	// current is the state the Node host showed last, stateSeq the number of that change (the reply of `get` shows change
	// 0, and each `state` event the next): a change that arrives out of date is ignored.
	current  *host.RefState
	stateSeq int
	states   []*chord.NativeWatch[*host.RefState]
	released bool
	watches  map[int]struct{}
	done     chan struct{}
}

// Get is the Go form of Pi's `server.get(Index)` for a local type the Node host's plugins serve: a reference once its object
// is live, as `host.Get` of a Go host. It sends `get` with ContractDataOf(contract); the Node host declares the same
// contract (a different fingerprint is refused) and runs `host.get` with the request's context, so what it does while the
// object is not live is Pi's: it waits while the reference is `loading`, `failed { transient }` or `failed { waiting }`
// until options.ReadyTimeout (the host's `readyTimeout` by default), then fails with the last cause, which crosses as a
// *NodeError wrapping the ChordError (`unavailable`, `aborted`, ...); a broken failure fails at once. It blocks, and
// aborting ctx aborts the request there. Only a local contract is held this way: a remote one is held through a Go host
// that routes its kind to the node (NodeHost.Link). params is the type's params object (a *delta.JsonObject).
func (h *NodeHost) Get(ctx context.Context, contract *contracts.Contract, params any, options host.GetOptions) (*NodeReference, error) {
	return h.reference(ctx, contract, params, options, false)
}

// Hold is Pi's `host.hold(Type, params)` for a local type the Node host's plugins serve, the reference at once whatever the
// state of its object (`loading`, `failed { waiting }` while nobody serves its type, ...), following it until released. It
// sends `get` with `hold` set, so the Node host's own `hold` makes the reference and the Go side watches its state
// (NodeReference.State). It fails only for invalid params, an unloaded host or an ended process.
func (h *NodeHost) Hold(ctx context.Context, contract *contracts.Contract, params any) (*NodeReference, error) {
	return h.reference(ctx, contract, params, host.GetOptions{}, true)
}

// reference is Get's and Hold's request.
func (h *NodeHost) reference(ctx context.Context, contract *contracts.Contract, params any, options host.GetOptions, hold bool) (*NodeReference, error) {
	declared, err := contracts.ContractDataOf(contract)
	if err != nil {
		return nil, err
	}
	if declared.Kind != contracts.KindLocal {
		return nil, fmt.Errorf("%s: NodeHost.Get holds a local type; a remote type is held through a Go host that routes its kind to the node", declared.Name)
	}
	wire, err := ContractDataOf(contract)
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	// Registered before the request is sent: the state changes that follow its reply find the reference.
	reference := newNodeReference(h, contract, declared)
	request := &getRequest{Contract: wire, Params: encoded, Handle: reference.handle, Hold: hold}
	if options.ReadyTimeout != nil {
		ms := options.ReadyTimeout.Milliseconds()
		request.ReadyTimeout = &ms
	}
	call, err := h.send("get", request)
	if err != nil {
		reference.forget()
		return nil, err
	}
	reply, err := h.awaitAborted(call, ctx)
	if err != nil {
		reference.forget()
		return nil, err
	}
	var answer struct {
		State stateWire `json:"state"`
	}
	if err := json.Unmarshal(reply.Result, &answer); err != nil {
		reference.Release()
		return nil, err
	}
	state, err := answer.State.refState()
	if err != nil {
		reference.Release()
		return nil, err
	}
	reference.setState(0, state)
	if ctx.Err() != nil {
		// Answered as the abort arrived: nothing may be held that the caller was told it did not get.
		reference.Release()
		return nil, context.Cause(ctx)
	}
	return reference, nil
}

func newNodeReference(h *NodeHost, contract *contracts.Contract, declared *contracts.ContractData) *NodeReference {
	r := &NodeReference{
		node: h, contract: contract,
		state: map[string]*NodeMember{}, keyed: map[string]*NodeKeyed{}, methods: map[string]struct{}{},
		current: &host.RefState{Kind: host.RefStateLoading}, stateSeq: -1, watches: map[int]struct{}{}, done: make(chan struct{}),
	}
	variant := declared.Versions[len(declared.Versions)-1].Variants.Value(contracts.LocalVariant)
	for name := range variant.State.All() {
		r.state[name] = &NodeMember{reference: r, name: name}
	}
	for name := range variant.Keyed.All() {
		r.keyed[name] = &NodeKeyed{reference: r, name: name}
	}
	for name := range variant.Methods.All() {
		r.methods[name] = struct{}{}
	}
	r.handle = h.bridge.addReference(r)
	// The process ends before the reference is released: the reference is `loading` for good, as a lost link's.
	//portlint:allow golifetime ends with the reference's release or the process's exit, whichever comes first
	go func() {
		select {
		case <-h.exited:
			r.setState(math.MaxInt, &host.RefState{Kind: host.RefStateLoading})
		case <-r.done:
		}
	}()
	return r
}

// stateWire is a host.RefState as bridge.mjs sends it.
type stateWire struct {
	Kind  host.RefStateKind `json:"kind"`
	Cause *struct {
		Kind  data.FailureKind `json:"kind"`
		Error json.RawMessage  `json:"error"`
	} `json:"cause"`
	RetryAt *int64          `json:"retryAt"`
	Error   json.RawMessage `json:"error"`
}

func (w stateWire) refState() (*host.RefState, error) {
	state := &host.RefState{Kind: w.Kind, RetryAt: w.RetryAt}
	if w.Cause != nil {
		chord, ok := chordErrorOf(w.Cause.Error)
		if !ok {
			return nil, errors.New("a reference's failed state has a cause that is no ChordError")
		}
		state.Cause = data.FailureCause{Kind: w.Cause.Kind, Error: chord}
	}
	if len(w.Error) > 0 {
		chord, ok := chordErrorOf(w.Error)
		if !ok {
			return nil, errors.New("a reference's ended state has an error that is no ChordError")
		}
		state.Error = chord
	}
	return state, nil
}

// Member is the state member name, nil when the contract has none.
func (r *NodeReference) Member(name string) *NodeMember { return r.state[name] }

// Keyed is the keyed member name, nil when the contract has none.
func (r *NodeReference) Keyed(name string) *NodeKeyed { return r.keyed[name] }

// Call calls the method of the object (nil args for none) and blocks until it answers. It runs while the reference is live
// and parks while it is loading (the Node host's reference parks it up to the `readyTimeout` of the Get), as a call on a
// reference does; aborting ctx aborts it there. A method the contract does not have is an error; after Release, or when the
// process ended, it fails (with the exit, for a process that ended).
func (r *NodeReference) Call(method string, args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
	if _, found := r.methods[method]; !found {
		return nil, fmt.Errorf("%s: %s is not a method", r.contract.Name, method)
	}
	encoded, err := marshalWire(args)
	if err != nil {
		return nil, err
	}
	reply, err := r.request("call", &callRequest{Handle: r.handle, Method: method, Args: encoded}, ctx)
	if err != nil {
		return nil, err
	}
	var answer struct {
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(reply.Result, &answer); err != nil {
		return nil, err
	}
	return unmarshalWire(answer.Result)
}

// request sends a control request about the reference and awaits its reply; a released reference fails without one.
func (r *NodeReference) request(op string, body request, ctx context.Context) (nodeReply, error) {
	if err := r.usable(); err != nil {
		return nodeReply{}, err
	}
	call, err := r.node.send(op, body)
	if err != nil {
		return nodeReply{}, err
	}
	return r.node.awaitAborted(call, ctx)
}

// awaitAborted is await whose error, when ctx was aborted, also satisfies errors.Is(err, context.Cause(ctx)): the caller's own
// abort, as a Go host's calls report it. The Node host answered the abort it was sent with Pi's rejection (the abort's
// reason, a *NodeError), which stays in the chain.
func (h *NodeHost) awaitAborted(call *nodeCall, ctx context.Context) (nodeReply, error) {
	reply, err := h.await(call, ctx)
	if err != nil && ctx.Err() != nil {
		if cause := context.Cause(ctx); !errors.Is(err, cause) {
			err = errors.Join(cause, err)
		}
	}
	return reply, err
}

// usable is the error of a released reference.
func (r *NodeReference) usable() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.released {
		return releasedError(r.contract.Name)
	}
	return nil
}

func releasedError(name string) *data.ChordError {
	return data.NewChordError(data.ChordErrorInit{Code: data.CodeReleased, Message: name + ": released"}, nil)
}

// State is the reference's state: `loading`, `live`, `failed` or `ended`, as the Node host's reference shows it; Value is the
// latest the Node host sent (the changes arrive in order on the control channel) and a watch gets every change. It is
// `loading` once the process ended and `ended` (`released`) after Release.
func (r *NodeReference) State() chord.NativeWatchable[*host.RefState] { return nodeRefState{r} }

// Release releases the reference (idempotent): the Node host releases its own, so its object may end when it was the last;
// the watches of the reference end `ended`. It does not wait for the Node host, whose own release follows every request sent
// before it.
func (r *NodeReference) Release() { r.end(true) }

// forget ends a reference whose `get` failed: nothing is held on the Node host to release.
func (r *NodeReference) forget() { r.end(false) }

func (r *NodeReference) end(release bool) {
	r.mu.Lock()
	if r.released {
		r.mu.Unlock()
		return
	}
	r.released = true
	r.current = &host.RefState{Kind: host.RefStateEnded, Error: releasedError(r.contract.Name)}
	states := r.states
	r.states = nil
	watches := r.watches
	r.watches = nil
	close(r.done)
	r.mu.Unlock()
	r.node.bridge.removeReference(r.handle)
	for id := range watches {
		if w := r.node.bridge.removeWatch(id); w != nil {
			// The Node host ends its watches with its reference.
			w.ended.Store(true)
			w.deliver(func(handle *chord.Watch[delta.JsonValue]) { handle.EndWith(chord.WatchEnd{Kind: chord.WatchEnded}) })
		}
	}
	for _, watch := range states {
		watch.EndWith(chord.WatchEnd{Kind: chord.WatchEnded})
	}
	if !release {
		return
	}
	// A notification, lost with a process that ended: nothing is left to release there.
	if port := r.node.port.Load(); port != nil {
		_ = port.control(map[string]any{"op": "release", "handle": r.handle})
	}
}

// setState makes state, change number seq of the Node host's, the reference's state and tells its watches; a change older
// than the one shown, and any change of a released reference (its state is final), is ignored.
func (r *NodeReference) setState(seq int, state *host.RefState) {
	r.mu.Lock()
	if r.released || seq <= r.stateSeq {
		r.mu.Unlock()
		return
	}
	r.current, r.stateSeq = state, seq
	watches := append([]*chord.NativeWatch[*host.RefState]{}, r.states...)
	r.mu.Unlock()
	for _, watch := range watches {
		watch.Advance(chordctx.Background(), state)
	}
}

// nodeRefState is `ref.state`.
type nodeRefState struct{ reference *NodeReference }

func (s nodeRefState) Value() *host.RefState {
	s.reference.mu.Lock()
	defer s.reference.mu.Unlock()
	return s.reference.current
}

func (s nodeRefState) Watch(ctx context.Context) (chord.NativeWatchHandle[*host.RefState], error) {
	r := s.reference
	var watch *chord.NativeWatch[*host.RefState]
	r.mu.Lock()
	released := r.released
	watch = chord.NewNativeWatch(r.current, func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		for i, each := range r.states {
			if each == watch {
				r.states = append(r.states[:i:i], r.states[i+1:]...)
				break
			}
		}
	}, func(error) {}, func() *host.RefState {
		r.mu.Lock()
		defer r.mu.Unlock()
		return r.current
	})
	if !released {
		r.states = append(r.states, watch)
	}
	r.mu.Unlock()
	if released {
		watch.EndWith(chord.WatchEnd{Kind: chord.WatchEnded})
		return watch, nil
	}
	watch.ObserveAbort(ctx)
	return watch, nil
}

// NodeMember is a state member of a NodeReference.
type NodeMember struct {
	reference *NodeReference
	name      string
}

// Value is the member's current value as the Node host's reference reads it: a round trip. The error is what the getter
// there throws (`not_ready` before the first binding, the failure's error, `released`), as a *NodeError wrapping the
// ChordError.
func (m *NodeMember) Value() (delta.JsonValue, error) {
	// The read ends with the process, and then with its exit: its context is not one the caller aborted, so the error is not
	// awaitAborted's.
	if err := m.reference.usable(); err != nil {
		return nil, err
	}
	node := m.reference.node
	call, err := node.send("value", &memberRequest{Handle: m.reference.handle, Member: m.name})
	if err != nil {
		return nil, err
	}
	reply, err := node.await(call, node.life)
	if err != nil {
		return nil, err
	}
	var answer struct {
		Value json.RawMessage `json:"value"`
	}
	if err := json.Unmarshal(reply.Result, &answer); err != nil {
		return nil, err
	}
	return unmarshalWire(answer.Value)
}

// Watch is a watch of the member that ends when the reference is released (`ended`), when the Node host ends it (its end is
// the Node host's: `ended`, `failed`), when it is stopped, or when ctx aborts. It blocks until the Node host acquired the
// watch, which starts at the current value and then gets one frame per change, in order.
func (m *NodeMember) Watch(ctx context.Context) (chord.WatchHandle[delta.JsonValue], error) {
	handle, err := m.reference.watch(m.name, nil, ctx)
	if err != nil {
		return nil, err
	}
	return handle, nil
}

// NodeKeyed is a keyed member of a NodeReference.
type NodeKeyed struct {
	reference *NodeReference
	name      string
}

// Get is the document at key, found false when absent: a round trip. It blocks, and aborting ctx aborts it there.
func (k *NodeKeyed) Get(ctx context.Context, key string) (value delta.JsonValue, found bool, err error) {
	reply, err := k.reference.request("value", &memberRequest{Handle: k.reference.handle, Member: k.name, Key: &key}, ctx)
	if err != nil {
		return nil, false, err
	}
	var answer struct {
		Found bool            `json:"found"`
		Value json.RawMessage `json:"value"`
	}
	if err := json.Unmarshal(reply.Result, &answer); err != nil {
		return nil, false, err
	}
	if !answer.Found {
		return nil, false, nil
	}
	value, err = unmarshalWire(answer.Value)
	return value, err == nil, err
}

// Watch is a watch of the document at key, nil when absent; it ends with the reference.
func (k *NodeKeyed) Watch(ctx context.Context, key string) (chord.WatchHandle[delta.JsonValue], error) {
	handle, err := k.reference.watch(k.name, &key, ctx)
	if err != nil || handle == nil {
		return nil, err
	}
	return handle, nil
}

// watch acquires a watch of a member (or of a keyed member's document at key) on the Node host. The id is chosen here and
// registered before the request is sent, so the frames that follow its reply find their watch.
func (r *NodeReference) watch(member string, key *string, ctx context.Context) (*chord.Watch[delta.JsonValue], error) {
	if err := r.usable(); err != nil {
		return nil, err
	}
	sink := &nodeWatch{}
	id := r.node.bridge.addWatch(sink)
	r.mu.Lock()
	if r.released {
		r.mu.Unlock()
		r.node.bridge.removeWatch(id)
		return nil, releasedError(r.contract.Name)
	}
	r.watches[id] = struct{}{}
	r.mu.Unlock()
	forget := func() {
		r.node.bridge.removeWatch(id)
		r.mu.Lock()
		delete(r.watches, id)
		r.mu.Unlock()
	}
	call, err := r.node.send("watch", &watchRequest{Handle: r.handle, Member: member, Key: key, Watch: id})
	if err != nil {
		forget()
		return nil, err
	}
	reply, err := r.node.awaitAborted(call, ctx)
	if err != nil {
		forget()
		return nil, err
	}
	var answer struct {
		Absent bool            `json:"absent"`
		Value  json.RawMessage `json:"value"`
	}
	if err := json.Unmarshal(reply.Result, &answer); err != nil {
		forget()
		return nil, err
	}
	if answer.Absent {
		forget()
		return nil, nil
	}
	value, err := unmarshalWire(answer.Value)
	if err != nil {
		r.node.stopWatch(id)
		forget()
		return nil, err
	}
	handle := chord.NewWatch[delta.JsonValue](value, func() {
		// The watch ended here (stopped, aborted, released): the Node host's watch stops too, unless it ended there first.
		forget()
		if !sink.ended.Load() {
			r.node.stopWatch(id)
		}
	}, func(error) {})
	sink.bind(handle)
	handle.ObserveAbort(ctx)
	return handle, nil
}

// stopWatch tells the Node host that watch id was stopped here: a notification, lost with a process that ended.
func (h *NodeHost) stopWatch(id int) {
	if port := h.port.Load(); port != nil {
		_ = port.control(map[string]any{"op": "unwatch", "watch": id})
	}
}

// bridgeEvent handles an event of the Node host that belongs to the reference RPC: a frame or the end of a watch, or the
// change of a reference's state (the reference RPC, Go to Node), or a request or notification the Node host's proxies make
// of the Go host (bridgedEvent). It runs on the goroutine that reads the channel, so the events of one watch reach it in the
// order the Node host sent them. A message that does not decode is dropped, as `control` drops one.
func (h *NodeHost) bridgeEvent(event string, payload []byte) {
	switch event {
	case "frame":
		h.watchFrame(payload)
	case "watchEnd":
		h.watchEnded(payload)
	case "state":
		h.referenceState(payload)
	default:
		h.bridgedEvent(event, payload)
	}
}

// watchFrame is one call of a watch's listener on the Node host: it goes to the watch's handle, in order.
func (h *NodeHost) watchFrame(payload []byte) {
	var message struct {
		Watch int `json:"watch"`
		Call  struct {
			Kind    chord.WatchCallKind `json:"kind"`
			Value   json.RawMessage     `json:"value"`
			Ops     delta.Ops           `json:"ops"`
			Retired bool                `json:"retired"`
			Error   *wireError          `json:"error"`
		} `json:"call"`
	}
	if err := json.Unmarshal(payload, &message); err != nil {
		return
	}
	sink := h.bridge.watch(message.Watch)
	if sink == nil {
		return
	}
	if message.Call.Kind == chord.WatchCallFailed {
		sink.deliver(func(w *chord.Watch[delta.JsonValue]) {
			w.Advance(chordctx.Background(), chord.WatchCall[delta.JsonValue]{Kind: chord.WatchCallFailed, Error: message.Call.Error.err()})
		})
		return
	}
	value, err := unmarshalWire(message.Call.Value)
	if err != nil {
		return
	}
	ops, err := unwireOps(message.Call.Ops)
	if err != nil {
		return
	}
	sink.deliver(func(w *chord.Watch[delta.JsonValue]) {
		w.Advance(chordctx.Background(), chord.WatchCall[delta.JsonValue]{
			Kind: chord.WatchCallValue, Value: value, Ops: ops, Retired: message.Call.Retired,
		})
	})
}

// watchEnded is the end of a watch on the Node host: the handle ends with the same end.
func (h *NodeHost) watchEnded(payload []byte) {
	var message struct {
		Watch int `json:"watch"`
		End   struct {
			Kind  chord.WatchEndKind `json:"kind"`
			Error *wireError         `json:"error"`
		} `json:"end"`
	}
	if err := json.Unmarshal(payload, &message); err != nil {
		return
	}
	sink := h.bridge.watch(message.Watch)
	if sink == nil {
		return
	}
	end := chord.WatchEnd{Kind: message.End.Kind}
	if message.End.Error != nil {
		end.Error = message.End.Error.err()
	}
	sink.ended.Store(true)
	sink.deliver(func(w *chord.Watch[delta.JsonValue]) { w.EndWith(end) })
}

// referenceState is a change of a reference's state on the Node host.
func (h *NodeHost) referenceState(payload []byte) {
	var message struct {
		Handle int       `json:"handle"`
		Seq    int       `json:"seq"`
		State  stateWire `json:"state"`
	}
	if err := json.Unmarshal(payload, &message); err != nil {
		return
	}
	state, err := message.State.refState()
	if err != nil {
		return
	}
	if reference := h.bridge.reference(message.Handle); reference != nil {
		reference.setState(message.Seq, state)
	}
}

// pinRequest is the `pin` control request: Pin is the id this side chose.
type pinRequest struct {
	envelope
	Handle int    `json:"handle"`
	Member string `json:"member"`
	Key    string `json:"key"`
	Pin    int    `json:"pin"`
}

// NodePinnedKey is a key of a keyed member held on the Node host (Pi's `pinKey`): the document at the key stays loaded and
// followed across the object's attempts until Release. Additive Go API.
type NodePinnedKey struct {
	keyed   *NodeKeyed
	key     string
	id      int
	release sync.Once
}

// Pin pins the document at key of the member on the Node host and returns it, nil when there is no such document: it blocks until
// the Node host pinned, aborting ctx aborts it there. The pin is released by its Release and with the reference.
func (k *NodeKeyed) Pin(ctx context.Context, key string) (*NodePinnedKey, error) {
	r := k.reference
	id := r.node.bridge.nextPinID()
	reply, err := r.request("pin", &pinRequest{Handle: r.handle, Member: k.name, Key: key, Pin: id}, ctx)
	if err != nil {
		return nil, err
	}
	var answer struct {
		Absent bool `json:"absent"`
	}
	if err := json.Unmarshal(reply.Result, &answer); err != nil {
		return nil, err
	}
	if answer.Absent {
		return nil, nil
	}
	return &NodePinnedKey{keyed: k, key: key, id: id}, nil
}

// Get is the pinned document's current value.
func (p *NodePinnedKey) Get(ctx context.Context) (delta.JsonValue, bool, error) {
	return p.keyed.Get(ctx, p.key)
}

// Release releases the pin (idempotent): a notification, lost with a process that ended.
func (p *NodePinnedKey) Release() {
	p.release.Do(func() {
		if port := p.keyed.reference.node.port.Load(); port != nil {
			_ = port.control(map[string]any{"op": "unpin", "pin": p.id})
		}
	})
}

func (b *nodeBridge) nextPinID() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.nextPin++
	return b.nextPin
}
