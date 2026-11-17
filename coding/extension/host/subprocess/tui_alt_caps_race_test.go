package subprocess

import (
	"net"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
	"github.com/MichaelKinsy/PiG/tui"
)

// The extension names the members of a component with its key (tuiAltCaps), and every call that names the component names them again: component() stores
// the newest. The screen reads them from the goroutine of a frame, of an input delivery, of a mouse event and of a focus change, and the extension's calls run
// on goroutines of their own, so a store that takes the bridge's lock while the reads take none is a data race (go test -race).

// A call that names a component again while the screen reads its members must not race them. Each member the screen reads is driven against the call that
// stores them, with no lock or channel between the two goroutines, so the race detector sees any pair that is not ordered.
func TestARenamedComponentsMembersAreNotRacedByTheScreensReads(t *testing.T) {
	reads := []struct {
		name string
		read func(c *tuiAltComponent)
	}{
		{"HandleInput", func(c *tuiAltComponent) { c.HandleInput("x") }},
		{"WantsKeyRelease", func(c *tuiAltComponent) { c.WantsKeyRelease() }},
		{"HandleMouse", func(c *tuiAltComponent) { c.HandleMouse(tui.TuiMouseEvent{}) }},
		{"SetFocused", func(c *tuiAltComponent) { c.SetFocused(true) }},
		{"CapturesViewportInput", func(c *tuiAltComponent) { c.CapturesViewportInput() }},
	}
	for _, tc := range reads {
		t.Run(tc.name, func(t *testing.T) {
			s := &tuiAltScreenState{components: map[string]*tuiAltComponent{}, byComp: map[tui.Component]string{}}
			key := "c"
			c, err := s.component(&key, &tuiAltCaps{})
			if err != nil {
				t.Fatal(err)
			}
			start := make(chan struct{})
			var wg sync.WaitGroup
			wg.Go(func() {
				<-start
				for range 100 {
					if _, err := s.component(&key, &tuiAltCaps{}); err != nil {
						t.Error(err)
						return
					}
				}
			})
			wg.Go(func() {
				<-start
				for range 100 {
					tc.read(c)
				}
			})
			close(start)
			wg.Wait()
		})
	}
}

// The same pair across the call boundary: the extension re-sends the caps of its focused component with setFocus on one goroutine while its terminal
// delivers input on another, as the Go SDK's request goroutines do. The input reaches the component's HandleInput, which reads the caps.
func TestSetFocusAndInputOfAnExtensionScreenDoNotRaceTheComponentsMembers(t *testing.T) {
	hostEnd, peerEnd := net.Pipe()
	conn := NewConn("alt-caps", hostEnd)
	conn.Start(t.Context())
	t.Cleanup(func() { _ = conn.Close("the test ends") })
	peer := newTerminalWritePeer(peerEnd)
	go peer.serve()
	b := &UIBridge{}
	call := func(body string) error {
		_, err := b.handleTuiAlt(conn, "", json.RawMessage(body))
		return err
	}
	for _, body := range []string{
		`{"op":"create","id":"s","terminal":{"columns":40,"rows":6}}`,
		`{"op":"addChild","id":"s","key":"c","caps":{"input":true,"focusable":true}}`,
		`{"op":"setFocus","id":"s","key":"c","caps":{"input":true,"focusable":true}}`,
		`{"op":"start","id":"s"}`,
	} {
		if err := call(body); err != nil {
			t.Fatalf("%s: %v", body, err)
		}
	}
	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() {
		<-start
		for range 50 {
			if err := call(`{"op":"setFocus","id":"s","key":"c","caps":{"input":true,"focusable":true}}`); err != nil {
				t.Error(err)
				return
			}
		}
	})
	wg.Go(func() {
		<-start
		for range 50 {
			if err := call(`{"op":"input","id":"s","data":"x"}`); err != nil {
				t.Error(err)
				return
			}
		}
	})
	close(start)
	wg.Wait()
	if err := call(`{"op":"stop","id":"s"}`); err != nil {
		t.Fatal(err)
	}
}
