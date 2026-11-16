package subprocess

import "iter"

// Pi keeps what an extension registered with the model runtime when a reload drops the extension: ModelRuntime holds the provider configuration, and with it
// the extension's own streamSimple closure, until the process ends or an extension unregisters or replaces the name (agent-session.ts:3815-3855 reload
// never unregisters; model-runtime.ts:965-993). A removed extension that owns a provider therefore keeps answering. Its process is the closure here, so the
// removed generation lingers: everything that made it an extension of the session goes (tools, commands, handlers, widgets, MCP servers, and every event,
// state push and tool call, because the runner and the host no longer list it), and its process, its connection and its registrations stay until the Host
// shuts down or another extension takes its last name over.

// ownsRegistrationsLocked reports whether the model runtime still holds a provider, a bridged OAuth login or a virtual model that me registered last. The caller holds h.mu.
func (h *Host) ownsRegistrationsLocked(me *managedExt) bool {
	if len(me.providerNames) != 0 || len(me.oauthProviderNames) != 0 {
		return true
	}
	for _, ref := range me.virtualModels {
		if h.virtualModelOwners[ref] == me {
			return true
		}
	}
	return false
}

// lingerRemoved keeps a generation the reload removed alive when it still owns registrations, and reports whether it did. A caller that gets false stops the generation.
func (h *Host) lingerRemoved(old *managedExt) bool {
	h.mu.Lock()
	if h.shuttingDown.Load() || !h.ownsRegistrationsLocked(old) {
		h.mu.Unlock()
		return false
	}
	if h.lingering == nil {
		h.lingering = make(map[*managedExt]struct{})
	}
	if old.packedProcess != nil {
		if !old.packedProcess.holdForLinger() {
			h.mu.Unlock()
			return false
		}
		old.lingerHold.Store(true)
	}
	h.lingering[old] = struct{}{}
	conn := old.connection()
	h.mu.Unlock()
	// The reload invalidated the old runner, so the generation's captured pi and ctx throw Pi's stale message; its bus listeners end with the notice.
	invalidateGeneration(old)
	if h.uiBridge != nil {
		h.uiBridge.RetireExtensionConn(old.config.Name, conn)
	}
	h.releaseMcpServers(old, nil)
	return true
}

// releaseLingerHold gives back the reference a lingering member held on its packed cell's process.
func (me *managedExt) releaseLingerHold() {
	if me.lingerHold.CompareAndSwap(true, false) {
		me.packedProcess.releaseHold()
	}
}

// endLingerIfEmptyLocked schedules the stop of a lingering generation that no longer owns a registration: another extension replaced or unregistered its last
// name. The stop checks again when it runs, so a claim that is given back keeps the generation. The caller holds h.mu.
func (h *Host) endLingerIfEmptyLocked(me *managedExt) {
	if _, ok := h.lingering[me]; !ok || h.ownsRegistrationsLocked(me) {
		return
	}
	h.goDrainTask(func() {
		h.mu.Lock()
		if _, ok := h.lingering[me]; !ok || h.ownsRegistrationsLocked(me) {
			h.mu.Unlock()
			return
		}
		delete(h.lingering, me)
		h.mu.Unlock()
		h.stopManaged(me, "provider replaced")
		me.reap()
	})
}

// stopLingering ends every lingering generation: the Host is shutting down.
func (h *Host) stopLingering() {
	h.mu.Lock()
	lingering := make([]*managedExt, 0, len(h.lingering))
	for me := range h.lingering {
		lingering = append(lingering, me)
	}
	clear(h.lingering)
	h.mu.Unlock()
	for _, me := range lingering {
		h.stopManaged(me, "shutdown")
	}
	for _, me := range lingering {
		me.reap()
	}
}

// generationsLocked lists the generations that can own a provider: the extensions the Host lists and the removed ones that linger. The caller holds h.mu.
func (h *Host) generationsLocked() iter.Seq[*managedExt] {
	return func(yield func(*managedExt) bool) {
		for _, me := range h.exts {
			if !yield(me) {
				return
			}
		}
		for me := range h.lingering {
			if !yield(me) {
				return
			}
		}
	}
}
