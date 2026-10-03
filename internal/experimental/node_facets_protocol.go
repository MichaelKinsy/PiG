package experimental

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"sync"

	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/internal/chord"
	"github.com/MichaelKinsy/PiG/internal/experimental/services"
)

//go:embed all:node-facets
var nodeFacetAssets embed.FS

type nodeFacetStateUpdate struct {
	ctx      context.Context
	sequence int
	ops      []chord.Op
}
type nodeFacetState struct {
	mu      sync.Mutex
	value   *chord.FacetState
	pending []nodeFacetStateUpdate
	ready   bool
}

func (state *nodeFacetState) apply(ctx context.Context, sequence int, ops []chord.Op) error {
	state.mu.Lock()
	if !state.ready {
		state.pending = append(state.pending, nodeFacetStateUpdate{ctx, sequence, ops})
		state.mu.Unlock()
		return nil
	}
	value := state.value
	state.mu.Unlock()
	return value.Apply(ctx, sequence, ops)
}
func (state *nodeFacetState) install(value any, sequence int) (*chord.FacetState, error) {
	current, err := chord.NewFacetState(value, sequence)
	if err != nil {
		return nil, err
	}
	for {
		state.mu.Lock()
		pending := state.pending
		state.pending = nil
		if len(pending) == 0 {
			state.value = current
			state.ready = true
			state.mu.Unlock()
			return current, nil
		}
		state.mu.Unlock()
		for _, update := range pending {
			if update.sequence > sequence {
				if err := current.Apply(update.ctx, update.sequence, update.ops); err != nil {
					return nil, err
				}
			}
		}
	}
}

// nodeFacetValue is a private capability value inside the existing extension request payload, not an extension protocol envelope or authored format.
type nodeFacetValue struct {
	Kind         string          `json:"kind"`
	Value        json.RawMessage `json:"value,omitempty"`
	Id           string          `json:"id,omitempty"`
	Callable     bool            `json:"callable,omitempty"`
	Asynchronous bool            `json:"asynchronous,omitempty"`
	Promise      bool            `json:"promise,omitempty"`
	Transient    bool            `json:"transient,omitempty"`
	Symbol       bool            `json:"symbol,omitempty"`
	Origin       string          `json:"origin,omitempty"`
	NodeOrigin   string          `json:"nodeOrigin,omitempty"`
	Tag          string          `json:"tag,omitempty"`
	Cancellable  bool            `json:"cancellable,omitempty"`
	// Aborted and AbortReason carry a Context that was already done when the value was encoded, so Node needs no earlier notification to decode it as aborted.
	Aborted     bool   `json:"aborted,omitempty"`
	AbortReason string `json:"abortReason,omitempty"`
	// Context is the host record of a cancellable Context sent to Node. It never crosses the wire.
	Context *nodeFacetContextRecord `json:"-"`
}

// MarshalJSON encodes the wire form. A Context that is done when its value is encoded travels as aborted.
func (value nodeFacetValue) MarshalJSON() ([]byte, error) {
	type wire nodeFacetValue
	if value.Context != nil {
		if err := value.Context.ctx.Err(); err != nil {
			value.Aborted, value.AbortReason = true, nodeFacetAbortReason(value.Context.ctx)
		}
	}
	return json.Marshal(wire(value))
}

func nodeFacetAbortReason(ctx context.Context) string {
	if cause := context.Cause(ctx); cause != nil {
		return cause.Error()
	}
	return "The operation was aborted"
}

// nodeFacetContextRecord tracks one cancellable Context registered with a generation. Node keeps an abort record for the Context until every request that carries it has returned, because such a request may reach Node after the abort notification. The host then tells Node to forget the record.
type nodeFacetContextRecord struct {
	generation *nodeFacetGeneration
	id         string
	ctx        context.Context
	// The fields below are guarded by generation.mu.
	uses      int  // requests in flight that carry the Context
	delivered bool // Node has been told the Context is aborted
	sticky    bool // the Context may reach Node outside a tracked request, so Node keeps its abort record until close
}

type nodeFacetReference struct {
	Id        string `json:"id"`
	Reference string `json:"reference"`
}

type nodeFacetHostValue struct {
	origin       *nodeFacetForeign
	original     nodeFacetValue
	get          func(context.Context, string) (nodeFacetValue, error)
	call         func(context.Context, []nodeFacetValue) (nodeFacetValue, error)
	wait         func(context.Context) (nodeFacetValue, error)
	tag          string
	asynchronous bool
}

type nodeFacetGeneration struct {
	bridge             *subprocess.FacetBridge
	external           FacetBundleExternalResolver
	directory          string
	mu                 sync.Mutex
	nextId             uint64
	host               map[string]nodeFacetHostValue
	closed             bool
	references         int
	environments       map[string]*chord.FacetEnvironment
	environmentViews   map[string]map[string]nodeFacetValue
	slashSubscriptions map[nodeFacetSlashKey]*nodeFacetSlashSubscription
	stateSubscriptions map[nodeFacetReplicaKey]*nodeFacetReplicaSubscription
	states             map[string]*nodeFacetState
	commands           map[*services.SlashCommandRegistrationIdentity]nodeFacetValue
	contexts           map[string]context.Context
	contextCancel      map[string]context.CancelFunc
	contextOrigins     map[string]context.Context
	contextIds         map[context.Context]string
	contextStops       map[string]func() bool
	contextRecords     map[string]*nodeFacetContextRecord
	notifications      sync.WaitGroup
	notifyCtx          context.Context
	notifyCancel       context.CancelFunc
	notifyErrors       []error
	foreign            map[nodeFacetForeign]nodeFacetValue
}
type nodeFacetForeign struct {
	generation *nodeFacetGeneration
	id         string
}

type nodeFacetSlashKey struct {
	service  services.SlashCommands
	callback string
}
type nodeFacetSlashSubscription struct {
	mu     sync.Mutex
	closed bool
	remove func()
	value  nodeFacetValue
	once   sync.Once
}

type nodeFacetReplicaKey struct {
	identity *chord.FacetReplicaIdentity
	callback string
}
type nodeFacetReplicaSubscription struct {
	mu       sync.Mutex
	closed   bool
	remove   func()
	value    nodeFacetValue
	latest   chord.JsonValue
	sequence int
	hydrated bool
}

func openNodeFacetGeneration(ctx context.Context, call func(context.Context, *nodeFacetGeneration, string, json.RawMessage) (json.RawMessage, error)) (_ *nodeFacetGeneration, err error) {
	directory, err := os.MkdirTemp("", "pig-facet-assets-")
	if err != nil {
		return nil, err
	}
	generation := &nodeFacetGeneration{references: 1, directory: directory, host: map[string]nodeFacetHostValue{}, environments: map[string]*chord.FacetEnvironment{}, states: map[string]*nodeFacetState{}, commands: map[*services.SlashCommandRegistrationIdentity]nodeFacetValue{}, contexts: map[string]context.Context{}, contextCancel: map[string]context.CancelFunc{}, contextOrigins: map[string]context.Context{}, contextIds: map[context.Context]string{}, foreign: map[nodeFacetForeign]nodeFacetValue{}}
	generation.environmentViews = map[string]map[string]nodeFacetValue{}
	generation.stateSubscriptions = map[nodeFacetReplicaKey]*nodeFacetReplicaSubscription{}
	generation.slashSubscriptions = map[nodeFacetSlashKey]*nodeFacetSlashSubscription{}
	generation.notifyCtx, generation.notifyCancel = context.WithCancel(context.Background())
	generation.contextStops = map[string]func() bool{}
	generation.contextRecords = map[string]*nodeFacetContextRecord{}
	defer func() {
		if err != nil {
			generation.notifyCancel()
			err = errors.Join(err, os.RemoveAll(directory))
		}
	}()
	if err := fs.WalkDir(nodeFacetAssets, "node-facets", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel("node-facets", path)
		if err != nil {
			return err
		}
		target := filepath.Join(directory, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		contents, err := nodeFacetAssets.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, contents, 0o600)
	}); err != nil {
		return nil, err
	}
	generation.bridge, err = subprocess.OpenFacetBridge(ctx, subprocess.FacetBridgeOptions{Module: filepath.Join(directory, "driver.mjs"), Call: func(ctx context.Context, method string, args json.RawMessage) (json.RawMessage, error) {
		return call(ctx, generation, method, args)
	}})
	if err != nil {
		return nil, err
	}
	return generation, nil
}

func (generation *nodeFacetGeneration) ownHost(value nodeFacetHostValue) nodeFacetValue {
	generation.mu.Lock()
	defer generation.mu.Unlock()
	generation.nextId++
	id := strconv.FormatUint(generation.nextId, 10)
	generation.host[id] = value
	return nodeFacetValue{Kind: "host", Id: id, Callable: value.call != nil, Asynchronous: value.asynchronous, Promise: value.wait != nil, Tag: value.tag}
}

func (generation *nodeFacetGeneration) hostValue(id string) (nodeFacetHostValue, error) {
	generation.mu.Lock()
	defer generation.mu.Unlock()
	if generation.closed {
		return nodeFacetHostValue{}, errors.New("Facet generation is closed")
	}
	value, exists := generation.host[id]
	if !exists {
		return nodeFacetHostValue{}, fmt.Errorf("Facet host reference %s is released", id)
	}
	return value, nil
}

func (generation *nodeFacetGeneration) request(ctx context.Context, synchronous bool, args any, result any) error {
	defer leaseNodeFacetContexts(args)()
	payload, err := json.Marshal(args)
	if err != nil {
		return err
	}
	var raw json.RawMessage
	if synchronous {
		raw, err = generation.bridge.CallSync(ctx, payload)
	} else {
		raw, err = generation.bridge.Call(ctx, payload)
	}
	if err != nil {
		return err
	}
	if result == nil {
		return nil
	}
	return json.Unmarshal(raw, result)
}

func (generation *nodeFacetGeneration) invoke(ctx context.Context, synchronous bool, reference nodeFacetValue, receiver *nodeFacetValue, args []nodeFacetValue) (nodeFacetValue, error) {
	return generation.invokeRequest(ctx, synchronous, false, reference, receiver, args)
}

// invokeObservation calls a keyed observer handler synchronously. An object or function result returns as a private Promise reference that only the observation's "await" or release drops.
func (generation *nodeFacetGeneration) invokeObservation(ctx context.Context, reference nodeFacetValue, args []nodeFacetValue) (nodeFacetValue, error) {
	return generation.invokeRequest(ctx, true, true, reference, nil, args)
}

func (generation *nodeFacetGeneration) invokeRequest(ctx context.Context, synchronous, observation bool, reference nodeFacetValue, receiver *nodeFacetValue, args []nodeFacetValue) (nodeFacetValue, error) {
	if reference.Kind == "host" {
		value, err := generation.hostValue(reference.Id)
		if err != nil {
			return nodeFacetValue{}, err
		}
		if value.call == nil {
			return nodeFacetValue{}, errors.New("Facet reference is not callable")
		}
		return value.call(ctx, args)
	}
	if reference.Kind != "node" || !reference.Callable {
		return nodeFacetValue{}, errors.New("Facet reference is not callable")
	}
	var result nodeFacetValue
	err := generation.request(ctx, synchronous, struct {
		Op          string           `json:"op"`
		Id          string           `json:"id"`
		Receiver    *nodeFacetValue  `json:"receiver,omitempty"`
		Args        []nodeFacetValue `json:"args"`
		Observation bool             `json:"observation,omitempty"`
	}{"invoke", reference.Id, receiver, args, observation}, &result)
	return result, err
}

func (generation *nodeFacetGeneration) invokeJSON(ctx context.Context, reference nodeFacetValue, receiver *nodeFacetValue, args []nodeFacetValue) (json.RawMessage, error) {
	if reference.Kind == "host" {
		value, err := generation.invoke(ctx, false, reference, receiver, args)
		if err != nil {
			return nil, err
		}
		return generation.jsonValue(ctx, value)
	}
	if reference.Kind != "node" || !reference.Callable {
		return nil, errors.New("Facet reference is not callable")
	}
	var value nodeFacetValue
	err := generation.request(ctx, false, struct {
		Op       string           `json:"op"`
		Id       string           `json:"id"`
		Receiver *nodeFacetValue  `json:"receiver,omitempty"`
		Args     []nodeFacetValue `json:"args"`
	}{"invokeJSON", reference.Id, receiver, args}, &value)
	if err != nil {
		return nil, err
	}
	return generation.jsonValue(ctx, value)
}

func (generation *nodeFacetGeneration) jsonValue(ctx context.Context, value nodeFacetValue) (json.RawMessage, error) {
	if value.Kind == "node" {
		var result nodeFacetValue
		if err := generation.request(ctx, true, struct {
			Op string `json:"op"`
			Id string `json:"id"`
		}{"json", value.Id}, &result); err != nil {
			return nil, err
		}
		value = result
	}
	switch value.Kind {
	case "undefined":
		return nil, nil
	case "json":
		return value.Value, nil
	default:
		return nil, errors.New("Facet value is not strict JSON")
	}
}

func nodeFacetJSON(value any) (nodeFacetValue, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nodeFacetValue{}, err
	}
	return nodeFacetValue{Kind: "json", Value: raw}, nil
}

func (generation *nodeFacetGeneration) adoptValue(ctx context.Context, source *nodeFacetGeneration, value nodeFacetValue) (nodeFacetValue, error) {
	if source == generation || value.Kind == "json" || value.Kind == "undefined" || value.Kind == "number" || value.Kind == "bigint" {
		return value, nil
	}
	if value.Kind == "context" {
		source.mu.Lock()
		original := source.contexts[value.Id]
		source.mu.Unlock()
		if original == nil {
			return nodeFacetValue{}, errors.New("Facet Context is released")
		}
		return generation.stickyContext(generation.contextValue(original)), nil
	}
	if value.Kind == "host" {
		owned, err := source.hostValue(value.Id)
		if err != nil {
			return nodeFacetValue{}, err
		}
		if owned.origin != nil {
			return generation.adoptValue(ctx, owned.origin.generation, owned.original)
		}
		return nodeFacetValue{}, errors.New("Cross-generation native facet capability requires an explicit adapter")
	}
	if value.Kind != "node" {
		return nodeFacetValue{}, errors.New("Cross-generation facet reference has no source object")
	}
	key := nodeFacetForeign{source, value.Id}
	generation.mu.Lock()
	existing, ok := generation.foreign[key]
	generation.mu.Unlock()
	if ok {
		return existing, nil
	}
	owned := nodeFacetHostValue{origin: &key, original: value, get: func(ctx context.Context, name string) (nodeFacetValue, error) {
		member, err := source.property(ctx, value, name)
		if err != nil {
			return nodeFacetValue{}, err
		}
		return generation.adoptValue(ctx, source, member)
	}}
	if value.Promise {
		owned.wait = func(ctx context.Context) (nodeFacetValue, error) {
			var result nodeFacetValue
			if err := source.request(ctx, false, map[string]any{"op": "await", "id": value.Id}, &result); err != nil {
				return nodeFacetValue{}, err
			}
			return generation.adoptValue(ctx, source, result)
		}
	}
	if value.Callable {
		owned.asynchronous = value.Asynchronous
		owned.call = func(ctx context.Context, args []nodeFacetValue) (nodeFacetValue, error) {
			translated := make([]nodeFacetValue, len(args))
			for i, arg := range args {
				result, err := source.adoptValue(ctx, generation, arg)
				if err != nil {
					return nodeFacetValue{}, err
				}
				translated[i] = result
			}
			result, err := source.invoke(ctx, !value.Asynchronous, value, nil, translated)
			if err != nil {
				return nodeFacetValue{}, err
			}
			return generation.adoptValue(ctx, source, result)
		}
	}
	result := generation.ownHost(owned)
	generation.mu.Lock()
	generation.foreign[key] = result
	generation.mu.Unlock()
	return result, nil
}

// releaseNodeValues drops Node-side references the host no longer uses. Node ignores ids it already released.
func (generation *nodeFacetGeneration) releaseNodeValues(ids ...string) {
	generation.mu.Lock()
	closed := generation.closed
	generation.mu.Unlock()
	if closed {
		return
	}
	err := generation.request(generation.notifyCtx, true, map[string]any{"op": "release", "ids": ids}, nil)
	generation.mu.Lock()
	if err != nil && !generation.closed {
		generation.notifyErrors = append(generation.notifyErrors, err)
	}
	generation.mu.Unlock()
}

func (generation *nodeFacetGeneration) contextValue(ctx context.Context) nodeFacetValue {
	if ctx == nil {
		ctx = context.Background()
	}
	generation.mu.Lock()
	id := ""
	comparable := reflect.ValueOf(ctx).Comparable()
	if comparable {
		id = generation.contextIds[ctx]
	}
	if id == "" {
		generation.nextId++
		id = strconv.FormatUint(generation.nextId, 10)
		generation.contextOrigins[id] = ctx
		if ctx.Done() != nil && !generation.closed {
			record := &nodeFacetContextRecord{generation: generation, id: id, ctx: ctx}
			generation.contextRecords[id] = record
			// The registration owns one notification slot: the callback releases it, or close does when the callback never ran.
			generation.notifications.Add(1)
			generation.contextStops[id] = context.AfterFunc(ctx, func() {
				defer generation.notifications.Done()
				generation.mu.Lock()
				if generation.closed {
					generation.mu.Unlock()
					return
				}
				generation.mu.Unlock()
				err := generation.request(generation.notifyCtx, true, map[string]any{"op": "contextAbort", "id": id, "reason": nodeFacetAbortReason(ctx)}, nil)
				generation.mu.Lock()
				if err != nil && !generation.closed {
					generation.notifyErrors = append(generation.notifyErrors, err)
				}
				// A finished Context is never sent under this id again.
				delete(generation.contextOrigins, id)
				if comparable {
					delete(generation.contextIds, ctx)
				}
				delete(generation.contextStops, id)
				record.delivered = true
				forget := generation.retireContextRecord(record)
				generation.mu.Unlock()
				if forget {
					generation.forgetContext(id)
				}
			})
		}
		if comparable {
			generation.contextIds[ctx] = id
		}
	}
	record := generation.contextRecords[id]
	generation.mu.Unlock()
	value := nodeFacetValue{Kind: "context", Origin: id, Cancellable: ctx.Done() != nil, Context: record}
	if origin, ok := ctx.Value(nodeFacetContextKey{}).(nodeFacetContextOrigin); ok && origin.generation == generation {
		value.NodeOrigin = origin.id
	}
	return value
}

// retireContextRecord reports whether Node's abort record for an aborted Context can be forgotten: no request that carries the Context is in flight. The caller holds generation.mu.
func (generation *nodeFacetGeneration) retireContextRecord(record *nodeFacetContextRecord) bool {
	if !record.delivered || record.uses != 0 || record.sticky || generation.closed {
		return false
	}
	if generation.contextRecords[record.id] != record {
		return false
	}
	delete(generation.contextRecords, record.id)
	return true
}

// forgetContext tells Node to drop the abort record of a Context that no request can still carry.
func (generation *nodeFacetGeneration) forgetContext(id string) {
	err := generation.request(generation.notifyCtx, true, map[string]any{"op": "contextForget", "id": id}, nil)
	generation.mu.Lock()
	if err != nil && !generation.closed {
		generation.notifyErrors = append(generation.notifyErrors, err)
	}
	generation.mu.Unlock()
}

// stickyContext keeps Node's abort record for a Context that reaches Node in a host call result, whose delivery no request scope covers.
func (generation *nodeFacetGeneration) stickyContext(value nodeFacetValue) nodeFacetValue {
	if value.Context != nil {
		generation.mu.Lock()
		value.Context.sticky = true
		generation.mu.Unlock()
	}
	return value
}

var nodeFacetContextRecordType = reflect.TypeFor[*nodeFacetContextRecord]()

// leaseNodeFacetContexts holds the abort record of every Context in args until the returned function runs, which the caller does when the request has returned. Node decodes the arguments of a request only when it receives the request, which can follow the abort notification of a Context that was cancelled after the request was built.
func leaseNodeFacetContexts(args any) func() {
	var records []*nodeFacetContextRecord
	collectNodeFacetContextRecords(reflect.ValueOf(args), &records)
	if len(records) == 0 {
		return func() {}
	}
	for _, record := range records {
		record.generation.mu.Lock()
		record.uses++
		record.generation.mu.Unlock()
	}
	return func() {
		for _, record := range records {
			generation := record.generation
			generation.mu.Lock()
			record.uses--
			forget := generation.retireContextRecord(record)
			generation.mu.Unlock()
			if forget {
				generation.forgetContext(record.id)
			}
		}
	}
}

func collectNodeFacetContextRecords(value reflect.Value, records *[]*nodeFacetContextRecord) {
	switch value.Kind() {
	case reflect.Pointer:
		if value.IsNil() {
			return
		}
		if value.Type() == nodeFacetContextRecordType {
			*records = append(*records, value.Interface().(*nodeFacetContextRecord))
			return
		}
		collectNodeFacetContextRecords(value.Elem(), records)
	case reflect.Interface:
		if !value.IsNil() {
			collectNodeFacetContextRecords(value.Elem(), records)
		}
	case reflect.Struct:
		for i := range value.NumField() {
			if value.Type().Field(i).IsExported() {
				collectNodeFacetContextRecords(value.Field(i), records)
			}
		}
	case reflect.Slice, reflect.Array:
		if value.Type().Elem().Kind() == reflect.Uint8 {
			return
		}
		for i := range value.Len() {
			collectNodeFacetContextRecords(value.Index(i), records)
		}
	case reflect.Map:
		for iterator := value.MapRange(); iterator.Next(); {
			collectNodeFacetContextRecords(iterator.Value(), records)
		}
	}
}

type nodeFacetContextKey struct{}
type nodeFacetContextOrigin struct {
	generation *nodeFacetGeneration
	id         string
}

type nodeFacetCallContext struct {
	context.Context
	business context.Context
}

func (ctx nodeFacetCallContext) Value(key any) any {
	if _, ok := key.(nodeFacetContextKey); ok {
		return ctx.business.Value(key)
	}
	if value := ctx.Context.Value(key); value != nil {
		return value
	}
	return ctx.business.Value(key)
}

func (generation *nodeFacetGeneration) operationContext(parent context.Context, value nodeFacetValue) (context.Context, func(), error) {
	if value.Kind != "context" {
		return nil, nil, errors.New("Remote service method requires a trailing Context")
	}
	generation.mu.Lock()
	business := generation.contexts[value.Id]
	generation.mu.Unlock()
	if business == nil {
		return nil, nil, errors.New("Facet Context is released")
	}
	ctx, cancel := context.WithCancel(parent)
	stop := context.AfterFunc(business, cancel)
	if business.Err() != nil {
		cancel()
	}
	return nodeFacetCallContext{ctx, business}, func() { stop(); cancel() }, nil
}

func (generation *nodeFacetGeneration) release(ctx context.Context) error {
	generation.mu.Lock()
	generation.references--
	last := generation.references == 0
	generation.mu.Unlock()
	if last {
		return generation.close(ctx)
	}
	return nil
}

func (generation *nodeFacetGeneration) close(ctx context.Context) error {
	generation.mu.Lock()
	if generation.closed {
		generation.mu.Unlock()
		return nil
	}
	generation.closed = true
	for _, stop := range generation.contextStops {
		if stop() {
			generation.notifications.Done()
		}
	}
	generation.mu.Unlock()
	generation.notifyCancel()
	generation.notifications.Wait()
	disposeErr := generation.request(ctx, false, struct {
		Op string `json:"op"`
	}{"close"}, nil)
	transportErr := generation.bridge.Close()
	generation.mu.Lock()
	clear(generation.host)
	clear(generation.environments)
	clear(generation.environmentViews)
	clear(generation.slashSubscriptions)
	clear(generation.stateSubscriptions)
	clear(generation.commands)
	clear(generation.foreign)
	clear(generation.states)
	for _, cancel := range generation.contextCancel {
		if cancel != nil {
			cancel()
		}
	}
	clear(generation.contextCancel)
	clear(generation.contexts)
	clear(generation.contextOrigins)
	clear(generation.contextIds)
	clear(generation.contextStops)
	clear(generation.contextRecords)
	notificationErr := errors.Join(generation.notifyErrors...)
	generation.notifyErrors = nil
	generation.mu.Unlock()
	return errors.Join(disposeErr, transportErr, notificationErr, os.RemoveAll(generation.directory))
}
