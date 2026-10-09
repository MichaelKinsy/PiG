package sdk

import (
	"fmt"
	"maps"
	"sync"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// SetupSessionManager is the SessionManager that Pi hands the setup callback of newSession (types.ts:411, agent-session-runtime.ts:254-257): the replacement Session's manager, with the reads of SessionManager and the appends that seed the new Session. Each call acts on the replacement Session and returns the new entry's id.
type SetupSessionManager struct {
	SessionManager
}

// SetupFunc is the setup option of NewSession, passed under the "setup" key. It runs after the replacement Session is bound and before the withSession callback; its error fails the call with the same error.
type SetupFunc func(SetupSessionManager) error

// requestSetup runs a setup callback (host protocol RequestSetup).
const requestSetup = "setup"

func (s SetupSessionManager) write(method string, args any) (string, error) {
	return hostValue[string](s.context, "sessionWrite", map[string]any{"method": method, "args": args})
}

// AppendMessage appends a message (session-manager.ts:218); message is the JSON object of a Pi Message, CustomMessage or BashExecutionMessage.
func (s SetupSessionManager) AppendMessage(message map[string]any) (string, error) {
	return s.write("appendMessage", map[string]any{"message": message})
}

// AppendCustomEntry appends an extension entry that does not take part in the model context (session-manager.ts:304).
func (s SetupSessionManager) AppendCustomEntry(customType string, data any) (string, error) {
	return s.write("appendCustomEntry", map[string]any{"customType": customType, "data": data})
}

// AppendCustomMessageEntry appends an extension message that takes part in the model context (session-manager.ts:353).
func (s SetupSessionManager) AppendCustomMessageEntry(customType string, content any, display bool, details any) (string, error) {
	return s.write("appendCustomMessageEntry", map[string]any{"customType": customType, "content": content, "display": display, "details": details})
}

// AppendSessionInfo sets the session name (session-manager.ts:318).
func (s SetupSessionManager) AppendSessionInfo(name string) (string, error) {
	return s.write("appendSessionInfo", map[string]any{"name": name})
}

// AppendModelChange records a model change (session-manager.ts:244).
func (s SetupSessionManager) AppendModelChange(provider, modelID string) (string, error) {
	return s.write("appendModelChange", map[string]any{"provider": provider, "modelId": modelID})
}

// AppendThinkingLevelChange records a thinking level change (session-manager.ts:231).
func (s SetupSessionManager) AppendThinkingLevelChange(thinkingLevel string) (string, error) {
	return s.write("appendThinkingLevelChange", map[string]any{"thinkingLevel": thinkingLevel})
}

// AppendLabelChange sets or clears the label of an entry; a nil label clears it (session-manager.ts:455).
func (s SetupSessionManager) AppendLabelChange(targetID string, label *string) (string, error) {
	return s.write("appendLabelChange", map[string]any{"targetId": targetID, "label": label})
}

type setupEntry struct {
	callback SetupFunc
	mu       sync.Mutex
	err      error
}

func (e *setupEntry) fail(err error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.err = err
}

func (e *setupEntry) failure() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.err
}

// setupRegistry holds the setup callbacks of NewSession calls in flight, by the handle each call names.
type setupRegistry struct {
	mu      sync.Mutex
	next    uint64
	entries map[string]*setupEntry
}

func (r *setupRegistry) add(prefix string, callback SetupFunc) (string, *setupEntry) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.entries == nil {
		r.entries = make(map[string]*setupEntry)
	}
	r.next++
	handle := fmt.Sprintf("%s:setup:%d", prefix, r.next)
	entry := &setupEntry{callback: callback}
	r.entries[handle] = entry
	return handle, entry
}

func (r *setupRegistry) remove(handle string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.entries, handle)
}

func (r *setupRegistry) get(handle string) *setupEntry {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.entries[handle]
}

// setupOption removes the setup callback from opts. A non-function value is an error.
func setupOption(opts map[string]any) (map[string]any, SetupFunc, error) {
	value, ok := opts["setup"]
	if !ok || value == nil {
		return opts, nil, nil
	}
	args := maps.Clone(opts)
	delete(args, "setup")
	switch callback := value.(type) {
	case SetupFunc:
		return args, callback, nil
	case func(SetupSessionManager) error:
		return args, callback, nil
	default:
		return nil, nil, fmt.Errorf("setup must be a func(SetupSessionManager) error, got %T", value)
	}
}

// dispatchSetup runs the callback a setup request names with the replacement Session's manager. ctx is the request's context, so its host calls belong to the request.
func (e *Extension) dispatchSetup(ctx Context, args json.RawMessage) error {
	var request struct {
		Handle string `json:"handle"`
	}
	if err := json.Unmarshal(args, &request); err != nil {
		return fmt.Errorf("decode setup request: %w", err)
	}
	entry := e.setups.get(request.Handle)
	if entry == nil {
		return fmt.Errorf("unknown setup callback %s", request.Handle)
	}
	err := entry.callback(SetupSessionManager{SessionManager: ctx.SessionManager()})
	if err != nil {
		entry.fail(err)
	}
	return err
}
