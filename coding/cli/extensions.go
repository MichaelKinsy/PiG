// pig-specific: no upstream equivalent.
//
// Subprocess loading consumes the canonical extension set resolved by startup
// and reuses that same resolver for reload.

package cli

import (
	"context"
	"fmt"
	"os"
	"sync/atomic"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/fusepack"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/coding/extension/pigsdk"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/tui"

	"golang.org/x/term"
)

func wireSubprocessModelRegistry(bridge *subprocess.UIBridge, session *coding.Session, services *coding.AgentSessionServices) func() {
	if bridge == nil || session == nil || services == nil {
		return func() {}
	}
	runtime := session.ModelRuntime()
	return codingagent.WireModelOperations(bridge, codingagent.ModelOperationBindings{
		CurrentModel: session.Model, ModelLookup: runtime.GetModel, ModelCatalog: runtime.GetModels, Classify: runtime.Classify, GenerateImages: runtime.GenerateImages,
		Registry: services.Registry().ModelRegistry, ModelBuilder: func(spec string) (*ai.Model, error) { return coding.BuildModel(spec, services) },
		SessionHandle: session,
	})
}

// watchSessionRunSignal reports each run the Session's agent begins or ends to the extension host, and returns the function that stops. Pi's ctx.signal is `() => this.agent.signal` (agent-session.ts:3368), read live by the extension that holds ctx, so every runtime must learn of a run change when it happens, not at its next request.
func watchSessionRunSignal(bridge *subprocess.UIBridge, session *coding.Session) func() {
	if bridge == nil || session == nil {
		return func() {}
	}
	return session.Agent().ObserveRunSignal(bridge.RunSignalChanged)
}

// loadSubprocessExtensions starts the already-resolved canonical extension set.
// Discovery, trust, Package, Piglet, settings, and CLI precedence are owned by
// collectExtensionConfigs. Reload receives the same normalized resolver.
//
// pig-specific: the subprocess host itself has no upstream equivalent.
// appMode is the run mode selected at startup. Mirrors upstream main.ts
// AppMode.
type appMode string

const (
	appModeInteractive appMode = "interactive"
	appModePrint       appMode = "print"
	appModeJSON        appMode = "json"
	appModeRPC         appMode = "rpc"
)

// resolveAppMode mirrors upstream main.ts resolveAppMode: --mode rpc and
// --mode json select those modes; --print, a stdin that is not a terminal, or
// a stdout that is not a terminal select print mode; anything else is
// interactive. `pig "q" > file` therefore prints the answer instead of drawing
// the TUI into the file.
func resolveAppMode(mode string, print, stdinIsTTY, stdoutIsTTY bool) appMode {
	switch {
	case mode == "rpc":
		return appModeRPC
	case mode == "json":
		return appModeJSON
	case print || !stdinIsTTY || !stdoutIsTTY:
		return appModePrint
	default:
		return appModeInteractive
	}
}

// isPlainRuntimeMetadataCommand preserves stdout only for metadata without explicit mode/print options.
// Ports packages/coding-agent/src/main.ts:129-130.
func isPlainRuntimeMetadataCommand(flags Args) bool {
	return !flags.Print && !flags.modeSet && (flags.Help || flags.ListModelsAll || flags.ListModels != "")
}

// processAppMode resolves the app mode for this process's flags and standard
// streams.
// shouldRunFirstTimeSetup is PiG's first-time setup gate: an interactive start in the default agent directory (no
// PIG_CODING_AGENT_DIR) that holds no settings.json, as Pi's shouldRunFirstTimeSetup requires (cli/startup-ui.ts:144-146).
// Print, JSON and RPC modes never show it.
// pig divergence (D88): Pi's gate also requires the official distribution and PI_EXPERIMENTAL.
func shouldRunFirstTimeSetup(mode appMode, agentDirConfigured bool, agentDir string) bool {
	return mode == appModeInteractive && !agentDirConfigured && !codingagent.SettingsFileExists(agentDir)
}

func processAppMode(flags Args) appMode {
	return resolveAppMode(flags.Mode, flags.Print,
		term.IsTerminal(int(os.Stdin.Fd())), term.IsTerminal(int(os.Stdout.Fd())))
}

// extensionMode maps the app mode to the extension run mode exposed as
// ctx.mode, with "interactive" rendered as ModeTUI.
func (m appMode) extensionMode() extension.ExtensionMode {
	switch m {
	case appModeRPC:
		return extension.ModeRPC
	case appModeJSON:
		return extension.ModeJSON
	case appModePrint:
		return extension.ModePrint
	default:
		return extension.ModeTUI
	}
}

func normalizeRuntimeExtensionConfigs(configs []subprocess.ExtConfig) []subprocess.ExtConfig {
	all := make([]subprocess.ExtConfig, 0, len(configs)+len(fusepack.FusedConfigs()))
	all = append(all, fusepack.FusedConfigs()...)
	all = append(all, configs...)
	return mergeExtConfigs(all)
}

// newSubprocessExtensionHost creates an extension host with no extensions
// loaded and no config loader.
func newSubprocessExtensionHost(cwd string, mode extension.ExtensionMode, registry *codingagent.ModelRegistry) (*subprocess.Host, *subprocess.UIBridge) {
	host := subprocess.NewHostWithConfigRoot(cwd, codingagent.ConfigRoot())
	// Create UIBridge: starts with NoopUIContext. The real UIContext is wired
	// in interactive mode after the TUI is created. Widget pushes still work
	// (cached by PushProxy); UI method calls return noop results until the
	// real UIContext is set via bridge.SetUIContext().
	bridge := subprocess.NewUIBridge(func() {})
	bridge.SetTerminalCapabilitiesFunc(func() subprocess.TerminalCapabilitiesPayload {
		caps := tui.GetCapabilities()
		return subprocess.TerminalCapabilitiesPayload{Images: string(caps.Images), TrueColor: caps.TrueColor, Hyperlinks: caps.Hyperlinks}
	})
	bridge.SetThemeFunc(codingagent.ActiveExtensionTheme)
	host.SetUIBridge(bridge)
	installStartupTrace(&trace, host.SetStartupTrace)
	host.SetMode(string(mode))
	// Refuse to compile an extension against a staged SDK that does not match
	// this binary. Without it a stale stage silently reintroduces bugs the
	// running pig already fixed.
	host.Builder().SetStagedSDKVerifier(pigsdk.VerifyStaged(codingagent.ConfigRoot()))
	if registry != nil {
		bindProviderRegistry(host, registry)
	}
	// Log crashes to stderr. Suppress during shutdown to avoid
	// "connection closed" noise when extensions are torn down.
	host.SetCrashHandler(func(name string, delay time.Duration, disabled bool, reason string) {
		if host.IsShuttingDown() {
			return
		}
		fmt.Fprintln(os.Stderr, subprocess.FormatCrashNotice(name, delay, disabled, reason))
	})
	return host, bridge
}

// startupRegistrationRefreshReady is set when startup makes Pi's awaited local refresh. Pi queues the provider registrations of the extensions it loads at startup and flushes them only after every factory has finished, immediately before that awaited refresh (agent-session-services.ts:158-182), so no Provider callback runs while startup loads extensions. Until then a host registration only queues its refresh, and the startup refresh yields to it.
var startupRegistrationRefreshReady atomic.Bool

// bindProviderRegistry binds an extension host's provider registrations to the registry. Pi's registerProvider, registerNativeProvider and unregisterProvider return and the JavaScript event loop then runs their unawaited local refresh (model-runtime.ts:744-797). After startup, a host registration returns to its extension when the callback returns, so each callback ends its caller's turn by starting the queued refresh; nothing else would run it until an unrelated awaited model-runtime call.
func bindProviderRegistry(host *subprocess.Host, registry *codingagent.ModelRegistry) {
	host.SetProviderCallbacks(func(name string, config extension.ProviderConfig) error {
		defer startRegistrationRefresh(registry)
		return registry.RegisterExtensionProvider(name, config)
	}, func(name string) {
		defer startRegistrationRefresh(registry)
		registry.UnregisterProvider(name)
	})
	host.SetNativeProviderCallback(func(ctx context.Context, provider *extension.NativeProvider) error {
		defer startRegistrationRefresh(registry)
		return registry.RegisterNativeProvider(ctx, provider)
	})
}

// startRegistrationRefresh starts the queued registration refresh without waiting for it. Services.Close cancels and drains the pass.
func startRegistrationRefresh(registry *codingagent.ModelRegistry) {
	if !startupRegistrationRefreshReady.Load() {
		return
	}
	registry.StartRegistrationRefresh(context.Background())
}

// setExtensionConfigLoader makes reloadConfigs the host's reload resolver, or
// the startup configs when reloadConfigs is nil.
func setExtensionConfigLoader(host *subprocess.Host, configs []subprocess.ExtConfig, reloadConfigs func() []subprocess.ExtConfig) {
	if reloadConfigs == nil {
		startupConfigs := append([]subprocess.ExtConfig(nil), configs...)
		host.SetConfigLoader(func() ([]subprocess.ExtConfig, error) {
			return append([]subprocess.ExtConfig(nil), startupConfigs...), nil
		})
		return
	}
	host.SetConfigLoader(func() ([]subprocess.ExtConfig, error) {
		return normalizeRuntimeExtensionConfigs(reloadConfigs()), nil
	})
}

// ensureReloadableExtensionHost returns host and bridge, creating an empty
// interactive host when startup loaded no extensions and extensions are
// enabled. Upstream /reload rediscovers extensions whether or not any loaded
// at startup; without a host, PiG's /reload could never load one.
func ensureReloadableExtensionHost(host *subprocess.Host, bridge *subprocess.UIBridge, noExtensions bool, cwd string, registry *codingagent.ModelRegistry, reloadConfigs func() []subprocess.ExtConfig) (*subprocess.Host, *subprocess.UIBridge) {
	if host != nil || noExtensions {
		return host, bridge
	}
	host, bridge = newSubprocessExtensionHost(cwd, extension.ModeTUI, registry)
	setExtensionConfigLoader(host, nil, reloadConfigs)
	return host, bridge
}

// loadSubprocessExtensions starts the already-resolved canonical extension set
// and returns every extension that failed to load. Callers own the failure
// policy: session startup exits on them as upstream main.ts does.
func loadSubprocessExtensions(ctx context.Context, cwd string, mode extension.ExtensionMode, registry *codingagent.ModelRegistry, configs []subprocess.ExtConfig, embedded []subprocess.EmbeddedCell, reloadConfigs func() []subprocess.ExtConfig) ([]extension.Extension, *subprocess.Host, *subprocess.UIBridge, []error) {
	return loadFinalSubprocessExtensions(ctx, cwd, mode, registry, configs, embedded, reloadConfigs, nil)
}

func loadFinalSubprocessExtensions(ctx context.Context, cwd string, mode extension.ExtensionMode, registry *codingagent.ModelRegistry, configs []subprocess.ExtConfig, embedded []subprocess.EmbeddedCell, reloadConfigs func() []subprocess.ExtConfig, preloaded *startupExtensionSet) ([]extension.Extension, *subprocess.Host, *subprocess.UIBridge, []error) {
	configs = normalizeRuntimeExtensionConfigs(configs)
	if len(configs) == 0 && len(embedded) == 0 && (preloaded == nil || preloaded.host == nil) {
		return nil, nil, nil, nil
	}

	var host *subprocess.Host
	var bridge *subprocess.UIBridge
	if preloaded != nil {
		host, bridge = preloaded.host, preloaded.bridge
	}
	if host == nil {
		host, bridge = newSubprocessExtensionHost(cwd, mode, registry)
	}
	setExtensionConfigLoader(host, configs, reloadConfigs)

	// Use LoadAll for cell-planned loading: factory-mode extensions are
	// packed into shared processes (D20) from the start, matching /reload.
	var loaded []extension.Extension
	var loadErrs []error
	if len(configs) > 0 || preloaded != nil {
		if preloaded != nil {
			loaded, loadErrs = host.LoadFinalExtensionSet(ctx, configs, preloaded.configs)
		} else {
			loaded, loadErrs = host.LoadAll(ctx, configs)
		}
	}
	if len(embedded) > 0 {
		embeddedLoaded, embeddedErrs := host.LoadEmbeddedCells(ctx, embedded)
		loadErrs = append(loadErrs, embeddedErrs...)
		loaded = append(loaded, embeddedLoaded...)
	}

	if preloaded != nil {
		preloaded.host, preloaded.bridge = host, bridge
		preloaded.configs = append([]subprocess.ExtConfig(nil), configs...)
	}
	scheduleAutomaticExtensionCacheGC(configs)

	return loaded, host, bridge, loadErrs
}

// startupExtensionSet owns the pre-trust host until the final extension set has
// been selected, including early exit and failed-load paths.
type startupExtensionSet struct {
	host    *subprocess.Host
	bridge  *subprocess.UIBridge
	configs []subprocess.ExtConfig
}

func (s *startupExtensionSet) close() {
	if s != nil && s.host != nil {
		s.host.Shutdown("quit")
	}
}

var stopStartupExtensions = func() {}

// flushExtensionHostVirtualModels binds the virtual models host's extensions register to registry, as Pi's createAgentSessionServices does after it loads extensions (agent-session-services.ts:182-193): the registrations queued while the extensions loaded apply now, after the provider registrations and before the awaited local refresh and model resolution, so --model and a restored selection can name them. Each failed registration becomes the error diagnostic `Extension "<path>" error: <message>`. Later registrations apply at once and return their error to the extension (runner.ts:497-541). A nil host has nothing to bind.
func flushExtensionHostVirtualModels(host *subprocess.Host, registry *coding.ModelRegistry) []codingagent.AgentSessionRuntimeDiagnostic {
	if host == nil || registry == nil {
		return nil
	}
	var diagnostics []codingagent.AgentSessionRuntimeDiagnostic
	host.Runtime().BindProviderActions(extension.ProviderActions{
		RegisterVirtualModel:   registry.RegisterVirtualModel,
		UnregisterVirtualModel: registry.UnregisterVirtualModel,
	}, func(err *extension.ExtensionError) {
		diagnostics = append(diagnostics, codingagent.AgentSessionRuntimeDiagnostic{Type: "error", Message: fmt.Sprintf(`Extension "%s" error: %s`, err.ExtensionPath, err.Error)})
	})
	return diagnostics
}
