// Package earlydispatch is the Go SDK extension of the pre-ready dispatch row (early_dispatch_conformance_test.go): one listener that echoes the payload it was
// dispatched, so a host that scripts the wire can tell which request each handler served.
package earlydispatch

import (
	"os"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

const (
	// Channel is the channel the listener subscribes to while the factory runs.
	Channel = "early:ch"
	// EchoChannel is the channel the listener emits each payload on, from the request that dispatched it.
	EchoChannel = "early:echo"
	// FactoryChannel is the channel the factory emits on, from its own host call, when FactoryEmitEnv is set.
	FactoryChannel = "early:factory"
	// FactoryEmitEnv names the environment variable that makes every SDK's factory emit on FactoryChannel before it runs.
	FactoryEmitEnv = "PIG_TEST_FACTORY_EMIT"
)

// Extension subscribes the echo listener while the factory runs, and emits once on FactoryChannel when FactoryEmitEnv is set.
func Extension() *sdk.Extension {
	e := sdk.New("early-dispatch")
	if _, err := e.Events().On(Channel, func(ctx sdk.Context, data any) error {
		return ctx.Events().Emit(EchoChannel, data)
	}); err != nil {
		panic(err)
	}
	if os.Getenv(FactoryEmitEnv) != "" {
		if err := e.Events().Emit(FactoryChannel, 1); err != nil {
			panic(err)
		}
	}
	return e
}
