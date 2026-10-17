//go:build tinygo

package codemode

// haltGuard on the Worker host: a stop has nothing to escalate to, because a synchronous QuickJS call cannot be preempted and the
// host stops a script it cannot interrupt by dropping the instance, so halting the host's instance does nothing.
type haltGuard struct{ machine *vm }

func (g *haltGuard) created(machine *vm) { g.machine = machine }

func (g *haltGuard) arm() {
	if g.machine != nil {
		g.machine.halt()
	}
}

func (g *haltGuard) exited() {}
