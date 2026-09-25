package agentharness

import (
	"errors"
	"fmt"

	"github.com/MichaelKinsy/PiG/agent/harness"
)

// callListener invokes an event listener, converting a panic into its error
// the way upstream catches a thrown value (non-Error values become
// Error(String(value))).
func callListener(listener EventListener, ctx harness.Context, event HarnessEvent) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = panicError(recovered)
		}
	}()
	return listener(ctx, event)
}

func panicError(recovered any) error {
	if err, ok := recovered.(error); ok {
		return err
	}
	return errors.New(fmt.Sprint(recovered))
}
