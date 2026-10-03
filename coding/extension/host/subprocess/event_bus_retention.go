package subprocess

// EventBusRetention counts the Host state the shared pi.events bus and the cross-process reference ledger retain. It is a product-neutral diagnostic of Stock mechanisms (D19): Pi's bus keeps one listener array per channel and nothing per emit (event-bus.ts:12-33), so a count that grows with the number of emits is a leak.
type EventBusRetention struct {
	// Channels, Listeners and Handlers describe the registry. Emissions counts routed emits whose realms have not settled, and Snapshots the dispatches in flight that still hold a listener.
	Channels, Listeners, Handlers, Emissions, Snapshots int
	// Realms, Leases, Holds and Expected describe the cross-process reference ledger.
	Realms, Leases, Holds, Expected int
}

// EventBusRetention reports the bus and reference-ledger state the Host retains now.
func (h *Host) EventBusRetention() EventBusRetention {
	bus := &h.eventBus
	bus.mu.Lock()
	retained := EventBusRetention{Channels: len(bus.listeners), Handlers: len(bus.handlers), Emissions: len(bus.emissions)}
	for _, listeners := range bus.listeners {
		retained.Listeners += len(listeners)
		for _, listener := range listeners {
			retained.Snapshots += listener.snapshots
		}
	}
	bus.mu.Unlock()
	stats := h.xref.stats()
	retained.Realms, retained.Leases, retained.Holds, retained.Expected = stats.Realms, stats.Leases, stats.Holds, stats.Expected
	return retained
}
