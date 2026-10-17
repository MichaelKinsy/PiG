//go:build !tinygo

package codemode

import (
	"sync"
	"sync/atomic"
	"time"
)

// terminationGrace is how long the interrupt flag has to stop a VM before the instance is halted. The flag stops
// any script that reaches QuickJS's interrupt check; the halt traps the instance at its next loop iteration, which
// stops a call that does not.
const terminationGrace = 500 * time.Millisecond

// haltGuard escalates a stop to halting the instance when the script does not reach an interrupt check. It is the
// only timer of a launcher.
type haltGuard struct {
	vmExited atomic.Bool
	// machine is the running VM, and halting records that the grace period ran out: whichever of the two is set
	// second halts the VM.
	machine atomic.Pointer[vm]
	halting atomic.Bool
	mu      sync.Mutex
	timer   *time.Timer
}

// created records the VM as soon as it exists.
func (g *haltGuard) created(machine *vm) {
	g.machine.Store(machine)
	if g.halting.Load() {
		machine.halt()
	}
}

// arm starts the grace period.
func (g *haltGuard) arm() {
	g.mu.Lock()
	g.timer = time.AfterFunc(terminationGrace, func() {
		if !g.vmExited.Load() {
			hardStops.Add(1)
			g.halting.Store(true)
			if machine := g.machine.Load(); machine != nil {
				machine.halt()
			}
		}
	})
	g.mu.Unlock()
}

// exited records that the VM is closed and ends the grace period. It drops the VM: a stopped timer stays in its P's
// timer heap until that P next runs, and its closure would keep the closed instance and its linear memory reachable.
func (g *haltGuard) exited() {
	g.vmExited.Store(true)
	g.machine.Store(nil)
	g.mu.Lock()
	if g.timer != nil {
		g.timer.Stop()
	}
	g.mu.Unlock()
}
