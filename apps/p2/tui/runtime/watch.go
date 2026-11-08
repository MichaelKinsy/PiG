package runtime

import (
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	chordtui "github.com/MichaelKinsy/PiG/chord/next/tui"
)

// Watch is p2's `TuiScope.watch`: a reference to typ held by scope (let go after its type's linger), the runtime redrawn on each state change. Once the
// reference is live bind runs on the owner loop, once; a reference that fails broken (a refusal asking again cannot change) or ends is reported
// as a status line. It returns the error of invalid params.
func (rt *TuiRuntime) Watch(scope *chordtui.TuiScope, typ *contracts.Contract, params any, bind func(ref *host.Reference)) (*host.Reference, error) {
	var stop func()
	ref, err := scope.HoldWith(typ, params, func(ref *host.Reference) {
		if stop != nil {
			stop()
		}
		p2runtime.UILinger.Release(ref, typ.Name)
	})
	if err != nil {
		return nil, err
	}
	bound := false
	stop = p2runtime.Follow(ref.State(), func(state *host.RefState) {
		rt.Post(func() {
			rt.Changed()
			switch {
			case state.Kind == host.RefStateLive && !bound:
				bound = true
				if bind != nil {
					bind(ref)
				}
			case state.Kind == host.RefStateFailed && state.Cause.Kind == data.FailureBroken:
				rt.Fail(state.Cause.Error)
			case state.Kind == host.RefStateEnded:
				rt.Fail(state.Error)
			}
		})
	})
	return ref, nil
}
