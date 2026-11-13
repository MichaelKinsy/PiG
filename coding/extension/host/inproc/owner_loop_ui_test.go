package inproc_test

import "github.com/MichaelKinsy/PiG/coding/extension"

// ownerLoopUI is a UI context whose host runs one input and render loop, as interactive mode's does.
type ownerLoopUI struct {
	extension.UIContext
}

func (ownerLoopUI) OnOwnerLoop() bool { return true }
