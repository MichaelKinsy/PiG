package subprocess

import (
	"context"
	"strconv"
	"sync"
	"sync/atomic"
)

// CellRegistry shares extension cell processes between the Hosts of one process that joined it (Host.ShareCells): a Host that would
// spawn a cell process another Host runs admits its members to that process as a further factory generation. Hosts that did not join
// keep one process per Host. pig additive (D232).
type CellRegistry struct {
	mu    sync.Mutex
	procs map[string]*processShare
	// starting holds one lock per key, so two Hosts that start the same cell together spawn one process.
	starting map[string]*sync.Mutex
	seq      atomic.Uint64
	// ctx is the lifetime of every process the registry's Hosts spawn; Close cancels it.
	ctx    context.Context
	cancel context.CancelFunc
}

// NewCellRegistry returns an empty registry.
func NewCellRegistry() *CellRegistry {
	//portlint:allow ctxbackground the registry owns the lifetime of the processes its Hosts spawn; Close cancels it
	ctx, cancel := context.WithCancel(context.Background())
	return &CellRegistry{procs: map[string]*processShare{}, starting: map[string]*sync.Mutex{}, ctx: ctx, cancel: cancel}
}

// Close ends the lifetime of the processes the registry's Hosts spawned; the server calls it at exit. pig additive (D232).
func (r *CellRegistry) Close() { r.cancel() }

type unsharedCellKey struct{}

// sharesCells reports whether the cell ctx starts takes part in the registry; a quarantined cell starts isolated and is never shared. pig additive (D232).
func (h *Host) sharesCells(ctx context.Context) bool {
	return h.cells != nil && ctx.Value(unsharedCellKey{}) == nil
}

// processParent is the context a spawned cell process lives under: the registry's for a shared cell, so the Host that spawned it ending its load does not kill it under the Hosts that joined, and the spawner's otherwise. pig additive (D232).
func (h *Host) processParent(ctx context.Context) context.Context {
	if h.sharesCells(ctx) {
		return h.cells.ctx
	}
	return runtimeParent(ctx)
}

// lookup returns a live, unparked, undead process registered under key, one reference held for the caller, or nil. It probes with the
// noop control line as RuntimeRetention.claim does.
func (r *CellRegistry) lookup(key string) *processShare {
	if key == "" {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	share := r.procs[key]
	if share == nil {
		return nil
	}
	if !share.alive() || share.exited() || share.dead.Load() || share.send(factoryAdmission{Op: "noop"}) != nil {
		delete(r.procs, key)
		return nil
	}
	if share.parked.Load() || !share.acquire() {
		return nil
	}
	return share
}

// register records share under key.
func (r *CellRegistry) register(key string, share *processShare) {
	if key == "" || share == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	share.onLast = func() { r.forget(key, share) }
	r.procs[key] = share
}

// forget deletes the entry for key when it still names share, so the registry does not keep a process nobody references. pig additive (D232).
func (r *CellRegistry) forget(key string, share *processShare) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.procs[key] == share {
		delete(r.procs, key)
	}
}

// group returns a new id unique in the registry: "g<seq>".
func (r *CellRegistry) group() string {
	return "g" + strconv.FormatUint(r.seq.Add(1), 10)
}

// startLock serializes the lookup, spawn and registration of the cell under key; the returned func releases it.
func (r *CellRegistry) startLock(key string) func() {
	r.mu.Lock()
	m := r.starting[key]
	if m == nil {
		m = &sync.Mutex{}
		r.starting[key] = m
	}
	r.mu.Unlock()
	m.Lock()
	return m.Unlock
}

// ShareCells joins r (pig additive (D232)): this Host admits into the cell processes r knows and registers the ones it spawns. Call it before the first load.
func (h *Host) ShareCells(r *CellRegistry) {
	h.cells = r
	h.group = ""
	if r != nil {
		h.group = r.group()
	}
}

// Group is the ready.group this Host's members receive; "" for a Host that shares no cells.
func (h *Host) Group() string {
	return h.group
}

// cellRegistryKey names a process in the registry: the retention key of the cell qualified by the cwd and config root. It is "" when the cell has no retention key.
func (h *Host) cellRegistryKey(base string) string {
	if base == "" {
		return ""
	}
	return base + "\x00" + h.cwd + "\x00" + h.builder.configRoot
}

// MemberProcessID returns the OS process id of the process serving the named member of this Host, or 0 when it has none (a fused or in-process member). Two members that report one id share a process. pig additive (D232).
func (h *Host) MemberProcessID(name string) int {
	h.mu.Lock()
	me := h.exts[name]
	h.mu.Unlock()
	if me == nil {
		return 0
	}
	share := me.share
	if me.packedProcess != nil {
		share = me.packedProcess.share
	}
	if share != nil {
		switch origin := share.origin.(type) {
		case *packedProcessState:
			if origin.cmd != nil && origin.cmd.Process != nil {
				return origin.cmd.Process.Pid
			}
		case *managedExt:
			me = origin
		}
	}
	if proc, _ := me.processHandle(); proc != nil {
		return proc.Pid
	}
	return 0
}
