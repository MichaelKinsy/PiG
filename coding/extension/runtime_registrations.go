package extension

// Ports packages/coding-agent/src/core/extensions/loader.ts (registerMcpServer, unregisterMcpServer, getMcpServers, registerVirtualModel, unregisterVirtualModel).
// Ports packages/coding-agent/src/core/extensions/runner.ts (mcpServers change listener, pending virtual models).

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/MichaelKinsy/PiG/internal/jsonstringify"
)

// PendingVirtualModelRegistration retains a virtual model and its owning extension until the runner binds.
//
// upstream: types.ts:2097 (pendingVirtualModelRegistrations)
type PendingVirtualModelRegistration struct {
	Definition    VirtualModelDefinition
	ExtensionPath string
}

// RegisterMcpServer validates config as an `mcpServers` entry named name, checks that no other extension registered the name, and registers a copy for extensionPath. A registration replaces the extension's earlier one. The change listener runs after the registry changed.
//
// It returns `Invalid MCP server registered by extension "<path>": <message>` for an invalid config and `MCP server "<name>" is already registered by extension "<owner>"` for a name another extension registered.
//
// It returns `MCP server "<name>" conflicts with registered server "<other>"` for a name that shares the namespace of a registered server.
//
// upstream: loader.ts:464-481 (registerMcpServer stores `structuredClone(validated)`, the alias-resolved copy, at 479)
func (r *ExtensionRuntime) RegisterMcpServer(extensionPath, name string, config json.RawMessage) error {
	validated, err := r.CheckMcpServer(extensionPath, name, config)
	if err != nil {
		return err
	}
	// Names that differ only in `-` and `_` would share a namespace.
	for _, server := range r.mcpServers.List() {
		if server.Name != name && McpNamespace(server.Name) == McpNamespace(name) {
			return fmt.Errorf(`MCP server "%s" conflicts with registered server "%s"`, name, server.Name)
		}
	}
	declared, err := jsonstringify.Canonicalize(resolveMcpExposureAliases(config))
	if err != nil {
		return fmt.Errorf(`Invalid MCP server registered by extension "%s": %w`, extensionPath, err)
	}
	r.mcpServers.Register(RegisteredMcpServer{Name: name, Config: validated, Declared: declared, ExtensionPath: extensionPath})
	return nil
}

// CheckMcpServer runs the checks of [ExtensionRuntime.RegisterMcpServer] without registering: the config is valid and no other extension registered the name. It returns the validated config and the error the registration would return.
//
// upstream: loader.ts:456-465 (registerMcpServer)
func (r *ExtensionRuntime) CheckMcpServer(extensionPath, name string, config json.RawMessage) (McpServerConfig, error) {
	validated, message := ValidateMcpServerConfig(name, config)
	if message != "" {
		return McpServerConfig{}, fmt.Errorf(`Invalid MCP server registered by extension "%s": %s`, extensionPath, message)
	}
	if owner, registered := r.mcpServers.Get(name); registered && owner.ExtensionPath != extensionPath {
		return McpServerConfig{}, fmt.Errorf(`MCP server "%s" is already registered by extension "%s"`, name, owner.ExtensionPath)
	}
	return validated, nil
}

// UnregisterMcpServer removes a server extensionPath registered. Servers of other extensions are left alone.
//
// upstream: loader.ts:470-473 (unregisterMcpServer)
func (r *ExtensionRuntime) UnregisterMcpServer(extensionPath, name string) {
	r.mcpServers.Unregister(name, extensionPath)
}

// McpServers returns copies of every registered server, in registration order.
//
// upstream: loader.ts:475-478 (getMcpServers)
func (r *ExtensionRuntime) McpServers() []RegisteredMcpServer {
	return r.mcpServers.List()
}

// SetMcpServersChangeListener sets the function called after every registration change. The runner sets it when it binds, to emit `mcp_servers_change`; nil removes it.
//
// upstream: runner.ts:459-462 (mcpServers.setChangeListener)
func (r *ExtensionRuntime) SetMcpServersChangeListener(listener func()) {
	r.mcpServers.SetChangeListener(listener)
}

// PendingVirtualModelRegistrations returns the queue in registration order.
//
// upstream: loader.ts:225-227 (pendingVirtualModelRegistrations)
func (r *ExtensionRuntime) PendingVirtualModelRegistrations() []PendingVirtualModelRegistration {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.pendingVirtualModelRegistrations.entries)
}

// RegisterVirtualModel queues a virtual model during loading and applies it through the bound [ProviderActions] afterward. Registering the same provider and id again replaces the virtual model.
//
// upstream: loader.ts:225-227, runner.ts:497-537 (registerVirtualModel)
func (r *ExtensionRuntime) RegisterVirtualModel(definition VirtualModelDefinition, extensionPath string) error {
	r.mu.Lock()
	if !r.virtualModelsBound {
		r.pendingVirtualModelRegistrations.entries = append(r.pendingVirtualModelRegistrations.entries, PendingVirtualModelRegistration{Definition: definition, ExtensionPath: extensionPath})
		r.mu.Unlock()
		return nil
	}
	register := r.providerActions.RegisterVirtualModel
	r.mu.Unlock()
	return register(definition)
}

// UnregisterVirtualModel removes every queued registration of the provider and id before binding, and applies the removal through the bound [ProviderActions] afterward.
//
// upstream: loader.ts:229-233, runner.ts:538-541 (unregisterVirtualModel)
func (r *ExtensionRuntime) UnregisterVirtualModel(provider, id string) {
	r.mu.Lock()
	if !r.virtualModelsBound {
		// Pi's filter replaces the queue array; an already-running flush keeps its original array.
		r.pendingVirtualModelRegistrations = &virtualModelRegistrationQueue{entries: slices.DeleteFunc(slices.Clone(r.pendingVirtualModelRegistrations.entries), func(entry PendingVirtualModelRegistration) bool {
			return entry.Definition.Provider == provider && entry.Definition.ID == id
		})}
		r.mu.Unlock()
		return
	}
	unregister := r.providerActions.UnregisterVirtualModel
	r.mu.Unlock()
	if unregister != nil {
		unregister(provider, id)
	}
}

// SetContextFactory installs the function that builds the extension Context a virtual model's Route receives. The runner sets it when it binds.
//
// upstream: runner.ts:436 (runtime.createContext)
func (r *ExtensionRuntime) SetContextFactory(create func() (*Context, error)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.createContext = create
}

// ErrRuntimeNotInitialized is returned by an action called while extensions are still loading. upstream: loader.ts:157-159 (notInitialized)
var ErrRuntimeNotInitialized = errors.New("Extension runtime not initialized. Action methods cannot be called during extension loading.")

// CreateContext builds an extension Context. It returns [ErrRuntimeNotInitialized] before the runner binds.
//
// upstream: loader.ts:190 (createContext: notInitialized), runner.ts:436
func (r *ExtensionRuntime) CreateContext() (*Context, error) {
	r.mu.Lock()
	create := r.createContext
	r.mu.Unlock()
	if create == nil {
		return nil, ErrRuntimeNotInitialized
	}
	return create()
}
