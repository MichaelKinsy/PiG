package sessiontesting

import "github.com/MichaelKinsy/PiG/agent/harness/session"

// StorageDecorator is a test-only forwarding base for decorators that alter
// one part of Storage behavior; embedded methods forward to the delegate.
type StorageDecorator struct {
	session.Storage
}

// NewStorageDecorator wraps delegate.
func NewStorageDecorator(delegate session.Storage) StorageDecorator {
	return StorageDecorator{Storage: delegate}
}
