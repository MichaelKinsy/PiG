package subprocess

import (
	"errors"
	"sync/atomic"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// tuiAltServer is what serves the `tui.alt` calls once EnableTuiAltScreens ran. The bridge reaches the renderer (tui.TuiAltScreen, its input
// components and the word-segmentation dictionaries behind them: 6.3 MB) only through this variable, so a program that never enables TuiAltScreens does not link
// them: the linker keeps a function only when something refers to it.
type tuiAltServer struct {
	handle func(b *UIBridge, owner *Conn, parent string, args json.RawMessage) (*CallResultPayload, error)
	// dispose forgets the screens of owner (ownerOnly) or of every connection of extName.
	dispose func(b *UIBridge, extName string, owner *Conn, ownerOnly bool)
}

var tuiAltEnabled atomic.Pointer[tuiAltServer]

// EnableTuiAltScreens lets an extension construct a TuiAltScreen (pi-tui's alternate-screen host). The `pig` command calls it where it creates the
// extension host, so every mode serves TuiAltScreens. An embedder that never draws one leaves it out and does not link the renderer; one that serves
// them to its own terminal calls it once before the first extension loads. It is safe to call more than once and from any goroutine.
func EnableTuiAltScreens() {
	tuiAltEnabled.Store(&tuiAltServer{
		handle: (*UIBridge).handleTuiAlt,
		dispose: func(b *UIBridge, extName string, owner *Conn, ownerOnly bool) {
			if ownerOnly {
				b.disposeTuiAlt(owner, false)
			} else {
				b.disposeTuiAltOf(extName)
			}
		},
	})
}

// callTuiAlt serves one `tui.alt` call; without EnableTuiAltScreens it fails with an error that names the call.
func (b *UIBridge) callTuiAlt(owner *Conn, parent string, args json.RawMessage) (*CallResultPayload, error) {
	server := tuiAltEnabled.Load()
	if server == nil {
		return nil, errors.New("tui.alt: TuiAltScreen is not enabled in this program: call subprocess.EnableTuiAltScreens before an extension constructs one")
	}
	return server.handle(b, owner, parent, args)
}

// clearTuiAlt forgets the screens of an extension that went away.
func (b *UIBridge) clearTuiAlt(extName string, owner *Conn, ownerOnly bool) {
	if server := tuiAltEnabled.Load(); server != nil {
		server.dispose(b, extName, owner, ownerOnly)
	}
}
