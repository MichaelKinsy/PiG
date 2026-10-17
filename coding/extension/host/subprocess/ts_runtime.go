package subprocess

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/MichaelKinsy/PiG/coding/extension/host/tsruntime"
	"github.com/MichaelKinsy/PiG/internal/linkerexec"
)

// pig additive (D19): the runtime that runs the TypeScript extension host is Node unless the process requests a runtime from the
// slot (tsruntime). The request is PIG_TS_RUNTIME, else the extensionsTypescriptRuntime setting the CLI registers here.
var typeScriptRuntimeSetting atomic.Pointer[string]

// The verifier is process-wide, so every Host of the process hashes and start-checks one executable state once. It is built on
// first use, so a process that runs no TypeScript extension on a slot runtime pays nothing at start.
var typeScriptRuntimeVerifier = sync.OnceValue(func() *tsruntime.Verifier { return tsruntime.NewVerifier(tsruntime.Lock) })

// SetTypeScriptRuntimeSetting registers the extensionsTypescriptRuntime setting for this process. PIG_TS_RUNTIME takes precedence.
func SetTypeScriptRuntimeSetting(id string) {
	typeScriptRuntimeSetting.Store(&id)
}

// TypeScriptRuntimeRequest is the runtime this process requests for its TypeScript extensions.
func TypeScriptRuntimeRequest() tsruntime.Request {
	setting := ""
	if value := typeScriptRuntimeSetting.Load(); value != nil {
		setting = *value
	}
	return tsruntime.RequestFrom(os.Getenv(tsruntime.EnvRuntime), setting)
}

// nodeSelection is the selection of a launch that runs on Node without a request for another runtime.
var nodeSelection = tsruntime.Selection{Request: tsruntime.Request{ID: tsruntime.Node, Source: tsruntime.SourceDefault}, Runtime: tsruntime.Node}

// typeScriptRuntimes records the runtime each launch of a Host used, by extension or cell name.
type typeScriptRuntimes struct {
	mu         sync.Mutex
	resolver   *tsruntime.Resolver
	selections map[string]tsruntime.Selection
	sets       map[string]tsruntime.Set
	// next holds the selection a launch that failed to start on a slot runtime retries with, by extension or cell name.
	next map[string]tsruntime.Selection
}

// nodeLauncherSet returns the extension set a launcher artifact hosts with its runtime tree, or false for any other artifact.
func nodeLauncherSet(binPath, name string) (tsruntime.Set, bool) {
	var members []tsruntime.Member
	var tree string
	if entry, ok := nodeLauncherEntry(binPath); ok {
		members = []tsruntime.Member{{Name: name, Entry: entry}}
		tree = binPath + ".runtime"
	} else if manifest, ok := nodePackedLauncherManifest(binPath); ok {
		for _, member := range manifest {
			members = append(members, tsruntime.Member{Name: member.Name, Entry: member.Entry})
		}
		tree = filepath.Join(filepath.Dir(binPath), "runtime")
	} else {
		return tsruntime.Set{}, false
	}
	return tsruntime.Set{Key: tsruntime.SetKey(nodeRuntimeDigest(), members), Members: members, RuntimeTree: tree}, true
}

// selectTypeScriptRuntime selects the runtime of one launch. A launch that runs on Node, requested or as the fallback, passes the
// Node preflight first.
func (h *Host) selectTypeScriptRuntime(ctx context.Context, label string, set tsruntime.Set) (tsruntime.Selection, error) {
	h.tsRuntimes.mu.Lock()
	sel, retry := h.tsRuntimes.next[label]
	delete(h.tsRuntimes.next, label)
	h.tsRuntimes.mu.Unlock()
	if !retry {
		sel = nodeSelection
		if req := TypeScriptRuntimeRequest(); req.ID != tsruntime.Node {
			sel = h.resolveUnlessNodeOnly(ctx, req, set)
		} else {
			sel.Request = req
		}
	}
	h.tsRuntimes.mu.Lock()
	if h.tsRuntimes.selections == nil {
		h.tsRuntimes.selections = map[string]tsruntime.Selection{}
		h.tsRuntimes.sets = map[string]tsruntime.Set{}
	}
	h.tsRuntimes.selections[label] = sel
	h.tsRuntimes.sets[label] = set
	h.tsRuntimes.mu.Unlock()
	if sel.Fallback != nil {
		h.markExtension(label, "ts-runtime-fallback:"+string(sel.Fallback.Reason))
	}
	if sel.IsNode() {
		return sel, h.ensureNodeRuntime(ctx)
	}
	return sel, nil
}

func (h *Host) typeScriptResolver() *tsruntime.Resolver {
	h.tsRuntimes.mu.Lock()
	defer h.tsRuntimes.mu.Unlock()
	if h.tsRuntimes.resolver == nil {
		h.tsRuntimes.resolver = tsruntime.NewResolver(tsruntime.Root(h.hostInfo.AgentDir), typeScriptRuntimeVerifier())
	}
	return h.tsRuntimes.resolver
}

// TypeScriptRuntimeSelections returns the runtime each TypeScript extension launch of this Host used, by extension or cell name.
func (h *Host) TypeScriptRuntimeSelections() map[string]tsruntime.Selection {
	h.tsRuntimes.mu.Lock()
	defer h.tsRuntimes.mu.Unlock()
	out := make(map[string]tsruntime.Selection, len(h.tsRuntimes.selections))
	maps.Copy(out, h.tsRuntimes.selections)
	return out
}

// typeScriptRuntimeCommand starts script (cli.mjs or cell.mjs) of the runtime tree with args on the selected runtime. Node names its
// loader by file URL in --import; Bun reads no bunfig.toml (it would preload a project's modules), loads no .env file from the project
// (Pi builds its Bun binaries with --no-compile-autoload-bunfig --no-compile-autoload-dotenv, scripts/build-binaries.sh:129-135) and
// installs nothing; a host executable built from the tree takes the script's name as its first argument.
func typeScriptRuntimeCommand(ctx context.Context, sel tsruntime.Selection, runtimeDir, script, loaderLabel string, args []string) *exec.Cmd {
	loaderPath := filepath.Join(runtimeDir, "register-loader.mjs")
	switch sel.Kind {
	case tsruntime.KindBun:
		bunArgs := []string{"--no-install", "--no-env-file", "--config=" + filepath.Join(runtimeDir, "shims", "bunfig.toml"), "--preload", loaderPath, filepath.Join(runtimeDir, script)}
		return linkerexec.CommandContext(ctx, sel.Executable, append(bunArgs, args...)...)
	case tsruntime.KindExecutable:
		return linkerexec.CommandContext(ctx, sel.Executable, append([]string{strings.TrimSuffix(script, ".mjs")}, args...)...)
	}
	loaderURL, urlErr := nodeFileURL(loaderPath)
	cmd := linkerexec.CommandContext(ctx, "node", append([]string{"--import", loaderURL, filepath.Join(runtimeDir, script)}, args...)...)
	if urlErr != nil && cmd.Err == nil {
		cmd.Err = fmt.Errorf("%s %s: %w", loaderLabel, loaderPath, urlErr)
	}
	return cmd
}

// typeScriptRuntimeEnv is the environment a selected runtime needs beyond the host's: a host executable finds the runtime tree,
// which holds the harness and shims it did not compile in, through PIG_TS_RUNTIME_TREE.
func typeScriptRuntimeEnv(sel tsruntime.Selection, set tsruntime.Set) []string {
	if sel.Kind != tsruntime.KindExecutable {
		return nil
	}
	return []string{"PIG_TS_RUNTIME_TREE=" + set.RuntimeTree}
}

// typeScriptBuildNeedsNode reports whether building a Node launcher should check for Node: only when the process runs its
// TypeScript extensions on Node by request. A process that requests another runtime checks Node when a launch falls back to it.
func typeScriptBuildNeedsNode() bool {
	return TypeScriptRuntimeRequest().ID == tsruntime.Node
}

// retryOnNextRuntime reports whether a launch that failed with err ran on a slot runtime and should start again on the next
// runtime: the one the failed runtime's manifest names as fallback, else Node. It records that selection for the retry, which
// selectTypeScriptRuntime consumes. A launch that ran on Node, a cancelled launch and a failure before the process started do not
// retry, so a launch starts at most three times: requested runtime, its fallback, Node.
func (h *Host) retryOnNextRuntime(ctx context.Context, label string, err error) bool {
	if err == nil || ctx.Err() != nil {
		return false
	}
	// Any other error, such as a provider registration the host rejected after the register handshake, is not a start failure: a retry would run the factory again over its partial registration.
	if !isStartFailure(err) {
		return false
	}
	h.tsRuntimes.mu.Lock()
	sel, ok := h.tsRuntimes.selections[label]
	set := h.tsRuntimes.sets[label]
	h.tsRuntimes.mu.Unlock()
	if !ok || sel.IsNode() {
		return false
	}
	next := h.typeScriptResolver().ResolveAfter(ctx, sel, set, err.Error())
	h.tsRuntimes.mu.Lock()
	if h.tsRuntimes.next == nil {
		h.tsRuntimes.next = map[string]tsruntime.Selection{}
	}
	h.tsRuntimes.next[label] = next
	h.tsRuntimes.mu.Unlock()
	h.markExtension(label, "ts-runtime-fallback:"+string(tsruntime.ReasonStartFailed))
	return true
}

// startFailureCodes are the load failures of a process that started on a runtime and ended or failed before every member
// registered. A packed member's failure (packedAcceptError) and an extension module that failed to evaluate or exported no factory, before its factory was called, count too.
var startFailureCodes = map[string]bool{"spawn_failed": true, "process_exited": true, "register_failed": true}

// errStartOnNextRuntime reports a packed cell whose first member failed to start on a slot runtime before any member result was
// published: startGoPackedCell starts the cell again on the next runtime.
type errStartOnNextRuntime struct{ err error }

func (e *errStartOnNextRuntime) Error() string { return e.err.Error() }
func (e *errStartOnNextRuntime) Unwrap() error { return e.err }

// isStartFailure reports whether err ended a launch because its process exited or a member failed to register: a *LoadError with a
// start failure code, a module that failed to evaluate or exported no factory before its factory was called, a Node cell that exited before admitting a member or whose admission write failed, or a
// packed cell whose every failed member failed so.
func isStartFailure(err error) bool {
	if _, ok := errors.AsType[*admissionWriteError](err); ok || errors.Is(err, errNodeCellExitedBeforeAdmission) {
		return true
	}
	if accept, ok := errors.AsType[*packedAcceptError](err); ok {
		for _, memberErr := range accept.perMember {
			if !isStartFailure(memberErr) {
				return false
			}
		}
		return len(accept.perMember) > 0
	}
	// An extension whose module fails to evaluate on a slot runtime may load on Node: it can import an API that runtime lacks. A factory that threw has run, as it runs once in Pi, so its error is the load's error on any runtime.
	if failure, ok := errors.AsType[*FactoryLoadError](err); ok {
		return failure.Phase == LoadPhaseImport
	}
	loadErr, ok := errors.AsType[*LoadError](err)
	return ok && startFailureCodes[loadErr.Code]
}

// resolveUnlessNodeOnly resolves req for set, except that a set recorded as starting only on Node runs there without trying the slot runtimes again.
// pig additive (D19): the record lasts until a file the set was recorded from changes, so a set that fails on every launch costs one failed start.
func (h *Host) resolveUnlessNodeOnly(ctx context.Context, req tsruntime.Request, set tsruntime.Set) tsruntime.Selection {
	resolver := h.typeScriptResolver()
	if detail, ok := tsruntime.ReadNodeOnly(resolver.Root, set); ok {
		skip := tsruntime.Skip{Runtime: req.ID, Reason: tsruntime.ReasonNodeOnly, Detail: detail}
		return tsruntime.Selection{Request: req, Runtime: tsruntime.Node, Fallback: &tsruntime.Fallback{Reason: skip.Reason, Detail: skip.Detail}, Skipped: []tsruntime.Skip{skip}}
	}
	return resolver.Resolve(ctx, req, set)
}

// rememberNodeOnly records the set of label as Node-only when it started on Node after a slot runtime failed to start it.
func (h *Host) rememberNodeOnly(label string, err error) {
	if err != nil {
		return
	}
	h.tsRuntimes.mu.Lock()
	sel, set := h.tsRuntimes.selections[label], h.tsRuntimes.sets[label]
	h.tsRuntimes.mu.Unlock()
	if !sel.IsNode() {
		return
	}
	for _, skip := range sel.Skipped {
		if skip.Reason == tsruntime.ReasonStartFailed {
			_ = tsruntime.RecordNodeOnly(h.typeScriptResolver().Root, set, skip.Detail)
			return
		}
	}
}

// rememberMemberStartFailure records the set of packed cell label as Node-only when member failed to start in it on a slot runtime and
// the cell did not start again on the next runtime: an isolated extension, or the cell's first member, would have loaded on Node, so
// the next launch of the set runs there instead of failing the member again. The member's failure stays this launch's load error.
func (h *Host) rememberMemberStartFailure(label, member string, err error) {
	if err == nil || !isStartFailure(err) {
		return
	}
	h.tsRuntimes.mu.Lock()
	sel, set := h.tsRuntimes.selections[label], h.tsRuntimes.sets[label]
	h.tsRuntimes.mu.Unlock()
	if sel.Runtime == "" || sel.IsNode() {
		return
	}
	_ = tsruntime.RecordNodeOnly(h.typeScriptResolver().Root, set, fmt.Sprintf("%s failed to start on %s: %v", member, sel.Runtime, err))
}

// dropStaleTier gives back a claimed process that runs a compiled host the launcher's set no longer resolves to, and returns nil; any other process comes back unchanged.
// pig additive (D19): a compiled host cannot re-evaluate an edited extension, so /reload starts a new process on the runtime the set resolves to now.
func (h *Host) dropStaleTier(ctx context.Context, share *processShare, binPath, label string) *processShare {
	if share == nil || share.tier == "" {
		return share
	}
	set, ok := nodeLauncherSet(binPath, label)
	if !ok || h.resolveUnlessNodeOnly(ctx, TypeScriptRuntimeRequest(), set).Executable == share.tier {
		return share
	}
	share.release()
	return nil
}

// tierOf is the executable a selection runs as a retention tier: only an ahead-of-time host compiles extensions in.
func tierOf(sel tsruntime.Selection) string {
	if sel.Kind == tsruntime.KindExecutable {
		return sel.Executable
	}
	return ""
}
