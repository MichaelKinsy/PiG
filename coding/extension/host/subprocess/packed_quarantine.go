package subprocess

import (
	"fmt"
	"slices"
	"strings"
)

// armPackedProcessGenerations bumps packedCellGeneration for every packed
// cell key this Host has ever spawned a process for (h.packedProcesses),
// atomically under one h.mu hold. Reload calls this only once config
// loading has succeeded (CNC-003), never before: a config-loader failure
// returns early and retains every currently running extension and process
// unchanged, so arming generations before that point would invalidate
// crash ownership for a process an aborted reload never actually replaces,
// causing its real, later crash to be silently discarded as stale instead
// of quarantined.
//
// Given a successful config load, every explicit Reload always respawns
// each packed cell still present in the plan with a brand-new process (see
// stagePackedNode/startGoPackedCell: there is no "keep the existing process
// if unchanged" path), and a cell key dropped entirely from the plan is
// torn down explicitly through the "removed" list, which stops its process
// before watchPackedProcess can act on it. So every currently-known packed
// process is, from this Reload's point of view, either about to be
// replaced or already being shut down deliberately - bumping its key's
// generation now can never make a still-relevant crash invisible.
//
// Doing this bump first, instead of only inside stagePackedNode/
// startGoPackedCell once a plan has been computed, closes CNC-002's
// remaining race: without it, a crash report for a cell key's old process
// could still observe its generation as current (because the bump for the
// key's *replacement* process had not happened yet) and write a quarantine
// entry after Reload's own releaseRetryableQuarantines already read an
// empty map but before PlanCells read it, causing this same Reload - the
// one recovering from that very crash - to wrongly fission the cell. Bumping
// every known key's generation up front, before any of that can run, means
// no crash report for a process spawned before this Reload can ever pass
// quarantinePackedCellGeneration's staleness check again, regardless of how
// the rest of Reload's work interleaves with the async report.
func (h *Host) armPackedProcessGenerations() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.packedCellGeneration == nil {
		h.packedCellGeneration = make(map[string]int)
	}
	for key := range h.packedProcesses {
		h.packedCellGeneration[key]++
	}
}

// packedCellSupervisor returns the persistent circuit breaker for cellKey,
// creating it with the same DefaultSupervisorConfig() every isolated
// extension and every packed member already uses (CNC-002: no new
// crash-loop policy). Unlike a managedExt's own Supervisor, which a fresh
// Reload replaces along with the managedExt (so an isolated extension's
// crash history does not survive past its next reload), a cell key's
// Supervisor here is stored on the Host and outlives any one packed
// process, because "did this cell key keep crashing" is a question about
// repeated reloads, not about one running process.
func (h *Host) packedCellSupervisor(cellKey string) *Supervisor {
	h.packedCellSupervisorsMu.Lock()
	defer h.packedCellSupervisorsMu.Unlock()
	if h.packedCellSupervisors == nil {
		h.packedCellSupervisors = make(map[string]*Supervisor)
	}
	s, ok := h.packedCellSupervisors[cellKey]
	if !ok {
		s = NewSupervisor(DefaultSupervisorConfig())
		h.packedCellSupervisors[cellKey] = s
	}
	return s
}

// quarantinePackedCell disables a crashed packed cell and tears down every
// extension currently attached to that cell. This is the runtime-cell fission
// point: callers/planners can inspect QuarantinedCells and reload the affected
// extensions as isolated subprocesses instead of reusing the failed pack.
// Callers that never claim a packedCellGeneration (an explicit non-process
// crash report, e.g. a per-member socket close, or a test) call this
// directly, which always quarantines.
func (h *Host) quarantinePackedCell(cellKey, reason string) {
	h.quarantinePackedCellGeneration(cellKey, 0, reason)
}

// quarantinePackedCellGeneration is quarantinePackedCell's generation-aware
// form (CNC-002). generation, when nonzero, is the packedCellGeneration value
// the caller's packed process claimed at spawn time. The staleness check
// against the cell key's current generation and every state mutation below
// (the quarantine-map write and detaching the cell's managedExts) happen
// under one h.mu hold, so a concurrent Reload that claims a fresh generation
// for this key cannot interleave between "the report is still current" and
// "tear the cell down": either this call observes its generation is already
// stale and does nothing, or it completes its teardown before any later
// nextPackedCellGeneration call for this key can be observed to have
// happened. Without that atomicity, a process-death report for a process a
// Reload has already replaced could still detach the replacement's
// newly-registered extensions from h.exts, which is exactly the permanent
// fission CNC-002 fixes.
func (h *Host) quarantinePackedCellGeneration(cellKey string, generation int, reason string) {
	if cellKey == "" {
		return
	}
	h.mu.Lock()
	if generation != 0 && h.packedCellGeneration[cellKey] != generation {
		// A later Reload already claimed a fresh generation for this key
		// (releaseRetryableQuarantines/nextPackedCellGeneration) before this
		// report reached the lock: it is stale and must not quarantine the
		// replacement process just because it shares the old content-derived
		// key.
		h.mu.Unlock()
		return
	}
	if h.quarantinedCells == nil {
		h.quarantinedCells = make(map[string]string)
	}
	if h.quarantinedCells[cellKey] == "" {
		h.quarantinedCells[cellKey] = reason
	}
	var members []*managedExt
	for name, me := range h.exts {
		if me.packedCellKey == cellKey {
			delete(h.exts, name)
			members = append(members, me)
		}
	}
	h.mu.Unlock()
	names := make([]string, len(members))
	for i, me := range members {
		names[i] = me.config.Name
	}
	slices.Sort(names)
	// The returned delay/error are for a caller that reschedules a restart
	// itself; packed cells have no such automatic restart (only an explicit
	// Reload repacks), so only the resulting IsDisabled() state matters here.
	_, _ = h.packedCellSupervisor(cellKey).RecordCrash()
	for _, me := range members {
		me.shuttingDown.Store(true)
		h.stopManaged(me, "packed cell quarantined")
		if h.onCrash != nil {
			h.onCrash(me.config.Name, 0, true, withStderrLog(quarantineNotice(reason, names), me.stderrLogPath))
		}
	}
}

// quarantineNotice states plainly which extensions a packed-cell crash
// stopped and that /reload recreates the cell (fission splits a quarantined
// cell back into isolated extensions on the next reload; see PlanCells).
func quarantineNotice(reason string, stoppedNames []string) string {
	if len(stoppedNames) == 0 {
		return reason
	}
	return fmt.Sprintf("%s; %s stopped; /reload restarts them", reason, strings.Join(stoppedNames, ", "))
}

// releaseRetryableQuarantines clears quarantine for every packed cell key
// whose circuit breaker has not tripped, so an explicit Reload gives it
// another chance to repack (CNC-002; the doc comment on Supervisor.Reset
// already says "Used by /reload" - this is that call site, extended to the
// cell-key-scoped supervisor packed cells use). Pig's default is one shared
// process per packable language (Go, Rust, Python, and Node cells alike);
// quarantine from a live crash degrades that to isolated subprocesses, but
// only until the next explicit reload, not permanently, unless the same
// cell key crashes DefaultSupervisorConfig().MaxCrashes times within
// CrashWindow - the exact same threshold every isolated extension's own
// Supervisor already enforces, so a Node cell's crash-loop protection reads
// no differently from an isolated extension's. PlanCells reads
// QuarantinedCells() right after this runs, so a cleared key is eligible to
// repack in the same reload.
func (h *Host) releaseRetryableQuarantines() {
	h.mu.Lock()
	keys := make([]string, 0, len(h.quarantinedCells))
	for key := range h.quarantinedCells {
		keys = append(keys, key)
	}
	h.mu.Unlock()
	for _, key := range keys {
		if h.packedCellSupervisor(key).IsDisabled() {
			continue
		}
		h.mu.Lock()
		delete(h.quarantinedCells, key)
		h.mu.Unlock()
	}
}

// nextPackedCellGeneration claims and returns the next packedCellGeneration
// value for key. Every attempt to spawn a packed process for a given cell key
// (startGoPackedCell) claims a fresh generation, whether this is the key's
// first spawn or a reload replacing an earlier one; watchPackedProcess only
// acts on a crash report whose generation is still the latest one claimed, so
// an async report for a since-superseded process can never quarantine the
// process that replaced it, even though they share the same content-derived
// key (CNC-002).
func (h *Host) nextPackedCellGeneration(key string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.packedCellGeneration == nil {
		h.packedCellGeneration = make(map[string]int)
	}
	h.packedCellGeneration[key]++
	return h.packedCellGeneration[key]
}
