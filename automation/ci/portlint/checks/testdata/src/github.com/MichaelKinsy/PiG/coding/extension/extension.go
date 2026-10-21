// Package extension stands in for coding/extension: its interfaces are the extension and host interfaces the ordering checks know.
package extension

// Handler is an extension interface: calling it hands control to extension code.
type Handler interface {
	Handle(event string)
}
