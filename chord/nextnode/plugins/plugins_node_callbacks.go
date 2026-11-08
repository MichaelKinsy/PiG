package plugins

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/data"
)

// Calls from the Node subprocess to the Go side (docs/plan/chord-plugins-node-evaluator.md, 10.1). No Pi file corresponds to
// this one: in Pi the app's functions are in the host's own process, so a plugin calls them directly. Here the process
// boundary carries them, in two forms, both over the framed socket of plugins_node.go (callbacks.mjs is the other end).
//
// An asynchronous call (`call`) is a Promise on the Node side: a `call` event names a Go function and its JSON arguments, the
// Go side runs it on a goroutine of its own and answers with an `answer` control message, in any order relative to other
// calls. The function's context ends when the process ends, and when the Node side sends `cancel` for the call (the abort of
// the request the plugin's call belongs to); a Go error crosses as a thrown Error (a *data.ChordError as a ChordError). A
// call still running when the process ends is cancelled and its answer is dropped.
//
// A synchronous call (`sync`) serves the host's synchronous functions (`principalKey`, `revokedBy`): the host thread of the
// process blocks until the answer comes, so the answer cannot travel the channel the host thread owns. A worker thread of the
// process owns a second connection to the same socket address (it needs no file descriptor, so it works on Windows, where
// the address is a named pipe); the Go side accepts it once, authenticated by a token the process was given in its `load`
// request, and answers each request on it in order. A sync handler is a pure function of its arguments: while it runs the host
// thread is blocked, so a call from it to the NodeHost could never be served. Its context therefore refuses every NodeHost
// call that waits for the process with ErrSyncCallback.

// GoFunction is a function of the Go app that a plugin calls: its JSON arguments in, its JSON result out. ctx ends when the
// plugin's request aborts (the call's context argument on the Node side), and when the process ends.
type GoFunction func(ctx context.Context, args delta.JsonValue) (delta.JsonValue, error)

// ErrSyncCallback is the error of a NodeHost call made from a synchronous callback: the process's host thread is blocked
// until the callback returns, so the call would wait for ever. It is returned (never waited for) by every NodeHost operation
// that waits for the process, when its context is the one a synchronous callback received.
var ErrSyncCallback = errors.New("plugins: a synchronous callback cannot call into its NodeHost")

// syncFunction is a synchronous Go function of the host: its JSON arguments in, its result (anything that marshals to JSON) out.
// ctx refuses NodeHost calls (ErrSyncCallback).
type syncFunction func(ctx context.Context, args json.RawMessage) (any, error)

// syncCallbackKey is the context key of a synchronous callback's context: its value is the NodeHost whose host thread is
// blocked.
type syncCallbackKey struct{}

// refuseSyncCallback is ErrSyncCallback when ctx is the context of a synchronous callback of h, else nil.
func (h *NodeHost) refuseSyncCallback(ctx context.Context) error {
	if ctx.Value(syncCallbackKey{}) == h {
		return ErrSyncCallback
	}
	return nil
}

// nodeCallbacks is the state of a NodeHost's calls from the process; the zero value is ready.
type nodeCallbacks struct {
	lifeOnce sync.Once
	// life ends when the process did.
	life    context.Context
	endLife context.CancelFunc

	mu        sync.Mutex
	functions map[string]GoFunction
	syncs     map[string]syncFunction
	// running are the cancel functions of the asynchronous calls running, by the id the process gave them.
	running map[int]context.CancelFunc

	// listener is the socket's listener, kept after the start for the process's worker thread to connect to; release closes
	// it and removes the socket's directory.
	listener      net.Listener
	listenerClean func()
	listenerOnce  sync.Once

	// telemetry replays the process's telemetry into the app's adapter; nil without an adapter. Guarded by mu.
	telemetry *nodeTelemetry
}

// lifetime is a context cancelled when the process ended.
func (h *NodeHost) lifetime() context.Context {
	h.cb.lifeOnce.Do(func() {
		//portlint:allow ctxbackground the process's lifetime has no caller: it ends when the process does
		h.cb.life, h.cb.endLife = context.WithCancel(context.Background())
		go func() {
			<-h.exited
			h.cb.endLife()
		}()
	})
	return h.cb.life
}

// keepListener hands the socket's listener and its cleanup to h: they are released when the process ended, or by Load once it
// knows no synchronous connection is wanted.
func (h *NodeHost) keepListener(listener net.Listener, cleanup func()) {
	h.cb.listener, h.cb.listenerClean = listener, cleanup
	go func() {
		<-h.exited
		h.releaseListener()
	}()
}

// releaseListener closes the listener and removes the socket's directory, once.
func (h *NodeHost) releaseListener() {
	h.cb.listenerOnce.Do(func() {
		if h.cb.listener != nil {
			_ = h.cb.listener.Close()
			h.cb.listenerClean()
		}
	})
}

// handleFunction registers the Go function a plugin calls by name.
func (h *NodeHost) handleFunction(name string, fn GoFunction) {
	h.cb.mu.Lock()
	defer h.cb.mu.Unlock()
	if h.cb.functions == nil {
		h.cb.functions = map[string]GoFunction{}
	}
	h.cb.functions[name] = fn
}

// handleSync registers a synchronous function of the host by name.
func (h *NodeHost) handleSync(name string, fn syncFunction) {
	h.cb.mu.Lock()
	defer h.cb.mu.Unlock()
	if h.cb.syncs == nil {
		h.cb.syncs = map[string]syncFunction{}
	}
	h.cb.syncs[name] = fn
}

// answerWire is the control message that answers a call of the process.
type answerWire struct {
	Op     string          `json:"op"`
	ID     int             `json:"id"`
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *goErrorWire    `json:"error,omitempty"`
}

// goErrorWire is a Go error as the process receives it: callbacks.mjs throws a ChordError when Chord is set, else an Error
// of Message.
type goErrorWire struct {
	Name    string          `json:"name,omitempty"`
	Message string          `json:"message"`
	Chord   json.RawMessage `json:"chord,omitempty"`
}

func goError(err error) *goErrorWire {
	wire := &goErrorWire{Message: err.Error()}
	if chord, ok := data.IsChordError(err); ok {
		if raw, marshalErr := json.Marshal(chord); marshalErr == nil {
			wire.Name, wire.Chord = "ChordError", raw
		}
	}
	return wire
}

// resultOf marshals what a Go function returned: the answer's result, or the error that says why it cannot be one.
func resultOf(value any, err error) (json.RawMessage, *goErrorWire) {
	if err != nil {
		return nil, goError(err)
	}
	raw, marshalErr := json.Marshal(value)
	if marshalErr != nil {
		return nil, goError(fmt.Errorf("the result is not JSON: %w", marshalErr))
	}
	return raw, nil
}

// callbackEvent handles the events a process sends that are calls or telemetry; it runs on the goroutine that reads the
// channel.
func (h *NodeHost) callbackEvent(event string, payload []byte) {
	switch event {
	case "call":
		var call struct {
			Call int             `json:"call"`
			Name string          `json:"name"`
			Args json.RawMessage `json:"args"`
		}
		if err := json.Unmarshal(payload, &call); err != nil {
			return
		}
		h.startCall(call.Call, call.Name, call.Args)
	case "cancel":
		var cancel struct {
			Call int `json:"call"`
		}
		if err := json.Unmarshal(payload, &cancel); err != nil {
			return
		}
		h.cb.mu.Lock()
		stop := h.cb.running[cancel.Call]
		h.cb.mu.Unlock()
		if stop != nil {
			stop()
		}
	case "telemetry":
		h.cb.mu.Lock()
		replay := h.cb.telemetry
		h.cb.mu.Unlock()
		if replay != nil {
			replay.replay(payload)
		}
	}
}

// startCall runs the Go function name for the call id of the process on a goroutine of its own and answers it. The call ends
// with its function: its context is cancelled by the process's `cancel`, and when the process ends.
func (h *NodeHost) startCall(id int, name string, args json.RawMessage) {
	h.cb.mu.Lock()
	fn := h.cb.functions[name]
	ctx, stop := context.WithCancel(h.lifetime())
	if h.cb.running == nil {
		h.cb.running = map[int]context.CancelFunc{}
	}
	h.cb.running[id] = stop
	h.cb.mu.Unlock()
	go func() {
		defer func() {
			stop()
			h.cb.mu.Lock()
			delete(h.cb.running, id)
			h.cb.mu.Unlock()
		}()
		var result json.RawMessage
		var failed *goErrorWire
		if fn == nil {
			failed = goError(fmt.Errorf("plugins: no Go function %q is offered", name))
		} else if value, err := delta.DecodeJson(args); err != nil {
			failed = goError(fmt.Errorf("the arguments of %q are not JSON: %w", name, err))
		} else {
			result, failed = resultOf(fn(ctx, value))
		}
		answer := answerWire{Op: "answer", ID: id, OK: failed == nil, Result: result, Error: failed}
		if answer.OK && answer.Result == nil {
			answer.Result = json.RawMessage("null")
		}
		// A process that ended has nothing to answer to.
		_ = h.port.Load().control(answer)
	}()
}

// syncRequest is a request of the process's synchronous connection.
type syncRequest struct {
	ID   int             `json:"id"`
	Name string          `json:"name"`
	Args json.RawMessage `json:"args"`
}

// listenSync makes h accept the process's synchronous connection once, authenticated by the token it returns (the process
// gets it in its `load` request), and serve it until it ends. The listener is released once the connection is made.
func (h *NodeHost) listenSync() string {
	var raw [16]byte
	_, _ = rand.Read(raw[:])
	token := hex.EncodeToString(raw[:])
	listener := h.cb.listener
	//portlint:allow golifetime ends when the listener is closed (releaseListener, at the latest when the process ended)
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			if h.serveSync(conn, token) {
				h.releaseListener()
				return
			}
		}
	}()
	return token
}

// syncHelloTimeout is how long a connection to the socket may take to say who it is before it is dropped.
const syncHelloTimeout = 10 * time.Second

// serveSync serves one connection as the synchronous connection if its first message carries token, and reports whether it
// was one; the connection's requests are answered in order on the goroutine that reads them.
func (h *NodeHost) serveSync(conn net.Conn, token string) bool {
	authenticated := make(chan bool, 1)
	var hello bool
	var valid bool
	var port *framedPort
	port = newFramedPort(conn, func(payload []byte) {
		if !hello {
			hello = true
			var message struct {
				Event string `json:"event"`
				Token string `json:"token"`
			}
			valid = json.Unmarshal(payload, &message) == nil && message.Event == "sync" && message.Token == token
			authenticated <- valid
			return
		}
		if valid {
			h.answerSync(port, payload)
		}
	})
	//portlint:allow golifetime ends with the connection, which serveSync or the process's end closes
	go port.read()
	timer := time.NewTimer(syncHelloTimeout)
	defer timer.Stop()
	select {
	case ok := <-authenticated:
		if ok {
			go func() {
				// The connection ends with the process.
				<-h.exited
				port.Disconnect()
			}()
			return true
		}
	case <-timer.C:
	case <-h.exited:
	}
	port.Disconnect()
	return false
}

// answerSync answers one synchronous request.
func (h *NodeHost) answerSync(port *framedPort, payload []byte) {
	var request syncRequest
	if err := json.Unmarshal(payload, &request); err != nil {
		return
	}
	h.cb.mu.Lock()
	fn := h.cb.syncs[request.Name]
	h.cb.mu.Unlock()
	var result json.RawMessage
	var failed *goErrorWire
	if fn == nil {
		failed = goError(fmt.Errorf("plugins: no synchronous Go function %q is offered", request.Name))
	} else {
		ctx := context.WithValue(h.lifetime(), syncCallbackKey{}, h)
		value, err := fn(ctx, request.Args)
		result, failed = resultOf(value, err)
	}
	answer := answerWire{ID: request.ID, OK: failed == nil, Result: result, Error: failed}
	if answer.OK && answer.Result == nil {
		answer.Result = json.RawMessage("null")
	}
	_ = port.control(answer)
}

// revokeRequest is the `revoke` control request of host.mjs.
type revokeRequest struct {
	envelope
	Revocation json.RawMessage `json:"revocation"`
}

// Revoke is `host.revoke(revocation)` on the process's host (Pi's `Host.revoke`, which Go's host.Host.Revoke mirrors): every
// remote object of the host whose principal its `revokedBy` (RuntimeLoad.RevokedBy) says the revocation revokes ends at once,
// whatever references remain (holders get `forbidden`, running calls are aborted, an activation is abandoned), and its child
// links close. A Go host that routes to this NodeHost sends the revocation down the route itself (host.Host.Revoke), which the
// process's host applies with its own `revokedBy`; Revoke is for the one that did not (a host that accepted the link as a
// child's, or an app that revokes a worker directly). It returns once the process applied the revocation, and the error of its
// `revokedBy` when that failed. It must not be called from a synchronous callback (ErrSyncCallback).
func (h *NodeHost) Revoke(revocation delta.JsonValue, ctx context.Context) error {
	if err := h.refuseSyncCallback(ctx); err != nil {
		return err
	}
	raw, err := json.Marshal(revocation)
	if err != nil {
		return fmt.Errorf("plugins.revoke: the revocation is not JSON: %w", err)
	}
	call, err := h.send("revoke", &revokeRequest{Revocation: raw})
	if err != nil {
		return err
	}
	_, err = h.await(call, ctx)
	return err
}

// callbacksWire are the fields of the `load` request that carry the app's callbacks.
type callbacksWire struct {
	GoPackages []goPackageWire `json:"goPackages"`
	// SyncToken is what the process's worker thread authenticates its synchronous connection with; set when the host needs one.
	SyncToken    string         `json:"syncToken,omitempty"`
	Telemetry    *telemetryWire `json:"telemetry,omitempty"`
	PrincipalKey bool           `json:"principalKey,omitempty"`
	RevokedBy    bool           `json:"revokedBy,omitempty"`
	// PrincipalAttributes is set when the host has a `principalAttributes` hook (called synchronously).
	PrincipalAttributes bool `json:"principalAttributes,omitempty"`
	ParentServes bool           `json:"parentServes,omitempty"`
}

// enableCallbacks registers the app's callbacks of options on h (the Go packages' functions, the synchronous `principalKey`
// and `revokedBy`, the telemetry replay) and returns the fields of the `load` request that tell the process which exist.
// packages are options.GoPackages as goPackagesWire made them. When the host needs a synchronous connection the process's
// worker thread makes it (listenSync); otherwise the socket's listener is released.
func (h *NodeHost) enableCallbacks(options RuntimeLoad, packages []goPackageWire) callbacksWire {
	wire := callbacksWire{GoPackages: packages, PrincipalKey: options.PrincipalKey != nil, RevokedBy: options.RevokedBy != nil, PrincipalAttributes: options.PrincipalAttributes != nil}
	h.handleGoPackages(options.GoPackages)
	if options.Telemetry != nil {
		replay, telemetry := newNodeTelemetry(options.Telemetry)
		h.cb.mu.Lock()
		h.cb.telemetry = replay
		h.cb.mu.Unlock()
		wire.Telemetry = &telemetry
		go func() {
			<-h.exited
			replay.close()
		}()
	}
	if key := options.PrincipalKey; key != nil {
		h.handleSync("principalKey", func(ctx context.Context, args json.RawMessage) (any, error) {
			principal, err := principalOf(args)
			if err != nil {
				return nil, err
			}
			return data.PrincipalKey(principal, ctx, key), nil
		})
	}
	if revokedBy := options.RevokedBy; revokedBy != nil {
		h.handleSync("revokedBy", func(_ context.Context, args json.RawMessage) (any, error) {
			principal, err := principalOf(args)
			if err != nil {
				return nil, err
			}
			revocations, err := revokedBy(principal)
			if err != nil {
				return nil, err
			}
			// A JSON array also when there is none (a nil slice is `null`).
			return append([]delta.JsonValue{}, revocations...), nil
		})
	}
	if attributes := options.PrincipalAttributes; attributes != nil {
		h.handleSync("principalAttributes", func(_ context.Context, args json.RawMessage) (any, error) {
			principal, err := principalOf(args)
			if err != nil {
				return nil, err
			}
			object := delta.NewJsonObject(0)
			for _, attribute := range attributes(principal) {
				// A nil value is upstream's undefined: omitted.
				if attribute.Value != nil {
					object.Set(attribute.Name, attribute.Value)
				}
			}
			return object, nil
		})
	}
	if serves := options.ParentServes; serves != nil {
		wire.ParentServes = true
		h.handleSync("parentServes", func(_ context.Context, args json.RawMessage) (any, error) {
			var name string
			if err := json.Unmarshal(args, &name); err != nil {
				return nil, fmt.Errorf("parentServes: the type name is not a string: %w", err)
			}
			return serves(name), nil
		})
	}
	if wire.PrincipalKey || wire.RevokedBy || wire.PrincipalAttributes || wire.ParentServes {
		wire.SyncToken = h.listenSync()
	} else {
		h.releaseListener()
	}
	return wire
}

// principalOf decodes the principal a synchronous callback of the host got.
func principalOf(args json.RawMessage) (data.Principal, error) {
	value, err := delta.DecodeJson(args)
	if err != nil {
		return nil, fmt.Errorf("the principal is not JSON: %w", err)
	}
	principal, ok := value.(*delta.JsonObject)
	if !ok {
		return nil, errors.New("the principal is not a JSON object")
	}
	return principal, nil
}
