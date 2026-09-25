package subprocess

import (
	"context"
	"runtime"
	"sync"
)

// Cells prepare concurrently and start in configured cell order. Preparation
// (source discovery, build checks, cold builds) touches no extension code and
// runs in parallel. Starting a process runs extension factories, so a cell
// starts its process only after cells 0..i-1 have committed. Packed members
// still start within their one process; the runner's extension slice restores
// exact configured dispatch order.

type startTurnKey struct{}

// withStartTurn marks ctx as belonging to a cell whose process may start once
// turn is closed.
func withStartTurn(ctx context.Context, turn <-chan struct{}) context.Context {
	return context.WithValue(ctx, startTurnKey{}, turn)
}

// waitStartTurn blocks until the cell that owns ctx may start its process.
// Contexts outside startup staging have no turn.
func waitStartTurn(ctx context.Context) error {
	turn, _ := ctx.Value(startTurnKey{}).(<-chan struct{})
	if turn == nil {
		return nil
	}
	select {
	case <-turn:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type cellStageResult struct {
	outcome stageOutcome
	err     error
}

// maxConcurrentCellPreparations bounds concurrent cell preparation, including
// cold builds.
func maxConcurrentCellPreparations() int {
	return max(2, runtime.GOMAXPROCS(0))
}

// stageCellsInOrder prepares every cell concurrently and calls commit for each
// cell in configured cell order. A cell's process starts only after commit has
// returned for every earlier cell. Slots are taken in that order, so a cell
// never waits for a slot held by a later cell that is itself waiting for its
// turn.
func (h *Host) stageCellsInOrder(ctx context.Context, cells []CellSpec, commit func(CellSpec, stageOutcome, error)) {
	if len(cells) == 0 {
		return
	}
	turns := make([]chan struct{}, len(cells))
	results := make([]chan cellStageResult, len(cells))
	for i := range cells {
		turns[i] = make(chan struct{})
		results[i] = make(chan cellStageResult, 1)
	}
	close(turns[0])
	slots := make(chan struct{}, maxConcurrentCellPreparations())
	var stages sync.WaitGroup
	stages.Go(func() {
		for i, cell := range cells {
			slots <- struct{}{}
			stages.Go(func() {
				defer func() { <-slots }()
				outcome, err := h.stageCell(withStartTurn(ctx, turns[i]), cell, nil)
				results[i] <- cellStageResult{outcome: outcome, err: err}
			})
		}
	})
	for i, cell := range cells {
		result := <-results[i]
		commit(cell, result.outcome, result.err)
		if i+1 < len(turns) {
			close(turns[i+1])
		}
	}
	stages.Wait()
}
