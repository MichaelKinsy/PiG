package registries

import (
	"errors"
	"fmt"
)

// Ports packages/chord/src/next/registries/internal.ts (what Chord's layers import: the record, the contribution and
// the value source). The barrel itself is designed out: Go imports the package.

// try runs work, returning what a `throw` would: its error, or a panic recovered as one. A registry runs the
// provider's `compose` and adapters this way: they are plugin code, and a failure of theirs is reported, not fatal.
func try(work func() (any, error)) (value any, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			value, err = nil, asError(recovered)
		}
	}()
	return work()
}

// asError is a recovered panic value as the error a JavaScript `throw` of it would be.
func asError(recovered any) error {
	if err, ok := recovered.(error); ok {
		return err
	}
	return errors.New(fmt.Sprint(recovered))
}
