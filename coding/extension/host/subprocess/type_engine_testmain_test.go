package subprocess

import "github.com/MichaelKinsy/PiG/internal/typebox/engine"

// The Go, Rust and Python SDK fixtures this package's tests host call the host's TypeBox type engine, and the host registers none of its own: coding/extension/pigsdk
// does, in every pig program that embeds an SDK, and this package's test binary links no pigsdk (the host must not link the engine, and a test of the host must not
// depend on the product assembly above it). So the binary registers the engine the way pigsdk does. The stock pig command links pigsdk, so an SDK extension it hosts
// always meets an engine (TestStockPigHostsTheTypeEngineForSDKExtensionsInEveryMode in coding/cli); a program that links the host without pigsdk (the coding library)
// answers an SDK's type.engine call with the error that names the missing engine.
func init() { SetTypeEngine(engine.Serve) }
