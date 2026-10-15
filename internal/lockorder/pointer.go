package lockorder

import "unsafe"

// unsafePointer returns the instance address used as the identity of a lock; it is only compared, never dereferenced.
func unsafePointer[T any](p *T) unsafe.Pointer { return unsafe.Pointer(p) } //nolint:gosec // G103: the address is an identity key that is compared and never dereferenced
