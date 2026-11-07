package sdk

import (
	"fmt"
	"os"
	"runtime/debug"
)

// callbackError runs call, a callback of the extension's own code that the SDK runs on a goroutine it started (a credential or settings store's lock, which the
// host drives in two calls), and returns its error. A panic in it is that call's error, as an error the callback returned is: nothing recovers a goroutine but
// its own code, and a panic that leaves it ends the process, which in a packed cell serves other extensions. The stack goes to stderr, as the SDK's other
// recovered callbacks report theirs.
func callbackError(what string, call func() error) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			fmt.Fprintf(os.Stderr, "extension: recovered from panic in %s: %v\n%s\n", what, recovered, debug.Stack())
			err = fmt.Errorf("%s panicked: %v", what, recovered)
		}
	}()
	return call()
}
