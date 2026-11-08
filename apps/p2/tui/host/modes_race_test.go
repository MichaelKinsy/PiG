package host_test

import (
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/apps/p2/tui/host"
)

// The runtime's output is filtered on the owner loop while the trusted UI, closing on the visitor's input goroutine, restores the modes it covered
// (Client.ui.OnClose), and Close cleans them up: the tracked state is read and written from those goroutines. Run under -race.
func TestTerminalModesTrackTheRuntimesOutputWhileTheTrustedUIRestoresAndCloseCleansUp(t *testing.T) {
	modes := host.NewTerminalModes(host.MouseOn)
	frames := []string{"\x1b[?1000h\x1b[?1002h", "\x1b[?1000l\x1b[?1049h", "\x1b[?1006h\x1b[?1004h", "\x1b[?1002l\x1b[?1049l"}
	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		for i := range 2000 {
			if _, err := modes.Filter(frames[i%len(frames)]); err != nil {
				t.Error(err)
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		for range 2000 {
			_ = modes.Restore()
			_ = modes.AltScreen()
			_ = modes.ResumeSequence()
		}
	}()
	go func() {
		defer wg.Done()
		for range 500 {
			_ = modes.SuspendSequence()
			_ = modes.Cleanup()
		}
	}()
	wg.Wait()
}
