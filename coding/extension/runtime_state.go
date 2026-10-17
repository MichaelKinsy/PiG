package extension

// Ports packages/coding-agent/src/core/extensions/loader.ts createExtensionRuntime (assertActive, invalidate, pendingNativeProviderRegistrations).

import (
	"context"
	"errors"
	"slices"
	"sync"

	"github.com/MichaelKinsy/PiG/ai"
)

// DefaultStaleRuntimeMessage is the error an invalidated runtime reports when Invalidate gets no message.
const DefaultStaleRuntimeMessage = "This extension ctx is stale after session replacement or reload. Do not use a captured pi or command ctx after ctx.newSession(), ctx.fork(), ctx.switchSession(), or ctx.reload(). For newSession, fork, and switchSession, move post-replacement work into withSession and use the ctx passed to withSession. For reload, do not use the old ctx after await ctx.reload()."

// PendingNativeProviderRegistration retains a native provider and its owning extension until the registry is bound. Provider is Pi's Provider
// object: the one a compiled-in extension registered, or the object assembled from the carrier of a subprocess extension's provider.
// upstream: runner.ts:481-497 (pendingNativeProviderRegistrations: { provider: Provider; extensionPath: string }[])
type PendingNativeProviderRegistration struct {
	Provider      *ai.ModelsProvider
	ExtensionPath string
	carrier       *NativeProvider
}

// AssertActive returns the stale message as an error once the runtime was invalidated, and nil before.
// upstream: loader.ts assertActive
func (r *ExtensionRuntime) AssertActive() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.staleMessage != "" {
		return errors.New(r.staleMessage)
	}
	return nil
}

// FlagValue returns the value of an extension flag and whether the flag has one.
// upstream: loader.ts:329-337 (runtime.flagValues.has/get)
func (r *ExtensionRuntime) FlagValue(name string) (any, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	value, has := r.FlagValues[name]
	return value, has
}

// SetFlagDefault sets a flag's value unless the flag already has one.
// upstream: loader.ts:408-412, :538-540 (flagValues.set for a registered default)
func (r *ExtensionRuntime) SetFlagDefault(name string, value any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, has := r.FlagValues[name]; !has {
		r.FlagValues[name] = value
	}
}

// ReplaceFactoryAPI records retire as the way to make the API of the compiled-in extension loaded from path stale, and retires the API a
// previous load of the same path left. A reload runs a built-in factory again on the runtime the replacement runner keeps, so the API
// the earlier run captured stops working with the stale message, as upstream's runtime.invalidate makes the old runtime's API fail.
//
// upstream: loader.ts:193-203 (invalidate), agent-session.ts _buildRuntime (a reload creates a new runtime and runs every factory again)
func (r *ExtensionRuntime) ReplaceFactoryAPI(path string, retire func(message string)) {
	r.mu.Lock()
	previous := r.factoryAPIs[path]
	if r.factoryAPIs == nil {
		r.factoryAPIs = map[string]func(string){}
	}
	r.factoryAPIs[path] = retire
	r.mu.Unlock()
	if previous != nil {
		previous(DefaultStaleRuntimeMessage)
	}
}

// Invalidate marks the runtime stale after runtime replacement or reload. Only the first call takes effect; an empty message selects [DefaultStaleRuntimeMessage].
// upstream: loader.ts invalidate
func (r *ExtensionRuntime) Invalidate(message ...string) {
	r.mu.Lock()
	if r.staleMessage != "" {
		r.mu.Unlock()
		return
	}
	r.staleMessage = DefaultStaleRuntimeMessage
	if len(message) != 0 && message[0] != "" {
		r.staleMessage = message[0]
	}
	tracked := r.eventBusUnsubscribers
	r.eventBusUnsubscribers = nil
	r.mu.Unlock()
	for _, entry := range tracked {
		entry.run()
	}
}

// eventBusSubscription is one retained event-bus subscription; run is idempotent.
type eventBusSubscription struct {
	once        sync.Once
	unsubscribe func()
	runtime     *ExtensionRuntime
}

func (e *eventBusSubscription) run() {
	e.once.Do(func() {
		e.runtime.mu.Lock()
		e.runtime.eventBusUnsubscribers = slices.DeleteFunc(e.runtime.eventBusUnsubscribers, func(other *eventBusSubscription) bool { return other == e })
		e.runtime.mu.Unlock()
		e.unsubscribe()
	})
}

// TrackEventBusSubscription retains an event-bus subscription until the runtime is invalidated: Invalidate runs every retained unsubscribe, in subscription order. It returns the unsubscribe function that runs the original once and forgets it.
// upstream: loader.ts:201-210 trackEventBusSubscription, :198-199 invalidate
func (r *ExtensionRuntime) TrackEventBusSubscription(unsubscribe func()) func() {
	entry := &eventBusSubscription{unsubscribe: unsubscribe, runtime: r}
	r.mu.Lock()
	r.eventBusUnsubscribers = append(r.eventBusUnsubscribers, entry)
	r.mu.Unlock()
	return entry.run
}

// RegisterNativeProvider queues a Provider object while extensions load and applies it to the bound registry afterward, as upstream's registerNativeProvider does
// before and after bindCore. extensionPath defaults to "<unknown>".
// upstream: loader.ts:215 registerNativeProvider, runner.ts:481-497
func (r *ExtensionRuntime) RegisterNativeProvider(ctx context.Context, provider *ai.ModelsProvider, extensionPath ...string) error {
	return r.registerNativeProvider(ctx, provider, NativeProviderOf(provider), extensionPath)
}

// RegisterNativeProviderCarrier is RegisterNativeProvider for a Provider object that lives in a subprocess extension: the carrier holds the object's
// members with the operation's context.Context each callback receives, and the pending registration exposes the object assembled from it.
func (r *ExtensionRuntime) RegisterNativeProviderCarrier(ctx context.Context, carrier *NativeProvider, extensionPath ...string) error {
	return r.registerNativeProvider(ctx, carrier.ProviderObject(), carrier, extensionPath)
}

func (r *ExtensionRuntime) registerNativeProvider(ctx context.Context, provider *ai.ModelsProvider, carrier *NativeProvider, extensionPath []string) error {
	path := "<unknown>"
	if len(extensionPath) != 0 {
		path = extensionPath[0]
	}
	r.mu.Lock()
	if !r.nativeBound {
		r.pendingNativeProviders = append(r.pendingNativeProviders, PendingNativeProviderRegistration{Provider: provider, ExtensionPath: path, carrier: carrier})
		r.mu.Unlock()
		return nil
	}
	actions := r.providerActions
	r.mu.Unlock()
	return actions.registerNative(ctx, provider, carrier)
}

// PendingNativeProviderRegistrations returns the queued native providers in registration order.
func (r *ExtensionRuntime) PendingNativeProviderRegistrations() []PendingNativeProviderRegistration {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.pendingNativeProviders)
}

func (r *ExtensionRuntime) bindNativeProviders(actions ProviderActions, report func(*ExtensionError)) {
	r.mu.Lock()
	r.nativeBound = false
	r.mu.Unlock()
	// upstream: runner.ts:481-497 flushes the live array, so a registration queued by a callback is flushed too.
	for i := 0; ; i++ {
		r.mu.Lock()
		if i == len(r.pendingNativeProviders) {
			r.pendingNativeProviders = nil
			r.providerActions.RegisterNativeProvider = actions.RegisterNativeProvider
			r.providerActions.RegisterNativeProviderCarrier = actions.RegisterNativeProviderCarrier
			r.nativeBound = true
			r.mu.Unlock()
			return
		}
		entry := r.pendingNativeProviders[i]
		r.mu.Unlock()
		if err := actions.registerNative(context.Background(), entry.Provider, entry.carrier); err != nil && report != nil {
			report(&ExtensionError{ExtensionPath: entry.ExtensionPath, Event: "register_provider", Error: err.Error(), Stack: ErrorStack(err)})
		}
	}
}
