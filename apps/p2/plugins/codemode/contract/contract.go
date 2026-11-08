// Package contract ports apps/p2/plugins/codemode/contract.ts: the worker's code mode sandbox.
package contract

import (
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
)

// MemoryLimitBytes is the heap cap of each script's QuickJS VM. Keep it constant across restarts: the limit is part of the
// VM's fingerprint, which Durable records when a script starts, and a script interrupted by a restart resumes only on a VM
// with the same fingerprint. A changed limit makes every interrupted script end with "the sandbox changed" instead of
// resuming.
const MemoryLimitBytes = 256 * 1024 * 1024

// CodemodeRuntime is the worker's code mode sandbox (host-internal): one per worker, closed when the plugin is removed or
// the worker stops; a reload opens a new one and drains the old one in the background (its scripts run on). Each running
// script holds one worker thread; closing aborts them.
var CodemodeRuntime = func() *contracts.Contract {
	contract, err := contracts.Local("p2.codemode-runtime", contracts.LocalDeclaration{Version: 2, Params: contracts.Params{}, Members: []contracts.LocalEntry{}})
	if err != nil {
		panic(err)
	}
	return contract
}()
