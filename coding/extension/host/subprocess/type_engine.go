package subprocess

import (
	"errors"
	"sync/atomic"
)

// typeEngine is the TypeBox type engine the host answers CallTypeEngine with, nil until a program registers one.
var typeEngine atomic.Pointer[func(request []byte) []byte]

// SetTypeEngine registers the engine that answers CallTypeEngine: serve takes the request and returns the reply (internal/typebox/engine.Serve).
// Linking coding/extension/pigsdk with at least one of the Go, Rust and Python SDKs, the three that call it, registers it, so a program that links the
// host without that package (the coding library, examples/headless) does not link the engine through it.
func SetTypeEngine(serve func(request []byte) []byte) {
	typeEngine.Store(&serve)
}

// errTypeEngineAbsent is the reply to a CallTypeEngine that arrives where no engine is registered.
var errTypeEngineAbsent = errors.New("type.engine: this build does not link the TypeBox type engine (it embeds none of the Go, Rust and Python SDKs, or the program links the extension host without them)")

func (h *Host) handleTypeEngine(call *CallPayload) (*CallResultPayload, error) {
	serve := typeEngine.Load()
	if serve == nil {
		return nil, errTypeEngineAbsent
	}
	return &CallResultPayload{Result: (*serve)(call.Args)}, nil
}
