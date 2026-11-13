package subprocess

// supersedeGenerations is the point of a reload at which Pi has invalidated the old runner: right after session_shutdown, before the settings and the extensions load again (agent-session.ts:3825-3826), and from then a captured pi or ctx throws the stale message (runner.ts:729-741). The generations run on until the commit replaces them, with every handler that was in flight, so from here the host answers their calls with that message (runCall) and their runtimes, told with the invalidate notification, throw it from the members they answer locally. Reload calls it once the config loaded, and both of its callers have awaited the session_shutdown handlers by then (coding.Session.Reload, the interactive /reload): a call a session_shutdown handler makes is served, and a config loader failure leaves every generation as it was.
//
// The returned function ends it. Reload defers it, so the generations are superseded for the length of that reload only: one that a reload leaves registered is in service again when the reload returns, and a later crash recovery finds nothing marked.
func (h *Host) supersedeGenerations() (end func()) {
	h.mu.Lock()
	members := make(map[*managedExt]struct{}, len(h.exts))
	for _, me := range h.exts {
		members[me] = struct{}{}
	}
	h.superseded.Store(&members)
	h.mu.Unlock()
	for me := range members {
		invalidateGeneration(me)
	}
	return func() { h.superseded.Store(nil) }
}

// supersedes reports whether a running reload is replacing me and Pi has invalidated its runner (supersedeGenerations).
func (h *Host) supersedes(me *managedExt) bool {
	members := h.superseded.Load()
	if members == nil {
		return false
	}
	_, ok := (*members)[me]
	return ok
}

// releasesBusListeners reports whether method is one of the calls a runtime makes to release its bus listeners when the invalidate notification arrives: events.off, after its lazy xref.hello. Pi's runtime unsubscribes them in invalidate, which never throws (loader.ts:200-207), so the host applies them for an invalidated generation instead of answering with the stale message, which the runtime would take for a failure of its own and exit on.
func releasesBusListeners(method string) bool {
	return method == callEventsOff || method == callXrefHello
}
