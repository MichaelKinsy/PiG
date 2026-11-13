package sdk

import "testing"

type hostInvalidateComponent struct {
	invalidations int
	renders       int
}

func (c *hostInvalidateComponent) Render(int) []string {
	c.renders++
	return []string{"row"}
}

func (*hostInvalidateComponent) HandleInput(string) (RemoteComponentResult, error) {
	return RemoteComponentResult{}, nil
}

func (c *hostInvalidateComponent) Invalidate() { c.invalidations++ }

// The host's ui.custom.invalidate is one message of the component's serial queue. A component that closed while the message was
// queued is not invalidated and not rendered, and nothing is sent: the message is dropped without an error.
func TestHostInvalidateQueuedForAClosedComponentIsDropped(t *testing.T) {
	component := &hostInvalidateComponent{}
	overlay := newRemoteOverlay(component)
	overlay.closed = true
	// A nil connection: a send would panic, and handleMessage would report the panic through that same connection.
	overlay.handleMessage(nil, "custom-1", overlayMessage{invalidate: 3}, 80)
	if component.invalidations != 0 || component.renders != 0 {
		t.Fatalf("a closed component got %d invalidations and %d renders, want none", component.invalidations, component.renders)
	}
	overlay.active.Store(false)
	overlay.enqueue(nil, "custom-1", overlayMessage{invalidate: 1})
	if component.invalidations != 0 || component.renders != 0 {
		t.Fatalf("an unmounted component got %d invalidations and %d renders, want none", component.invalidations, component.renders)
	}
}
