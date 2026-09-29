package subprocess

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"time"
)

type recoveryMembersKey struct{}
type recoveryLifetimeKey struct{}

type nodeRecoveryLifetime struct {
	context  context.Context
	original context.Context
	cancel   context.CancelFunc
	stopHost func() bool
	refs     atomic.Int64
}

func (l *nodeRecoveryLifetime) release() {
	if l.refs.Add(-1) == 0 {
		l.cancel()
		l.stopHost()
	}
}

func (h *Host) newRecoveryLifetime(original context.Context) *nodeRecoveryLifetime {
	ctx, cancel := context.WithCancel(original)
	l := &nodeRecoveryLifetime{context: ctx, original: original, cancel: cancel, stopHost: context.AfterFunc(h.recoveryContext, cancel)}
	l.refs.Store(1)
	return l
}

// current resolves a capability for a new invocation after automatic recovery. An invocation already in progress retains its original connection and is never replayed. Explicit reload never links old capabilities to its replacement.
func (me *managedExt) current() *managedExt {
	for {
		next := me.replacement.Load()
		if next == nil {
			return me
		}
		me = next
	}
}

// acceptsNodeGeneration rejects traffic retained by an obsolete Node connection without changing registration-time host calls for a current member.
func (h *Host) acceptsNodeGeneration(me *managedExt) bool {
	p := me.packedProcess
	if p == nil || !p.node {
		return true
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return !p.stopping.Load() && h.packedCellGeneration[p.key] == p.generation
}

// planCells preserves explicit isolation and applies host-owned crash partitions only to shareable Node factories. transitionMu owns the diagnostic maps.
func (h *Host) planCells(configs []ExtConfig) []CellSpec {
	configs = slices.Clone(configs)
	for i, cfg := range configs {
		cfg = normalizeUnresolvedNodeConfig(cfg)
		if isPackableNode(cfg) {
			cfg.nodeRecoveryGroup = h.nodeGroups[cfg.Name]
			if h.nodeFaults[cfg.Name] != "" {
				cfg.nodeRecoveryGroup = "quarantine:" + cfg.Name
			}
		}
		configs[i] = cfg
	}
	cells := PlanCells(configs, h.QuarantinedCells())
	for i := range cells {
		if cells[i].Strategy == CellStrategyPackedNode && len(cells[i].Extensions) == 1 && h.nodeFaults[cells[i].Extensions[0].Name] != "" {
			cells[i].Isolation = "quarantined:" + cells[i].Extensions[0].Name
		}
	}
	return cells
}

func (p *packedProcessState) culprit() string {
	if owner := p.factoryOwner.Load(); owner != nil {
		return *owner
	}
	spellings := make([][]string, len(p.members))
	for i, candidate := range p.members {
		spellings[i] = stackFrameSpellings(valueOr(candidate.nodeEntry, candidate.config.Source))
	}
	for _, me := range p.members {
		data := readStderrTail(me.stderrLogPath)
		for line := range strings.SplitSeq(string(data), "\n") {
			if !strings.HasPrefix(strings.TrimSpace(line), "at ") {
				continue
			}
			for i, candidate := range p.members {
				for _, spelling := range spellings[i] {
					if strings.Contains(line, spelling+":") {
						return candidate.config.Name
					}
				}
			}
		}
		break
	}
	if owner := p.lastOwner.Load(); owner != nil {
		return *owner
	}
	return ""
}

// recoverNodeProcess restarts factories, never interrupted invocations. Unknown failure gets one whole-group retry, then progressively smaller diagnostic groups. Identifying the culprit rejoins all healthy members on one bus.
// pig additive (D20): process recovery preserves cooperating Node extensions instead of isolating every member.
func (h *Host) recoverNodeProcess(failed *packedProcessState, reason string) {
	h.transitionMu.Lock()
	defer h.transitionMu.Unlock()
	h.mu.Lock()
	if h.shuttingDown.Load() || failed.stopping.Load() || h.packedCellGeneration[failed.key] != failed.generation {
		h.mu.Unlock()
		return
	}
	var processes []*packedProcessState
	configured := map[string]ExtConfig{}
	var members []*managedExt
	for key, p := range h.packedProcesses {
		if !p.node || p.stopping.Load() || h.packedCellGeneration[key] != p.generation {
			continue
		}
		processes = append(processes, p)
		for _, cfg := range p.configs {
			configured[cfg.Name] = cfg
		}
		members = append(members, p.members...)
	}
	slices.SortFunc(members, func(a, b *managedExt) int { return h.loadOrder[a.config.Name] - h.loadOrder[b.config.Name] })
	// Invalidate every old process before any replacement may register. Old sockets retain no UI/provider authority.
	for _, p := range processes {
		h.packedCellGeneration[p.key]++
		p.stopping.Store(true)
	}
	for _, me := range members {
		if h.exts[me.config.Name] == me {
			delete(h.exts, me.config.Name)
		}
	}
	h.mu.Unlock()
	if h.nodeFaults == nil {
		h.nodeFaults = map[string]string{}
		h.nodeGroups = map[string]string{}
		h.nodeCrashes = map[string]int{}
	}
	culprit := failed.culprit()
	h.nodeCrashes[failed.key]++
	action := "restart packed group"
	bisect := false
	if culprit != "" || len(failed.members) == 1 {
		if culprit == "" {
			culprit = failed.members[0].config.Name
		}
		h.nodeFaults[culprit] = reason
		clear(h.nodeGroups)
		action = "quarantine " + culprit + "; restart healthy members together"
	} else if h.nodeCrashes[failed.key] > 1 {
		bisect = true
		middle := len(failed.members) / 2
		for i, me := range failed.members {
			side := 0
			if i >= middle {
				side = 1
			}
			h.nodeGroups[me.config.Name] = fmt.Sprintf("%s/%d", failed.key, side)
		}
		action = "bisect repeatedly failing packed group"
	}
	// Reserve the failed process's diagnostic before recovery teardown can remove it. Healthy processes and unreported crashes keep normal cleanup.
	logPath := ""
	if h.onCrash != nil {
		logPath = failed.stderrLog.retain()
	}
	var configs []ExtConfig
	oldByName := map[string]*managedExt{}
	for _, me := range members {
		oldByName[me.config.Name] = me
		cfg, ok := configured[me.config.Name]
		if !ok {
			cfg = me.config
			cfg.Path = ""
		}
		cfg.RuntimeKind = "subprocess"
		cfg.RuntimeLanguage = "node"
		cfg.EntrypointKind = "factory"
		configs = append(configs, cfg)
		h.stopManaged(me, "Node process recovery")
	}
	for _, p := range processes {
		p.stopAndReap()
	}
	delay, breakerErr := h.packedCellSupervisor(failed.key).RecordCrash()
	if breakerErr != nil && len(failed.members) == 1 {
		disabled := failed.members[0].config.Name
		configs = slices.DeleteFunc(configs, func(cfg ExtConfig) bool { return cfg.Name == disabled })
		if h.onCrash != nil {
			h.onCrash(disabled, 0, true, withStderrLog(reason+"; "+breakerErr.Error(), logPath))
		}
		delay = 0
	}
	for _, cfg := range configs {
		if h.onCrash != nil {
			h.onCrash(cfg.Name, delay, false, withStderrLog(reason+"; "+action+"; interrupted callbacks are not replayed", logPath))
		}
	}
	lifetime := h.newRecoveryLifetime(failed.originalOwner)
	defer lifetime.release()
	ctx := context.WithValue(lifetime.context, recoveryLifetimeKey{}, lifetime)
	ctx = context.WithValue(ctx, runtimeParentKey{}, lifetime.context)
	ctx = context.WithValue(ctx, recoveryMembersKey{}, oldByName)
	ctx = context.WithValue(ctx, deferNodeReadyKey{}, true)
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return
	case <-timer.C:
	}
	if h.shuttingDown.Load() {
		return
	}
	cells := h.planCells(configs)
	h.planEventBus(ctx, cells)
	// Diagnostic halves have already received the group's whole-process retry; their next unknown crash narrows immediately.
	if bisect {
		for _, cell := range cells {
			if h.nodeCrashes[cell.Key] == 0 {
				h.nodeCrashes[cell.Key] = 1
			}
		}
	}
	var replacement []stagedManagedExt
	h.stageCellsInOrder(ctx, cells, func(cell CellSpec, outcome stageOutcome, err error) {
		outcomes, failures := h.isolateCellFailure(ctx, cell, nil, outcome, err)
		for _, failure := range failures {
			if h.onCrash != nil {
				h.onCrash(failure.cfg.Name, 0, true, "recovery failed: "+failure.err.Error())
			}
		}
		for _, outcome := range outcomes {
			replacement = append(replacement, outcome.staged...)
		}
	})
	for _, item := range replacement {
		if old := oldByName[item.name]; old != nil {
			old.replacement.Store(item.me)
		}
	}
	h.commitStaged(replacement, nil, "Node recovery")
	for _, item := range replacement {
		if err := item.me.activateNode(); err != nil {
			h.disablePackedMember(item.me, err.Error())
		}
	}
	// Replacement processes keep their connection lifetime until the host or original owner stops; the request-scoped wait above has completed.
}

// stackFrameSpellings returns the ways a V8 stack frame can name entry. An ES
// module frame names the module by the file URL Node loaded it from: its
// resolved real path, percent-encoded, with forward slashes and a leading
// slash before a Windows drive letter. A CommonJS frame names the plain path.
// Matching only the plain path missed every Windows ESM frame and any path
// containing a space.
func stackFrameSpellings(entry string) []string {
	spellings := []string{entry}
	paths := []string{entry}
	if real, err := filepath.EvalSymlinks(entry); err == nil && real != entry {
		spellings = append(spellings, real)
		paths = append(paths, real)
	}
	for _, path := range paths {
		if href, err := nodeFileURL(path); err == nil {
			spellings = append(spellings, href)
		}
	}
	return spellings
}
