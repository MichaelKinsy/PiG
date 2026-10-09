package extensiontest

import "github.com/MichaelKinsy/PiG/coding/extension"

// NewMemBus returns a fresh in-process event bus, [extension.CreateEventBus].
func NewMemBus() extension.EventBusController { return extension.CreateEventBus() }
