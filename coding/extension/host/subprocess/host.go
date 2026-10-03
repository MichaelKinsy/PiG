package subprocess

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/invocation"
	"github.com/MichaelKinsy/PiG/coding/extension/host/runtimecell"
)

// ExtConfig describes a subprocess extension to load.
type ExtConfig struct {
	nodeRecoveryGroup string
	// Name is the extension's identifier (must match the name in register).
	Name string

	// Path is the path to the extension binary. Resolved relative to
	// the extension directory (~/.pig/extensions/) if not absolute.
	// Mutually exclusive with Source.
	Path string

	// Source is the path to the extension source directory (Go module or
	// Rust crate). When set, the host auto-builds before spawning and
	// caches the binary by content hash. Mutually exclusive with Path.
	Source string

	// SourceInfo is the Pi-compatible provenance stamped onto this loaded
	// extension and every command it registers.
	SourceInfo extension.SourceInfo

	// Enabled controls whether the extension is loaded. Default: true.
	Enabled bool

	// SupervisorConfig overrides crash recovery settings. Zero value uses defaults.
	SupervisorConfig SupervisorConfig

	// Runtime metadata is derived from the conventional source form. These
	// fields drive runtime-cell planning and are not authored configuration.
	RuntimeKind        string
	RuntimeLanguage    string
	SDKName            string
	Isolation          string
	EntrypointKind     string
	ModulePath         string
	Package            string
	Factory            string
	ContentHash        string
	GoWorkspaceModules []string

	selectedPath string
	resolveErr   error
	// identity is the extension identity when Name is a host key made unique
	// for a second copy of the same extension from another path.
	identity string
}

// acceptsRegisteredName reports whether a registration names this config:
// its host key, or the identity it was disambiguated from.
func (c ExtConfig) acceptsRegisteredName(name string) bool {
	return name == c.Name || (c.identity != "" && name == c.identity)
}

// disambiguateIdentities gives each later copy of an extension identity from a
// different path its own host key ("ask:2", "ask:3", ...). Upstream identifies
// an extension by its path, so copies from two Packages both load; the host
// keys its registry by name, so the copies need distinct keys.
func disambiguateIdentities(configs []ExtConfig) []ExtConfig {
	counts := make(map[string]int, len(configs))
	for _, config := range configs {
		counts[config.Name]++
	}
	if len(counts) == len(configs) {
		return configs
	}
	out := slices.Clone(configs)
	taken := make(map[string]struct{}, len(configs))
	for _, config := range configs {
		taken[config.Name] = struct{}{}
	}
	seen := make(map[string]int, len(configs))
	for i := range out {
		name := out[i].Name
		seen[name]++
		if seen[name] == 1 {
			continue
		}
		key := name
		for suffix := seen[name]; ; suffix++ {
			key = fmt.Sprintf("%s:%d", name, suffix)
			if _, exists := taken[key]; !exists {
				break
			}
		}
		taken[key] = struct{}{}
		out[i].identity = name
		out[i].Name = key
	}
	return out
}

// UnresolvedExtConfig returns a config for an extension path whose source did
// not resolve. It stays enabled, so every consumer that selects enabled
// extensions sees the attempt, but the host never plans or starts it: LoadAll
// returns err as an ExtensionLoadError and Reload records it in
// ReloadReport.Issues, as upstream loadExtensions records a failed extension
// beside the ones it loaded.
func UnresolvedExtConfig(path string, err error) ExtConfig {
	return ExtConfig{Name: path, Enabled: true, selectedPath: path, resolveErr: err}
}

// SelectedAs returns c reporting path as its configured extension path in load
// errors and reload issues. Discovery uses it when an entry file such as
// <dir>/index.js selects its directory, since upstream names the entry file.
func (c ExtConfig) SelectedAs(path string) ExtConfig {
	c.selectedPath = path
	return c
}

// ResolveError returns the source resolution failure of an
// UnresolvedExtConfig, or nil.
func (c ExtConfig) ResolveError() error {
	return c.resolveErr
}

// ExtensionLoadError is one extension that failed to load. Path is the
// configured extension path.
type ExtensionLoadError struct {
	Name  string
	Path  string
	Err   error
	fused bool
}

func (e *ExtensionLoadError) Error() string {
	if e.fused {
		return fmt.Sprintf("extension %q (fused): %v", e.Name, e.Err)
	}
	return fmt.Sprintf("extension %q: %v", e.Name, e.Err)
}

func (e *ExtensionLoadError) Unwrap() error {
	return e.Err
}

func unresolvedLoadErrors(configs []ExtConfig) []error {
	var errs []error
	for _, config := range configs {
		if config.resolveErr != nil {
			errs = append(errs, &ExtensionLoadError{Name: config.Name, Path: config.selectedPath, Err: config.resolveErr})
		}
	}
	return errs
}

func prependUniquePath(first, existing string) string {
	return prependUniquePathForGOOS(runtime.GOOS, first, existing)
}

func prependUniquePathForGOOS(goos, first, existing string) string {
	separator := os.PathListSeparator
	if goos == "windows" {
		separator = ';'
	}
	seen := make(map[string]struct{})
	paths := make([]string, 0)
	for _, value := range append([]string{first}, strings.Split(existing, string(separator))...) {
		if value == "" {
			continue
		}
		clean := filepath.Clean(value)
		if goos == "windows" {
			clean = strings.ToLower(clean)
		}
		if _, duplicate := seen[clean]; duplicate {
			continue
		}
		seen[clean] = struct{}{}
		paths = append(paths, value)
	}
	return strings.Join(paths, string(separator))
}

func envHasKey(value, key string) bool {
	name, _, ok := strings.Cut(value, "=")
	return ok && strings.EqualFold(name, key)
}

// extensionSourcePaths returns the loaded extension's path and resolved path:
// the path it was selected from, not a built executable, as upstream's
// createExtension(extensionPath, resolvedPath). ExtensionError, bug reports and
// extension listings show these.
func extensionSourcePaths(config ExtConfig) (path, resolvedPath string) {
	path = config.selectedPath
	if path == "" {
		path = config.Source
	}
	if path == "" {
		path = config.Path
	}
	resolvedPath = path
	if absolute, err := filepath.Abs(path); err == nil && path != "" {
		resolvedPath = absolute
	}
	return path, resolvedPath
}

func extConfigOrigin(config ExtConfig) string {
	if config.selectedPath != "" {
		return config.selectedPath
	}
	if config.Source != "" {
		return config.Source
	}
	if config.Path != "" {
		return config.Path
	}
	return "<unknown>"
}

// Host manages all subprocess extensions. It spawns binaries, manages
// connections, handles the register handshake, and produces
// [extension.Extension] structs suitable for feeding to [inproc.Runner].
//
// pig-specific: no upstream equivalent.
type Host struct {
	// slotCalls keeps UI replacements in the order the extensions sent them (runSlotCall).
	slotCalls          pendingSlotCalls
	toolRegistrationMu sync.Mutex
	mu                 sync.Mutex
	exts               map[string]*managedExt
	xref               xrefHub
	// providerConfigRefs maps a provider to the xref of its effective registered configuration root in the owning Node realm.
	providerConfigRefs map[string]providerConfigRef
	eventBus           hostEventBus
	// loadOrder ranks extension names by their position in the configured
	// extension list, so Extensions reports them in load order as upstream
	// does.
	loadOrder map[string]int
	// configSourceInfo is each configured extension's SourceInfo by name.
	// Packed cells start their members from cell descriptors, not the
	// configs, so buildExtension reads the provenance back from here.
	configSourceInfo map[string]extension.SourceInfo
	// configSelectedPath preserves the authored extension path for packed and isolated members.
	configSelectedPath map[string]string
	// embeddedCells is the immutable source-free cell set supplied by a Piglet
	// Binary. Reload reconstructs these cells alongside source extensions.
	embeddedCells []EmbeddedCell

	// sessionLogSubs names the extensions that have read the session log and so
	// receive it. Guarded by mu.
	sessionLogSubs map[string]struct{}

	cwd       string
	mode      string // run mode: tui|rpc|json|print, sent in ReadyPayload (ctx.mode)
	socketDir string

	// harnessBinary and harnessArgs are the executable and command-line
	// arguments of the process this Host runs in. A Node extension sees them
	// the way a Pi extension sees Pi's: process.argv[1] is a harness entry
	// that runs harnessBinary, followed by harnessArgs (harness_identity.go).
	harnessBinary   string
	harnessArgs     []string
	harnessArgvOnce sync.Once
	harnessArgvPath string
	harnessArgvErr  error

	// sockRuntime is a per-Host directory holding this instance's extension
	// sockets, created lazily under socketDir. Isolating sockets per Host keeps
	// parallel Pig instances (same user, same extensions) from computing the same
	// socket path and deleting each other's live sockets. Removed on Shutdown.
	sockRuntimeOnce sync.Once
	sockRuntimeDir  string
	sockRuntimeErr  error

	// sockNames maps an extension name to its short, stable socket leaf within
	// sockRuntimeDir (guarded by sockMu).
	sockMu    sync.Mutex
	sockNames map[string]string

	// shuttingDown is set when Shutdown is called. Crash handlers check
	// this to suppress "connection closed" noise during normal exit.
	shuttingDown atomic.Bool

	// builder handles auto-compilation of source-based extensions.
	builder *Builder

	// A successful Node preflight is stable for this Host's environment and
	// applies to every Node extension it starts.
	nodeRuntimeMu    sync.Mutex
	nodeRuntimeReady bool

	// uiBridge handles extension→host UI calls and widget pushes.
	uiBridge *UIBridge

	// staleMessage, once set, makes the Host reject every extension→host call with that message.
	staleMessage atomic.Pointer[string]

	// onCall is called when an extension sends a fire-and-forget call
	// (e.g. ui.notify). Set by the host wiring layer.
	onCall func(extName string, call *CallPayload) (*CallResultPayload, error)

	// onCrash is called when an extension process exits unexpectedly.
	// The callback receives the extension name and the supervisor's decision
	// (delay before restart, or error if circuit breaker tripped).
	onCrash func(name string, delay time.Duration, disabled bool, reason string)

	// configLoader supplies the authoritative normalized startup resolver for
	// Reload. Startup and reload must use the same extension inputs.
	configLoader func() ([]ExtConfig, error)

	providerRuntime          *extension.ExtensionRuntime
	onRegisterNativeProvider func(context.Context, *extension.NativeProvider) error
	nativeProviders          map[*Conn]map[string]*nativeProviderProxy
	nativeProviderHandles    map[string]*nativeProviderProxy

	// oauthLoginSessions holds the in-flight OAuth login callbacks per extension
	// name so the oauth.cb.* calls an extension issues during login route back
	// to the host UI callbacks the bridged provider published.
	oauthLoginMu       sync.Mutex
	oauthLoginSessions map[string]oauthLoginSession

	// transitionMu serializes startup, explicit reload and crash recovery. Node recovery owns a cancellable host lifetime and drains before Shutdown returns.
	transitionMu    sync.Mutex
	recoveryContext context.Context
	recoveryCancel  context.CancelFunc
	nodeFaults      map[string]string
	nodeGroups      map[string]string
	nodeCrashes     map[string]int

	// quarantinedCells records packed cells that crashed; PlanCells fissions a
	// quarantined key back to isolated subprocesses.
	quarantinedCells map[string]string
	// packedCellSupervisors gives each packed cell key the same crash-loop
	// circuit breaker (Supervisor, DefaultSupervisorConfig) every isolated
	// extension and every packed member already has, so a Node (or Go/Rust/
	// Python) cell that keeps crashing stays isolated instead of an explicit
	// Reload endlessly re-packing and re-crashing it (CNC-002). It is
	// host-scoped, not managedExt-scoped, because it must survive the
	// managedExt churn a repack causes. Guarded by packedCellSupervisorsMu,
	// not h.mu, so quarantinePackedCellGeneration can call RecordCrash while
	// holding h.mu for its own atomic generation check (h.mu is not
	// reentrant).
	packedCellSupervisors   map[string]*Supervisor
	packedCellSupervisorsMu sync.Mutex
	// packedCellGeneration counts, per packed cell key, how many processes
	// have ever been spawned for that key (nextPackedCellGeneration). Each
	// packedProcessState records the generation it claimed when spawned;
	// watchPackedProcess only quarantines on a crash report whose generation
	// is still the latest claimed for that key, so an async report for an
	// already-superseded process (a later Reload already started a fresh
	// process under the same content-derived key) cannot tear the new one
	// down (CNC-002).
	packedCellGeneration map[string]int
	// packedProcesses records every packed cell key this Host has ever
	// spawned a process for (startGoPackedCell). Reload's
	// armPackedProcessGenerations (CNC-002) reads its keys to bump each
	// one's packedCellGeneration up front, even after
	// quarantinePackedCellGeneration/disablePackedMember have already
	// removed that process's managedExts from h.exts (which normally
	// happens well before an explicit Reload, since per-extension crash
	// detection is independent of and typically faster than the
	// packed-process-level watcher).
	packedProcesses map[string]*packedProcessState
	// watchedPacked holds every started packed process until its watcher
	// has released its cache usage lease. Reload stops a replaced process
	// without waiting for it, so Shutdown drains these as well as the
	// processes of the extensions still registered.
	watchedPacked map[*packedProcessState]struct{}

	lastReload *ReloadReport

	// retained holds the extension processes that outlive a Session's Host; retainedShared is true when SetRuntimeRetention supplied it.
	retained       *RuntimeRetention
	retainedShared bool
	epoch          int
	parkSeq        atomic.Int64

	// loadErrors holds the formatted errors from the most recent LoadAll so the
	// interactive session can surface startup load/build/register failures
	// in-session instead of dropping them to a TUI-clobbered stderr. Guarded by
	// h.mu.
	loadErrors []string

	// startupMark records optional startup checkpoints. The command wires it only
	// when startup tracing is enabled; extension hosting otherwise pays one nil check.
	startupMark func(string)

	runtimeDrainHandler func()

	// commandFlights are the extension commands whose request is in flight; commandSuspendHandler receives their suspended count.
	commandFlightMu sync.Mutex
	// commandNotifyMu orders the suspended-count deliveries: the count is snapshotted and delivered under it, so the handler never sees a stale count after a newer one.
	commandNotifyMu       sync.Mutex
	inputEnded            bool
	quitSuspendedCh       chan struct{}
	commandFlights        map[*commandFlight]struct{}
	commandSuspendHandler func(suspended int)

	// widthFunc and heightFunc return the current terminal dimensions. Set by
	// the wiring layer (interactive mode) so the host can pass real geometry
	// to extensions in the ready payload and in change notifications.
	widthFunc  func() int
	heightFunc func() int

	// lastWidth and lastHeight track the most recent broadcasts so we only
	// notify extensions when the value actually changes.
	lastWidth  int
	lastHeight int
}

// managedExt tracks a single subprocess extension's lifecycle.
// LoadError is a structured extension load/startup failure. Validation and
// diagnostics use Phase/Code to tell users exactly where startup failed.
type LoadError struct {
	Extension string
	Phase     string
	Code      string
	StderrLog string
	// Hint is optional operator-facing remediation guidance (e.g. rebuild a
	// stale standalone binary). It is advisory and appended to Error().
	Hint string
	Err  error
}

func (e *LoadError) Error() string {
	if e == nil {
		return ""
	}
	if factoryErr, ok := errors.AsType[*FactoryLoadError](e.Err); ok {
		// The extension's own loader reported the failure, in Pi's words.
		return factoryErr.Error()
	}
	prefix := e.Phase
	if e.Code != "" {
		prefix += ":" + e.Code
	}
	var msg string
	if e.StderrLog != "" {
		msg = fmt.Sprintf("%s: %v (stderr: %s)", prefix, e.Err, e.StderrLog)
	} else {
		msg = fmt.Sprintf("%s: %v", prefix, e.Err)
	}
	if e.Hint != "" {
		msg += ": " + e.Hint
	}
	return msg
}

func (e *LoadError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func newLoadError(extensionName, phase, code string, err error) *LoadError {
	return &LoadError{Extension: extensionName, Phase: phase, Code: code, Err: err}
}

// standaloneRebuildHint returns a remediation hint for a standalone
// (prebuilt-binary) extension, or "" for source-mode extensions. Source-mode
// extensions recompile whenever the SDK changes (builder.hashSourceDir folds
// the replaced SDK into the cache key), so a wire-contract skew self-heals on
// next load; a prebuilt binary cannot and must be rebuilt by its author.
func standaloneRebuildHint(cfg ExtConfig) string {
	if cfg.Source != "" {
		return ""
	}
	return fmt.Sprintf("%q ships a prebuilt binary; if you updated pig, rebuild it against the current pig SDK", cfg.Name)
}

// registerDecodeHint explains a registration this host could not decode. For
// a runner pig builds from source, that happens when pig or its staged SDK
// changed after the runner was built, and `pig reload` restages the SDKs and
// drops the stale builds so the next load rebuilds it.
func registerDecodeHint(err error) string {
	if transport, ok := errors.AsType[*TransportError](err); ok && transport.Operation == "decode frame" {
		return "if pig or its SDK changed since this extension was built, run `pig reload` to rebuild it"
	}
	return ""
}

// frameSkewHint attributes an extension disconnect to extension-SDK frame-cap
// skew: the host sent a frame larger than a binary built against an older SDK
// could receive (notably a large getBranch result). Returns "" when no
// oversized frame was recently sent to the extension.
func frameSkewHint(cfg ExtConfig, size uint64, recent bool) string {
	if !recent {
		return ""
	}
	msg := fmt.Sprintf("%q disconnected just after pig sent a %.1f MiB frame; a binary built against a pig SDK older than the current %d MiB frame cap silently rejects frames above %d MiB",
		cfg.Name, float64(size)/(1<<20), MaxFrameSize/(1<<20), legacyFrameSize/(1<<20))
	if cfg.Source == "" {
		return msg + ". Rebuild this prebuilt extension against the current pig SDK"
	}
	return msg + ". pig will rebuild it on next load"
}

type managedExt struct {
	facetBridge  *FacetBridge
	pendingReady *Envelope
	nodeEntry    string
	replacement  atomic.Pointer[managedExt]
	config       ExtConfig
	host         *Host
	supervisor   *Supervisor
	// conn is the current connection. Only setConnLocked stores it, and only while holding Host.mu, so a Host.mu holder reads a stable value; every other reader loads it once through connection() and keeps that snapshot for the whole operation.
	conn atomic.Pointer[Conn]
	// procMu guards the process handle: proc, processTree, cmd and exitedCh. An automatic restart replaces them in startExt while a stop, a crash handler or a status reader can run; it is a leaf lock, never held while calling out.
	procMu          sync.Mutex
	proc            *os.Process
	processTree     *processTree
	cmd             *exec.Cmd // Retained so cmd.Wait() can be called during cleanup to avoid goroutine leaks
	cancel          context.CancelFunc
	parentCtx       context.Context      // Context the extension was started under; if cancelled, an exit is intentional teardown, not a crash
	exitedCh        <-chan struct{}      // Closed by the process reaper when cmd.Wait returns
	waitErr         error                // Process exit status; read only after <-exitedCh (channel close synchronizes)
	ext             *extension.Extension // Populated after successful register
	flagNames       []string
	flagDefaults    map[string]any
	wantsSessionLog bool
	nodeRuntime     bool // isolated extension hosted by the Node runtime; a packed one reports through packedProcess.node

	// entryCursor is how many session entries this extension has been sent.
	// Guarded by entryCursorMu because pushes originate from event, command,
	// and tool dispatch goroutines.
	entrySessionID        string
	entryCursor           int
	entryCursorMu         sync.Mutex
	sessionTransferActive bool
	providerNames         []string
	oauthProviderNames    []string // subset of providerNames that also registered a bridged OAuth provider
	stderrLogPath         string
	stderrLog             *processStderrLog
	sockPath              string               // Socket file path (for cleanup)
	packedCellKey         string               // Non-empty when hosted by a packed runtime cell
	packedProcess         *packedProcessState  // Shared process authority for packed-member sockets.
	shuttingDown          atomic.Bool          // True when graceful shutdown was initiated
	inProcServe           func(net.Conn) error // Non-nil for a fused in-process extension (D31)
	nodeRealm             bool                 // Hosted by a Node process, which is one xref realm
	releaseLiveness       func()               // Releases heartbeat ownership for live provider state.
	livenessOwnerMu       sync.Mutex
	cacheLease            *runtimecell.UsageLease
	// share counts the extensions hosted by this isolated Node process, which Pi's loader re-invokes factories in across reloads. procOwner is the extension that spawned it, and retainedFrom the process a pending start will re-invoke its factory in.
	share         *processShare
	shareReleased atomic.Bool
	procOwner     *managedExt
	retainedFrom  *processShare

	// toolRenders routes this extension's renderer invalidations to the
	// tool cards whose renderers it runs.
	toolRenders toolRenderSessions
}

// connection returns the current connection. An operation bound to one connection keeps the returned value instead of loading it again, because an adoption may replace it at any time.
func (me *managedExt) connection() *Conn { return me.conn.Load() }

// setConnLocked replaces the current connection. The caller holds Host.mu.
func (me *managedExt) setConnLocked(conn *Conn) { me.conn.Store(conn) }

func (me *managedExt) releaseLivenessOwner() {
	me.livenessOwnerMu.Lock()
	release := me.releaseLiveness
	me.releaseLiveness = nil
	me.livenessOwnerMu.Unlock()
	if release != nil {
		release()
	}
}

type stagedManagedExt struct {
	name string
	me   *managedExt
}

// NewHost creates an extension host using the process environment's Pig config
// root. Product assembly should prefer [NewHostWithConfigRoot].
func NewHost(cwd string) *Host {
	return NewHostWithConfigRoot(cwd, resolveConfigRoot())
}

// NewHostWithConfigRoot creates an extension host pinned to configRoot. The
// socketDir is where Unix sockets are created (for example
// $XDG_RUNTIME_DIR/pig/ or $TMPDIR/pig-<uid>/).
func NewHostWithConfigRoot(cwd, configRoot string) *Host {
	if strings.TrimSpace(configRoot) == "" {
		configRoot = resolveConfigRoot()
	}
	cacheDir := filepath.Join(configRoot, "cache", "ext")
	recoveryContext, recoveryCancel := context.WithCancel(context.Background())

	return &Host{
		providerRuntime:       extension.CreateExtensionRuntime(),
		recoveryContext:       recoveryContext,
		recoveryCancel:        recoveryCancel,
		cwd:                   cwd,
		socketDir:             resolveSocketDir(),
		harnessBinary:         harnessExecutable(),
		harnessArgs:           slices.Clone(os.Args[1:]),
		exts:                  make(map[string]*managedExt),
		loadOrder:             make(map[string]int),
		builder:               NewBuilderWithConfigRoot(cacheDir, configRoot),
		quarantinedCells:      make(map[string]string),
		packedCellSupervisors: make(map[string]*Supervisor),
	}
}

// SetStartupTrace registers startup checkpoint instrumentation. Labels are
// stable and contain only the extension identity and lifecycle phase.
func (h *Host) SetStartupTrace(mark func(string)) {
	h.startupMark = mark
}

func (h *Host) markExtension(name, phase string) {
	if h.startupMark == nil {
		return
	}
	name = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			return r
		default:
			return '-'
		}
	}, name)
	if len(name) > 64 {
		name = name[:64]
	}
	h.startupMark("extension." + name + "." + phase)
}

// SetCallHandler registers the callback for extension→host calls (ui.notify,
// sendMessage, etc.). Must be called before [Host.Load].
func (h *Host) SetCallHandler(fn func(extName string, call *CallPayload) (*CallResultPayload, error)) {
	h.onCall = fn
}

// Invalidate makes the Host reject every later extension→host call with message, as Pi's ExtensionRunner.invalidate makes a captured pi or ctx throw (runner.ts:679-690). A call that already started keeps its result, so the replacement command that triggered the teardown still returns. The first message wins.
func (h *Host) Invalidate(message string) {
	h.staleMessage.CompareAndSwap(nil, &message)
}

// SetUIBridge attaches a UI bridge for handling widget pushes and UI method
// calls. Must be called before [Host.Load].
func (h *Host) SetUIBridge(bridge *UIBridge) {
	h.uiBridge = bridge
	if bridge != nil {
		bridge.WatchSessionLog = h.watchSessionLog
		bridge.OnStateChanged = h.BroadcastStateUpdate
		bridge.resolveFlag = h.resolveFlagValue
	}
}

// watchSessionLog returns one page. The caller holds entryCursorMu through the
// response write so state updates cannot overtake the transfer.
func (h *Host) watchSessionLog(extName string, cursor int, complete bool) ([]json.RawMessage, int, bool, string) {
	h.mu.Lock()
	me := h.exts[extName]
	if h.sessionLogSubs == nil {
		h.sessionLogSubs = make(map[string]struct{})
	}
	h.sessionLogSubs[extName] = struct{}{}
	h.mu.Unlock()

	if me != nil && !complete {
		me.sessionTransferActive = true
	}
	if h.uiBridge == nil {
		if me != nil && complete {
			me.entryCursor = cursor
			me.sessionTransferActive = false
		}
		return nil, cursor, false, ""
	}
	h.uiBridge.mu.RLock()
	actions := h.uiBridge.actions
	h.uiBridge.mu.RUnlock()
	if actions == nil || actions.GetEntriesPage == nil {
		if me != nil && complete {
			me.entryCursor = cursor
			me.sessionTransferActive = false
		}
		return nil, cursor, false, ""
	}
	entries, next, more, leafID := actions.GetEntriesPage(cursor, sessionLogPageBytes)
	if me != nil {
		me.entryCursor = next
		if complete && !more && len(entries) == 0 {
			me.sessionTransferActive = false
		}
	}
	return entries, next, more, leafID
}

// subscribedToSessionLog reports whether an extension has asked for the
// session log. Subscriptions are keyed by name rather than held on the
// managedExt because an extension subscribes from its startup path, which runs
// before the host has committed it under that name.
func (h *Host) subscribedToSessionLog(name string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	_, ok := h.sessionLogSubs[name]
	return ok
}

// SetCrashHandler registers a callback for extension crash events.
func (h *Host) SetCrashHandler(fn func(name string, delay time.Duration, disabled bool, reason string)) {
	h.onCrash = fn
}

// withStderrLog appends an extension's captured stderr log path to a crash
// reason, so a user reading a crash or disable notice is told where the
// process's stderr went instead of having to discover the temp file (see
// startExt and startGoPackedCell, which create it) on their own.
func withStderrLog(reason, stderrLogPath string) string {
	if stderrLogPath == "" {
		return reason
	}
	if reason == "" {
		return fmt.Sprintf("(stderr: %s)", stderrLogPath)
	}
	return fmt.Sprintf("%s (stderr: %s)", reason, stderrLogPath)
}

// FormatCrashNotice renders the operator-facing text for an extension crash
// event. Both crash surfaces (the stderr handler in non-interactive mode and
// the TUI transcript notice) use it so their wording cannot drift.
func FormatCrashNotice(name string, delay time.Duration, disabled bool, reason string) string {
	switch {
	case disabled:
		return fmt.Sprintf("extension %q disabled: %s", name, reason)
	case reason != "":
		return fmt.Sprintf("extension %q crashed, restart in %s: %s", name, delay, reason)
	default:
		return fmt.Sprintf("extension %q crashed, restart in %s", name, delay)
	}
}

// SetConfigLoader overrides the config source used by Reload.
func (h *Host) SetConfigLoader(fn func() ([]ExtConfig, error)) {
	h.configLoader = fn
}

// SetMode sets the run mode (tui|rpc|json|print) sent to extensions in
// the ReadyPayload and read back as ctx.mode. Call before Load. An empty
// mode is sent as omitted; SDKs default it to "print" to match upstream
// (runner.ts:229).
func (h *Host) SetMode(mode string) {
	h.mode = mode
}

// SetWidthFunc registers a callback that returns the current terminal
// width. The host uses this to populate the ready payload with the real
// terminal width (instead of a hardcoded default) and to broadcast
// width_change notifications to all connected extensions.
func (h *Host) SetWidthFunc(fn func() int) {
	h.widthFunc = fn
}

// readyGeometry returns the terminal geometry to advertise in a ready payload.
//
// Width falls back to 120 because extensions load before the TUI exists, and
// the wiring layer pushes the real width immediately afterwards. Height has no
// comparable fallback: an extension that must know the height should treat 0 as
// "not reported yet" and wait for the height_change notification.
func (h *Host) readyGeometry() (width, height int) {
	width = 120
	if h.widthFunc != nil {
		if w := h.widthFunc(); w > 0 {
			width = w
		}
	}
	if h.heightFunc != nil {
		height = h.heightFunc()
	}
	return width, height
}

// SetHeightFunc registers the current terminal height.
// pig additive (D52): expose height to extension SDKs.
func (h *Host) SetHeightFunc(fn func() int) {
	h.heightFunc = fn
}

// NotifyWidth broadcasts a width_change notification to every connected
// extension, but only when the value has actually changed. Call this
// from the TUI's resize handler.
func (h *Host) NotifyWidth(width int) {
	if width <= 0 {
		return
	}
	if h.uiBridge != nil {
		h.uiBridge.SetWidth(width)
	}
	h.mu.Lock()
	if width == h.lastWidth {
		h.mu.Unlock()
		return
	}
	h.lastWidth = width
	conns := make([]*Conn, 0, len(h.exts))
	for _, me := range h.exts {
		if conn := me.connection(); conn != nil {
			conns = append(conns, conn)
		}
	}
	h.mu.Unlock()

	env := &Envelope{
		Type: MsgNotify,
		Notify: &NotifyPayload{
			Method: "width_change",
			Args:   json.RawMessage(fmt.Sprintf(`{"width":%d}`, width)),
		},
	}
	for _, c := range conns {
		_ = c.Send(env)
	}
}

// NotifyHeight broadcasts a height_change notification to every connected
// extension, but only when the value has actually changed. Call this from the
// TUI's resize handler alongside NotifyWidth, so extensions that render
// height-dependent content (chain graphs, dashboards) can reflow.
func (h *Host) NotifyHeight(height int) {
	if height <= 0 {
		return
	}
	h.mu.Lock()
	if height == h.lastHeight {
		h.mu.Unlock()
		return
	}
	h.lastHeight = height
	conns := make([]*Conn, 0, len(h.exts))
	for _, me := range h.exts {
		if conn := me.connection(); conn != nil {
			conns = append(conns, conn)
		}
	}
	h.mu.Unlock()

	env := &Envelope{
		Type: MsgNotify,
		Notify: &NotifyPayload{
			Method: "height_change",
			Args:   json.RawMessage(fmt.Sprintf(`{"height":%d}`, height)),
		},
	}
	for _, c := range conns {
		_ = c.Send(env)
	}
}

// SetNativeProviderCallback binds connection-owned native provider registrations.
func (h *Host) SetNativeProviderCallback(register func(context.Context, *extension.NativeProvider) error) {
	h.onRegisterNativeProvider = register
}

// Runtime returns the shared provider registration state for a Runner created from this Host's loaded extensions.
func (h *Host) Runtime() *extension.ExtensionRuntime { return h.providerRuntime }

// SetProviderCallbacks binds the registry, drains pending registrations and returns their owning-path diagnostics. Configure callbacks before dispatching handlers. A Runner can instead bind Runtime through BindCore to deliver these diagnostics to its error listeners.
func (h *Host) SetProviderCallbacks(register func(name string, config extension.ProviderConfig) error, unregister func(name string)) []*extension.ExtensionError {
	var failures []*extension.ExtensionError
	h.providerRuntime.BindProviderActions(extension.ProviderActions{RegisterProvider: register, UnregisterProvider: unregister}, func(err *extension.ExtensionError) {
		failures = append(failures, err)
	})
	return failures
}

// Load spawns and registers a subprocess extension. Blocks until the register
// handshake completes or the context expires.
// The extension runs in its own process outside the cell plan, so a later
// Reload claims that process only when the extension's plan is isolated too.
func (h *Host) Load(ctx context.Context, cfg ExtConfig) (*extension.Extension, error) {
	h.recordLoadOrder([]ExtConfig{cfg}, false)
	// Pi hands every extension of a loader one event bus (resource-loader.ts), so a Node realm added by Load joins the realms already running.
	h.planEventBus(ctx, PlanCells([]ExtConfig{cfg}, nil))
	me, ext, err := h.startManaged(ctx, cfg)
	if err != nil {
		return nil, err
	}

	var old *managedExt
	h.mu.Lock()
	old = h.exts[cfg.Name]
	h.exts[cfg.Name] = me
	// Another extension's registration may transfer a provider away from me once it is visible in h.exts.
	keep := providerNameSet(me.providerNames)
	h.mu.Unlock()
	if old != nil {
		h.stopManagedSkippingProviders(old, "replaced", keep)
	}
	return ext, nil
}

// LoadInProcess loads an extension whose factory runs inside the host process,
// served over an in-memory pipe with no subprocess and no runtime build. A fused
// Piglet Binary supplies serve as factory().RunWithConn. Registration, handshake, and
// lifecycle are otherwise identical to Load.
// pig additive (D31): fused in-process extension runtime.
func (h *Host) LoadInProcess(ctx context.Context, cfg ExtConfig, serve func(net.Conn) error) (*extension.Extension, error) {
	h.recordLoadOrder([]ExtConfig{cfg}, false)
	staged, err := h.stageInProcess(ctx, cfg, serve)
	if err != nil {
		return nil, err
	}
	h.commitStaged([]stagedManagedExt{staged}, nil, "replaced")
	return staged.me.ext, nil
}

func (h *Host) stageInProcess(ctx context.Context, cfg ExtConfig, serve func(net.Conn) error) (stagedManagedExt, error) {
	if serve == nil {
		return stagedManagedExt{}, newLoadError(cfg.Name, "resolve", "missing_serve", errors.New("in-process serve func is required"))
	}
	if cfg.Name == "" {
		return stagedManagedExt{}, newLoadError(cfg.Name, "resolve", "missing_name", errors.New("extension name is required"))
	}
	supCfg := cfg.SupervisorConfig
	if supCfg.MaxCrashes == 0 {
		supCfg = DefaultSupervisorConfig()
	}
	me := &managedExt{config: cfg, host: h, supervisor: NewSupervisor(supCfg), inProcServe: serve}
	if _, err := h.startExt(ctx, me, false); err != nil {
		h.stopManaged(me, "load failed")
		return stagedManagedExt{}, err
	}
	return stagedManagedExt{name: cfg.Name, me: me}, nil
}

func (h *Host) startManaged(ctx context.Context, cfg ExtConfig) (*managedExt, *extension.Extension, error) {
	return h.startManagedIn(ctx, cfg, nil)
}

// startManagedIn starts cfg, re-invoking its factory in the retained process when from is not nil.
func (h *Host) startManagedIn(ctx context.Context, cfg ExtConfig, from *processShare) (*managedExt, *extension.Extension, error) {
	released := from == nil
	defer func() {
		if !released {
			from.release()
		}
	}()
	if cfg.Name == "" {
		return nil, nil, newLoadError(cfg.Name, "resolve", "missing_name", errors.New("extension name is required"))
	}

	// Auto-build from source if needed.
	if cfg.Source != "" && cfg.Path == "" {
		result, err := h.builder.BuildContext(ctx, cfg.Name, cfg.Source)
		if err != nil {
			return nil, nil, newLoadError(cfg.Name, "build", "build_failed", fmt.Errorf("build from %s: %w", cfg.Source, err))
		}
		cfg.Path = result.BinaryPath
	}

	if cfg.Path == "" {
		return nil, nil, newLoadError(cfg.Name, "resolve", "missing_entrypoint", errors.New("either path or source must be set"))
	}

	supCfg := cfg.SupervisorConfig
	if supCfg.MaxCrashes == 0 {
		supCfg = DefaultSupervisorConfig()
	}

	me := &managedExt{
		config:       cfg,
		host:         h,
		supervisor:   NewSupervisor(supCfg),
		retainedFrom: from,
	}
	released = true // me owns the reference now.

	ext, err := h.startExt(ctx, me, false)
	if err != nil {
		h.stopManaged(me, "load failed")
		return nil, nil, err
	}
	return me, ext, nil
}

// LoadAll loads multiple extensions using the cell planner, which packs
// compatible factory-mode extensions into shared processes (D20). This is
// the cell-planned equivalent of calling Load() per-extension and should
// be used for initial startup to get the same packing behavior as /reload.
// Non-fatal errors are collected and returned alongside successfully loaded
// extensions.
func (h *Host) LoadAll(ctx context.Context, configs []ExtConfig) ([]extension.Extension, []error) {
	h.transitionMu.Lock()
	defer h.transitionMu.Unlock()
	// Processes a predecessor parked for this Host but no cell claimed end with the load.
	defer func() {
		h.mu.Lock()
		epoch := h.epoch
		h.mu.Unlock()
		h.retention().loadDone(epoch)
	}()
	if len(configs) == 0 {
		return nil, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, []error{err}
	}
	unresolved := unresolvedLoadErrors(configs)
	if len(unresolved) > 0 {
		configs = slices.DeleteFunc(slices.Clone(configs), func(config ExtConfig) bool { return config.resolveErr != nil })
	}
	configs = disambiguateIdentities(configs)
	var loaded []extension.Extension
	errs := unresolved
	// Fused extensions (Piglet Binary fused, D31) run in-process over a pipe; the rest
	// plan into subprocess cells. fusedResolver is nil in stock pig, so cellConfigs
	// aliases configs and every extension takes the unchanged cell path.
	cellConfigs := configs
	if fusedResolver != nil {
		cellConfigs = cellConfigs[:0:0]
		for _, cfg := range configs {
			serve, ok := fusedResolver.FusedServe(cfg)
			if !ok {
				cellConfigs = append(cellConfigs, cfg)
				continue
			}
			ext, err := h.LoadInProcess(ctx, cfg, serve)
			if err != nil {
				errs = append(errs, &ExtensionLoadError{Name: cfg.Name, Path: extConfigOrigin(cfg), Err: err, fused: true})
				continue
			}
			loaded = append(loaded, *ext)
		}
	}
	// Populate ContentHash for source-based extensions so the cell planner
	// can correctly invalidate packed-cell caches when source files change.
	// Without this, cell keys depend only on static metadata (name, path,
	// package) and stale binaries survive source edits.
	for i := range cellConfigs {
		if !cellConfigs[i].Enabled {
			continue
		}
		h.markExtension(cellConfigs[i].Name, "discover-start")
		if cellConfigs[i].Source != "" && cellConfigs[i].ContentHash == "" {
			bt, err := detectBuildType(cellConfigs[i].Source)
			if err == nil {
				if hash, err := hashSourceDir(cellConfigs[i].Source, bt); err == nil {
					cellConfigs[i].ContentHash = hash
				}
			}
		}
		h.markExtension(cellConfigs[i].Name, "discover-done")
	}
	h.recordLoadOrder(configs, false)
	cells := h.planCells(cellConfigs)
	h.planEventBus(ctx, cells)
	h.stageCellsInOrder(ctx, cells, func(cell CellSpec, outcome stageOutcome, err error) {
		outcomes, failures := h.isolateCellFailure(ctx, cell, nil, outcome, err)
		for _, failure := range failures {
			errs = append(errs, &ExtensionLoadError{Name: failure.cfg.Name, Path: extConfigOrigin(failure.cfg), Err: failure.err})
		}
		for _, outcome := range outcomes {
			h.commitStaged(outcome.staged, nil, "initial load")
			for _, s := range outcome.staged {
				h.mu.Lock()
				me := h.exts[s.name]
				h.mu.Unlock()
				if me != nil && me.ext != nil {
					loaded = append(loaded, *me.ext)
				}
			}
		}
	})
	h.mu.Lock()
	h.loadErrors = h.loadErrors[:0]
	for _, e := range errs {
		h.loadErrors = append(h.loadErrors, e.Error())
	}
	h.mu.Unlock()
	h.sortExtensionsByLoadOrder(loaded)
	return loaded, errs
}

// LoadErrors returns the formatted errors from the most recent LoadAll, for
// surfacing startup extension load/build/register failures in-session. Safe for
// concurrent UI use.
func (h *Host) LoadErrors() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.loadErrors...)
}

// Extensions returns all successfully registered extension structs.
func (h *Host) Extensions() []extension.Extension {
	h.mu.Lock()
	defer h.mu.Unlock()

	var result []extension.Extension
	for _, me := range h.exts {
		if me.ext != nil {
			result = append(result, *me.ext)
		}
	}
	h.sortExtensionsByLoadOrderLocked(result)
	return result
}

func (h *Host) sortExtensionsByLoadOrder(extensions []extension.Extension) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.sortExtensionsByLoadOrderLocked(extensions)
}

func (h *Host) sortExtensionsByLoadOrderLocked(extensions []extension.Extension) {
	slices.SortStableFunc(extensions, func(a, b extension.Extension) int {
		orderA, knownA := h.loadOrder[a.Name]
		orderB, knownB := h.loadOrder[b.Name]
		switch {
		case knownA && knownB && orderA != orderB:
			return orderA - orderB
		case knownA != knownB:
			if knownA {
				return -1
			}
			return 1
		}
		return strings.Compare(a.Name, b.Name)
	})
}

// recordLoadOrder ranks configs by their list position. A reload replaces the
// ranking; a load appends names not yet ranked.
func (h *Host) recordLoadOrder(configs []ExtConfig, replace bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if replace || h.loadOrder == nil {
		h.loadOrder = make(map[string]int, len(configs))
	}
	if replace || h.configSourceInfo == nil {
		h.configSourceInfo = make(map[string]extension.SourceInfo, len(configs))
	}
	if replace || h.configSelectedPath == nil {
		h.configSelectedPath = make(map[string]string, len(configs))
	}
	for _, config := range configs {
		if _, ranked := h.loadOrder[config.Name]; !ranked {
			h.loadOrder[config.Name] = len(h.loadOrder)
		}
		if config.SourceInfo != nil {
			h.configSourceInfo[config.Name] = config.SourceInfo
		}
		if config.selectedPath != "" {
			h.configSelectedPath[config.Name] = config.selectedPath
		}
	}
}

// configuredSelectedPath returns the configured authored extension path.
func (h *Host) configuredSelectedPath(name string) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.configSelectedPath[name]
}

// extensionSourceInfo returns the SourceInfo configured for me's extension.
func (h *Host) extensionSourceInfo(me *managedExt) extension.SourceInfo {
	if me.config.SourceInfo != nil {
		return me.config.SourceInfo
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.configSourceInfo[me.config.Name]
}

// ExtensionCount returns the number of loaded subprocess extensions.
func (h *Host) ExtensionCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	count := 0
	for _, me := range h.exts {
		if me.ext != nil {
			count++
		}
	}
	return count
}

// Builder exposes the extension builder so the owner can install policy that
// core cannot reach, such as the staged-SDK freshness check whose reference
// data lives outside core.
func (h *Host) Builder() *Builder { return h.builder }

// BroadcastStateUpdate sends a "state_update" notify to every connected
// subprocess extension with a freshly-snapshotted [StatePayload]. Call this
// after host-side transitions that mutate the synchronous getter surface
// (active tools, thinking level, idle state, system prompt, context usage,
// flag values, etc.) so extension caches stay accurate.
//
// Safe to call when no extensions are loaded or no UIBridge is wired -
// in that case it is a no-op.
func (h *Host) BroadcastStateUpdate() {
	if h.uiBridge == nil {
		return
	}
	h.mu.Lock()
	type target struct {
		me   *managedExt
		conn *Conn
	}
	targets := make([]target, 0, len(h.exts))
	for _, me := range h.exts {
		if conn := me.connection(); conn != nil {
			targets = append(targets, target{me, conn})
		}
	}
	h.mu.Unlock()
	// Each extension holds its own session-entry cursor, so the payload cannot
	// be shared: every recipient needs the tail it is personally missing.
	for _, t := range targets {
		_ = h.pushStateTo(context.Background(), t.me, t.conn)
	}
}

func readableSessionFile(path string) bool {
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

// pushStateTo sends me's state on conn, one of me's connections.
func (h *Host) pushStateTo(ctx context.Context, me *managedExt, conn *Conn) error {
	if h.uiBridge == nil || me == nil || conn == nil {
		return nil
	}
	me.entryCursorMu.Lock()
	defer me.entryCursorMu.Unlock()

	for {
		subscribed := h.subscribedToSessionLog(me.config.Name)
		includeEntries := subscribed && !me.sessionTransferActive
		state := h.uiBridge.Snapshot(me.flagNames, me.entryCursor, includeEntries)
		if state.Session != nil && state.Session.SessionID != me.entrySessionID {
			me.entrySessionID = state.Session.SessionID
			me.entryCursor = 0
			me.sessionTransferActive = false
			includeEntries = subscribed
			state = h.uiBridge.Snapshot(me.flagNames, 0, includeEntries)
		}
		if !subscribed && !me.sessionTransferActive && me.wantsSessionLog && state.Session != nil && !readableSessionFile(state.Session.SessionFile) {
			h.mu.Lock()
			if h.sessionLogSubs == nil {
				h.sessionLogSubs = make(map[string]struct{})
			}
			h.sessionLogSubs[me.config.Name] = struct{}{}
			h.mu.Unlock()
			includeEntries = true
			state = h.uiBridge.Snapshot(me.flagNames, me.entryCursor, includeEntries)
		}
		args, err := json.Marshal(map[string]any{"state": state})
		if err != nil {
			return err
		}
		if err := conn.sendAndWait(ctx, &Envelope{
			Type: MsgNotify,
			Notify: &NotifyPayload{
				Method: "state_update",
				Args:   args,
			},
		}); err != nil {
			return err
		}
		if state.Session == nil || !includeEntries {
			return nil
		}
		me.entryCursor = state.Session.EntryCount
		if !state.Session.EntriesRemaining {
			return nil
		}
	}
}

// ProviderNames returns provider names registered by a loaded extension.
func (h *Host) ProviderNames(extName string) []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	me, ok := h.exts[extName]
	if !ok || len(me.providerNames) == 0 {
		return nil
	}
	return append([]string(nil), me.providerNames...)
}

// OAuthProviderNames returns runtime-registered OAuth provider IDs owned by an extension.
func (h *Host) OAuthProviderNames(extName string) []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	me, ok := h.exts[extName]
	if !ok {
		return nil
	}
	return append([]string(nil), me.oauthProviderNames...)
}

// FlagDefault returns the first committed default for a flag registered by the named extension.
func (h *Host) FlagDefault(extName, flagName string) any {
	return h.resolveFlagValue(extName, flagName, nil)
}

func (h *Host) resolveFlagValue(extName, flagName string, override any) any {
	h.mu.Lock()
	defer h.mu.Unlock()
	if extName != "" {
		me := h.exts[extName]
		if me == nil || me.ext == nil {
			return nil
		}
		if _, registered := me.ext.Flags[flagName]; !registered {
			return nil
		}
	}
	if override != nil {
		return override
	}
	var value any
	var firstName string
	firstRank := int(^uint(0) >> 1)
	for name, me := range h.exts {
		if me.ext == nil {
			continue
		}
		candidate, exists := me.flagDefaults[flagName]
		if !exists {
			continue
		}
		rank, known := h.loadOrder[name]
		if !known {
			rank = int(^uint(0) >> 1)
		}
		if firstName == "" || rank < firstRank || (rank == firstRank && name < firstName) {
			value, firstName, firstRank = candidate, name, rank
		}
	}
	return value
}

// QuarantinedCells returns a snapshot of packed runtime cells disabled after a crash.
func (h *Host) QuarantinedCells() map[string]string {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make(map[string]string, len(h.quarantinedCells))
	maps.Copy(out, h.quarantinedCells)
	return out
}

// SetRuntimeDrainHandler installs the process-owner callback for a Node event-loop drain after input end. A drain is not handler completion; the callback must not resume pending extension work.
func (h *Host) SetRuntimeDrainHandler(fn func()) {
	h.mu.Lock()
	h.runtimeDrainHandler = fn
	h.mu.Unlock()
}

// commandFlight is one extension command request that has not returned. suspended is guarded by Host.commandFlightMu.
type commandFlight struct {
	node      bool
	suspended bool
}

func (me *managedExt) hostsNodeRuntime() bool {
	if me.packedProcess != nil {
		return me.packedProcess.node
	}
	return me.nodeRuntime
}

func (h *Host) beginCommandFlight(me *managedExt) *commandFlight {
	f := &commandFlight{node: me.hostsNodeRuntime()}
	h.commandFlightMu.Lock()
	if h.commandFlights == nil {
		h.commandFlights = make(map[*commandFlight]struct{})
	}
	h.commandFlights[f] = struct{}{}
	h.commandFlightMu.Unlock()
	return f
}

func (h *Host) endCommandFlight(f *commandFlight) {
	h.commandFlightMu.Lock()
	delete(h.commandFlights, f)
	was := f.suspended
	h.commandFlightMu.Unlock()
	if was {
		h.notifyCommandSuspended()
	}
}

func (h *Host) notifyCommandSuspended() {
	h.commandNotifyMu.Lock()
	defer h.commandNotifyMu.Unlock()
	h.commandFlightMu.Lock()
	suspended := 0
	for f := range h.commandFlights {
		if f.suspended {
			suspended++
		}
	}
	handler := h.commandSuspendHandler
	h.commandFlightMu.Unlock()
	if handler != nil {
		handler(suspended)
	}
}

// setCommandSuspended records that the runtime reports the command suspended: its event-loop window closed unresponded.
func (h *Host) setCommandSuspended(f *commandFlight, suspended bool) {
	h.commandFlightMu.Lock()
	changed := f.suspended != suspended
	f.suspended = suspended
	h.commandFlightMu.Unlock()
	if changed {
		h.notifyCommandSuspended()
	}
}

func (h *Host) quitSuspended() chan struct{} {
	if h.quitSuspendedCh == nil {
		h.quitSuspendedCh = make(chan struct{})
	}
	return h.quitSuspendedCh
}

// setQuitHandlerSuspended records that a quit session_shutdown handler is suspended after its event-loop window: in Pi its wait keeps the process alive, so suspended commands can still settle and respond.
// pig divergence (D85): the suspended quit handler keeps only this runtime process's commands alive; Pi's single process keeps every extension's command alive.
func (h *Host) setQuitHandlerSuspended() {
	h.commandFlightMu.Lock()
	defer h.commandFlightMu.Unlock()
	ch := h.quitSuspended()
	select {
	case <-ch:
	default:
		close(ch)
	}
}

// inputHasEnded reports whether EndInput ran. Only then can a runtime's suspension report change what a command or quit handler response does.
func (h *Host) inputHasEnded() bool {
	h.commandFlightMu.Lock()
	defer h.commandFlightMu.Unlock()
	return h.inputEnded
}

// withCommandSuspension has a command request report its runtime's suspension to the command's flight. Before input ends a suspension cannot hold the response, so the response does not wait for the delivery of a suspension, which waits for every earlier call of the connection, including another request's exec or model stream that has not finished.
func (h *Host) withCommandSuspension(ctx context.Context, f *commandFlight) context.Context {
	return withRequestSuspension(ctx, func(suspended bool) { h.setCommandSuspended(f, suspended) }, h.inputHasEnded)
}

// heldAfterInputEnd reports whether the command's response must not be delivered: input ended and the command is suspended, so Pi has exited before its continuation could run.
func (h *Host) heldAfterInputEnd(f *commandFlight) bool {
	h.commandFlightMu.Lock()
	defer h.commandFlightMu.Unlock()
	return h.inputEnded && f.suspended
}

// SetCommandSuspendHandler installs the process-owner callback that receives how many in-flight extension commands are suspended: a Node command whose runtime reported its event-loop window closed unresponded, or a command of another runtime that was running at the checkpoint. The callback runs on host goroutines, must not block, and must not resume or cancel the command.
func (h *Host) SetCommandSuspendHandler(fn func(suspended int)) {
	h.commandFlightMu.Lock()
	h.commandSuspendHandler = fn
	h.commandFlightMu.Unlock()
}

// FlushCommands is the stdout-flush checkpoint of an RPC shutdown. Pi's flush follows stdin's end within microtasks and one tick, so a runtime with no microtask continuation has nothing that can still settle: its command still running counts as suspended. A Node runtime reports its own commands as their windows close. A command that settles first responds normally. It does not cancel requests or synthesize responses.
func (h *Host) FlushCommands() {
	// pig additive (D19): only the host can apply Pi's flushRawStdout boundary (output-guard.ts:105-108) to a runtime that reports no event-loop window.
	h.commandFlightMu.Lock()
	for f := range h.commandFlights {
		if !f.node {
			f.suspended = true
		}
	}
	h.commandFlightMu.Unlock()
	h.notifyCommandSuspended()
}

// EndInput tells hosted runtimes that no further terminal or RPC input can arrive. This does not cancel requests or synthesize responses.
func (h *Host) EndInput() {
	// pig additive (D19): the subprocess transport is not an upstream event-loop keepalive after stdin ends.
	h.commandFlightMu.Lock()
	h.inputEnded = true
	h.commandFlightMu.Unlock()
	h.mu.Lock()
	var connections []*Conn
	for _, me := range h.exts {
		connections = append(connections, me.connection())
	}
	h.mu.Unlock()
	for _, conn := range connections {
		if conn != nil {
			_ = conn.Send(&Envelope{Type: MsgNotify, Notify: &NotifyPayload{Method: "runtime_input_end"}})
		}
	}
}

// TerminateProcesses kills hosted processes without running extension callbacks or waiting for their handlers. It is only for a host process that is about to exit; ordinary owners use Shutdown to release all resources.
func (h *Host) TerminateProcesses() {
	// pig additive (D19): subprocess runtimes must not outlive a forced host exit.
	h.shuttingDown.Store(true)
	h.mu.Lock()
	processes := make(map[*os.Process]struct{})
	trees := make(map[*processTree]struct{})
	for _, me := range h.exts {
		if proc, tree := me.processHandle(); tree != nil {
			trees[tree] = struct{}{}
		} else if proc != nil {
			processes[proc] = struct{}{}
		}
	}
	for process := range h.watchedPacked {
		if process.processTree != nil {
			trees[process.processTree] = struct{}{}
		} else if process.cmd != nil && process.cmd.Process != nil {
			processes[process.cmd.Process] = struct{}{}
		}
	}
	h.mu.Unlock()
	for tree := range trees {
		_ = tree.Kill()
	}
	for process := range processes {
		_ = process.Kill()
	}
}

// Shutdown detaches all extension consumers before removing providers, then stops and reaps every managed process. Session owners emit and await session_shutdown before calling it.
func (h *Host) Shutdown(reason string) {
	h.shuttingDown.Store(true)
	if h.recoveryCancel != nil {
		h.recoveryCancel()
	}
	h.transitionMu.Lock()
	h.mu.Lock()
	exts := make([]*managedExt, 0, len(h.exts))
	for _, me := range h.exts {
		exts = append(exts, me)
	}
	h.mu.Unlock()

	// All consumers are ending together. Retire their UI/catalog subscriptions before provider removal, so cleanup cannot rebuild and send snapshots to siblings that are also shutting down.
	if h.uiBridge != nil {
		for _, me := range exts {
			h.uiBridge.ClearExtensionConn(me.config.Name, me.connection())
		}
	}
	for _, me := range exts {
		h.shutdownExt(me.config.Name)
	}
	// Drain the stopped processes so none outlives Shutdown with its handles
	// and cache usage lease still open.
	for _, me := range exts {
		me.reap()
	}
	h.mu.Lock()
	watched := make([]*packedProcessState, 0, len(h.watchedPacked))
	for process := range h.watchedPacked {
		watched = append(watched, process)
	}
	h.mu.Unlock()
	for _, process := range watched {
		process.stopAndReap()
	}
	h.transitionMu.Unlock()
	for _, process := range watched {
		// A process parked for the replacement Host keeps running past this Host; its watcher ends with it.
		if process.watcherDone != nil && process.processReleased() {
			<-process.watcherDone
		}
	}

	if h.sockRuntimeDir != "" {
		_ = os.RemoveAll(h.sockRuntimeDir)
	}
}

// ensureSockRuntimeDir lazily creates this Host's private socket directory under
// socketDir and returns it. os.MkdirTemp guarantees a unique name even across
// concurrent Hosts, so no two instances share a socket path.
func (h *Host) ensureSockRuntimeDir() (string, error) {
	h.sockRuntimeOnce.Do(func() {
		if err := os.MkdirAll(h.socketDir, 0o700); err != nil {
			h.sockRuntimeErr = err
			return
		}
		h.sockRuntimeDir, h.sockRuntimeErr = os.MkdirTemp(h.socketDir, "host-*")
	})
	return h.sockRuntimeDir, h.sockRuntimeErr
}

// sockPathFor returns this Host's socket path for the named extension. Names are
// short and stable per Host (e-0.sock, e-1.sock, ...) rather than derived from
// the extension name: the runtime directory is private, so a counter is unique,
// and a short leaf keeps the full path well under the platform sun_path limit
// (104 bytes on macOS) that a long extension name plus a deep $TMPDIR could
// otherwise exceed. The mapping is stable across reloads so a reload rebinds the
// same path.
func (h *Host) sockPathFor(name string) (string, error) {
	dir, err := h.ensureSockRuntimeDir()
	if err != nil {
		return "", err
	}
	h.sockMu.Lock()
	defer h.sockMu.Unlock()
	if h.sockNames == nil {
		h.sockNames = make(map[string]string)
	}
	leaf, ok := h.sockNames[name]
	if !ok {
		leaf = fmt.Sprintf("e-%d.sock", len(h.sockNames))
		h.sockNames[name] = leaf
	}
	path := filepath.Join(dir, leaf)
	if err := validateUnixSocketPath(runtime.GOOS, path); err != nil {
		return "", err
	}
	return path, nil
}

// IsShuttingDown returns true after Shutdown has been called.
func (h *Host) IsShuttingDown() bool {
	return h.shuttingDown.Load()
}

// shutdownExt gracefully shuts down a single managed extension by name.
// Sends a shutdown message, cancels the context, kills the process, and
// removes the socket file.
func (h *Host) shutdownExt(name string) {
	h.mu.Lock()
	me, ok := h.exts[name]
	if ok {
		delete(h.exts, name)
	}
	h.mu.Unlock()
	if !ok {
		return
	}
	h.stopManaged(me, "reload")
}

// reap waits until a stopped extension's process has exited and been reaped
// and its cache usage lease released. The process was killed, so the wait is
// bounded by process teardown and the command's WaitDelay. A fused extension
// has no process.
func (me *managedExt) reap() {
	if me.packedProcess != nil {
		if me.packedProcess.cmd != nil && me.packedProcess.processReleased() {
			_ = me.packedProcess.wait()
			// Callers may hold transitionMu; process.wait owns log cleanup and Shutdown joins the watcher after releasing that lock.
			me.packedProcess.releaseUsageLease()
		}
		return
	}
	if me.share != nil && me.share.alive() && !me.share.exited() {
		// Another extension still runs in this process.
		return
	}
	if exited := me.exited(); exited != nil {
		<-exited
	}
	me.stderrLog.remove()
}

// releaseShare gives back the reference me holds on its process, once per process. The process ends with its last reference.
func (me *managedExt) releaseShare() {
	if me.share != nil && me.shareReleased.CompareAndSwap(false, true) {
		me.share.release()
	}
}

func (h *Host) stopManaged(me *managedExt, reason string) {
	h.stopManagedSkippingProviders(me, reason, nil)
}

// processSurvivesStop reports whether the process hosting me keeps running after me stops, because another generation or a parked reference holds it.
func (me *managedExt) processSurvivesStop() bool {
	switch {
	case me.packedProcess != nil && me.packedProcess.share != nil:
		own := int32(0)
		if !me.packedProcess.stopping.Load() {
			own = 1
		}
		return me.packedProcess.share.refs.Load()-own > 0
	case me.share != nil:
		own := int32(0)
		if !me.shareReleased.Load() {
			own = 1
		}
		return me.share.refs.Load()-own > 0
	}
	return false
}

func (h *Host) stopManagedSkippingProviders(me *managedExt, reason string, keepProviders map[string]struct{}) {
	if me == nil {
		return
	}
	survives := me.processSurvivesStop()
	me.shuttingDown.Store(true)
	h.mu.Lock()
	conn := me.connection()
	released := h.forgetNativeProvidersLocked(conn)
	h.mu.Unlock()
	h.releaseProviderCallbacks(released)
	me.releaseLivenessOwner()
	if conn != nil {
		if survives {
			// The process keeps running, so the generation must finish its teardown before the successor stands alone.
			_ = conn.Retire(reason)
		} else {
			_ = conn.Close(reason)
		}
	}
	switch {
	case me.packedProcess != nil:
		me.packedProcess.stop()
	case me.share != nil:
		// The process ends with its last extension; a spawner's own context is the process's and is cancelled with it.
		me.releaseShare()
		if me.procOwner != nil && me.cancel != nil {
			me.cancel()
		}
	default:
		if me.cancel != nil {
			me.cancel()
		}
		me.killProcess()
	}
	if me.sockPath != "" {
		_ = os.Remove(me.sockPath)
	}
	if h.uiBridge != nil {
		h.uiBridge.ClearExtensionConn(me.config.Name, conn)
	}
	for _, name := range me.providerNames {
		if _, keep := keepProviders[name]; keep {
			continue
		}
		h.dropProviderConfigRef(name)
		if h.providerRuntime != nil {
			h.providerRuntime.UnregisterProvider(name)
		}
		if h.uiBridge != nil {
			h.uiBridge.ForgetProviderRegistration(name)
		}
	}
	h.unregisterOAuthProviders(me, keepProviders)
}

func providerNameSet(names []string) map[string]struct{} {
	out := make(map[string]struct{}, len(names))
	for _, name := range names {
		out[name] = struct{}{}
	}
	return out
}

// Reload re-reads extension config, starts a fresh replacement for every
// enabled extension beside the currently running ones (unchanged extensions
// reuse their cached build artifact but, like upstream's re-invoked factories,
// never their running instance), validates their handshakes, then swaps the
// new set into the registry. Like upstream reload, an extension that fails to
// resolve, build, start, or register is not loaded: its previous runtime is
// stopped, its failure is recorded in ReloadReport.Issues, and every other
// extension loads. Only a config loader failure fails the reload.
func (h *Host) Reload(ctx context.Context) ([]extension.Extension, error) {
	ctx = context.WithValue(ctx, deferNodeReadyKey{}, true)
	ctx = context.WithValue(ctx, reloadingKey{}, reloadPasses.Add(1))
	h.transitionMu.Lock()
	defer h.transitionMu.Unlock()
	reloadStart := time.Now()
	rep := &ReloadReport{StartedAt: reloadStart}
	defer func() {
		rep.Duration = time.Since(reloadStart)
		h.recordReloadReport(rep)
	}()
	var (
		cfgs []ExtConfig
		err  error
	)
	if h.configLoader == nil {
		err = errors.New("extension config loader is required")
	} else {
		cfgs, err = h.configLoader()
	}
	if err != nil {
		rep.Error = err.Error()
		return nil, fmt.Errorf("reload config: %w", err)
	}
	// Only once config loading has actually succeeded (CNC-003): invalidate
	// every currently-known packed process's generation (CNC-002; see
	// armPackedProcessGenerations), then give a crashed packed cell (from an
	// earlier reload cycle) another chance to repack, unless it has already
	// crashed too many times this process's lifetime. A config-loader
	// failure returns above and retains every currently running extension
	// and process unchanged, so arming generations before this point would
	// invalidate crash ownership for a process this aborted reload never
	// actually replaced: a real, later crash of that still-current process
	// would then be silently discarded as stale instead of quarantined.
	h.armPackedProcessGenerations()
	h.releaseRetryableQuarantines()
	// Explicit reload ends inconclusive diagnostic partitions; identified culprits remain quarantined.
	clear(h.nodeGroups)

	for _, cfg := range cfgs {
		if cfg.resolveErr != nil {
			rep.Issues = append(rep.Issues, reloadIssue(cfg, cfg.resolveErr))
		}
	}
	cfgs = disambiguateIdentities(cfgs)
	newByName := make(map[string]ExtConfig, len(cfgs))
	for _, cfg := range cfgs {
		if cfg.Enabled {
			newByName[cfg.Name] = cfg
		}
	}

	h.mu.Lock()
	oldByName := make(map[string]*managedExt, len(h.exts))
	maps.Copy(oldByName, h.exts)
	embeddedCells := cloneEmbeddedCells(h.embeddedCells)
	h.mu.Unlock()
	for _, cfg := range embeddedCellConfigs(embeddedCells) {
		newByName[cfg.Name] = cfg
	}

	var staged []stagedManagedExt
	var removed []*managedExt

	for name, old := range oldByName {
		if _, ok := newByName[name]; !ok {
			removed = append(removed, old)
			rep.Removed = append(rep.Removed, name)
		}
	}
	removeOld := func(name string) {
		old := oldByName[name]
		if old == nil || slices.Contains(removed, old) {
			return
		}
		removed = append(removed, old)
		rep.Removed = append(rep.Removed, name)
	}
	for _, cfg := range cfgs {
		if cfg.resolveErr != nil {
			removeOld(cfg.Name)
		}
	}

	// Populate ContentHash for source-based extensions (see LoadAll).
	for i := range cfgs {
		if cfgs[i].Source != "" && cfgs[i].ContentHash == "" {
			bt, err := detectBuildType(cfgs[i].Source)
			if err == nil {
				if hash, err := hashSourceDir(cfgs[i].Source, bt); err == nil {
					cfgs[i].ContentHash = hash
				}
			}
		}
	}

	loadOrder := append(slices.Clone(cfgs), embeddedCellConfigs(embeddedCells)...)
	h.recordLoadOrder(loadOrder, true)
	cellConfigs := make([]ExtConfig, 0, len(cfgs))
	for _, cfg := range cfgs {
		if !cfg.Enabled || cfg.resolveErr != nil || fusedResolver == nil {
			cellConfigs = append(cellConfigs, cfg)
			continue
		}
		serve, fused := fusedResolver.FusedServe(cfg)
		if !fused {
			cellConfigs = append(cellConfigs, cfg)
			continue
		}
		item, stageErr := h.stageInProcess(ctx, cfg, serve)
		if stageErr != nil {
			rep.Issues = append(rep.Issues, reloadIssue(cfg, stageErr))
			removeOld(cfg.Name)
			continue
		}
		staged = append(staged, item)
	}
	cells := h.planCells(cellConfigs)
	h.planEventBus(ctx, cells)
	h.stageCellsInOrder(ctx, cells, func(cell CellSpec, outcome stageOutcome, stageErr error) {
		outcomes, failures := h.isolateCellFailure(ctx, cell, oldByName, outcome, stageErr)
		for _, outcome := range outcomes {
			outcome.report.Replaced = anyExisting(cell.Extensions, oldByName)
			index := slices.IndexFunc(rep.Cells, func(existing ReloadCellReport) bool {
				return cell.Strategy == CellStrategyPackedNode && existing.Key == outcome.report.Key
			})
			if index < 0 {
				rep.Cells = append(rep.Cells, outcome.report)
			} else {
				rep.Cells[index].Extensions = append(rep.Cells[index].Extensions, outcome.report.Extensions...)
				rep.Cells[index].Replaced = rep.Cells[index].Replaced || outcome.report.Replaced
			}
			staged = append(staged, outcome.staged...)
		}
		// Upstream reload does not keep a failed extension's previous
		// runtime: the extension is not loaded and its error is reported.
		for _, failure := range failures {
			rep.Issues = append(rep.Issues, reloadIssue(failure.cfg, failure.err))
			removeOld(failure.cfg.Name)
		}
	})

	for _, cell := range embeddedCells {
		embeddedStaged, _, stageErr := h.stageEmbeddedCell(ctx, cell)
		names := make([]string, len(cell.Extensions))
		for i := range cell.Extensions {
			names[i] = cell.Extensions[i].Name
		}
		if stageErr != nil {
			for _, cfg := range embeddedCellConfigs([]EmbeddedCell{cell}) {
				rep.Issues = append(rep.Issues, reloadIssue(cfg, stageErr))
				removeOld(cfg.Name)
			}
			continue
		}
		staged = append(staged, embeddedStaged...)
		strategy := CellStrategy(cell.Strategy)
		if strategy == "" {
			strategy = CellStrategyPackedGo
		}
		rep.Cells = append(rep.Cells, ReloadCellReport{
			Key:        cell.Key,
			Strategy:   strategy,
			Language:   cell.Language,
			Extensions: names,
			BinaryPath: cell.BinaryPath,
			Cached:     true,
			Replaced:   anyExisting(embeddedCellConfigs([]EmbeddedCell{cell}), oldByName),
			Reason:     "embedded Piglet Binary cell",
		})
	}

	rep.Cells = append(rep.Cells, h.quarantineReports(reloadStart)...)

	h.commitStaged(staged, removed, "reload replaced")
	for _, item := range staged {
		if err := item.me.activateNode(); err != nil {
			h.disablePackedMember(item.me, err.Error())
			rep.Issues = append(rep.Issues, reloadIssue(item.me.config, err))
		}
	}
	return h.Extensions(), nil
}

func reloadIssue(cfg ExtConfig, err error) string {
	// Upstream extensions/loader.ts supplies the full factory error, including invalid-export diagnostics without a load-error prefix.
	if factoryErr, ok := errors.AsType[*FactoryLoadError](err); ok {
		return fmt.Sprintf("%s: %s", extConfigOrigin(cfg), factoryErr.Error())
	}
	return fmt.Sprintf("%s: Failed to load extension: %v", extConfigOrigin(cfg), err)
}

func (h *Host) commitStaged(staged []stagedManagedExt, removed []*managedExt, replaceReason string) {
	replaced := make([]*managedExt, 0, len(staged))
	h.mu.Lock()
	for _, old := range removed {
		delete(h.exts, old.config.Name)
		old.shuttingDown.Store(true)
	}
	for _, item := range staged {
		if old := h.exts[item.name]; old != nil {
			old.shuttingDown.Store(true)
			replaced = append(replaced, old)
		}
		h.exts[item.name] = item.me
	}
	// Another extension's registration may transfer a provider away from a staged member once it is visible in h.exts.
	replacementProviders := make(map[string]map[string]struct{}, len(staged))
	for _, item := range staged {
		replacementProviders[item.name] = providerNameSet(item.me.providerNames)
	}
	// Providers leave the registry in extension order, replaced generations first; each generation that shares a live process finishes its teardown before the next stops.
	h.mu.Unlock()

	for _, old := range replaced {
		h.stopManagedSkippingProviders(old, replaceReason, replacementProviders[old.config.Name])
	}
	for _, old := range removed {
		h.stopManaged(old, "reload removed")
	}
	// The stopped processes were killed, so waiting is bounded by process
	// teardown. A caller then sees no replaced process still running and no
	// cache usage lease still held for it.
	for _, old := range replaced {
		old.reap()
	}
	for _, old := range removed {
		old.reap()
	}
}

// ── Internal ─────────────────────────────────────────────────────────────────

// startExt spawns the binary, creates the socket listener, waits for connect,
// performs the register handshake, and builds the extension.Extension struct.
func (h *Host) startExt(ctx context.Context, me *managedExt, isRestart bool) (_ *extension.Extension, err error) {
	me.stderrLog, me.stderrLogPath = nil, ""
	me.setProcessHandle(nil, nil, nil, nil)
	retainedFrom := me.retainedFrom
	me.retainedFrom, me.share, me.procOwner = nil, nil, nil
	me.shareReleased.Store(false)
	if retainedFrom != nil {
		if err := me.adoptProcess(retainedFrom); err != nil {
			return nil, newLoadError(me.config.Name, "spawn", "admit_failed", err)
		}
	}
	extCtx, cancel := context.WithCancel(runtimeParent(ctx))
	me.cancel = cancel
	me.parentCtx = runtimeParent(ctx)
	defer func() {
		if err == nil {
			return
		}
		if loadErr, ok := errors.AsType[*LoadError](err); ok {
			if ctx.Err() == nil {
				loadErr.StderrLog = me.retainStderrLog()
			} else {
				loadErr.StderrLog = ""
			}
		}
		cancel()
		// A claimed process belongs to its spawner and ends with its last reference, which releaseShare gives back below.
		if _, tree := me.processHandle(); tree != nil && me.procOwner == nil {
			_ = tree.Kill()
		}
		// Give back the reference this start holds, or reap sees a process still shared and returns before it exits.
		me.releaseShare()
		me.reap()
	}()

	h.markExtension(me.config.Name, "spawn-start")
	rawConn, err := h.connectExt(ctx, extCtx, cancel, me)
	if err != nil {
		return nil, err
	}
	h.markExtension(me.config.Name, "spawn-done")
	h.markExtension(me.config.Name, "handshake-start")
	ext, err := h.adoptConn(ctx, me, rawConn, extCtx, cancel, isRestart)
	if err == nil {
		h.markExtension(me.config.Name, "handshake-done")
	}
	return ext, err
}

func (h *Host) ensureNodeRuntime(ctx context.Context) error {
	h.nodeRuntimeMu.Lock()
	defer h.nodeRuntimeMu.Unlock()
	if h.nodeRuntimeReady {
		return nil
	}
	if _, err := ensureNodeRuntime(ctx); err != nil {
		return err
	}
	h.nodeRuntimeReady = true
	return nil
}

// connectExt establishes the extension connection. For a fused in-process
// extension (D31) it serves the factory over an in-memory pipe in a host
// goroutine, with no socket and no subprocess; otherwise it creates a Unix
// socket, spawns the binary, and accepts the connection.
func (h *Host) connectExt(ctx, extCtx context.Context, cancel context.CancelFunc, me *managedExt) (net.Conn, error) {
	if me.inProcServe != nil {
		hostConn, extConn := net.Pipe()
		serve := me.inProcServe
		go func() {
			if err := serve(extConn); err != nil && extCtx.Err() == nil {
				fmt.Fprintf(os.Stderr, "extension %s (fused): %v\n", me.config.Name, err)
			}
		}()
		return hostConn, nil
	}

	// Extension code runs as soon as the process starts, so the process starts
	// only after every earlier cell in the plan has committed.
	if err := waitStartTurn(ctx); err != nil {
		return nil, newLoadError(me.config.Name, "spawn", "load_cancelled", err)
	}

	binPath := me.config.Path
	if !filepath.IsAbs(binPath) {
		binPath = filepath.Join(h.socketDir, "..", "extensions", binPath)
	}
	me.nodeRuntime = usesNodeRuntime(binPath, me.config.RuntimeLanguage)
	if me.nodeRuntime {
		if err := h.ensureNodeRuntime(extCtx); err != nil {
			cancel()
			return nil, newLoadError(me.config.Name, "spawn", "node_runtime_unsupported", err)
		}
	}

	// Create the socket path inside this Host's private runtime directory, so a
	// parallel Pig instance running the same extension cannot collide on it.
	sockPath, err := h.sockPathFor(me.config.Name)
	if err != nil {
		return nil, newLoadError(me.config.Name, "spawn", "socket_dir_failed", fmt.Errorf("create socket dir: %w", err))
	}

	// Clean up a stale socket from a prior load of this same Host (private dir,
	// so this only ever removes our own file, never a peer's live socket).
	_ = os.Remove(sockPath)

	me.sockPath = sockPath

	// Create listener.
	listener, address, err := ListenExtension(sockPath, usesNodeRuntime(binPath, me.config.RuntimeLanguage))
	if err != nil {
		return nil, newLoadError(me.config.Name, "spawn", "listen_failed", fmt.Errorf("listen %s: %w", sockPath, err))
	}
	defer func() { _ = listener.Close() }()

	if me.procOwner != nil {
		// pig additive (D20): the process that already holds this extension's modules re-invokes the factory, as Pi's loader does inside one runtime.
		entry, _ := nodeLauncherEntry(binPath)
		if err := me.share.send(factoryAdmission{Name: me.config.Name, Entry: entry, Socket: address, ReloadPass: reloadPass(ctx), Cwd: h.cwd}); err != nil {
			return nil, newLoadError(me.config.Name, "spawn", "admit_failed", fmt.Errorf("admit %s to the running extension process: %w", me.config.Name, err))
		}
		return h.acceptExtConn(ctx, cancel, me, listener)
	}

	// Spawn the extension binary.
	harnessEnv, err := h.harnessEnv()
	if err != nil {
		cancel()
		return nil, newLoadError(me.config.Name, "spawn", "socket_dir_failed", fmt.Errorf("write harness arguments: %w", err))
	}
	var cmd = buildExtCommand(extCtx, binPath, me.config.RuntimeLanguage)
	nodeOutput := usesNodeRuntime(binPath, me.config.RuntimeLanguage)
	cmd.Env = append(append(os.Environ(), harnessEnv...),
		fmt.Sprintf("PIG_EXT_SOCKET=%s", address),
		fmt.Sprintf("PIG_EXT_NAME=%s", me.config.Name),
	)
	if nodeOutput {
		me.nodeRealm = true
		busRouted := h.busRoutedForSpawn()
		if busRouted {
			cmd.Env = append(cmd.Env, eventBusRoutedEnv)
		}
		h.noteNodeRealm(me, busRouted)
		cmd.Env = h.withNodeRuntimeEnv(cmd.Env)
	}
	if me.config.RuntimeLanguage == "python" || strings.HasSuffix(binPath, ".py") {
		pythonSDK := filepath.Join(h.builder.configRoot, "state", "pigsdk", "sdk-py")
		pythonPath := prependUniquePath(pythonSDK, os.Getenv("PYTHONPATH"))
		cmd.Env = slices.DeleteFunc(cmd.Env, func(value string) bool { return envHasKey(value, "PYTHONPATH") })
		cmd.Env = append(cmd.Env, "PYTHONPATH="+pythonPath)
	}
	cmd.Dir = h.cwd
	cmd.Stdout = nil // Extensions shouldn't write to stdout
	// pig divergence (D56): after the host stops an extension, its output
	// pipes get this long to drain before they are closed; it never bounds
	// extension work.
	cmd.WaitDelay = 5 * time.Second
	// A factory launcher takes further generations of its factory on stdin.
	var admissionWriter io.WriteCloser
	entry, factoryLauncher := nodeLauncherEntry(binPath)
	if factoryLauncher {
		// The first generation keys Pi's factory cache by this Host's configured cwd, as every later admission does; the process's own cwd is the physical path.
		cmd.Env = append(cmd.Env, "PIG_EXT_CWD="+h.cwd)
		if admissionWriter, err = cmd.StdinPipe(); err != nil {
			cancel()
			return nil, newLoadError(me.config.Name, "spawn", "spawn_failed", fmt.Errorf("spawn %s: %w", binPath, err))
		}
	}
	cacheLease, err := runtimecell.AcquireArtifactUsageLease(binPath)
	if err != nil {
		cancel()
		return nil, newLoadError(me.config.Name, "spawn", "cache_lease_failed", fmt.Errorf("lease extension cache artifact %s: %w", binPath, err))
	}
	me.cacheLease = cacheLease
	// pig divergence (D56): retain stderr only when a subprocess failure diagnostic references it.
	stderrFile, _ := os.CreateTemp("", fmt.Sprintf("pig-ext-%s-*.log", fileNameComponent(me.config.Name)))
	if stderrFile != nil {
		me.stderrLogPath = stderrFile.Name()
		me.stderrLog = &processStderrLog{path: stderrFile.Name(), writer: stderrFile}
		cmd.Stderr = stderrFile
	}
	if nodeOutput {
		cmd.Stdout, cmd.Stderr = h.nodeExtensionOutput(stderrFile)
	}
	processTree, err := startProcessTree(cmd)
	if stderrFile != nil && (!nodeOutput || err != nil) {
		// Direct child handles are inherited; Node's host-side copy owns its writer until Cmd.Wait drains it.
		me.stderrLog.closeWriter()
	}
	if err != nil {
		if admissionWriter != nil {
			_ = admissionWriter.Close()
		}
		_ = me.cacheLease.Release()
		me.cacheLease = nil
		cancel()
		loadErr := newLoadError(me.config.Name, "spawn", "spawn_failed", fmt.Errorf("spawn %s: %w", binPath, err))
		loadErr.StderrLog = me.stderrLogPath
		return nil, loadErr
	}
	exitedCh := make(chan struct{})
	me.setProcessHandle(cmd.Process, processTree, cmd, exitedCh)

	// Reap the process in the background to prevent goroutine leaks.
	// exec.CommandContext spawns a watchCtx goroutine that blocks on
	// resultc until cmd.Wait() is called. Without this, each cancelled
	// extension leaves a leaked goroutine stuck in chan-send. The exit
	// status is captured so the crash path can tell a real crash
	// (non-zero/signal) from a clean, intentional exit(0).
	stderrLog := me.stderrLog
	go func() {
		me.waitErr = cmd.Wait()
		stderrLog.closeWriter()
		_ = processTree.Close()
		if me.cacheLease != nil {
			_ = me.cacheLease.Release()
			me.cacheLease = nil
		}
		close(exitedCh)
	}()
	// The share becomes claimable by concurrent starts only once every process field it exposes is set.
	if admissionWriter != nil {
		me.share = newProcessShare("node-extension:"+me.config.Name+"\x00"+entry, me, exitedCh, admissionWriter, func() {
			cancel()
			if processTree != nil {
				_ = processTree.Kill()
			} else if cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
		})
		me.share.node = true
		h.retention().register(me.share)
	}

	return h.acceptExtConn(ctx, cancel, me, listener)
}

// adoptProcess makes me another extension of the process share's spawner. The process fields are the spawner's; a stop of me releases its reference instead of killing the process.
func (me *managedExt) adoptProcess(share *processShare) error {
	origin, ok := share.origin.(*managedExt)
	if !ok {
		share.release()
		return errors.New("retained process does not host an isolated extension")
	}
	me.share, me.procOwner = share, origin
	origin.procMu.Lock()
	proc, tree, cmd := origin.proc, origin.processTree, origin.cmd
	origin.procMu.Unlock()
	me.setProcessHandle(proc, tree, cmd, share.exitCh)
	me.stderrLog, me.stderrLogPath = origin.stderrLog, origin.stderrLogPath
	return nil
}

// exitErr is the exit status of the extension's process, valid once it has exited.
func (me *managedExt) exitErr() error {
	if me.procOwner != nil {
		return me.procOwner.waitErr
	}
	return me.waitErr
}

// acceptExtConn waits for the extension to connect. Upstream awaits the extension factory
// with no deadline, and a Node factory runs before its runtime connects, so
// the wait ends only when the extension connects, its process exits, or the
// caller cancels.
func (h *Host) acceptExtConn(ctx context.Context, cancel context.CancelFunc, me *managedExt, listener net.Listener) (net.Conn, error) {
	connCh := make(chan net.Conn, 1)
	errCh := make(chan error, 1)
	go func() {
		c, err := listener.Accept()
		if err != nil {
			errCh <- err
			return
		}
		connCh <- c
	}()

	var rawConn net.Conn
	select {
	case rawConn = <-connCh:
	case err := <-errCh:
		cancel()
		loadErr := newLoadError(me.config.Name, "connect", "accept_failed", fmt.Errorf("accept: %w", err))
		loadErr.StderrLog = me.stderrLogPath
		loadErr.Hint = standaloneRebuildHint(me.config)
		return nil, loadErr
	case <-me.exited():
		cancel()
		exitErr := errors.New("extension process exited before connecting")
		if err := me.exitErr(); err != nil {
			exitErr = fmt.Errorf("extension process exited before connecting: %w", err)
		}
		// Upstream reports the loader's own error; lead with the cause the
		// process wrote to stderr.
		if cause := stderrCause(me.stderrLogPath, me.config.Name); cause != "" {
			exitErr = fmt.Errorf("%s (%w)", cause, exitErr)
		}
		loadErr := newLoadError(me.config.Name, "connect", "process_exited", exitErr)
		loadErr.StderrLog = me.stderrLogPath
		loadErr.Hint = standaloneRebuildHint(me.config)
		return nil, loadErr
	case <-ctx.Done():
		// Close listener to unblock the Accept goroutine, then drain
		// the channel to close any late-arriving connection.
		_ = listener.Close()
		select {
		case c := <-connCh:
			_ = c.Close()
		case <-errCh:
			// Accept failed (expected from listener.Close): fine.
		}
		cancel()
		loadErr := newLoadError(me.config.Name, "connect", "load_cancelled", fmt.Errorf("extension load cancelled before it connected: %w", ctx.Err()))
		loadErr.StderrLog = me.stderrLogPath
		return nil, loadErr
	}

	return rawConn, nil
}

// adoptConn runs the register/ready handshake over an established connection and
// builds the extension.Extension. Shared by the subprocess and fused
// in-process load paths.
func (h *Host) adoptConn(ctx context.Context, me *managedExt, rawConn net.Conn, extCtx context.Context, cancel context.CancelFunc, isRestart bool) (*extension.Extension, error) {
	// Wrap in managed connection.
	conn := NewConn(me.config.Name, rawConn)
	h.observeXref(conn)
	// Adopt before Start so this connection's own close finds it as me's current connection.
	h.mu.Lock()
	released := h.forgetNativeProvidersLocked(me.connection())
	me.setConnLocked(conn)
	h.mu.Unlock()
	h.releaseProviderCallbacks(released)
	conn.Start(extCtx)

	// Wait for register message. Like connecting, registering has no host
	// deadline: it ends when the extension registers, its connection closes,
	// or the caller cancels.
	reg, err := h.waitForRegister(ctx, me, conn)
	if err != nil {
		_ = conn.Close("register failed")
		cancel()
		if factoryErr, ok := errors.AsType[*FactoryLoadError](err); ok {
			return nil, me.factoryLoadError(factoryErr)
		}
		loadErr := newLoadError(me.config.Name, "register", "register_failed", fmt.Errorf("register handshake: %w", err))
		loadErr.StderrLog = me.stderrLogPath
		loadErr.Hint = standaloneRebuildHint(me.config)
		if loadErr.Hint == "" {
			loadErr.Hint = registerDecodeHint(err)
		}
		return nil, loadErr
	}

	if !me.config.acceptsRegisteredName(reg.Name) {
		_ = conn.Close("name mismatch")
		cancel()
		loadErr := newLoadError(me.config.Name, "register", "name_mismatch", fmt.Errorf(
			"selected identity %q does not match registered identity %q for source %s; a direct directory must be renamed/reselected under %q, or selected through a Package/Piglet declaration with that exact member identity",
			me.config.Name, reg.Name, extConfigOrigin(me.config), reg.Name,
		))
		loadErr.StderrLog = me.stderrLogPath
		return nil, loadErr
	}

	me.flagNames = make([]string, 0, len(reg.Flags))
	me.wantsSessionLog = reg.WantsSessionLog
	for _, f := range reg.Flags {
		me.flagNames = append(me.flagNames, f.Name)
	}

	if err := validateRegisterPayload(me.config.Name, reg); err != nil {
		_ = conn.Close("invalid registration")
		cancel()
		loadErr := newLoadError(me.config.Name, "register", "invalid_registration", err)
		loadErr.StderrLog = me.stderrLogPath
		return nil, loadErr
	}

	me.flagDefaults = reg.flagDefaults

	// Send ready.
	readyWidth, readyHeight := h.readyGeometry()
	h.lastWidth = readyWidth
	h.lastHeight = readyHeight
	readyPayload := &ReadyPayload{
		Cwd:    h.cwd,
		Mode:   h.mode,
		Width:  readyWidth,
		Height: readyHeight,
	}
	if h.uiBridge != nil {
		flagNames := make([]string, 0, len(reg.Flags))
		for _, f := range reg.Flags {
			flagNames = append(flagNames, f.Name)
		}
		// Node's synchronous session API can read a persisted session directly.
		// Keep it unsubscribed until first use, then enroll at the local cursor.
		// A session with no file cannot be loaded locally, so send it in bounded
		// pages before the runtime handles requests.
		me.entryCursorMu.Lock()
		me.entryCursor = 0
		readyPayload.State = h.uiBridge.Snapshot(flagNames, 0, false)
		readyPayload.Models = h.uiBridge.ModelCatalog()
		if reg.WantsSessionLog && readyPayload.State.Session != nil && !readableSessionFile(readyPayload.State.Session.SessionFile) {
			h.mu.Lock()
			if h.sessionLogSubs == nil {
				h.sessionLogSubs = make(map[string]struct{})
			}
			h.sessionLogSubs[me.config.Name] = struct{}{}
			h.mu.Unlock()
			readyPayload.State = h.uiBridge.Snapshot(flagNames, 0, true)
		}
		if readyPayload.State.Session != nil {
			me.entryCursor = readyPayload.State.Session.EntryCount
		}
		me.entryCursorMu.Unlock()
	}
	if err := conn.Send(&Envelope{
		Type:  MsgReady,
		Ready: readyPayload,
	}); err != nil {
		cancel()
		loadErr := newLoadError(me.config.Name, "ready", "send_ready_failed", fmt.Errorf("send ready: %w", err))
		loadErr.StderrLog = me.stderrLogPath
		return nil, loadErr
	}
	if readyPayload.State != nil && readyPayload.State.Session != nil && readyPayload.State.Session.EntriesRemaining {
		if err := h.pushStateTo(ctx, me, conn); err != nil {
			cancel()
			loadErr := newLoadError(me.config.Name, "ready", "send_session_failed", fmt.Errorf("send session pages: %w", err))
			loadErr.StderrLog = me.stderrLogPath
			return nil, loadErr
		}
	}

	// Reverse provider callbacks may call the host while registering.
	go h.handleIncoming(me, conn)
	// On a crash restart the providers are already registered and their
	// handlers resolve the current connection at call time, so re-registering would duplicate
	// them. Same binary → same providers, so skip.
	for _, provider := range reg.Providers {
		if provider.Native != nil {
			if err := h.registerNativeProvider(ctx, me, conn, provider.Native); err != nil {
				return nil, err
			}
		}
	}
	if !isRestart {
		for _, provider := range reg.Providers {
			if provider.Native != nil {
				continue
			}
			var cfg extension.ProviderConfig
			if err := json.Unmarshal(provider.Config, &cfg); err != nil {
				_ = conn.Close("provider registration failed")
				cancel()
				loadErr := newLoadError(me.config.Name, "register", "provider_config_invalid", fmt.Errorf("provider %s: decode config: %w", provider.Name, err))
				loadErr.StderrLog = me.stderrLogPath
				return nil, loadErr
			}
			// Model-provider registration and OAuth registration are
			// independent concerns: an extension may contribute an OAuth login
			// with no model-provider callback wired on the host, so gate only
			// the model-provider hook, never the OAuth registration.
			if provider.StreamSimple {
				cfg.StreamSimple = h.providerStreamCallback(me, provider.Name)
			}
			if err := h.providerRuntime.RegisterProvider(provider.Name, cfg, extConfigOrigin(me.config)); err != nil {
				_ = conn.Close("provider registration failed")
				cancel()
				return nil, err
			}
			if h.uiBridge != nil {
				h.uiBridge.RecordProviderRegistration(provider.Name, provider.Config)
			}
			h.mu.Lock()
			notifySuperseded := h.transferProviderOwnershipLocked(me, conn, provider.Name)
			h.mu.Unlock()
			notifySuperseded()
			if err := h.registerOAuthProvider(me, provider.Name, provider.Config); err != nil {
				_ = conn.Close("provider registration failed")
				cancel()
				loadErr := newLoadError(me.config.Name, "register", "provider_config_invalid", err)
				loadErr.StderrLog = me.stderrLogPath
				return nil, loadErr
			}
		}
	}

	if len(reg.Providers) > 0 {
		me.releaseLiveness = conn.holdLiveness()
	}
	if h.uiBridge != nil {
		h.uiBridge.RegisterExtConn(me.config.Name, conn)
	}

	// Build extension.Extension from the register payload. A restarted
	// extension keeps the Extension its runner already holds: its handlers
	// become exactly the new registration, so subscriptions the new process
	// makes reach dispatch and ones the old process made do not.
	ext := h.buildExtension(me, reg)
	if isRestart && me.ext != nil {
		me.ext.ReplaceEventHandlers(ext)
		me.ext.ReplaceRegisteredTools(ext)
		ext = me.ext
	}
	me.ext = ext
	me.supervisor.RecordSuccess()

	return ext, nil
}

// waitForRegister reads messages until we get a register or context expires.
// A Node realm can take part in the shared event bus while its factory loads, before it registers; those calls are served here.
func (h *Host) waitForRegister(ctx context.Context, me *managedExt, conn *Conn) (*RegisterPayload, error) {
	lanes := newCallLanes()
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case env, ok := <-conn.Incoming():
			if !ok {
				if failure := conn.failureError(); failure != nil {
					return nil, fmt.Errorf("connection closed before register: %w", failure)
				}
				return nil, errors.New("connection closed before register")
			}
			if env.Type == MsgRegister && env.Register != nil {
				return env.Register, nil
			}
			if env.Type == MsgCall && env.Call != nil && loadingBusCall(env.Call.Method) {
				h.queueCall(me, conn, lanes, env.ID, env.Call)
				continue
			}
			if env.Type == MsgCall {
				conn.dropReservedHostCall(env.ID)
			}
			if env.Type == MsgNotify && env.Notify != nil && env.Notify.Method == NotifyLoadFailed {
				var failure FactoryLoadError
				if err := json.Unmarshal(env.Notify.Args, &failure); err != nil || failure.Message == "" {
					return nil, fmt.Errorf("decode load failure: %s", env.Notify.Args)
				}
				return nil, &failure
			}
			// Ignore non-register messages during handshake.
		}
	}
}

// Ports packages/coding-agent/src/core/extensions/loader.ts
func validateRegisterPayload(expectedName string, reg *RegisterPayload) error {
	if reg == nil {
		return errors.New("missing register payload")
	}
	if strings.TrimSpace(reg.Name) == "" {
		return errors.New("register.name is required")
	}
	// upstream: packages/coding-agent/src/core/extensions/loader.ts:registerTool
	for _, tool := range reg.Tools {
		parameters := bytes.TrimSpace(tool.Parameters)
		if len(parameters) != 0 && !json.Valid(parameters) {
			return fmt.Errorf("tool %q has invalid parameters JSON", tool.Name)
		}
		if len(parameters) == 0 || parameters[0] != '{' {
			return fmt.Errorf(`Tool "%s" registered by extension "%s" must define an object parameter schema.`, tool.Name, expectedName)
		}
	}
	// Upstream registration writes each name into a Map, so a repeated name
	// keeps its first position and its last definition.
	var err error
	if reg.Tools, err = lastRegistrationWins("tool", reg.Tools, func(t ToolDecl) string { return t.Name }); err != nil {
		return err
	}
	if reg.Commands, err = lastRegistrationWins("command", reg.Commands, func(c CommandDecl) string { return c.Name }); err != nil {
		return err
	}
	if reg.Shortcuts, err = lastRegistrationWins("shortcut", reg.Shortcuts, func(s ShortcutDecl) string { return s.Key }); err != nil {
		return err
	}
	reg.flagDefaults = make(map[string]any, len(reg.Flags))
	for _, flag := range reg.Flags {
		if len(flag.Default) == 0 {
			continue
		}
		var value any
		if err := json.Unmarshal(flag.Default, &value); err != nil {
			return fmt.Errorf("flag %q has an invalid default: %w", flag.Name, err)
		}
		kind := "object"
		switch value.(type) {
		case bool:
			kind = "boolean"
		case string:
			kind = "string"
		case float64:
			kind = "number"
		}
		if kind != flag.Type {
			return fmt.Errorf("Invalid default for flag %q: expected %s, got %s", flag.Name, flag.Type, kind)
		}
		if _, exists := reg.flagDefaults[flag.Name]; !exists {
			reg.flagDefaults[flag.Name] = value
		}
	}
	if reg.Flags, err = lastRegistrationWins("flag", reg.Flags, func(f FlagDecl) string { return f.Name }); err != nil {
		return err
	}
	if reg.Providers, err = lastRegistrationWins("provider", reg.Providers, func(p ProviderDecl) string { return p.Name }); err != nil {
		return err
	}
	for _, provider := range reg.Providers {
		if provider.Native != nil && provider.Native.ID != provider.Name {
			return fmt.Errorf("native provider %q id %q does not match registration", provider.Name, provider.Native.ID)
		}
	}
	if reg.MessageRenderers, err = lastRegistrationWins("message renderer", reg.MessageRenderers, func(r MessageRendererDecl) string { return r.CustomType }); err != nil {
		return err
	}
	if reg.EntryRenderers, err = lastRegistrationWins("entry renderer", reg.EntryRenderers, func(r EntryRendererDecl) string { return r.CustomType }); err != nil {
		return err
	}
	if err := validateHandlerDeclarations(reg.Handlers); err != nil {
		return err
	}
	for _, tool := range reg.Tools {
		if len(tool.ValidationParameters) > 0 && !json.Valid(tool.ValidationParameters) {
			return fmt.Errorf("tool %q has invalid validation_parameters JSON", tool.Name)
		}
		if len(tool.ConstrainedSampling) > 0 && !json.Valid(tool.ConstrainedSampling) {
			return fmt.Errorf("tool %q has invalid constrained_sampling JSON", tool.Name)
		}
	}
	for _, provider := range reg.Providers {
		if len(provider.Config) > 0 && !json.Valid(provider.Config) {
			return fmt.Errorf("provider %q has invalid config JSON", provider.Name)
		}
	}
	return nil
}

func validateHandlerDeclarations(handlers []HandlerDecl) error {
	ids := make(map[int]struct{}, len(handlers))
	for _, handler := range handlers {
		if handler.Event == "" {
			return errors.New("event handler name is required")
		}
		if handler.HandlerID <= 0 {
			return fmt.Errorf("event handler %q requires a positive handler_id", handler.Event)
		}
		if _, duplicate := ids[handler.HandlerID]; duplicate {
			return fmt.Errorf("duplicate event handler id %d", handler.HandlerID)
		}
		ids[handler.HandlerID] = struct{}{}
	}
	return nil
}

// lastRegistrationWins applies upstream Map.set semantics to repeated
// registrations: each name keeps the position of its first registration and
// the value of its last. An empty name is an error.
func lastRegistrationWins[T any](kind string, values []T, nameOf func(T) string) ([]T, error) {
	index := make(map[string]int, len(values))
	out := values[:0:0]
	for _, value := range values {
		name := strings.TrimSpace(nameOf(value))
		if name == "" {
			return nil, fmt.Errorf("%s name is required", kind)
		}
		if at, ok := index[name]; ok {
			out[at] = value
			continue
		}
		index[name] = len(out)
		out = append(out, value)
	}
	return out, nil
}

// buildExtension translates a RegisterPayload into an extension.Extension struct
// suitable for inproc.Runner consumption.
func (h *Host) buildExtension(me *managedExt, reg *RegisterPayload) *extension.Extension {
	path, resolvedPath := extensionSourcePaths(me.config)
	ext := &extension.Extension{
		Name:             me.config.Name,
		Path:             path,
		ResolvedPath:     resolvedPath,
		SourceInfo:       h.extensionSourceInfo(me),
		Tools:            make(map[string]extension.RegisteredTool, len(reg.Tools)),
		Commands:         make(map[string]extension.RegisteredCommand, len(reg.Commands)),
		MessageRenderers: make(map[string]extension.MessageRenderer, len(reg.MessageRenderers)),
		EntryRenderers:   make(map[string]extension.EntryRenderer, len(reg.EntryRenderers)),
		Flags:            make(map[string]extension.ExtensionFlag, len(reg.Flags)),
		Shortcuts:        make(map[extension.KeyID]extension.ExtensionShortcut, len(reg.Shortcuts)),
		Handlers:         make(map[string][]extension.HandlerFn),
	}
	ext.InitializeEventHandlers()

	// Build tools.
	for _, td := range reg.Tools {
		tool := td // capture
		source := tool.Source
		if source == "" {
			source = me.config.Name // default: extension name (matches upstream sourceInfo stamping)
		}
		definition := extension.ToolDefinition{
			Name:                 tool.Name,
			Label:                tool.Label,
			Description:          tool.Description,
			Parameters:           tool.Parameters,
			ValidationParameters: tool.ValidationParameters,
			ConstrainedSampling:  tool.ConstrainedSampling,
			PromptSnippet:        tool.PromptSnippet,
			PromptGuidelines:     tool.PromptGuidelines,
			ExecutionMode:        extension.ToolExecutionMode(tool.ExecutionMode),
			RenderShell:          extension.ToolRenderShell(tool.RenderShell),
			Execute:              h.makeToolExecuteFunc(me, tool.Name),
			BuiltInRenderers:     tool.BuiltInRenderers,
		}
		if tool.RendersCall {
			definition.RenderCall = h.makeToolRenderCall(me, tool.Name)
		}
		if tool.RendersResult {
			definition.RenderResult = h.makeToolRenderResult(me, tool.Name)
		}
		ext.Tools[td.Name] = extension.RegisteredTool{Definition: definition, SourceInfo: source}
		ext.ToolOrder = append(ext.ToolOrder, td.Name)
	}

	// Build commands.
	for _, cd := range reg.Commands {
		cmd := cd // capture
		registered := extension.RegisteredCommand{
			Name:        cmd.Name,
			Description: cmd.Description,
			SourceInfo:  ext.SourceInfo,
			Handler:     h.makeCommandHandler(me, cmd.Name),
		}
		if cmd.ArgumentCompletions {
			registered.GetArgumentCompletions = makeCommandArgumentCompletions(me, cmd.Name)
		}
		ext.Commands[cd.Name] = registered
		ext.CommandOrder = append(ext.CommandOrder, cd.Name)
	}

	// Build shortcuts.
	for _, sd := range reg.Shortcuts {
		sc := sd // capture
		ext.Shortcuts[extension.KeyID(sd.Key)] = extension.ExtensionShortcut{
			Shortcut:      extension.KeyID(sc.Key),
			Description:   sc.Description,
			Handler:       h.makeShortcutHandler(me, sc.Key),
			ExtensionPath: path,
		}
	}

	for _, fd := range reg.Flags {
		// Defaults are validated before construction. The declaration keeps its last default, while runtime values keep the first.
		var defaultValue any
		if len(fd.Default) > 0 {
			if err := json.Unmarshal(fd.Default, &defaultValue); err != nil {
				panic(fmt.Sprintf("validated flag default for %q: %v", fd.Name, err))
			}
		}
		if _, registered := ext.Flags[fd.Name]; !registered {
			ext.FlagOrder = append(ext.FlagOrder, fd.Name)
		}
		ext.Flags[fd.Name] = extension.ExtensionFlag{
			Name:          fd.Name,
			Description:   fd.Description,
			Type:          extension.FlagType(fd.Type),
			Default:       defaultValue,
			ExtensionPath: path,
		}
	}

	for _, rd := range reg.MessageRenderers {
		customType := rd.CustomType
		ext.MessageRenderers[customType] = h.makeMessageRenderer(me, customType)
	}

	for _, rd := range reg.EntryRenderers {
		customType := rd.CustomType
		ext.EntryRenderers[customType] = h.makeEntryRenderer(me, customType)
	}

	if reg.MarkdownTransformer {
		proxy := &markdownTransformProxy{conn: func() *Conn { return me.current().connection() }, inactivity: rendererInactivity}
		ext.MarkdownTransformer = proxy.transform
	}

	// Build event handlers.
	for _, hd := range reg.Handlers {
		h := hd // capture
		ext.AddEventHandler(h.Event, h.HandlerID, me.makeEventHandler(h.Event, h.HandlerID))
	}

	ext.InitializeToolRegistry()
	return ext
}

// pig divergence (D56): renderer inactivity and transport heartbeat are separate host policy.
const rendererInactivity = 5 * time.Second

// makeEntryRenderer returns an extension.EntryRenderer that renders a custom
// session entry over the socket. Like makeMessageRenderer it hands back a lazy
// proxy: the render loop never blocks and IPC happens off the TUI loop.
func (h *Host) makeEntryRenderer(me *managedExt, customType string) extension.EntryRenderer {
	return func(entry extension.CustomEntry, options extension.EntryRenderOptions, _ extension.Theme) extension.Component {
		me := me.current()
		return newEntryRenderProxyComponent(
			me.config.Name,
			customType,
			entry,
			options,
			me.connection(),
			rendererInactivity,
			func() {
				if h.uiBridge != nil {
					h.uiBridge.Invalidate()
				}
			},
		)
	}
}

func (h *Host) makeMessageRenderer(me *managedExt, customType string) extension.MessageRenderer {
	return func(message extension.CustomMessage, options extension.MessageRenderOptions, _ extension.Theme) extension.Component {
		me := me.current()
		return newRenderProxyComponent(
			me.config.Name,
			customType,
			message,
			options,
			me.connection(),
			rendererInactivity,
			func() {
				if h.uiBridge != nil {
					h.uiBridge.Invalidate()
				}
			},
		)
	}
}

// makeToolExecuteFunc returns a ToolExecuteFunc that dispatches tool calls
// over the socket to the subprocess extension.
//
// Tools intentionally do not get a host completion or inactivity timeout, for the
// same reason commands do not. Upstream awaits ToolDefinition.execute with no
// host-imposed deadline and passes the tool the same ExtensionContext.ui that
// commands receive, so a tool may legitimately host a long-lived interactive
// overlay via ctx.ui.custom() and block on a human for as long as it takes.
// A host deadline here surfaced as `context deadline exceeded` on the tool
// result, which the model reads as a failure and reasons past, silently
// discarding the answer the user was still typing. The caller-owned ctx
// remains authoritative for cancellation.
func (h *Host) makeToolExecuteFunc(me *managedExt, toolName string) extension.ToolExecuteFunc {
	return func(
		ctx context.Context,
		toolCallID string,
		params json.RawMessage,
		onUpdate extension.AgentToolUpdateCallback,
	) (extension.AgentToolResult, error) {
		me := me.current()
		conn := me.connection()
		if conn == nil {
			return nil, errors.New("extension not connected")
		}

		reqCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		if err := h.pushStateTo(reqCtx, me, conn); err != nil {
			return nil, fmt.Errorf("sync extension state for tool %s: %w", toolName, err)
		}

		resp, err := conn.requestWithUpdates(reqCtx, &Envelope{
			Type: MsgRequest,
			Request: &RequestPayload{
				Method:     "tool_call",
				Tool:       toolName,
				ToolCallID: toolCallID,
				Args:       params,
			},
		}, toolUpdateSink(onUpdate))
		if err != nil {
			return nil, fmt.Errorf("tool %s: %w", toolName, err)
		}

		if resp.Response != nil && resp.Response.Error != nil {
			return nil, resp.Response.Error.ToError()
		}

		// Convert subprocess ToolResult → agent.AgentToolResult so
		// the bridge layer's type assertion succeeds.
		if resp.Response != nil && resp.Response.Result != nil {
			raw := resp.Response.Result
			var result ToolResult
			if err := json.Unmarshal(raw, &result); err != nil {
				if trimmed := bytes.TrimSpace(raw); len(trimmed) > 0 && trimmed[0] == '{' {
					return nil, fmt.Errorf("tool %s: %w", toolName, err)
				}
				// Extension returned a plain value (string, number, etc.)
				// instead of a {content, details, is_error} object.
				// Unwrap JSON string or use raw JSON as content.
				var plain string
				if uerr := json.Unmarshal(raw, &plain); uerr != nil {
					plain = string(raw)
				}
				result.Content = []ai.ToolResultMessageContent{ai.TextContent{Text: plain}}
			}
			return agent.AgentToolResult{
				Content:   result.Content,
				Details:   detailsToAny(result.Details),
				IsError:   result.IsError,
				Usage:     result.Usage,
				Terminate: result.Terminate,
				Preview:   result.Preview,
			}, nil
		}

		return nil, nil
	}
}

// toolUpdateSink adapts the agent's update callback to wire partial results.
// Upstream passes every tool an onUpdate; a nil or foreign callback drops
// updates, as the agent has nowhere to show them.
func toolUpdateSink(onUpdate extension.AgentToolUpdateCallback) func(json.RawMessage) {
	var update func(string, any)
	switch cb := onUpdate.(type) {
	case agent.ToolUpdateCallback:
		update = cb
	case func(string, any):
		update = cb
	}
	return func(raw json.RawMessage) {
		if update == nil {
			return
		}
		var partial ToolResult
		if err := json.Unmarshal(raw, &partial); err != nil {
			return
		}
		update((agent.AgentToolResult{Content: partial.Content}).Text(), detailsToAny(partial.Details))
	}
}

// detailsToAny decodes a JSON-encoded details blob into a Go value suitable
// for passing through agent.AgentToolResult.Details. Empty / nil input
// returns nil. Invalid JSON falls back to the raw bytes as a string.
func detailsToAny(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return string(raw)
	}
	return v
}

// invocationError keeps transport failure propagation separate from diagnostic ownership.
func (h *Host) invocationError(err error) error {
	if h.onCrash == nil {
		return err
	}
	_, transport := errors.AsType[*TransportError](err)
	_, heartbeat := errors.AsType[*ExtensionUnresponsiveError](err)
	// pig divergence (D56): the lifecycle handler owns a connection failure's diagnostic, while callers still receive the typed error.
	if transport || heartbeat {
		return &invocation.LifecycleError{Err: err}
	}
	return err
}

// makeCommandHandler returns a CommandHandler that dispatches commands over
// the socket to the subprocess extension.
//
// Commands intentionally do not get a host completion or inactivity timeout.
// Unlike tool/event responses, extension commands are allowed to host
// long-lived interactive UIs via ctx.ui.custom() and can legitimately stay
// open for minutes. Applying the 5s default timeout here makes an interactive
// command exit with `context deadline exceeded` after its overlay closes.
// The caller-owned ctx remains authoritative for cancellation.
func (h *Host) makeCommandHandler(me *managedExt, cmdName string) extension.CommandHandler {
	return func(ctx context.Context, args string) error {
		me := me.current()
		conn := me.connection()
		if conn == nil {
			return errors.New("extension not connected")
		}

		argsJSON, err := json.Marshal(args)
		if err != nil {
			return fmt.Errorf("marshal command args: %w", err)
		}

		if err := h.pushStateTo(ctx, me, conn); err != nil {
			return me.dispatchError(fmt.Errorf("sync extension state for command %s: %w", cmdName, h.invocationError(err)))
		}

		flight := h.beginCommandFlight(me)
		defer h.endCommandFlight(flight)
		ctx = h.withCommandSuspension(ctx, flight)
		resp, err := conn.Request(ctx, &Envelope{
			Type: MsgRequest,
			Request: &RequestPayload{
				Method: "command",
				Tool:   cmdName,
				Args:   argsJSON,
			},
		})
		if err != nil {
			return me.dispatchError(fmt.Errorf("command %s: %w", cmdName, h.invocationError(err)))
		}

		if h.heldAfterInputEnd(flight) {
			// pig additive (D19): Pi exits before a suspended command's continuation can respond once stdin ended, unless a suspended session_shutdown handler keeps it alive.
			h.commandFlightMu.Lock()
			alive := h.quitSuspended()
			h.commandFlightMu.Unlock()
			select {
			case <-alive:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		if resp.Response != nil && resp.Response.Error != nil {
			return resp.Response.Error.ToError()
		}

		return nil
	}
}

// makeCommandArgumentCompletions asks the extension for a command's
// getArgumentCompletions. The caller runs it off the TUI loop, as upstream
// awaits it.
func makeCommandArgumentCompletions(me *managedExt, cmdName string) extension.ArgumentCompletionsFunc {
	return func(prefix string) ([]extension.AutocompleteItem, error) {
		me := me.current()
		conn := me.connection()
		if conn == nil {
			return nil, errors.New("extension not connected")
		}
		args, err := json.Marshal(prefix)
		if err != nil {
			return nil, err
		}
		resp, err := conn.Request(context.Background(), &Envelope{
			Type:    MsgRequest,
			Request: &RequestPayload{Method: RequestCommandArgumentCompletions, Tool: cmdName, Args: args},
		})
		if err != nil {
			return nil, fmt.Errorf("command %s argument completions: %w", cmdName, err)
		}
		if resp.Response == nil {
			return nil, nil
		}
		if resp.Response.Error != nil {
			return nil, resp.Response.Error.ToError()
		}
		var items []extension.AutocompleteItem
		if len(resp.Response.Result) > 0 {
			if err := json.Unmarshal(resp.Response.Result, &items); err != nil {
				return nil, fmt.Errorf("command %s argument completions: %w", cmdName, err)
			}
		}
		return items, nil
	}
}

// makeShortcutHandler returns a ShortcutHandler that dispatches shortcuts
// over the socket to the subprocess extension.
func (h *Host) makeShortcutHandler(me *managedExt, key string) extension.ShortcutHandler {
	return func(ctx context.Context) error {
		me := me.current()
		conn := me.connection()
		if conn == nil {
			return errors.New("extension not connected")
		}
		if err := h.pushStateTo(ctx, me, conn); err != nil {
			return me.dispatchError(fmt.Errorf("sync extension state for shortcut %s: %w", key, h.invocationError(err)))
		}

		resp, err := conn.Request(ctx, &Envelope{
			Type: MsgRequest,
			Request: &RequestPayload{
				Method: "shortcut",
				Tool:   key,
			},
		})
		if err != nil {
			return me.dispatchError(fmt.Errorf("shortcut %s: %w", key, h.invocationError(err)))
		}

		if resp.Response != nil && resp.Response.Error != nil {
			return resp.Response.Error.ToError()
		}

		return nil
	}
}

func (h *Host) handleEventSubscription(me *managedExt, call *CallPayload) (*CallResultPayload, error) {
	var request struct {
		Event     string `json:"event"`
		HandlerID int    `json:"handlerId"`
	}
	if err := json.Unmarshal(call.Args, &request); err != nil {
		return nil, fmt.Errorf("parse %s args: %w", call.Method, err)
	}
	if request.Event == "" || request.HandlerID <= 0 {
		return nil, fmt.Errorf("%s requires event and handlerId", call.Method)
	}
	h.mu.Lock()
	registered := h.exts[me.config.Name]
	h.mu.Unlock()
	if registered != me || me.ext == nil {
		return nil, errors.New("extension registration is no longer active")
	}
	if call.Method == "event.subscribe" {
		me.ext.AddEventHandler(request.Event, request.HandlerID, me.makeEventHandler(request.Event, request.HandlerID))
	} else {
		me.ext.RemoveEventHandler(request.Event, request.HandlerID)
	}
	return &CallResultPayload{}, nil
}

// makeEventHandler returns a HandlerFn that dispatches events over the socket.
func (me *managedExt) makeEventHandler(event string, handlerID int) extension.HandlerFn {
	return func(args ...any) (any, error) {
		conn := me.connection()
		if conn == nil {
			return nil, errors.New("extension not connected")
		}

		var argsJSON json.RawMessage
		if len(args) > 0 {
			var err error
			argsJSON, err = json.Marshal(args[0])
			if err != nil {
				return nil, fmt.Errorf("marshal event args: %w", err)
			}
		}

		parent := context.Background()
		if len(args) > 1 {
			if dispatchContext, ok := args[1].(context.Context); ok && dispatchContext != nil {
				parent = dispatchContext
			}
		}
		if event == "before_agent_start" {
			var err error
			argsJSON, err = promptOptionsEventArgs(parent, argsJSON)
			if err != nil {
				return nil, err
			}
		}
		// The event and session state are read here, in the handler's synchronous prefix. Only the wait for the extension process is a suspension.
		if event == "session_shutdown" {
			parent = withRequestSuspension(parent, func(suspended bool) {
				if suspended {
					me.host.setQuitHandlerSuspended()
				}
			}, me.host.inputHasEnded)
		}
		if err := me.host.pushStateTo(parent, me, conn); err != nil {
			return nil, me.dispatchError(fmt.Errorf("sync extension state for event %s: %w", event, me.host.invocationError(err)))
		}

		var resp *Envelope
		var err error
		request := func() error {
			resp, err = conn.Request(parent, &Envelope{
				Type: MsgRequest,
				Request: &RequestPayload{
					Method:    "event",
					Event:     event,
					HandlerID: handlerID,
					Args:      argsJSON,
				},
			})
			return nil
		}
		if observation := ai.StreamObservationFromContext(parent); observation != nil {
			// Pi awaits the handler while the provider keeps running; a Go caller that holds the stream's continuation queue across this wait would freeze it. The process reply settles like an I/O callback.
			_ = observation.AwaitExternal(request)
		} else {
			_ = request()
		}
		if err != nil {
			return nil, me.dispatchError(fmt.Errorf("event %s: %w", event, me.host.invocationError(err)))
		}

		if resp.Response != nil {
			if event == "before_agent_start" && len(args) > 0 && len(resp.Response.Result) > 0 && strings.TrimSpace(string(resp.Response.Result)) != "null" {
				var mutation BeforeAgentStartResponsePayload
				if err := json.Unmarshal(resp.Response.Result, &mutation); err != nil {
					return nil, fmt.Errorf("decode prompt section response: %w", err)
				}
				// runner.ts:1339 hands every handler the same options object; write the handler's values back into the per-run options the next handler receives.
				if target := extension.BeforeAgentStartOptions(parent); target != nil {
					if target.Sections != nil {
						*target.Sections = mutation.Sections
					}
					extension.SetBeforeAgentStartSelectedTools(parent, mutation.SelectedTools)
				}
				resp.Response.Result = mutation.Result
			}
			// pig additive (D19): both actionable boundaries return mutated proposals even when their handler fails.
			if (event == "agent_before_settle" || event == "turn_end") && len(args) > 0 && len(resp.Response.Result) > 0 && strings.TrimSpace(string(resp.Response.Result)) != "null" {
				var boundary struct {
					Entries []extension.SessionBoundaryDraft `json:"_pigBoundaryEntries"`
					Result  json.RawMessage                  `json:"_pigBoundaryResult"`
				}
				if err := json.Unmarshal(resp.Response.Result, &boundary); err != nil {
					return nil, fmt.Errorf("decode boundary response: %w", err)
				}
				switch target := args[0].(type) {
				case *extension.AgentBeforeSettleEvent:
					target.Entries = boundary.Entries
				case extension.TurnEndEvent:
					if target.BoundaryState != nil {
						target.Entries = boundary.Entries
					}
				}
				resp.Response.Result = boundary.Result
			}
			if resp.Response.Error != nil {
				return nil, resp.Response.Error.ToError()
			}
			// A handler that returns undefined (Node), None (Python) or no
			// value sends JSON null. Upstream treats undefined as "no result",
			// so null never reaches the Runner as a result value.
			if result := resp.Response.Result; len(result) > 0 && strings.TrimSpace(string(result)) != "null" {
				// Return raw JSON for the Runner to interpret.
				return result, nil
			}
		}

		return nil, nil
	}
}

// handleIncoming processes extension→host messages (calls, widget pushes) from conn, one connection of me.
// When the connection closes (extension crashed/exited), this goroutine
// invokes the crash supervisor and optionally restarts.
func (h *Host) handleIncoming(me *managedExt, conn *Conn) {
	// Capture this generation before an automatic restart replaces the managed extension's process fields.
	log, exited := me.stderrLog, me.exited()
	defer func() {
		if log != nil {
			if exited != nil {
				<-exited
			}
			log.remove()
		}
	}()
	lanes := newCallLanes()
	for env := range conn.Incoming() {
		if !h.acceptsNodeGeneration(me) {
			if env.Type == MsgRequestState && env.RequestState != nil {
				conn.settleSuspension(env.RequestState.RequestID)
			}
			if env.Type == MsgCall {
				conn.dropReservedHostCall(env.ID)
			}
			continue
		}
		switch env.Type {
		case MsgRequestState:
			// pig additive (D19): Pi applies a command's calls, such as writing a dialog request, before the process can exit, so the command counts as suspended only after the calls received before its report applied their synchronous parts. The wait runs off the read loop because those calls can need later frames.
			if env.RequestState != nil {
				state := *env.RequestState
				go func() {
					lanes.barrier()
					conn.deliverRequestState(state)
					conn.settleSuspension(state.RequestID)
				}()
			}

		case MsgCall:
			if env.Call == nil {
				continue
			}
			call := env.Call
			if h.uiBridge != nil {
				h.uiBridge.reserveAutocompleteCall(conn, call)
			}
			if h.uiBridge != nil && call.Method == CallUICustom {
				h.uiBridge.reserveCustomOverlay(me.config.Name, conn, call.Args)
			}
			// Calls apply in arrival order per lane off the read loop, so the
			// host keeps receiving notifications, widget pushes, and nested
			// calls the same extension sends while an earlier call runs.
			h.queueCall(me, conn, lanes, env.ID, call)

		case MsgWidgetPush:
			if env.WidgetPush == nil {
				continue
			}
			pending := h.slotCalls.register("widgets", &slotCall{apply: func() {
				select {
				case <-conn.Done():
					return
				default:
				}
				if !h.acceptsNodeGeneration(me) || h.uiBridge == nil {
					return
				}
				defer func() {
					if r := recover(); r != nil {
						fmt.Fprintf(os.Stderr, "panic in HandleWidgetPush for %s: %v\n", me.config.Name, r)
					}
				}()
				h.uiBridge.HandleWidgetPush(me.config.Name, env.WidgetPush)
			}})
			// No-reply pushes share widget ordering but cannot wait for a host-call lane: a width handler may send one while that lane awaits the extension.
			lanes.push(MsgWidgetPush, func() { h.runSlotCall("widgets", pending) })

		case MsgNotify:
			// Node→Go fire-and-forget notification. Used by the TS
			// shim runtime to push render frames or close events to
			// host-side overlay surfaces (e.g. ui.custom). Errors
			// are intentionally swallowed because the producer does
			// not expect a response.
			if env.Notify == nil {
				continue
			}
			// pig additive (D19): a subprocess event-loop drain is a process-exit observation, not a completed handler.
			if env.Notify.Method == "runtime_drained" || env.Notify.Method == "runtime_quit_yield" {
				// The runtime sent its earlier calls first. Their synchronous parts, such as writing a dialog request, happen before the owner exits.
				lanes.barrier()
				h.mu.Lock()
				handler := h.runtimeDrainHandler
				h.mu.Unlock()
				if handler != nil {
					handler()
				}
				continue
			}
			if env.Notify.Method == notifyOAuthSignalRelease {
				conn.handleOAuthSignalRelease(env.Notify.Args)
				continue
			}
			if h.uiBridge != nil && env.Notify.Method == "ui.autocomplete.release" {
				h.uiBridge.releaseAutocompleteReference(conn, env.Notify.Args)
				continue
			}
			if env.Notify.Method == NotifyToolRenderInvalidate {
				var card ToolRenderCardPayload
				if json.Unmarshal(env.Notify.Args, &card) == nil {
					me.toolRenders.invalidate(conn, card.Card)
				}
				continue
			}
			if h.uiBridge == nil {
				continue
			}
			h.uiBridge.HandleNotifyFrom(me.config.Name, conn, env.Notify)
		}
	}

	// Connection closed: extension exited.
	// Only record a crash if this wasn't a graceful shutdown (of this
	// extension or the whole host).
	if me.shuttingDown.Load() || h.shuttingDown.Load() {
		return // Graceful exit: don't record crash.
	}

	// If the context the extension was started under is cancelled, the owner
	// (app shutdown, /reload, or a test tearing down) initiated the exit. That
	// is intentional teardown, not a crash, so do not record or restart.
	if me.parentCtx != nil && me.parentCtx.Err() != nil {
		return
	}
	if me.packedProcess != nil && me.packedProcess.stopping.Load() {
		return
	}
	// The host did not close this connection. A killed process still takes control lines until it is reaped, so neither its exit channel nor a claim's probe can tell that it is gone: withdraw it from retention claims now, whether it is a packed cell or an isolated Node factory process.
	me.withdrawProcessFromRetention()

	if unresponsive, ok := errors.AsType[*ExtensionUnresponsiveError](conn.failureError()); ok {
		h.handleUnresponsiveExtension(me, unresponsive)
		return
	}

	// Distinguish a crash from a clean, intentional exit. Only a non-zero or
	// signal exit is a crash worth restarting. Both outcomes are reported: the
	// extension's tools and commands stay registered and would otherwise fail
	// with "connection closed" and no explanation.
	if me.exited() != nil {
		h.awaitClosedProcess(me)
		if me.exitErr() == nil {
			if h.uiBridge != nil {
				h.uiBridge.ClearExtensionConn(me.config.Name, conn)
			}
			if h.onCrash != nil {
				h.onCrash(me.config.Name, 0, true, "the extension process exited with status 0; its tools and commands are unavailable until /reload")
			}
			return
		}
	}

	if me.packedCellKey != "" {
		if me.packedProcess != nil {
			// A closed connection proves only that this one member stopped
			// talking to us. It does not prove the shared process is still
			// running: if the process itself exited, watchPackedProcess
			// independently quarantines the whole cell (with an accurate,
			// "process exited" reason) whether or not it wins the race
			// against this handler. Don't assert a liveness state we have
			// not verified either way; the process is only withdrawn from
			// retention claims above.
			h.disablePackedMember(me, "packed member connection closed")
			return
		}
		h.quarantinePackedCell(me.packedCellKey, fmt.Sprintf("extension %s connection closed", me.config.Name))
		return
	}

	if me.supervisor != nil && !me.supervisor.IsDisabled() {
		// If the host had just sent a frame too large for a binary built
		// against an older SDK, attribute the disconnect to frame-cap skew
		// instead of reporting a bare crash.
		size, recent := conn.RecentOversizedFrame()
		hint := frameSkewHint(me.config, size, recent)
		delay, err := me.supervisor.RecordCrash()
		if err != nil {
			// Circuit breaker tripped.
			if h.uiBridge != nil {
				h.uiBridge.ClearExtensionConn(me.config.Name, conn)
			}
			if h.onCrash != nil {
				reason := me.supervisor.DisableReason()
				if hint != "" {
					reason += "; " + hint
				}
				h.onCrash(me.config.Name, 0, true, withStderrLog(reason, me.retainStderrLog()))
			}
			return
		}
		if h.onCrash != nil {
			h.onCrash(me.config.Name, delay, false, withStderrLog(hint, me.retainStderrLog()))
		}
		// Relaunch after the supervisor's backoff. The "restart in %s" notice
		// above is only truthful because of this call.
		h.scheduleRestart(me, delay)
	}
}

// awaitClosedProcess waits for the process behind a closed connection to exit.
// A process that closed its socket can no longer serve, so one that has not
// exited within the heartbeat deadline is stopped and its exit is a crash.
func (h *Host) awaitClosedProcess(me *managedExt) {
	exited := me.exited()
	select {
	case <-exited:
		return
	default:
	}
	// pig divergence (D56): the heartbeat deadline also bounds how long a
	// process may outlive its closed connection.
	timer := time.NewTimer(defaultHeartbeatTimeout)
	defer timer.Stop()
	select {
	case <-exited:
	case <-timer.C:
		if proc, _ := me.processHandle(); proc != nil {
			_ = proc.Kill()
		}
		<-exited
	}
}

func (h *Host) disablePackedMember(me *managedExt, reason string) {
	h.mu.Lock()
	conn := me.connection()
	released := h.forgetNativeProvidersLocked(conn)
	if h.exts[me.config.Name] != me || (me.packedProcess != nil && me.packedProcess.stopping.Load()) {
		h.mu.Unlock()
		h.releaseProviderCallbacks(released)
		return
	}
	// Reserve the diagnostic before detaching the member so process cleanup cannot remove its log first.
	logPath := ""
	if h.onCrash != nil {
		logPath = me.retainStderrLog()
	}
	delete(h.exts, me.config.Name)
	h.mu.Unlock()
	h.releaseProviderCallbacks(released)
	me.releaseLivenessOwner()
	if me.sockPath != "" {
		_ = os.Remove(me.sockPath)
	}
	if h.uiBridge != nil {
		h.uiBridge.ClearExtensionConn(me.config.Name, conn)
	}
	for _, name := range me.providerNames {
		h.dropProviderConfigRef(name)
		if h.providerRuntime != nil {
			h.providerRuntime.UnregisterProvider(name)
		}
		if h.uiBridge != nil {
			h.uiBridge.ForgetProviderRegistration(name)
		}
	}
	h.unregisterOAuthProviders(me, nil)
	if h.onCrash != nil {
		h.onCrash(me.config.Name, 0, true, withStderrLog(reason, logPath))
	}
}

func (h *Host) handleUnresponsiveExtension(me *managedExt, failure *ExtensionUnresponsiveError) {
	reason := failure.Error()
	if me.packedCellKey != "" {
		h.disablePackedMember(me, reason)
		return
	}
	if me.cancel != nil {
		me.cancel()
	}
	me.killProcess()
	if me.supervisor == nil || me.supervisor.IsDisabled() {
		return
	}
	delay, err := me.supervisor.RecordCrash()
	if err != nil {
		if h.onCrash != nil {
			h.onCrash(me.config.Name, 0, true, withStderrLog(me.supervisor.DisableReason()+"; "+reason, me.retainStderrLog()))
		}
		return
	}
	if h.onCrash != nil {
		h.onCrash(me.config.Name, delay, false, withStderrLog(reason, me.retainStderrLog()))
	}
	h.scheduleRestart(me, delay)
}

// scheduleRestart relaunches a crashed extension after the supervisor's backoff
// delay. Every tool/command/event handler resolves the current connection at call time, so
// restarting in place (same managedExt, fresh connection) restores the
// extension's capabilities without re-registering anything with the agent.
// Packed cells are excluded upstream of this call: they are quarantined.
func (h *Host) scheduleRestart(me *managedExt, delay time.Duration) {
	go func() {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		<-timer.C
		h.attemptRestart(me)
	}()
}

func (h *Host) attemptRestart(me *managedExt) {
	if h.shuttingDown.Load() || me.shuttingDown.Load() {
		return
	}
	// Bail if a /reload or Load already replaced this instance in the slot.
	h.mu.Lock()
	current := h.exts[me.config.Name]
	h.mu.Unlock()
	if current != me {
		return
	}
	// The dead process is gone: give back the reference me holds so reap ends its state.
	me.releaseShare()
	me.reap()

	// Release the dead process's context before spawning a fresh one so the
	// old cancel func and its watcher goroutine don't leak.
	if me.cancel != nil {
		me.cancel()
		me.cancel = nil
	}

	if _, err := h.startExt(context.Background(), me, true); err != nil {
		// The relaunch itself failed: the fresh handleIncoming never started,
		// so nothing else will retry unless we drive it here. Count it against
		// the same circuit breaker and back off or give up.
		if me.supervisor == nil {
			return
		}
		nextDelay, breakerErr := me.supervisor.RecordCrash()
		if breakerErr != nil {
			if h.uiBridge != nil {
				h.uiBridge.ClearExtensionConn(me.config.Name, me.connection())
			}
			if h.onCrash != nil {
				h.onCrash(me.config.Name, 0, true, withStderrLog(me.supervisor.DisableReason(), me.retainStderrLog()))
			}
			return
		}
		if h.onCrash != nil {
			h.onCrash(me.config.Name, nextDelay, false, "restart failed: "+err.Error())
		}
		h.scheduleRestart(me, nextDelay)
		return
	}
	// Success: startExt restarted handleIncoming and called RecordSuccess, so a
	// subsequent crash is detected and handled again.
}

// resolveSocketDir returns the platform-appropriate socket directory. Windows
// uses its per-user temporary directory without a Unix uid. Unix prefers
// $XDG_RUNTIME_DIR/pig and otherwise uses $TMPDIR/pig-<uid>.
func resolveSocketDir() string {
	return resolveSocketDirForGOOS(
		runtime.GOOS,
		os.Getenv("XDG_RUNTIME_DIR"),
		os.Getenv("TMPDIR"),
		os.TempDir(),
		currentUserID(),
	)
}

// nodeExtensionOutput is where a Node extension process writes. Pi runs its
// extensions in its own process, so their console output reaches Pi's
// terminal: stdout in interactive mode, and stderr otherwise, where Pi has
// taken stdout over for its own output (main.ts takeOverStdout). Stderr also
// goes to the extension's log, which load and crash diagnostics read.
//
// Both are writers rather than PiG's own files, so the process gets pipes
// the host copies from: Node makes its stdio pipes non-blocking, which on a
// descriptor shared with PiG would turn PiG's own writes into EAGAIN errors.
func (h *Host) nodeExtensionOutput(log *os.File) (stdout, stderr io.Writer) {
	terminal := os.Stderr
	if h.mode == "tui" {
		terminal = os.Stdout
	}
	stdout = extensionConsole{terminal: terminal}
	stderr = extensionConsole{terminal: os.Stderr}
	if log != nil {
		stderr = extensionConsole{terminal: os.Stderr, log: log}
	}
	if h.mode != "tui" {
		// os/exec gives equal writers one shared pipe, preserving Pi's stdout-to-stderr console ordering.
		stdout = stderr
	}
	return stdout, stderr
}

// extensionConsole copies an extension's output to PiG's terminal and, for
// stderr, its log. A failed write drops that copy and keeps the pipe
// draining, so an unwritable terminal never blocks or kills the extension.
type extensionConsole struct {
	terminal io.Writer
	log      io.Writer
}

func (c extensionConsole) Write(p []byte) (int, error) {
	if c.log != nil {
		_, _ = c.log.Write(p)
	}
	_, _ = c.terminal.Write(p)
	return len(p), nil
}

// buildExtCommand constructs the exec.Cmd for launching an extension process.
// Python extensions are launched via an explicit interpreter rather than
// relying on the shebang line. When uv is available, "uv run" provides
// faster startup and deterministic Python resolution; otherwise Pig uses
// python.exe on Windows and python3 on Unix. Go and Rust extensions are
// compiled binaries and run directly.
func buildExtCommand(ctx context.Context, binPath, runtimeLanguage string) *exec.Cmd {
	return buildExtCommandForGOOS(ctx, binPath, runtimeLanguage, runtime.GOOS, exec.LookPath)
}

func buildExtCommandForGOOS(
	ctx context.Context,
	binPath, runtimeLanguage, goos string,
	lookPath func(string) (string, error),
) *exec.Cmd {
	if runtimeLanguage == "python" || strings.HasSuffix(strings.ToLower(binPath), ".py") {
		if uvPath, err := lookPath("uv"); err == nil {
			return exec.CommandContext(ctx, uvPath, "run", binPath)
		}
		return exec.CommandContext(ctx, findPythonExecutable(goos, lookPath), binPath)
	}
	if cmd, ok := nodeLauncherCommand(ctx, binPath); ok {
		return cmd
	}
	// Windows cannot execute a script, so a Node script standalone runs through
	// node there; elsewhere its shebang selects the interpreter.
	if runtimeLanguage == "node" && goos == "windows" {
		node, err := lookPath("node")
		if err != nil {
			node = "node"
		}
		return exec.CommandContext(ctx, node, binPath)
	}
	return exec.CommandContext(ctx, binPath)
}

// stoppedByOwner reports whether the host stopped this extension: a graceful
// shutdown of it or of the host, its owner's context ending, or its packed
// process being stopped.
func (me *managedExt) stoppedByOwner() bool {
	return me.shuttingDown.Load() ||
		(me.host != nil && me.host.shuttingDown.Load()) ||
		(me.parentCtx != nil && me.parentCtx.Err() != nil) ||
		(me.packedProcess != nil && me.packedProcess.stopping.Load())
}

// dispatchError marks a failed handler call as cut short by the host when
// the host stopped the extension, so runners do not report it as the
// handler's failure (extension.ErrHandlerStopped).
func (me *managedExt) dispatchError(err error) error {
	if err != nil && me.stoppedByOwner() {
		return fmt.Errorf("%w: %w", extension.ErrHandlerStopped, err)
	}
	return err
}

// processHandle returns the extension's process and process tree as of now.
func (me *managedExt) processHandle() (*os.Process, *processTree) {
	me.procMu.Lock()
	defer me.procMu.Unlock()
	return me.proc, me.processTree
}

// exited returns the channel the process reaper closes, or nil when the extension has no process.
func (me *managedExt) exited() <-chan struct{} {
	me.procMu.Lock()
	defer me.procMu.Unlock()
	return me.exitedCh
}

func (me *managedExt) setProcessHandle(proc *os.Process, tree *processTree, cmd *exec.Cmd, exited <-chan struct{}) {
	me.procMu.Lock()
	defer me.procMu.Unlock()
	me.proc, me.processTree, me.cmd, me.exitedCh = proc, tree, cmd, exited
}

// killProcess kills the process tree, or the process when it has no tree.
func (me *managedExt) killProcess() {
	proc, tree := me.processHandle()
	if tree != nil {
		_ = tree.Kill()
	} else if proc != nil {
		_ = proc.Kill()
	}
}

// setProcess records the process of a packed member, whose tree, command and exit channel belong to the packed process state.
func (me *managedExt) setProcess(proc *os.Process) {
	me.procMu.Lock()
	defer me.procMu.Unlock()
	me.proc = proc
}
