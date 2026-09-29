package extension

// Ports packages/coding-agent/src/core/extensions/loader.ts
// Ports packages/coding-agent/src/core/extensions/runner.ts

import (
	"slices"
	"sync"
)

// PendingProviderRegistration retains the configuration and its owning extension until the registry is bound.
type PendingProviderRegistration struct {
	Name          string
	Config        ProviderConfig
	ExtensionPath string
}

type providerRegistrationQueue struct {
	entries []PendingProviderRegistration
}

// ExtensionRuntime shares pending and bound provider actions between extension loading and the Runner. It stores no model catalog and does not execute factories.
type ExtensionRuntime struct {
	mu                           sync.Mutex
	pendingProviderRegistrations *providerRegistrationQueue
	providerActions              ProviderActions
	bound                        bool
}

// CreateExtensionRuntime creates the pre-bind provider registration state.
func CreateExtensionRuntime() *ExtensionRuntime {
	return &ExtensionRuntime{pendingProviderRegistrations: &providerRegistrationQueue{}}
}

// PendingProviderRegistrations returns the current queue in registration order. Repeated provider names remain separate entries.
func (r *ExtensionRuntime) PendingProviderRegistrations() []PendingProviderRegistration {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.pendingProviderRegistrations.entries)
}

// RegisterProvider queues during loading and invokes the bound registry synchronously afterward. Configuration callbacks are retained, not serialized.
func (r *ExtensionRuntime) RegisterProvider(name string, config ProviderConfig, extensionPath ...string) error {
	path := "<unknown>"
	if len(extensionPath) != 0 {
		path = extensionPath[0]
	}
	r.mu.Lock()
	if !r.bound {
		r.pendingProviderRegistrations.entries = append(r.pendingProviderRegistrations.entries, PendingProviderRegistration{Name: name, Config: config, ExtensionPath: path})
		r.mu.Unlock()
		return nil
	}
	register := r.providerActions.RegisterProvider
	r.mu.Unlock()
	return register(name, config)
}

// UnregisterProvider removes every queued registration of name before binding, and invokes the registry immediately afterward.
func (r *ExtensionRuntime) UnregisterProvider(name string) {
	r.mu.Lock()
	if !r.bound {
		// Pi's filter replaces the queue array; an already-running bind iteration retains its original array.
		r.pendingProviderRegistrations = &providerRegistrationQueue{entries: slices.DeleteFunc(slices.Clone(r.pendingProviderRegistrations.entries), func(entry PendingProviderRegistration) bool { return entry.Name == name })}
		r.mu.Unlock()
		return
	}
	unregister := r.providerActions.UnregisterProvider
	r.mu.Unlock()
	if unregister != nil {
		unregister(name)
	}
}

// BindProviderActions drains the loading queue in order, reports each failure before continuing, and installs immediate actions. The owner serializes binding with other binds. Callbacks run outside state locks and may register or unregister providers.
func (r *ExtensionRuntime) BindProviderActions(actions ProviderActions, report func(*ExtensionError)) {
	if actions.RegisterProvider == nil {
		return
	}
	r.mu.Lock()
	r.bound = false
	pending := r.pendingProviderRegistrations
	r.mu.Unlock()
	for i := 0; ; i++ {
		r.mu.Lock()
		if i == len(pending.entries) {
			r.pendingProviderRegistrations = &providerRegistrationQueue{}
			r.providerActions = actions
			r.bound = true
			r.mu.Unlock()
			return
		}
		entry := pending.entries[i]
		r.mu.Unlock()
		if err := actions.RegisterProvider(entry.Name, entry.Config); err != nil && report != nil {
			report(&ExtensionError{ExtensionPath: entry.ExtensionPath, Event: "register_provider", Error: err.Error(), Stack: ErrorStack(err)})
		}
	}
}
