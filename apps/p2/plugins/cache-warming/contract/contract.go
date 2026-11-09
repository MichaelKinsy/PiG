// Package contract is the cache warming plugin's host-internal contract (apps/p2/plugins/cache-warming/worker.ts: `p2.cache-warming-runtime`).
package contract

import (
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
)

// ExtensionName is the Durable extension whose `afterResponse` hook starts the warming.
const ExtensionName = "cache-warming"

// CacheWarmingRuntime is the worker's cache warming (host-internal): one per worker, started with it; a reload or a stop
// closes its timers and in-flight refreshes, and the next real request starts warming again.
var CacheWarmingRuntime = func() *contracts.Contract {
	contract, err := contracts.Local("p2.cache-warming-runtime", contracts.LocalDeclaration{Version: 1, Params: contracts.Params{}, Members: []contracts.LocalEntry{}})
	if err != nil {
		panic(err)
	}
	return contract
}()
