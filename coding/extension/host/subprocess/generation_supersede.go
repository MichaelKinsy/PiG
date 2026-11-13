package subprocess

// supersedeGenerations marks every running generation as one the reload replaces and tells its runtime that its runner is stale. Pi's reload invalidates the old runner right after session_shutdown, before the settings and the extensions load again (agent-session.ts:3825-3826), and from then a captured pi or ctx throws the stale message (runner.ts:729-741). The generations run on until the commit replaces them, with every handler that was in flight, so from here the host answers their calls with that message (runCall) and their runtimes throw it from the members they answer locally. Reload calls it once the config loaded: a config loader failure before that leaves every generation as it was.
func (h *Host) supersedeGenerations() {
	h.mu.Lock()
	members := make([]*managedExt, 0, len(h.exts))
	for _, me := range h.exts {
		members = append(members, me)
	}
	h.mu.Unlock()
	for _, me := range members {
		if me.superseded.CompareAndSwap(false, true) {
			invalidateGeneration(me)
		}
	}
}
