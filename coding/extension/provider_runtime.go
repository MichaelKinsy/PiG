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
	// Outcome, when set, is told the registry's verdict when the queue drains: nil when it accepted the registration, its error when it rejected it. It is not called for an entry an unregistration removed first. A registration queued while an extension loads uses it to undo what the loader recorded for an extension whose registration the registry refused.
	Outcome func(error)
}

type providerRegistrationQueue struct {
	entries []PendingProviderRegistration
}

// virtualModelRegistrationQueue is Pi's pendingVirtualModelRegistrations array. Registration appends to the current queue, which a running flush sees; unregistration replaces the queue, which a running flush does not see.
type virtualModelRegistrationQueue struct {
	entries []PendingVirtualModelRegistration
}

// ExtensionRuntime shares pending and bound provider actions between extension loading and the Runner. It stores no model catalog and does not execute factories.
type ExtensionRuntime struct {
	// ExtensionActions are the action handlers of upstream ExtensionRuntime, which extends ExtensionActions. Runner.BindCore installs the host's handlers and every extension context calls through them; they are nil until a runner binds.
	ExtensionActions
	// FlagValues holds the value of each extension flag: defaults set during registration, command-line values set afterwards.
	// upstream: types.ts ExtensionRuntimeState.flagValues
	FlagValues map[string]any

	mu                           sync.Mutex
	pendingNativeProviders       []PendingNativeProviderRegistration
	staleMessage                 string
	eventBusUnsubscribers        []*eventBusSubscription
	factoryAPIs                  map[string]func(message string)
	nativeBound                  bool
	pendingProviderRegistrations *providerRegistrationQueue
	providerActions              ProviderActions
	bound                        bool

	// mcpServers holds the servers registered with API.RegisterMcpServer.
	// upstream: types.ts:2117 (ExtensionRuntime.mcpServers)
	mcpServers *McpServerRegistry
	// pendingVirtualModelRegistrations queues virtual models registered while extensions load, until the runner binds.
	// upstream: types.ts:2097 (ExtensionRuntime.pendingVirtualModelRegistrations)
	pendingVirtualModelRegistrations *virtualModelRegistrationQueue
	virtualModelsBound               bool
	// createContext builds an extension Context for a virtual model's Route. It fails before the runner binds.
	// upstream: types.ts:2099 (ExtensionRuntime.createContext)
	createContext func() (*Context, error)
	// reportError delivers a diagnostic to the bound runner's error
	// listeners, the channel each mode shows extension errors on.
	reportError func(*ExtensionError)
}

// SetErrorReporter installs the bound runner's error reporting. pig additive
// (D107): a subprocess host reports diagnostics that arise outside any
// handler, such as a rejected extension view, through it.
func (r *ExtensionRuntime) SetErrorReporter(report func(*ExtensionError)) {
	r.mu.Lock()
	r.reportError = report
	r.mu.Unlock()
}

// ReportError delivers err to the bound runner's error listeners. It
// reports false when no runner has bound.
func (r *ExtensionRuntime) ReportError(err *ExtensionError) bool {
	r.mu.Lock()
	report := r.reportError
	r.mu.Unlock()
	if report == nil {
		return false
	}
	report(err)
	return true
}

// CreateExtensionRuntime creates the pre-bind provider registration state.
func CreateExtensionRuntime() *ExtensionRuntime {
	return &ExtensionRuntime{FlagValues: map[string]any{}, pendingProviderRegistrations: &providerRegistrationQueue{}, pendingVirtualModelRegistrations: &virtualModelRegistrationQueue{}, mcpServers: NewMcpServerRegistry()}
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

// QueueProviderRegistration queues the registration an extension makes while it loads, whether or not a registry is bound: Pi's loader queues it in the runtime of the load, which is not bound yet, and the services or the runner that binds applies it after every extension of the load finished, so a registration the registry rejects is a diagnostic of that binding and the extension stays loaded (loader.ts:217-218, agent-session-services.ts:157-169, runner.ts:471-487). A runtime that outlives a load (a reload, a host the owner bound before loading) is not bound again until its owner binds it, which applies the queue.
//
// upstream: loader.ts:217-218 (`runtime.registerProvider` of the loading runtime)
func (r *ExtensionRuntime) QueueProviderRegistration(name string, config ProviderConfig, extensionPath string, outcome func(error)) {
	r.mu.Lock()
	r.pendingProviderRegistrations.entries = append(r.pendingProviderRegistrations.entries, PendingProviderRegistration{Name: name, Config: config, ExtensionPath: extensionPath, Outcome: outcome})
	r.mu.Unlock()
}

// UnregisterProvider removes every queued registration of name before binding, and invokes the registry immediately afterward.
// The optional extensionPath is accepted as upstream's second parameter; neither upstream branch reads it.
// upstream: loader.ts:220 `unregisterProvider: (name) =>`, runner.ts:534
func (r *ExtensionRuntime) UnregisterProvider(name string, extensionPath ...string) {
	_ = extensionPath
	r.mu.Lock()
	// Pi's filter replaces the queue array; an already-running bind iteration retains its original array. A bound runtime holds a queue too when an extension registered while it loaded (QueueProviderRegistration), and an unregistration removes those, as Pi's loader does before the runner binds.
	r.pendingProviderRegistrations = &providerRegistrationQueue{entries: slices.DeleteFunc(slices.Clone(r.pendingProviderRegistrations.entries), func(entry PendingProviderRegistration) bool { return entry.Name == name })}
	r.pendingNativeProviders = slices.DeleteFunc(slices.Clone(r.pendingNativeProviders), func(entry PendingNativeProviderRegistration) bool {
		return entry.carrier != nil && entry.carrier.ID == name
	})
	if !r.bound {
		r.mu.Unlock()
		return
	}
	unregister := r.providerActions.UnregisterProvider
	r.mu.Unlock()
	if unregister != nil {
		unregister(name)
	}
}

// BoundProviderActions returns the provider actions a host already bound to this runtime. A field is nil until its queue was drained by BindProviderActions.
func (r *ExtensionRuntime) BoundProviderActions() ProviderActions {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.providerActions
}

// BindProviderActions drains the loading queues in order, reports each failure before continuing, and installs immediate actions. Provider registrations drain first, then native providers, then virtual models. Actions left nil leave their queue pending. The owner serializes binding with other binds. Callbacks run outside state locks and may register or unregister providers and virtual models.
//
// upstream: runner.ts:265-294, 413-417, 497-541 (bindCore)
func (r *ExtensionRuntime) BindProviderActions(actions ProviderActions, report func(*ExtensionError)) {
	if actions.RegisterProvider != nil {
		r.bindProviders(actions, report)
	}
	if actions.bindsNativeProvider() {
		r.bindNativeProviders(actions, report)
	}
	if actions.RegisterVirtualModel != nil {
		r.bindVirtualModels(actions, report)
	}
}

func (r *ExtensionRuntime) bindProviders(actions ProviderActions, report func(*ExtensionError)) {
	r.mu.Lock()
	r.bound = false
	pending := r.pendingProviderRegistrations
	r.mu.Unlock()
	for i := 0; ; i++ {
		r.mu.Lock()
		if i == len(pending.entries) {
			r.pendingProviderRegistrations = &providerRegistrationQueue{}
			r.providerActions.RegisterProvider = actions.RegisterProvider
			r.providerActions.UnregisterProvider = actions.UnregisterProvider
			r.bound = true
			r.mu.Unlock()
			return
		}
		entry := pending.entries[i]
		r.mu.Unlock()
		err := actions.RegisterProvider(entry.Name, entry.Config)
		if entry.Outcome != nil {
			entry.Outcome(err)
		}
		if err != nil && report != nil {
			report(&ExtensionError{ExtensionPath: entry.ExtensionPath, Event: "register_provider", Error: err.Error(), Stack: ErrorStack(err)})
		}
	}
}

func (r *ExtensionRuntime) bindVirtualModels(actions ProviderActions, report func(*ExtensionError)) {
	r.mu.Lock()
	r.virtualModelsBound = false
	pending := r.pendingVirtualModelRegistrations
	r.mu.Unlock()
	// upstream: runner.ts:501-513: the for-of loop reads the live array, so a registration queued by a callback is flushed too.
	for i := 0; ; i++ {
		r.mu.Lock()
		if i == len(pending.entries) {
			r.pendingVirtualModelRegistrations = &virtualModelRegistrationQueue{}
			r.providerActions.RegisterVirtualModel = actions.RegisterVirtualModel
			r.providerActions.UnregisterVirtualModel = actions.UnregisterVirtualModel
			r.virtualModelsBound = true
			r.mu.Unlock()
			return
		}
		entry := pending.entries[i]
		r.mu.Unlock()
		if err := actions.RegisterVirtualModel(entry.Definition); err != nil && report != nil {
			report(&ExtensionError{ExtensionPath: entry.ExtensionPath, Event: "register_virtual_model", Error: err.Error(), Stack: ErrorStack(err)})
		}
	}
}
