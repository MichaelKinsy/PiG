package sdk

// Pi's createCodemodeExtension, createToolSearchExtension and createMcpExtension (extensions/codemode/index.ts, tool-search/index.ts, mcp/index.ts) return an ExtensionFactory, a function Pi's DefaultResourceLoader runs in its own process. A function
// cannot cross the process boundary, so these return a reference to the compiled-in extension of that name: pass it in [DefaultResourceLoaderOptions].ExtensionFactories and the host runs its own factory of that name, with the same options, for
// the extensions the loader loads.

// ExtensionFactory is a reference to a compiled-in extension of the host, which [NewDefaultResourceLoader] takes in its ExtensionFactories option.
type ExtensionFactory struct {
	builtin string
	options map[string]any
	// mcp holds the function options of CreateMcpExtension, which are named to the host when a loader is built.
	mcp *mcpFunctionOptions
}

// CodemodeExtensionOptions is Pi's CodemodeExtensionOptions. A zero member is Pi's undefined.
type CodemodeExtensionOptions struct {
	// Mode overrides the `codemode.mode` setting: "on" or "only".
	Mode string
	// InlineBudget overrides the `codemode.inlineBudget` setting.
	InlineBudget *float64
	// Models exposes the model catalog and classifiers to scripts as `models`. Nil is Pi's default, true.
	Models *bool
}

// CreateCodemodeExtension is `createCodemodeExtension(options)`: the `codemode` tool, registered inactive. Activate it with the tools option, the `defaultTools` setting or `setActiveTools`.
func CreateCodemodeExtension(options ...CodemodeExtensionOptions) ExtensionFactory {
	wire := map[string]any{}
	if len(options) > 0 {
		given := options[0]
		if given.Mode != "" {
			wire["mode"] = given.Mode
		}
		if given.InlineBudget != nil {
			wire["inlineBudget"] = *given.InlineBudget
		}
		if given.Models != nil {
			wire["models"] = *given.Models
		}
	}
	return ExtensionFactory{builtin: "codemode", options: wire}
}

// CreateToolSearchExtension is `createToolSearchExtension()`: the `tool_search` tool, registered inactive.
func CreateToolSearchExtension() ExtensionFactory {
	return ExtensionFactory{builtin: "tool-search", options: map[string]any{}}
}

// McpExtensionOptions is Pi's McpExtensionOptions. A zero member is Pi's undefined. The functions run in this process: the host calls them when the extension needs them (see the types of mcp_extension_options.go).
type McpExtensionOptions struct {
	// LoadConfig defaults to reading `mcp.json` from the agent directory and the trusted project. It runs when a session starts; an error is the throw of Pi's loadConfig.
	LoadConfig func(ctx McpLoadConfigContext) (LoadedMcpConfig, error)
	// CreateTransport defaults to stdio and streamable HTTP transports built from the server config.
	CreateTransport McpTransportFactory
	// Credentials defaults to `mcp-auth.json` in the agent directory.
	Credentials McpOAuthCredentialStore
	// LogPath is the file server log messages are appended to. Empty is `mcp.log` in the agent directory.
	LogPath string
	// OpenURL opens the OAuth authorization URL. Defaults to doing nothing; the Go host supplies no browser.
	OpenURL func(url string)
	// UpdateConfig saves `/mcp` changes to the server's config file: its project Override when set, else its Source. Defaults to editing that `mcp.json`. An error is the throw of Pi's updateConfig.
	UpdateConfig func(entry McpServerEntry, patch McpServerConfigPatch) error
	// StartupWaitMs is how long the first prompt waits for servers with `direct` tools that are still connecting at startup. Nil is Pi's default, 10000.
	StartupWaitMs *float64
}

// CreateMcpExtension is `createMcpExtension(options)`: the MCP extension.
func CreateMcpExtension(options ...McpExtensionOptions) ExtensionFactory {
	wire := map[string]any{}
	reference := ExtensionFactory{builtin: "mcp", options: wire}
	if len(options) > 0 {
		given := options[0]
		if given.LogPath != "" {
			wire["logPath"] = given.LogPath
		}
		if given.StartupWaitMs != nil {
			wire["startupWaitMs"] = *given.StartupWaitMs
		}
		if given.LoadConfig != nil || given.CreateTransport != nil || given.Credentials != nil || given.OpenURL != nil || given.UpdateConfig != nil {
			reference.mcp = &mcpFunctionOptions{loadConfig: given.LoadConfig, createTransport: given.CreateTransport, credentials: given.Credentials, openURL: given.OpenURL, updateConfig: given.UpdateConfig}
		}
	}
	return reference
}

// extensionFactoriesWire is the loader's extensionFactories option on the wire: each reference as its name and options. Any other value is a function of this process, which the host cannot run.
func extensionFactoriesWire(c Context, factories []any) ([]map[string]any, error) {
	wire := make([]map[string]any, 0, len(factories))
	for _, factory := range factories {
		reference, ok := factory.(ExtensionFactory)
		if !ok {
			return nil, errExtensionFactoryNotServed
		}
		// The option object is built for each loader: a function is named to the host in the connection's table once per loader it is passed to.
		options := make(map[string]any, len(reference.options)+5)
		for name, value := range reference.options {
			options[name] = value
		}
		reference.mcp.register(c, options)
		wire = append(wire, map[string]any{"builtin": reference.builtin, "options": options})
	}
	return wire, nil
}
