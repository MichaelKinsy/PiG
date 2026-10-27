package sdk

// ExtensionRuntime is the object Pi's createExtensionRuntime() returns (core/extensions/loader.ts): the runtime state every extension of a load registers against, which an ExtensionRunner over those extensions binds its actions into. The host owns the object. A [DefaultResourceLoader] given it in [DefaultResourceLoaderOptions].Runtime loads its extensions against it, and [DefaultResourceLoader.GetExtensions] returns the runtime of the load in [LoadExtensionsResult].Runtime.
//
// Pi's runtime is a record of functions and Maps; this holds its data members: the flag values, the provider, native provider and virtual model registrations queued before a runner bound, the registered MCP servers and the stale state. The action functions (sendMessage and the rest) are the runner's, bound by [ExtensionRunner.BindCore].
type ExtensionRuntime struct{ object *graphObject }

// CreateExtensionRuntime is `createExtensionRuntime()`.
func CreateExtensionRuntime(c Context) (*ExtensionRuntime, error) {
	object, err := newGraphObject(c, "ExtensionRuntime", "create")
	if err != nil {
		return nil, err
	}
	return &ExtensionRuntime{object: object}, nil
}

// extensionRuntimeOf is the runtime of the extensions a loader loaded: `loader.getExtensions().runtime`.
func extensionRuntimeOf(c Context, loader *graphObject) (*ExtensionRuntime, error) {
	object, err := newGraphObject(c, "ExtensionRuntime", "of", loader.Handle())
	if err != nil {
		return nil, err
	}
	return &ExtensionRuntime{object: object}, nil
}

// Handle is the host's name for the runtime, which a loader option and [NewExtensionRunner] put on the wire.
func (r *ExtensionRuntime) Handle() string { return r.object.Handle() }

// GetFlagValues is `new Map(runtime.flagValues)`: the value of each flag that has one, as the registrations' defaults and later [ExtensionRuntime.SetFlagValue] calls left them. The host lists them by name.
func (r *ExtensionRuntime) GetFlagValues() (map[string]any, error) {
	return graphMethodCall[map[string]any](r.object, "getFlagValues")
}

// SetFlagValue is `runtime.flagValues.set(name, value)`: value is a bool or a string.
func (r *ExtensionRuntime) SetFlagValue(name string, value any) error {
	_, err := graphMethodCall[graphDone](r.object, "setFlagValue", name, value)
	return err
}

// PendingProviderRegistration is one entry of Pi's runtime.pendingProviderRegistrations: a provider an extension registered while loading, which the runner's bindCore applies. Config is the provider configuration as data.
type PendingProviderRegistration struct {
	Name          string         `json:"name"`
	Config        map[string]any `json:"config"`
	ExtensionPath string         `json:"extensionPath"`
}

// PendingNativeProviderRegistration is one entry of Pi's runtime.pendingNativeProviderRegistrations: the provider object an extension registered while loading, by its id and name.
type PendingNativeProviderRegistration struct {
	Provider struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"provider"`
	ExtensionPath string `json:"extensionPath"`
}

// PendingVirtualModelRegistration is one entry of Pi's runtime.pendingVirtualModelRegistrations: the virtual model an extension registered while loading, without its route function.
type PendingVirtualModelRegistration struct {
	Definition    map[string]any `json:"definition"`
	ExtensionPath string         `json:"extensionPath"`
}

// PendingProviderRegistrations is `runtime.pendingProviderRegistrations`, in registration order.
func (r *ExtensionRuntime) PendingProviderRegistrations() ([]PendingProviderRegistration, error) {
	return graphMethodCall[[]PendingProviderRegistration](r.object, "getPendingProviderRegistrations")
}

// PendingNativeProviderRegistrations is `runtime.pendingNativeProviderRegistrations`, in registration order.
func (r *ExtensionRuntime) PendingNativeProviderRegistrations() ([]PendingNativeProviderRegistration, error) {
	return graphMethodCall[[]PendingNativeProviderRegistration](r.object, "getPendingNativeProviderRegistrations")
}

// PendingVirtualModelRegistrations is `runtime.pendingVirtualModelRegistrations`, in registration order.
func (r *ExtensionRuntime) PendingVirtualModelRegistrations() ([]PendingVirtualModelRegistration, error) {
	return graphMethodCall[[]PendingVirtualModelRegistration](r.object, "getPendingVirtualModelRegistrations")
}

// McpServers is `runtime.mcpServers.list()`: the MCP servers extensions registered, in registration order, each as {name, config, extensionPath}.
func (r *ExtensionRuntime) McpServers() ([]map[string]any, error) {
	return graphMethodCall[[]map[string]any](r.object, "getMcpServers")
}

// AssertActive is `runtime.assertActive()`: it returns the stale message as an error once the runtime was invalidated.
func (r *ExtensionRuntime) AssertActive() error {
	_, err := graphMethodCall[graphDone](r.object, "assertActive")
	return err
}

// Invalidate is `runtime.invalidate(message?)`: later uses of the runtime fail with message, or Pi's default one when it is nil. A second call keeps the first message.
func (r *ExtensionRuntime) Invalidate(message *string) error {
	_, err := graphMethodCall[graphDone](r.object, "invalidate", message)
	return err
}
