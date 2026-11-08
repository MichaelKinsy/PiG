package host

import (
	"runtime"
	"testing"
	"weak"

	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
	"github.com/MichaelKinsy/PiG/chord/next/link"
)

// link.test.ts › a closed memory link: keeps no listener set after its close: accepting it keeps no host. (The test
// lives in the link package in Pi; Go's link tests cannot import the host.)
func TestAClosedMemoryLinkKeepsNoHost(t *testing.T) {
	a, b := link.MemoryLinkPair()
	a.Close()
	a.Flushed()
	ref := func() weak.Pointer[chordHost] {
		host := newHost(t, HostOptions{})
		mustLoad(t)(host.Load(nil, background))
		must(t, host.Accept(a, LinkOptions{Principal: ownerPrincipal()}, background))
		return weak.Make(host.(*chordHost))
	}()
	nexttest.ExpectCollected(t, ref)
	runtime.KeepAlive(a)
	runtime.KeepAlive(b)
}
