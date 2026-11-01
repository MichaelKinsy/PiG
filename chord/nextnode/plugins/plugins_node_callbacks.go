package plugins

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/data"
)

// Calls from the Node subprocess to the Go side (docs/plan/chord-plugins-node-evaluator.md, 10.1). No Pi file corresponds to
// this one: in Pi the app's functions are in the host's own process, so a plugin calls them directly. Here the process
// boundary carries them over the framed socket of plugins_node.go (callbacks.mjs is the other end).
//
// A call is a Promise on the Node side: a `call` event names a Go function and its JSON arguments, the Go side runs it on a
// goroutine of its own and answers with an `answer` control message, in any order relative to other calls. The function's
// context ends when the process ends, and when the Node side sends `cancel` for the call (the abort of the request the
// plugin's call belongs to); a Go error crosses as a thrown Error (a *data.ChordError as a ChordError). A call still running
// when the process ends is cancelled and its answer is dropped.

// GoFunction is a function of the Go app that a plugin calls: its JSON arguments in, its JSON result out. ctx ends when the
// plugin's request aborts (the call's context argument on the Node side), and when the process ends.
type GoFunction func(ctx context.Context, args delta.JsonValue) (delta.JsonValue, error)

// nodeCallbacks is the state of a NodeHost's calls from the process; the zero value is ready.
type nodeCallbacks struct {
	lifeOnce sync.Once
	// life ends when the process did.
	life    context.Context
	endLife context.CancelFunc

	mu        sync.Mutex
	functions map[string]GoFunction
	// running are the cancel functions of the calls running, by the id the process gave them.
	running map[int]context.CancelFunc
}

// lifetime is a context cancelled when the process ended.
func (h *NodeHost) lifetime() context.Context {
	h.cb.lifeOnce.Do(func() {
		h.cb.life, h.cb.endLife = context.WithCancel(context.Background())
		go func() {
			<-h.exited
			h.cb.endLife()
		}()
	})
	return h.cb.life
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

// callbackEvent handles the events a process sends that are calls; it runs on the goroutine that reads the channel.
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

// callbacksWire are the fields of the `load` request that carry the app's callbacks.
type callbacksWire struct {
	GoPackages []goPackageWire `json:"goPackages"`
}

// enableCallbacks registers the app's callbacks of options on h (the Go packages' functions) and returns the fields of the
// `load` request that tell the process which exist. packages are options.GoPackages as goPackagesWire made them.
func (h *NodeHost) enableCallbacks(options RuntimeLoad, packages []goPackageWire) callbacksWire {
	h.handleGoPackages(options.GoPackages)
	return callbacksWire{GoPackages: packages}
}
