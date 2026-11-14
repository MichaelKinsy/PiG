package inproc

// ownerLoop is the UI context of a host that runs one input and render loop, as interactive mode does.
type ownerLoop interface {
	// OnOwnerLoop reports whether the caller is the goroutine that runs the loop.
	OnOwnerLoop() bool
}

// CallerOnOwnerLoop reports whether the caller is the goroutine of the bound UI's input and render loop. Upstream runs a handler on the stack of the call that fired its event; here the handler runs on another goroutine and may need that loop, so a caller on it must not wait for the handler. A mode without such a loop (print, RPC) binds no ownerLoop, and the answer is false.
func (r *Runner) CallerOnOwnerLoop() bool {
	r.uiMu.RLock()
	ui := r.uiContext
	r.uiMu.RUnlock()
	if prompts, ok := ui.(*uiPromptContext); ok {
		ui = prompts.UIContext
	}
	loop, ok := ui.(ownerLoop)
	return ok && loop.OnOwnerLoop()
}
