package subprocess

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"runtime"
	"weak"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

type autocompleteReference struct {
	owner    *Conn
	name     string
	provider *extension.AutocompleteProvider
}

type autocompleteOrigin struct {
	owner *Conn
	id    string
}

type autocompleteQueryKey struct {
	owner *Conn
	id    string
}

type autocompleteQuery struct {
	ctx    context.Context
	cancel context.CancelFunc
}

func (b *UIBridge) autocompleteReference(name string, owner *Conn, provider *extension.AutocompleteProvider) autocompleteDescriptor {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.autocompleteReferences == nil {
		b.autocompleteReferences = make(map[string]autocompleteReference)
	}
	id := rand.Text()
	b.autocompleteReferences[id] = autocompleteReference{owner: owner, name: name, provider: provider}
	for key := range b.autocompleteOrigins {
		if key.Value() == nil {
			delete(b.autocompleteOrigins, key)
		}
	}
	return autocompleteDescriptor{ID: id, LocalID: b.autocompleteOrigins[weak.Make(provider)].id, TriggerCharacters: provider.TriggerCharacters, HasFileTrigger: provider.ShouldTriggerFileCompletion != nil}
}

func autocompleteInvoke(ctx context.Context, provider *extension.AutocompleteProvider, request autocompleteInvocation) (any, error) {
	switch request.Operation {
	case "getSuggestions":
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

func (b *UIBridge) handleAutocompleteInvoke(ctx context.Context, owner *Conn, args json.RawMessage) (*CallResultPayload, error) {
	var request autocompleteInvocation
	if err := json.Unmarshal(args, &request); err != nil {
		return nil, err
	}
	b.mu.RLock()
	ref, ok := b.autocompleteReferences[request.ID]
	b.mu.RUnlock()
	if !ok || ref.owner != owner {
		return nil, errors.New("autocomplete provider is no longer available")
	}
	// A captured provider can call a preceding subprocess provider which can itself make host calls.
	if request.QueryID != "" {
		key := autocompleteQueryKey{owner: owner, id: request.QueryID}
		b.mu.RLock()
		query := b.autocompleteQueries[key]
		b.mu.RUnlock()
		if query != nil {
			var cancel context.CancelFunc
			ctx, cancel = context.WithCancel(ctx)
			stop := context.AfterFunc(query.ctx, cancel)
			if query.ctx.Err() != nil {
				cancel()
			}
			defer func() { stop(); cancel() }()
		}
	}
	extension.CallInitiated(ctx)
	value, err := autocompleteInvoke(ctx, ref.provider, request)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(value)
	return &CallResultPayload{Result: data}, err
}

func autocompleteRequest(ctx context.Context, conn *Conn, method string, args any, result any) error {
	data, err := json.Marshal(args)
	if err != nil {
		return err
	}
	response, err := conn.Request(ctx, &Envelope{Type: MsgRequest, Request: &RequestPayload{Method: method, Args: data}})
	if err != nil {
		return err
	}
	if response == nil || response.Response == nil {
		return errors.New("missing autocomplete response")
	}
	if response.Response.Error != nil {
		return response.Response.Error.ToError()
	}
	if result == nil {
		return nil
	}
	return json.Unmarshal(response.Response.Result, result)
}

func (b *UIBridge) handleAddAutocompleteProvider(ctx context.Context, name string, owner *Conn, ui extension.UIContext, args json.RawMessage) (*CallResultPayload, error) {
	var request struct {
		FactoryID string `json:"factoryId"`
	}
	if err := json.Unmarshal(args, &request); err != nil {
		return nil, err
	}
	if request.FactoryID == "" {
		return nil, errors.New("autocomplete factory is missing")
	}
	if owner == nil {
		b.mu.RLock()
		owner = b.extConns[name]
		b.mu.RUnlock()
	}
	if owner == nil {
		return nil, errors.New("autocomplete extension is disconnected")
	}
	extension.CallInitiated(ctx)
	factory := extension.AutocompleteProviderFactory(func(ctx context.Context, current *extension.AutocompleteProvider) (*extension.AutocompleteProvider, error) {
		currentRef := b.autocompleteReference(name, owner, current)
		var desc autocompleteDescriptor
		if err := autocompleteRequest(ctx, owner, "autocomplete.sync", map[string]any{"operation": "wrap", "factoryId": request.FactoryID, "current": currentRef}, &desc); err != nil {
			return nil, err
		}
		if desc.ID == "" {
			return nil, errors.New("autocomplete factory returned no provider")
		}
		lease := &autocompleteRemoteLease{owner: owner, id: desc.ID}
		runtime.AddCleanup(lease, releaseRemoteAutocomplete, autocompleteOrigin{owner: owner, id: desc.ID})
		provider := &extension.AutocompleteProvider{TriggerCharacters: desc.TriggerCharacters}
		provider.GetSuggestions = func(ctx context.Context, lines []string, line, col int, force bool) (*extension.AutocompleteSuggestions, error) {
			var result *extension.AutocompleteSuggestions
			err := lease.request(ctx, "autocomplete.suggest", autocompleteInvocation{Operation: "getSuggestions", Lines: lines, CursorLine: line, CursorCol: col, Force: force}, &result)
			return result, err
		}
		provider.ApplyCompletion = func(ctx context.Context, lines []string, line, col int, item extension.AutocompleteItem, prefix string) (extension.AutocompleteCompletion, error) {
			var result extension.AutocompleteCompletion
			err := lease.request(ctx, "autocomplete.sync", autocompleteInvocation{Operation: "applyCompletion", Lines: lines, CursorLine: line, CursorCol: col, Item: item, Prefix: prefix}, &result)
			return result, err
		}
		if desc.HasFileTrigger {
			provider.ShouldTriggerFileCompletion = func(ctx context.Context, lines []string, line, col int) (bool, error) {
				var result bool
				err := lease.request(ctx, "autocomplete.sync", autocompleteInvocation{Operation: "shouldTriggerFileCompletion", Lines: lines, CursorLine: line, CursorCol: col}, &result)
				return result, err
			}
		}
		b.mu.Lock()
		if b.autocompleteOrigins == nil {
			b.autocompleteOrigins = make(map[weak.Pointer[extension.AutocompleteProvider]]autocompleteOrigin)
		}
		b.autocompleteOrigins[weak.Make(provider)] = autocompleteOrigin{owner: owner, id: desc.ID}
		b.mu.Unlock()
		return provider, nil
	})
	var err error
	if scoped, ok := ui.(interface {
		AddAutocompleteProviderWithLifetime(context.Context, extension.AutocompleteProviderFactory) error
	}); ok {
		err = scoped.AddAutocompleteProviderWithLifetime(owner.lifetime, factory)
	} else {
		err = ui.AddAutocompleteProvider(factory)
	}
	if err != nil {
		return nil, err
	}
	return &CallResultPayload{}, nil
}

type autocompleteRemoteLease struct {
	owner *Conn
	id    string
}

func (l *autocompleteRemoteLease) request(ctx context.Context, method string, args autocompleteInvocation, result any) error {
	defer runtime.KeepAlive(l)
	args.ID = l.id
	return autocompleteRequest(ctx, l.owner, method, args, result)
}
func releaseRemoteAutocomplete(origin autocompleteOrigin) {
	data, _ := json.Marshal(map[string]string{"id": origin.id})
	// The connection's close path releases all owned references if this advisory release cannot be delivered.
	_ = origin.owner.Send(&Envelope{Type: MsgNotify, Notify: &NotifyPayload{Method: "autocomplete.release", Args: data}})
}
func (b *UIBridge) releaseAutocompleteReference(owner *Conn, args json.RawMessage) {
	var request struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(args, &request) != nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if ref, ok := b.autocompleteReferences[request.ID]; ok && ref.owner == owner {
		delete(b.autocompleteReferences, request.ID)
	}
}

func autocompleteCallKey(owner *Conn, call *CallPayload) (autocompleteQueryKey, bool) {
	if call == nil || call.Method != "ui.autocomplete.invoke" {
		return autocompleteQueryKey{}, false
	}
	var request struct {
		QueryID string `json:"queryId"`
	}
	if json.Unmarshal(call.Args, &request) != nil || request.QueryID == "" {
		return autocompleteQueryKey{}, false
	}
	return autocompleteQueryKey{owner: owner, id: request.QueryID}, true
}

// Reserve on the receive loop before dispatch, so cancellation on another call lane cannot overtake query registration.
func (b *UIBridge) reserveAutocompleteCall(owner *Conn, call *CallPayload) {
	key, ok := autocompleteCallKey(owner, call)
	if !ok {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.autocompleteQueries == nil {
		b.autocompleteQueries = make(map[autocompleteQueryKey]*autocompleteQuery)
	}
	if old := b.autocompleteQueries[key]; old != nil {
		old.cancel()
	}
	b.autocompleteQueries[key] = &autocompleteQuery{ctx: ctx, cancel: cancel}
}
func (b *UIBridge) releaseAutocompleteCall(owner *Conn, call *CallPayload) {
	key, ok := autocompleteCallKey(owner, call)
	if !ok {
		return
	}
	b.mu.Lock()
	query := b.autocompleteQueries[key]
	delete(b.autocompleteQueries, key)
	b.mu.Unlock()
	if query != nil {
		query.cancel()
	}
}

func (b *UIBridge) handleAutocompleteCancel(owner *Conn, args json.RawMessage) (*CallResultPayload, error) {
	var request struct {
		QueryID string `json:"queryId"`
	}
	if err := json.Unmarshal(args, &request); err != nil {
		return nil, err
	}
	b.mu.RLock()
	query := b.autocompleteQueries[autocompleteQueryKey{owner: owner, id: request.QueryID}]
	b.mu.RUnlock()
	if query != nil {
		query.cancel()
	}
	return &CallResultPayload{}, nil
}

type autocompleteProviderSource interface {
	AutocompleteProvider(context.Context) (*extension.AutocompleteProvider, error)
}

func (b *UIBridge) handleAutocompleteCurrent(ctx context.Context, name string, owner *Conn, ui extension.UIContext) (*CallResultPayload, error) {
	source, ok := ui.(autocompleteProviderSource)
	if !ok {
		return &CallResultPayload{Result: json.RawMessage("null")}, nil
	}
	provider, err := source.AutocompleteProvider(ctx)
	if err != nil {
		return nil, err
	}
	if provider == nil {
		return &CallResultPayload{Result: json.RawMessage("null")}, nil
	}
	data, err := json.Marshal(b.autocompleteReference(name, owner, provider))
	return &CallResultPayload{Result: data}, err
}

// SyncAutocomplete gives an installed subprocess editor the same provider as the default editor. It runs off the UI loop and awaits the editor's synchronous setter.
func (b *UIBridge) SyncAutocomplete(ctx context.Context, provider *extension.AutocompleteProvider) error {
	b.mu.RLock()
	editor := b.editor
	b.mu.RUnlock()
	if editor == nil || !editor.live() {
		return nil
	}
	desc := b.autocompleteReference(editor.ext, editor.conn, provider)
	return autocompleteRequest(ctx, editor.conn, "autocomplete.sync", map[string]any{"operation": "changed", "current": desc}, nil)
}
