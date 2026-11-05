package engine

// pig divergence (D226): Value.Check and Value.Errors of TypeBox 1.3.27 for a schema whose nodes carry `Type.Refine` refinements, served to the Go, Rust, Python and Lisp SDKs.
//
// A refinement is a function of the extension process, so the host walks the schema (typebox.RefineCheck, typebox.RefineErrors) and the extension answers each call of a
// refinement. The walk runs on a goroutine of its own, parked at each refinement call until the extension answers it: the host replies with the call and a token, the extension runs
// the refinement and sends only its answer with the token, and the walk goes on from where it stopped. The schema and the value are sent once, every refinement runs once, in the order
// TypeBox calls them, and the cost is linear in the number of calls. An error a refinement throws never reaches the host: the extension cancels the walk and fails the call with it, as
// a thrown JavaScript error does.
//
// A parked walk belongs to a [Session], the extension connection that started it (a fused extension: the program): the host closes the session when the connection closes (a reload, a
// crash, the end of the session), which cancels every walk it still holds. A walk's lifetime is the connection's; no timer or count limits it.
//
//	start    {"member":"Value.Check"|"Value.Errors","args":[<schema wire>,<value wire>]}
//	answer   {"member":"Value.Answer","args":["<token>",<true|false for a check, a string for an error message>]}
//	cancel   {"member":"Value.Cancel","args":["<token>"]}   (reply {"value":null})
//	reply    {"value":true|false} for Value.Check; {"value":[{"keyword","schemaPath","instancePath","params","message"},...]} for Value.Errors;
//	         {"call":{"token":"...","id":N,"message":false|true,"value":<json>}} for the next call the extension must answer: refinement N (the index of its `{"\u0000":"f","i":N}` entry
//	         in the schema), asking for its check (message false) or its error message (message true) on the value;
//	         {"error":{"name","message"}}: a walk deeper than typebox.MaxDepth (a schema that refers to itself) is a RangeError "Maximum call stack size exceeded", as in TypeBox.

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"math/big"
	"strconv"
	"sync"

	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
	"github.com/MichaelKinsy/PiG/internal/typebox"
)

// The members of TypeBox's `Value` namespace Serve answers, and the two that continue or cancel a walk.
const (
	memberValueCheck  = "Value.Check"
	memberValueErrors = "Value.Errors"
	memberValueAnswer = "Value.Answer"
	memberValueCancel = "Value.Cancel"
)

// Session holds the walks one extension connection has parked at a refinement call. [Session.Close] cancels them all, so none outlives the connection that owns it.
type Session struct {
	mu     sync.Mutex
	closed bool
	walks  map[string]*valueWalk
}

// NewSession is a session with no walks.
func NewSession() *Session { return &Session{walks: map[string]*valueWalk{}} }

// Close cancels every walk of the session and refuses a new one. It may be called more than once.
func (s *Session) Close() {
	s.mu.Lock()
	s.closed = true
	walks := s.walks
	s.walks = map[string]*valueWalk{}
	s.mu.Unlock()
	for _, walk := range walks {
		walk.stop()
	}
}

// Parked is the number of walks the session holds.
func (s *Session) Parked() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.walks)
}

func (s *Session) lookup(token string) *valueWalk {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.walks[token]
}

// forget removes the walk from the session and stops it.
func (s *Session) forget(walk *valueWalk) {
	s.mu.Lock()
	delete(s.walks, walk.token)
	s.mu.Unlock()
	walk.stop()
}

// walkCancelled is the panic that unwinds a cancelled walk.
type walkCancelled struct{}

// walkEvent is what a walk reports: the next refinement call, or its end.
type walkEvent struct {
	call   *walkCall
	result []byte // the reply of a finished walk
}

type walkCall struct {
	id      int
	message bool
	value   any
}

// valueWalk is one Value.Check or Value.Errors walk, parked at a refinement call between requests.
type valueWalk struct {
	token   string
	events  chan walkEvent
	answers chan any
	cancel  chan struct{}
	once    sync.Once
	// mu guards message: the host call that receives the call reply and the one that answers it run on different goroutines.
	mu sync.Mutex
	// message is whether the call the walk waits for asks for an error message (the answer must then be a string).
	message bool
}

func (w *valueWalk) stop() { w.once.Do(func() { close(w.cancel) }) }

func (w *valueWalk) hook(id int, message bool, value any) any {
	select {
	case w.events <- walkEvent{call: &walkCall{id, message, value}}:
	case <-w.cancel:
		panic(walkCancelled{})
	}
	select {
	case answer := <-w.answers:
		if message {
			text, _ := answer.(string)
			return text
		}
		passed, _ := answer.(bool)
		return passed
	case <-w.cancel:
		panic(walkCancelled{})
	}
}

// schemaData is a decoded wire schema as the typebox walkers read it: JSON Schema data whose objects keep their member order, plus each node's refinements as the RefineKey list of ids.
func schemaData(value any) any {
	switch v := value.(type) {
	case *Obj:
		keys := v.EnumKeys()
		members := make(map[string]any, len(keys)+1)
		for _, key := range keys {
			members[key] = schemaData(v.props[key].value)
		}
		if refinements, ok := v.Get(RefineName); ok {
			if list, isList := refinements.([]any); isList && len(list) > 0 {
				ids := make([]any, 0, len(list))
				for _, entry := range list {
					if opaque, isOpaque := entry.(*Opaque); isOpaque {
						if id, err := strconv.Atoi(opaque.Raw); err == nil {
							ids = append(ids, float64(id))
						}
					}
				}
				if _, declared := members[typebox.RefineKey]; !declared {
					keys = append(keys, typebox.RefineKey)
				}
				members[typebox.RefineKey] = ids
			}
		}
		return typebox.NewOrdered(keys, members)
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = schemaData(item)
		}
		return out
	case *Opaque, *undefined, *big.Int:
		return nil
	}
	return value
}

// RefineName is the hidden member of a schema node that lists its refinements.
const RefineName = "~refine"

// valueData is a decoded wire value as the walkers read it: objects keep the member order they arrived in.
func valueData(value any) any {
	switch v := value.(type) {
	case *Obj:
		keys := v.EnumKeys()
		out := chordjson.NewObject(len(keys))
		for _, key := range keys {
			out.Set(key, valueData(v.props[key].value))
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = valueData(item)
		}
		return out
	case *Opaque, *undefined, *big.Int:
		return nil
	}
	return value
}

// Serve is [Session.Serve] on the process's default session, which no connection owns: the host gives each connection its own session, and the fused Go SDK its program's.
func Serve(request []byte) []byte { return defaultSession.Serve(request) }

var defaultSession = NewSession()

// serveValue answers one Value.* request of the session (see the file comment).
func (s *Session) serveValue(member string, args []any, fail func(name, message string) []byte) []byte {
	switch member {
	case memberValueAnswer:
		if len(args) != 2 {
			return fail("TypeError", member+" takes a token and an answer")
		}
		token, _ := args[0].(string)
		walk := s.lookup(token)
		if walk == nil {
			return fail("Error", "no Value walk waits for this answer (it was answered, cancelled or its connection closed)")
		}
		walk.mu.Lock()
		message := walk.message
		walk.mu.Unlock()
		if _, isString := args[1].(string); isString != message {
			s.forget(walk)
			return fail("TypeError", "the answer of a refinement call is a boolean for a check and a string for an error message")
		}
		select {
		case walk.answers <- args[1]:
		case <-walk.cancel:
			return fail("Error", "the Value walk was cancelled")
		}
		return s.await(walk, fail)
	case memberValueCancel:
		if len(args) != 1 {
			return fail("TypeError", member+" takes a token")
		}
		token, _ := args[0].(string)
		if walk := s.lookup(token); walk != nil {
			s.forget(walk)
		}
		return []byte(`{"value":null}`)
	}
	if len(args) != 2 {
		return fail("TypeError", member+" takes a schema and a value")
	}
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return fail("Error", "cannot make a token: "+err.Error())
	}
	walk := &valueWalk{token: hex.EncodeToString(raw[:]), events: make(chan walkEvent), answers: make(chan any), cancel: make(chan struct{})}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return fail("Error", "the extension connection is closed")
	}
	s.walks[walk.token] = walk
	s.mu.Unlock()
	schema, value := schemaData(args[0]), valueData(args[1])
	go walk.run(member, schema, value)
	return s.await(walk, fail)
}

// run is the walk: it reports its calls through hook and its end as the last event.
func (w *valueWalk) run(member string, schema, value any) {
	defer func() {
		recovered := recover()
		switch failure := recovered.(type) {
		case nil:
		case walkCancelled:
			return
		case *typebox.StackOverflowError:
			w.finish([]byte(`{"error":{"name":"RangeError","message":"Maximum call stack size exceeded"}}`))
		default:
			w.finish([]byte(fmt.Sprintf(`{"error":{"name":"InternalError","message":%q}}`, fmt.Sprint(failure))))
		}
	}()
	if member == memberValueCheck {
		w.finish(fmt.Appendf(nil, `{"value":%t}`, typebox.RefineCheck(schema, value, w.hook)))
		return
	}
	list := make([]any, 0)
	for _, failure := range typebox.RefineErrors(schema, value, w.hook) {
		list = append(list, chordjson.ObjectOf(
			"keyword", failure.Keyword,
			"schemaPath", failure.SchemaPath,
			"instancePath", failure.InstancePath,
			"params", failure.ParamsObject(),
			"message", failure.Message,
		))
	}
	text, err := chordjson.Marshal(list)
	if err != nil {
		w.finish([]byte(fmt.Sprintf(`{"error":{"name":"TypeError","message":%q}}`, "the errors cannot be written as JSON: "+err.Error())))
		return
	}
	w.finish(append(append([]byte(`{"value":`), text...), '}'))
}

func (w *valueWalk) finish(reply []byte) {
	select {
	case w.events <- walkEvent{result: reply}:
	case <-w.cancel:
	}
}

// await waits for the walk's next event and turns it into the reply: the end of the walk, or a refinement call with the token. A cancel that wins the race with the walk's next event
// ends the wait, so no host call hangs on a walk that is gone.
func (s *Session) await(w *valueWalk, fail func(name, message string) []byte) []byte {
	var event walkEvent
	select {
	case event = <-w.events:
	case <-w.cancel:
		return fail("Error", "the Value walk was cancelled")
	}
	if event.call == nil {
		s.forget(w)
		return event.result
	}
	text, err := chordjson.Marshal(event.call.value)
	if err != nil {
		s.forget(w)
		return fail("TypeError", "the value cannot be written as JSON: "+err.Error())
	}
	w.mu.Lock()
	w.message = event.call.message
	w.mu.Unlock()
	return fmt.Appendf(nil, `{"call":{"token":%q,"id":%d,"message":%t,"value":%s}}`, w.token, event.call.id, event.call.message, text)
}
