// Package earlydispatch is the Go SDK extension of the pre-ready dispatch row (early_dispatch_conformance_test.go): one listener that echoes the payload it was
// dispatched, so a host that scripts the wire can tell which request each handler served.
package earlydispatch

import (
	"fmt"
	"os"
	"path/filepath"

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
	// PureListenerEnv names the environment variable that makes every SDK's listener make no host call: it writes the payload's tag to the file named by the payload's seq in the directory the variable holds, so the host sees what the listener produced without the listener depending on a host call.
	PureListenerEnv = "PIG_TEST_PURE_LISTENER"
)

// Extension subscribes the echo listener while the factory runs, which writes a file instead when PureListenerEnv is set, and emits once on FactoryChannel when FactoryEmitEnv is set.
func Extension() *sdk.Extension {
	e := sdk.New("early-dispatch")
	listener := func(ctx sdk.Context, data any) error { return ctx.Events().Emit(EchoChannel, data) }
	if dir := os.Getenv(PureListenerEnv); dir != "" {
		listener = func(_ sdk.Context, data any) error {
			payload, _ := data.(map[string]any)
			return os.WriteFile(filepath.Join(dir, fmt.Sprint(payload["seq"])), []byte(fmt.Sprint(payload["tag"])), 0o600)
		}
	}
	if _, err := e.Events().On(Channel, listener); err != nil {
		panic(err)
	}
	if os.Getenv(FactoryEmitEnv) != "" {
		if err := e.Events().Emit(FactoryChannel, 1); err != nil {
			panic(err)
		}
	}
	return e
}
