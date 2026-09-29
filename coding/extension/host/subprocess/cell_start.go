package subprocess

import (
	"cmp"
	"context"
	"maps"
	"runtime"
	"slices"
	"sync"
)

type startTurnKey struct{}
type runtimeParentKey struct{}
type deferNodeReadyKey struct{}

func nodeReadyDeferred(ctx context.Context) bool {
	deferred, _ := ctx.Value(deferNodeReadyKey{}).(bool)
	return deferred
}

func runtimeParent(ctx context.Context) context.Context {
	if parent, ok := ctx.Value(runtimeParentKey{}).(context.Context); ok {
		return parent
	}
	return ctx
}

func withStartTurn(ctx context.Context, turn <-chan struct{}) context.Context {
	return context.WithValue(ctx, startTurnKey{}, turn)
}
func waitStartTurn(ctx context.Context) error {
	turn, _ := ctx.Value(startTurnKey{}).(<-chan struct{})
	if turn == nil {
		return ctx.Err()
	}
	select {
	case <-turn:
		return ctx.Err()
	case <-ctx.Done():
		return ctx.Err()
	}
}

type cellStageResult struct {
	outcome stageOutcome
	err     error
}

func maxConcurrentCellPreparations() int { return max(2, runtime.GOMAXPROCS(0)) }

type memberAdmissionKey struct{}
type memberAdmission struct {
	staged []stagedManagedExt
	turn   chan struct{}
	result chan cellStageResult
	cell   CellSpec
	report ReloadCellReport
	sent   bool
}

func admissionFor(ctx context.Context, name string) *memberAdmission {
	admissions, _ := ctx.Value(memberAdmissionKey{}).(map[string]*memberAdmission)
	return admissions[name]
}
func (a *memberAdmission) publish(staged []stagedManagedExt, err error) {
	a.sent = true
	a.staged = staged
	a.result <- cellStageResult{outcome: stageOutcome{staged: staged, report: a.report}, err: err}
}

// stageCellsInOrder prepares cells concurrently. The host admits Node factories individually, allowing a shared Node process to span native cells without starting a later factory early. Each admission commits before the next configured factory starts.
// pig additive (D20): per-member admission preserves one shared Node process and configured factory order.
func (h *Host) stageCellsInOrder(ctx context.Context, cells []CellSpec, commit func(CellSpec, stageOutcome, error)) {
	parent := runtimeParent(ctx)
	stageCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	if h.recoveryContext != nil {
		stop := context.AfterFunc(h.recoveryContext, cancel)
		defer stop()
	}
	ctx = context.WithValue(stageCtx, runtimeParentKey{}, parent)
	type unit struct {
		order     int
		admission *memberAdmission
	}
	var units []unit
	admissions := map[string]*memberAdmission{}
	cellUnits := make([][]*memberAdmission, len(cells))
	h.mu.Lock()
	order := make(map[string]int, len(h.loadOrder))
	maps.Copy(order, h.loadOrder)
	h.mu.Unlock()
	for i, cell := range cells {
		members := []CellSpec{cell}
		if cell.Strategy == CellStrategyPackedNode {
			members = nil
			for _, cfg := range cell.Extensions {
				member := cell
				member.Extensions = []ExtConfig{cfg}
				members = append(members, member)
			}
		}
		for j, member := range members {
			a := &memberAdmission{turn: make(chan struct{}), result: make(chan cellStageResult, 1), cell: member,
				report: ReloadCellReport{Key: member.Key, Strategy: member.Strategy, Language: member.Language, Extensions: cellExtNames(member), Reason: reasonFor(member)}}
			position, ok := order[member.Extensions[0].Name]
			if !ok {
				position = cell.Order + j
			}
			units = append(units, unit{order: position, admission: a})
			cellUnits[i] = append(cellUnits[i], a)
			if cell.Strategy == CellStrategyPackedNode {
				admissions[member.Extensions[0].Name] = a
			}
		}
	}
	slices.SortStableFunc(units, func(a, b unit) int { return cmp.Compare(a.order, b.order) })
	if len(units) == 0 {
		return
	}
	ctx = context.WithValue(ctx, memberAdmissionKey{}, admissions)
	slots := make(chan struct{}, maxConcurrentCellPreparations())
	var stages sync.WaitGroup
	// Node preparations do not hold a native preparation slot while awaiting an interleaved member.
	for i, cell := range cells {
		if cell.Strategy != CellStrategyPackedNode {
			continue
		}
		stages.Go(func() {
			outcome, err := h.stageCell(withStartTurn(ctx, cellUnits[i][0].turn), cell, nil)
			for _, a := range cellUnits[i] {
				if !a.sent {
					a.publish(outcome.staged, err)
				}
			}
		})
	}
	stages.Go(func() {
		for i, cell := range cells {
			if cell.Strategy == CellStrategyPackedNode {
				continue
			}
			slots <- struct{}{}
			stages.Go(func() {
				defer func() { <-slots }()
				outcome, err := h.stageCell(withStartTurn(ctx, cellUnits[i][0].turn), cell, nil)
				cellUnits[i][0].result <- cellStageResult{outcome: outcome, err: err}
			})
		}
	})
	for _, unit := range units {
		close(unit.admission.turn)
		result := <-unit.admission.result
		commit(unit.admission.cell, result.outcome, result.err)
	}
	stages.Wait()
	if nodeReadyDeferred(ctx) {
		return
	}
	// Pi binds action slots only after all factories have finished. Registration is an admission barrier, not permission for an early factory to call host actions while a later factory is still loading.
	for _, unit := range units {
		for _, item := range unit.admission.staged {
			if err := item.me.activateNode(); err != nil {
				h.stopFailedPackedMember(item.me)
				commit(unit.admission.cell, stageOutcome{}, &packedAcceptError{perMember: map[string]error{item.name: err}})
			}
		}
	}
}

func (me *managedExt) activateNode() error {
	if me.pendingReady == nil {
		return nil
	}
	ready := me.pendingReady
	me.pendingReady = nil
	if me.host != nil && me.host.uiBridge != nil {
		bridge := me.host.uiBridge
		// Notifications may have arrived during later factories. Activation must not overwrite that state with the earlier registration snapshot.
		ready.Ready.Models = bridge.ModelCatalog()
		me.entryCursorMu.Lock()
		ready.Ready.State = bridge.Snapshot(me.flagNames, 0, me.host.subscribedToSessionLog(me.config.Name))
		if ready.Ready.State.Session != nil {
			me.entryCursor = ready.Ready.State.Session.EntryCount
		}
		me.entryCursorMu.Unlock()
	}
	if err := me.connection().Send(ready); err != nil {
		return newLoadError(me.config.Name, "ready", "send_ready_failed", err)
	}
	return nil
}
