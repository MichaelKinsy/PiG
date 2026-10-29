package sdk

import (
	"crypto/rand"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// AutocompleteSuggestions is the complete provider answer. Cursor columns in this API count UTF-16 code units, as in Pi.
type AutocompleteSuggestions struct {
	Items  []AutocompleteItem `json:"items"`
	Prefix string             `json:"prefix"`
}

// AutocompleteCompletion replaces the buffer and its cursor after selection.
type AutocompleteCompletion struct {
	Lines      []string `json:"lines"`
	CursorLine int      `json:"cursorLine"`
	CursorCol  int      `json:"cursorCol"`
}

// AutocompleteProvider wraps the current provider. Each callback receives its live request context; mutable provider state belongs to the returned instance.
type AutocompleteProvider struct {
	TriggerCharacters           []string
	GetSuggestions              func(Context, []string, int, int, bool) (*AutocompleteSuggestions, error)
	ApplyCompletion             func(Context, []string, int, int, AutocompleteItem, string) (AutocompleteCompletion, error)
	ShouldTriggerFileCompletion func(Context, []string, int, int) (bool, error)
}

// AutocompleteProviderFactory runs when the host rebuilds the provider chain, not on each query.
type AutocompleteProviderFactory func(Context, *AutocompleteProvider) (*AutocompleteProvider, error)

type autocompleteRegistry struct {
	mu        sync.Mutex
	factories map[string]AutocompleteProviderFactory
	providers map[string]*AutocompleteProvider
	invoked   map[string]bool
	// suggests are the getSuggestions requests being served, by request id.
	suggests map[string]*autocompleteSettle
}

// pig divergence (D213): autocompleteCheckpoint is how long a provider that blocks its goroutine may run, outside a call to the host, before the host is told that it awaits
// real I/O (protocol NotifyUIAutocompletePending, D213). Pi's microtask checkpoint has no duration; a blocking provider has no event loop to end it.
const autocompleteCheckpoint = 50 * time.Millisecond

// autocompleteSettle tracks one getSuggestions request for the ui.autocomplete.pending notice: the request awaits real I/O when it has made no
// progress for a checkpoint, unfinished and outside a host call.
type autocompleteSettle struct {
	mu        sync.Mutex
	hostCalls int
	epoch     uint64
	done      bool
	announced bool
	timer     *time.Timer
	announce  func()
}

// arm starts a checkpoint: it announces the request when nothing has happened since.
func (s *autocompleteSettle) arm() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.done || s.announced {
		return
	}
	s.epoch++
	epoch := s.epoch
	if s.timer != nil {
		s.timer.Stop()
	}
	s.timer = time.AfterFunc(autocompleteCheckpoint, func() {
		s.mu.Lock()
		announce := !s.done && !s.announced && s.hostCalls == 0 && s.epoch == epoch
		if announce {
			s.announced = true
		}
		s.mu.Unlock()
		if announce {
			s.announce()
		}
	})
}

// hostCall marks the request as waiting for the host until the returned function runs.
func (s *autocompleteSettle) hostCall() func() {
	s.mu.Lock()
	s.hostCalls++
	s.mu.Unlock()
	return func() {
		s.mu.Lock()
		s.hostCalls--
		s.mu.Unlock()
		s.arm()
	}
}

func (s *autocompleteSettle) finish() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.done = true
	if s.timer != nil {
		s.timer.Stop()
	}
}

func (r *autocompleteRegistry) settleOf(requestID string) *autocompleteSettle {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.suggests[requestID]
}

type autocompleteDescriptor struct {
	ID                string   `json:"id"`
	TriggerCharacters []string `json:"triggerCharacters"`
	HasFileTrigger    bool     `json:"hasFileTrigger"`
}

type autocompleteInvocation struct {
	ID         string                 `json:"id"`
	FactoryID  string                 `json:"factoryId,omitempty"`
	Current    autocompleteDescriptor `json:"current"`
	Operation  string                 `json:"operation"`
	Lines      []string               `json:"lines"`
	CursorLine int                    `json:"cursorLine"`
	CursorCol  int                    `json:"cursorCol"`
	Force      bool                   `json:"force"`
	Item       AutocompleteItem       `json:"item"`
	Prefix     string                 `json:"prefix"`
}

// AddAutocompleteProvider appends a factory and waits until the host installs its rebuilt chain. Headless contexts do not call or retain the factory.
func (c Context) AddAutocompleteProvider(factory AutocompleteProviderFactory) error {
	if !c.HasUI() {
		return nil
	}
	if factory == nil {
		return errors.New("autocomplete factory is missing")
	}
	registry := &c.ext.autocomplete
	id := rand.Text()
	registry.mu.Lock()
	if registry.factories == nil {
		registry.factories = make(map[string]AutocompleteProviderFactory)
	}
	registry.factories[id] = factory
	if registry.invoked == nil {
		registry.invoked = make(map[string]bool)
	}
	registry.mu.Unlock()
	result, err := c.callHost("ui.addAutocompleteProvider", map[string]string{"factoryId": id})
	registry.mu.Lock()
	if !registry.invoked[id] {
		delete(registry.factories, id)
	}
	registry.mu.Unlock()
	return callResultError(result, err)
}

type autocompleteLease struct {
	owner *Extension
	id    string
}
type autocompleteLeaseCleanup struct {
	conn *conn
	id   string
}

func releaseAutocompleteLease(lease autocompleteLeaseCleanup) {
	_ = lease.conn.notify("ui.autocomplete.release", map[string]string{"id": lease.id})
}

func autocompleteCall[T any](ctx Context, lease *autocompleteLease, request autocompleteInvocation) (result T, err error) {
	defer runtime.KeepAlive(lease)
	request.ID = lease.id
	if err = ctx.Err(); err != nil {
		return result, err
	}
	if request.Operation == "getSuggestions" && ctx.requestID != "" {
		if settle := ctx.ext.autocomplete.settleOf(ctx.requestID); settle != nil {
			defer settle.hostCall()()
		}
	}
	if ctx.ext != lease.owner {
		ctx.ext = lease.owner
		ctx.requestID = ""
	}
	response, err := ctx.callHost("ui.autocomplete.invoke", request)
	if err = callResultError(response, err); err != nil {
		return result, err
	}
	if response == nil {
		return result, errors.New("missing autocomplete response")
	}
	err = json.Unmarshal(response.Result, &result)
	return result, err
}

func autocompleteProxy(ctx Context, desc autocompleteDescriptor) *AutocompleteProvider {
	lease := &autocompleteLease{owner: ctx.ext, id: desc.ID}
	runtime.AddCleanup(lease, releaseAutocompleteLease, autocompleteLeaseCleanup{conn: ctx.ext.conn, id: desc.ID})
	provider := &AutocompleteProvider{TriggerCharacters: desc.TriggerCharacters}
	provider.GetSuggestions = func(ctx Context, lines []string, line, col int, force bool) (*AutocompleteSuggestions, error) {
		return autocompleteCall[*AutocompleteSuggestions](ctx, lease, autocompleteInvocation{Operation: "getSuggestions", Lines: lines, CursorLine: line, CursorCol: col, Force: force})
	}
	provider.ApplyCompletion = func(ctx Context, lines []string, line, col int, item AutocompleteItem, prefix string) (AutocompleteCompletion, error) {
		return autocompleteCall[AutocompleteCompletion](ctx, lease, autocompleteInvocation{Operation: "applyCompletion", Lines: lines, CursorLine: line, CursorCol: col, Item: item, Prefix: prefix})
	}
	if desc.HasFileTrigger {
		provider.ShouldTriggerFileCompletion = func(ctx Context, lines []string, line, col int) (bool, error) {
			return autocompleteCall[bool](ctx, lease, autocompleteInvocation{Operation: "shouldTriggerFileCompletion", Lines: lines, CursorLine: line, CursorCol: col})
		}
	}
	return provider
}

func (e *Extension) dispatchAutocomplete(ctx Context, raw json.RawMessage) (any, error) {
	var request autocompleteInvocation
	if err := json.Unmarshal(raw, &request); err != nil {
		return nil, err
	}
	registry := &e.autocomplete
	registry.mu.Lock()
	factory, provider := registry.factories[request.FactoryID], registry.providers[request.ID]
	if request.Operation == "wrap" && factory != nil {
		registry.invoked[request.FactoryID] = true
	}
	registry.mu.Unlock()
	if request.Operation == "wrap" {
		if factory == nil {
			return nil, errors.New("autocomplete factory is no longer available")
		}
		provider, err := factory(ctx, autocompleteProxy(ctx, request.Current))
		if err != nil {
			return nil, err
		}
		if provider == nil || provider.GetSuggestions == nil || provider.ApplyCompletion == nil {
			return nil, errors.New("autocomplete factory must return a provider")
		}
		id := rand.Text()
		registry.mu.Lock()
		if registry.providers == nil {
			registry.providers = make(map[string]*AutocompleteProvider)
		}
		registry.providers[id] = provider
		registry.mu.Unlock()
		return autocompleteDescriptor{ID: id, TriggerCharacters: provider.TriggerCharacters, HasFileTrigger: provider.ShouldTriggerFileCompletion != nil}, nil
	}
	if provider == nil {
		return nil, errors.New("autocomplete provider is no longer available")
	}
	switch request.Operation {
	case "getSuggestions":
		if ctx.requestID != "" {
			settle := &autocompleteSettle{announce: func() {
				_ = e.conn.notify("ui.autocomplete.pending", map[string]string{"requestId": ctx.requestID})
			}}
			registry.mu.Lock()
			if registry.suggests == nil {
				registry.suggests = make(map[string]*autocompleteSettle)
			}
			registry.suggests[ctx.requestID] = settle
			registry.mu.Unlock()
			defer func() {
				settle.finish()
				registry.mu.Lock()
				delete(registry.suggests, ctx.requestID)
				registry.mu.Unlock()
			}()
			settle.arm()
		}
		return provider.GetSuggestions(ctx, request.Lines, request.CursorLine, request.CursorCol, request.Force)
	case "applyCompletion":
		return provider.ApplyCompletion(ctx, request.Lines, request.CursorLine, request.CursorCol, request.Item, request.Prefix)
	case "shouldTriggerFileCompletion":
		if provider.ShouldTriggerFileCompletion == nil {
			return true, nil
		}
		return provider.ShouldTriggerFileCompletion(ctx, request.Lines, request.CursorLine, request.CursorCol)
	default:
		return nil, fmt.Errorf("unknown autocomplete operation %q", request.Operation)
	}
}
