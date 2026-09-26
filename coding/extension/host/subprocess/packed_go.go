package subprocess

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/runtimecell"
)

type packedProcessState struct {
	key    string
	cmd    *exec.Cmd
	parent context.Context
	cancel context.CancelFunc
	// generation is this cell key's packedCellGeneration counter value at the
	// moment this process was spawned (CNC-002). A crash report for this
	// process is stale, and must not quarantine anything, once
	// releaseRetryableQuarantines has bumped the key's generation past this
	// value: that only happens once a later Reload has already replaced this
	// process with a fresh attempt, and an async crash report for the old one
	// arriving after that must not tear down the new one just because they
	// share the same content-derived cell key.
	generation   int
	stopping     atomic.Bool
	stopOnce     sync.Once
	waitOnce     sync.Once
	waitDone     chan struct{}
	waitErr      error
	processTree  *processTree
	lease        *runtimecell.UsageLease
	releaseLease sync.Once
}

func (p *packedProcessState) stop() {
	p.stopOnce.Do(func() {
		p.stopping.Store(true)
		if p.cancel != nil {
			p.cancel()
		}
		if p.processTree != nil {
			_ = p.processTree.Kill()
		} else if p.cmd != nil && p.cmd.Process != nil {
			_ = p.cmd.Process.Kill()
		}
	})
}

func (p *packedProcessState) wait() error {
	<-p.startWait()
	return p.waitErr
}

func (p *packedProcessState) startWait() <-chan struct{} {
	p.waitOnce.Do(func() {
		p.waitDone = make(chan struct{})
		go func() {
			p.waitErr = p.cmd.Wait()
			_ = p.processTree.Close()
			close(p.waitDone)
		}()
	})
	return p.waitDone
}

func (p *packedProcessState) releaseUsageLease() {
	p.releaseLease.Do(func() { _ = p.lease.Release() })
}

func cellExtensionNames(extensions []runtimecell.GoExtension) []string {
	names := make([]string, len(extensions))
	for i := range extensions {
		names[i] = extensions[i].Name
	}
	return names
}

type packedPendingExt struct {
	desc runtimecell.GoExtension
	me   *managedExt
	ln   net.Listener
}

// LoadGoPackedCell starts a generated Go packed-cell runner and registers all
// extensions contained in the cell as one atomic unit. The runner is still a
// subprocess; packing only reduces process/artifact count. If any contained
// extension fails to connect/register, all staged connections/processes are
// stopped and previously loaded extensions remain active.
func (h *Host) LoadGoPackedCell(ctx context.Context, cell *runtimecell.GoPackedCell) ([]extension.Extension, error) {
	staged, registered, err := h.startGoPackedCell(ctx, cell)
	if err != nil {
		if _, ok := errors.AsType[*packedAcceptError](err); ok {
			h.rollbackPartialPackedCell(staged)
		}
		return nil, err
	}
	h.commitStaged(staged, nil, "packed replaced")
	return registered, nil
}

func (h *Host) startGoPackedCell(ctx context.Context, cell *runtimecell.GoPackedCell) ([]stagedManagedExt, []extension.Extension, error) {
	if cell == nil {
		return nil, nil, newLoadError("packed-cell", "resolve", "missing_cell", fmt.Errorf("nil packed cell"))
	}
	if cell.BinaryPath == "" {
		return nil, nil, newLoadError(cell.Key, "resolve", "missing_entrypoint", fmt.Errorf("packed cell has no binary path"))
	}
	h.mu.Lock()
	quarantineReason := h.quarantinedCells[cell.Key]
	h.mu.Unlock()
	if quarantineReason != "" {
		return nil, nil, newLoadError(cell.Key, "quarantine", "cell_quarantined", fmt.Errorf("packed cell quarantined: %s", quarantineReason))
	}
	if len(cell.Extensions) == 0 {
		return nil, nil, newLoadError(cell.Key, "resolve", "empty_cell", fmt.Errorf("packed cell has no extensions"))
	}
	// The runner executes every member's factory when it starts, so it starts
	// only after every earlier cell in the plan has committed.
	if err := waitStartTurn(ctx); err != nil {
		return nil, nil, newLoadError(cell.Key, "spawn", "load_cancelled", err)
	}

	nodeRuntime := usesNodeRuntime(cell.BinaryPath, "")
	if nodeRuntime {
		if err := h.ensureNodeRuntime(ctx); err != nil {
			return nil, nil, newLoadError(cell.Key, "spawn", "runtime_unavailable", err)
		}
	}
	pendingExts := make([]packedPendingExt, 0, len(cell.Extensions))
	env := os.Environ()
	for _, desc := range cell.Extensions {
		if desc.Name == "" {
			return nil, nil, newLoadError(cell.Key, "resolve", "missing_name", fmt.Errorf("packed extension name is required"))
		}
		sockPath, err := h.sockPathFor(desc.Name)
		if err != nil {
			return nil, nil, newLoadError(desc.Name, "spawn", "socket_dir_failed", fmt.Errorf("create socket dir: %w", err))
		}
		_ = os.Remove(sockPath)
		ln, address, err := ListenExtension(sockPath, nodeRuntime)
		if err != nil {
			closePendingListeners(pendingExts)
			return nil, nil, newLoadError(desc.Name, "spawn", "listen_failed", fmt.Errorf("listen %s: %w", sockPath, err))
		}
		sockPath = address
		// Source (not just Path, which every member of this cell shares:
		// cell.BinaryPath) preserves each member's own identity for anything
		// keyed by extensionSourcePaths, such as DetectExtensionConflicts:
		// packed mode must report the same per-extension path an isolated
		// extension would, or two packed members that both register the same
		// tool stop looking like a conflict because they share one binary.
		me := &managedExt{config: ExtConfig{Name: desc.Name, Path: cell.BinaryPath, Source: desc.Root, Enabled: true}, host: h, supervisor: NewSupervisor(DefaultSupervisorConfig()), sockPath: sockPath, packedCellKey: cell.Key}
		pendingExts = append(pendingExts, packedPendingExt{desc: desc, me: me, ln: ln})
		env = append(env, fmt.Sprintf("%s=%s", runtimecell.SocketEnvName(desc.Name), sockPath))
	}
	defer closePendingListeners(pendingExts)

	extCtx, cancel := context.WithCancel(ctx)
	lease, err := runtimecell.AcquireArtifactUsageLease(cell.BinaryPath)
	if err != nil {
		cancel()
		return nil, nil, newLoadError(cell.Key, "spawn", "cache_lease_failed", fmt.Errorf("lease packed cell artifact %s: %w", cell.BinaryPath, err))
	}
	processState := &packedProcessState{key: cell.Key, parent: ctx, cancel: cancel, lease: lease, generation: cell.Generation}
	h.mu.Lock()
	if h.packedProcesses == nil {
		h.packedProcesses = make(map[string]*packedProcessState)
	}
	h.packedProcesses[cell.Key] = processState
	h.mu.Unlock()
	for i := range pendingExts {
		pendingExts[i].me.packedProcess = processState
		// As for an isolated extension (startExt), an exit after the owner
		// cancelled ctx is teardown, not a crash.
		pendingExts[i].me.parentCtx = ctx
	}
	started := false
	defer func() {
		if !started {
			cancel()
		}
	}()
	cmd := buildExtCommand(extCtx, cell.BinaryPath, "")
	env = append(env,
		fmt.Sprintf("PIG_EXT_PACKED_CELL=%s", cell.Key),
		fmt.Sprintf("PIG_EXT_PACKED_CELL_HASH=%s", cell.Hash),
		"PIG_EXT_ACTIVE_MEMBERS="+strings.Join(cellExtensionNames(cell.Extensions), ","),
	)
	cmd.Env = env
	cmd.Dir = h.cwd
	stderrFile, _ := os.CreateTemp("", fmt.Sprintf("pig-packed-%s-*.log", sanitizeLogName(cell.Key)))
	if stderrFile != nil {
		cmd.Stderr = stderrFile
		defer func() { _ = stderrFile.Close() }()
	}
	for _, pending := range pendingExts {
		h.markExtension(pending.me.config.Name, "spawn-start")
	}
	processTree, err := startProcessTree(cmd)
	if err != nil {
		processState.releaseUsageLease()
		cancel()
		loadErr := newLoadError(cell.Key, "spawn", "spawn_failed", fmt.Errorf("spawn packed runner %s: %w", cell.BinaryPath, err))
		if stderrFile != nil {
			loadErr.StderrLog = stderrFile.Name()
		}
		return nil, nil, loadErr
	}

	processState.cmd = cmd
	processState.processTree = processTree
	for _, pending := range pendingExts {
		h.markExtension(pending.me.config.Name, "spawn-done")
	}
	processState.startWait()
	for i := range pendingExts {
		pendingExts[i].me.proc = cmd.Process
		pendingExts[i].me.cancel = cancel
		if stderrFile != nil {
			pendingExts[i].me.stderrLogPath = stderrFile.Name()
		}
	}

	staged := make([]stagedManagedExt, 0, len(pendingExts))
	registered := make([]extension.Extension, 0, len(pendingExts))
	var acceptErr *packedAcceptError
	for i := range pendingExts {
		me := pendingExts[i].me
		ext, err := h.acceptPackedExt(ctx, me, pendingExts[i].ln)
		if err != nil {
			// A member's factory may have already run (with side effects)
			// inside the shared process by the time its own accept/register
			// fails, and any earlier sibling in this loop may already be
			// fully registered: see packedAcceptError. Report this member's
			// failure and keep going, instead of tearing the whole cell
			// down the way a build/spawn failure (which happens before any
			// member has run) still does.
			if acceptErr == nil {
				acceptErr = &packedAcceptError{perMember: make(map[string]error, len(pendingExts))}
			}
			acceptErr.perMember[me.config.Name] = err
			h.stopFailedPackedMember(me)
			continue
		}
		staged = append(staged, stagedManagedExt{name: me.config.Name, me: me})
		registered = append(registered, *ext)
	}
	if len(registered) == 0 && acceptErr != nil {
		// Nothing survived to keep the process alive for.
		processState.stop()
		_ = processState.wait()
		processState.releaseUsageLease()
		return nil, nil, acceptErr
	}
	started = true
	h.mu.Lock()
	if h.watchedPacked == nil {
		h.watchedPacked = make(map[*packedProcessState]struct{})
	}
	h.watchedPacked[processState] = struct{}{}
	h.mu.Unlock()
	go h.watchPackedProcess(processState)
	if acceptErr != nil {
		return staged, registered, acceptErr
	}
	return staged, registered, nil
}

// stopFailedPackedMember cleans up one packed cell member that failed to
// accept/register, without touching the shared packedProcess or any
// sibling. Unlike stopManaged/stopManagedSkippingProviders (used for an
// extension that owns its process outright, whether isolated or via
// rollbackPartialPackedCell's deliberate atomic teardown of a whole cell),
// calling packedProcess.stop() here would kill the shared process out from
// under every other member currently running inside it, including ones
// this same accept loop already registered successfully. A member that
// never registered has no conn (already closed by acceptPackedExt on a
// register-time failure; nil for an earlier accept/connect-time failure),
// no providers, and no OAuth registrations to unregister.
func (h *Host) stopFailedPackedMember(me *managedExt) {
	if me == nil {
		return
	}
	me.shuttingDown.Store(true)
	me.releaseLivenessOwner()
	if me.conn != nil {
		_ = me.conn.Close("packed member failed to register")
	}
	if me.sockPath != "" {
		_ = os.Remove(me.sockPath)
	}
	if h.uiBridge != nil {
		h.uiBridge.ClearExtensionConn(me.config.Name, me.conn)
	}
}

// packedAcceptError is startGoPackedCell's error type for one or more
// members that failed to accept/register after the shared packed process
// was already spawned. Unlike a build/spawn/quarantine failure (a plain
// error returned before any member has been accepted, which is safe to
// retry per member in isolation because nothing has run yet), an
// accept-time failure means the process is already running and any other
// member may already have executed its factory and registered
// successfully. The cell planner's ordinary packed-cell-failure fallback
// (isolateCellFailure) tears the whole cell down and retries every member
// in isolation; doing that here would silently re-invoke an already-
// succeeded sibling's factory a second time. That breaks the "extensions
// loaded before trust are reused exactly once" contract
// LoadFinalExtensionSet depends on, and matches nothing upstream: Pi's own
// loader.ts continue-on-error reports a throwing extension's error once
// and never retries it. isolateCellFailure recognizes this type (via
// errors.As, since a caller may wrap it) and skips its per-member retry,
// reporting exactly the members named here as failed and leaving every
// other member - already staged/registered by the same call - untouched.
//
// A direct LoadGoPackedCell/LoadRustPackedCell/LoadPythonPackedCell/
// LoadNodePackedCell caller (not the cell planner) keeps its own documented
// atomic contract instead: each checks for this error type itself and
// rolls the partial success back, matching its "all contained extensions
// register, or none do" promise.
type packedAcceptError struct {
	perMember map[string]error
}

func (e *packedAcceptError) Error() string {
	names := make([]string, 0, len(e.perMember))
	for name := range e.perMember {
		names = append(names, name)
	}
	slices.Sort(names)
	return fmt.Sprintf("packed cell member(s) failed to register: %s", strings.Join(names, ", "))
}

// Unwrap exposes every member's underlying error (in deterministic name
// order) so errors.As/errors.Is still finds a wrapped *LoadError even when a
// caller (e.g. a single-member isolated "cell") folds a packedAcceptError
// into another %w-wrapped error.
func (e *packedAcceptError) Unwrap() []error {
	names := make([]string, 0, len(e.perMember))
	for name := range e.perMember {
		names = append(names, name)
	}
	slices.Sort(names)
	errs := make([]error, len(names))
	for i, name := range names {
		errs[i] = e.perMember[name]
	}
	return errs
}

// rollbackPartialPackedCell tears down every staged member and the shared
// process they belong to. A direct LoadGoPackedCell/LoadRustPackedCell/
// LoadPythonPackedCell/LoadNodePackedCell caller (unlike the cell planner's
// LoadAll/Reload path) documents an atomic "all contained extensions
// register, or none do" contract, so on a packedAcceptError it calls this to
// restore that contract instead of keeping startGoPackedCell's partial
// success, which callers reaching it through the planner want to keep.
func (h *Host) rollbackPartialPackedCell(staged []stagedManagedExt) {
	var processState *packedProcessState
	for _, item := range staged {
		if processState == nil {
			processState = item.me.packedProcess
		}
		h.stopManaged(item.me, "packed cell load failed atomically")
	}
	if processState != nil {
		processState.stop()
		_ = processState.wait()
		processState.releaseUsageLease()
	}
}

func (h *Host) watchPackedProcess(process *packedProcessState) {
	defer func() {
		process.releaseUsageLease()
		h.mu.Lock()
		delete(h.watchedPacked, process)
		h.mu.Unlock()
	}()
	err := process.wait()
	if process.stopping.Load() || h.shuttingDown.Load() || (process.parent != nil && process.parent.Err() != nil) {
		return
	}
	reason := "packed process exited"
	if err != nil {
		reason += ": " + err.Error()
	}
	// quarantinePackedCellGeneration checks process.generation against the
	// key's current packedCellGeneration atomically with the quarantine
	// write (CNC-002): a plain pre-check here would leave a window between
	// "generation still current" and "quarantine written" for a concurrent
	// Reload to claim a fresh generation and start a replacement process
	// that this stale report would then wrongly tear down.
	h.quarantinePackedCellGeneration(process.key, process.generation, reason)
}

func (h *Host) acceptPackedExt(ctx context.Context, me *managedExt, ln net.Listener) (*extension.Extension, error) {
	connCh := make(chan net.Conn, 1)
	errCh := make(chan error, 1)
	go func() {
		c, err := ln.Accept()
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
		loadErr := newLoadError(me.config.Name, "connect", "accept_failed", fmt.Errorf("accept packed extension: %w", err))
		loadErr.StderrLog = me.stderrLogPath
		return nil, loadErr
	case <-me.packedProcess.startWait():
		exitErr := errors.New("packed process exited before extension connected")
		if me.packedProcess.waitErr != nil {
			exitErr = fmt.Errorf("packed process exited before extension connected: %w", me.packedProcess.waitErr)
		}
		// Upstream reports the loader's own error; lead with the cause the
		// process wrote to stderr (matches host.go's isolated-mode
		// "extension process exited before connecting" case).
		if cause := stderrCause(me.stderrLogPath); cause != "" {
			exitErr = fmt.Errorf("%s (%w)", cause, exitErr)
		}
		loadErr := newLoadError(me.config.Name, "connect", "process_exited", exitErr)
		loadErr.StderrLog = me.stderrLogPath
		return nil, loadErr
	case <-ctx.Done():
		loadErr := newLoadError(me.config.Name, "connect", "load_cancelled", fmt.Errorf("packed extension load cancelled before it connected: %w", ctx.Err()))
		loadErr.StderrLog = me.stderrLogPath
		return nil, loadErr
	}

	h.markExtension(me.config.Name, "handshake-start")
	conn := NewConn(me.config.Name, rawConn)
	conn.Start(ctx)
	me.conn = conn

	reg, err := h.waitForRegister(ctx, conn)
	if err != nil {
		_ = conn.Close("register failed")
		regErr := fmt.Errorf("register handshake: %w", err)
		// A packed member that fails to load never sends its error over the
		// wire: cell.mjs's signalLoadFailure only connects then destroys the
		// socket (packed_go.go's isolated-mode equivalent, host.go's
		// "process exited before connecting" case, has the same problem and
		// already leads with stderrCause for the same reason). Without this,
		// the host can only report a generic "connection closed", never the
		// actual "SyntaxError"/thrown message cell.mjs already printed to
		// this member's stderr log.
		if cause := stderrCause(me.stderrLogPath); cause != "" {
			regErr = fmt.Errorf("%s (%w)", cause, regErr)
		}
		loadErr := newLoadError(me.config.Name, "register", "register_failed", regErr)
		loadErr.StderrLog = me.stderrLogPath
		loadErr.Hint = registerDecodeHint(err)
		return nil, loadErr
	}
	if !me.config.acceptsRegisteredName(reg.Name) {
		_ = conn.Close("name mismatch")
		loadErr := newLoadError(me.config.Name, "register", "name_mismatch", fmt.Errorf("packed extension registered as %q", reg.Name))
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
		loadErr := newLoadError(me.config.Name, "register", "invalid_registration", err)
		loadErr.StderrLog = me.stderrLogPath
		return nil, loadErr
	}
	readyWidth := 120
	if h.widthFunc != nil {
		if w := h.widthFunc(); w > 0 {
			readyWidth = w
		}
	}
	readyPayload := &ReadyPayload{Cwd: h.cwd, Mode: h.mode, Width: readyWidth}
	if h.uiBridge != nil {
		// Same contract as the isolated path: a registering extension gets the
		// whole log once, then only what is new.
		me.entryCursorMu.Lock()
		me.entryCursor = 0
		readyPayload.State = h.uiBridge.Snapshot(me.flagNames, 0, h.subscribedToSessionLog(me.config.Name))
		if readyPayload.State.Session != nil {
			me.entryCursor = readyPayload.State.Session.EntryCount
		}
		me.entryCursorMu.Unlock()
	}
	if err := conn.Send(&Envelope{Type: MsgReady, Ready: readyPayload}); err != nil {
		loadErr := newLoadError(me.config.Name, "ready", "send_ready_failed", fmt.Errorf("send ready: %w", err))
		loadErr.StderrLog = me.stderrLogPath
		return nil, loadErr
	}
	for _, provider := range reg.Providers {
		var cfg extension.ProviderConfig
		if err := json.Unmarshal(provider.Config, &cfg); err != nil {
			loadErr := newLoadError(me.config.Name, "register", "provider_config_invalid", fmt.Errorf("provider %s: decode config: %w", provider.Name, err))
			loadErr.StderrLog = me.stderrLogPath
			return nil, loadErr
		}
		// Model-provider registration and OAuth registration are independent
		// concerns: an extension may contribute an OAuth login with no
		// model-provider callback wired, so gate only the model-provider hook.
		if h.onRegisterProvider != nil {
			h.onRegisterProvider(provider.Name, cfg)
		}
		me.providerNames = append(me.providerNames, provider.Name)
		if err := h.registerOAuthProvider(me, provider.Name, provider.Config); err != nil {
			loadErr := newLoadError(me.config.Name, "register", "provider_config_invalid", err)
			loadErr.StderrLog = me.stderrLogPath
			return nil, loadErr
		}
	}
	if len(reg.Providers) > 0 {
		me.releaseLiveness = conn.holdLiveness()
	}
	if h.uiBridge != nil {
		h.uiBridge.RegisterExtConn(me.config.Name, conn)
	}
	ext := h.buildExtension(me, reg)
	me.ext = ext
	me.supervisor.RecordSuccess()
	h.markExtension(me.config.Name, "handshake-done")
	go h.handleIncoming(me)
	return ext, nil
}

func closePendingListeners(items []packedPendingExt) {
	for _, item := range items {
		if item.ln != nil {
			_ = item.ln.Close()
		}
	}
}

func sanitizeLogName(value string) string {
	value = fileNameComponent(value)
	if value == "" {
		return "cell"
	}
	return value
}

// fileNameComponent replaces each space, control character, and character
// Windows rejects in file names with '-'. Duplicate identities are keyed name:N
// and packed cell keys contain ':' and '/', so a name used in a cache entry or
// log file must pass through here to be valid on every platform.
func fileNameComponent(value string) string {
	return strings.Map(func(r rune) rune {
		if r <= ' ' || r == 0x7f || strings.ContainsRune(`<>:"/\|?*`, r) {
			return '-'
		}
		return r
	}, value)
}
