package codingagent

import (
	"path/filepath"

	"github.com/MichaelKinsy/PiG/internal/viewcarrier"
	"github.com/MichaelKinsy/PiG/internal/viewendpoint"
)

// pig additive (D614): the wiring of the view endpoint to the interactive session. Nothing here runs unless the active
// Piglet selected the endpoint (InteractiveOptions.ViewEndpoint). The interactive loop only queues: the socket is
// created, served and removed on the endpoint's own goroutines.

// attachViewEndpoint serves the view registry of the bound build's extension bridge on the session's socket. The
// socket lives as long as the bridge is bound: a session replacement unbinds it, which closes every connection and
// removes the socket, and the next bridge is served under the same path. A reload keeps the bridge, so it keeps the
// connections.
func (m *InteractiveMode) attachViewEndpoint(bridge SubprocessUIBridge) {
	if !m.opts.ViewEndpoint {
		return
	}
	host, ok := bridge.(viewcarrier.Host)
	if !ok {
		return
	}
	endpoint := m.viewEndpoint.Load()
	if endpoint == nil {
		endpoint = viewendpoint.New(viewendpoint.Options{
			Dir: filepath.Join(m.opts.AgentDir, "view-endpoint"),
			Notify: func(message string) {
				// The endpoint's worker never waits for the loop: a notice the loop cannot take is dropped.
				m.postUITask(func() { (&ExtUIContext{m: m}).Notify(message, "warning") })
			},
		})
		m.viewEndpoint.Store(endpoint)
	}
	endpoint.Bind(host)
}

// detachViewEndpoint closes the connections of the outgoing build's registry and removes the socket.
func (m *InteractiveMode) detachViewEndpoint() {
	if endpoint := m.viewEndpoint.Load(); endpoint != nil {
		endpoint.Unbind()
	}
}

// closeViewEndpoint ends the endpoint for good and returns once its socket is removed. It waits for no client.
func (m *InteractiveMode) closeViewEndpoint() {
	if endpoint := m.viewEndpoint.Load(); endpoint != nil {
		endpoint.Close()
	}
}
