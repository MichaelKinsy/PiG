package runtime

// SlotsReady is whether the runtime has read the slot and command registries once: its surfaces exist and its commands are dispatched (Pi's `await
// runtime.start()`). Call it on the owner loop.
func (rt *TuiRuntime) SlotsReady() bool { return rt.slotTable != nil && rt.commandTable != nil }
