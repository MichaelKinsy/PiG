// Package contract ports apps/p2/plugins/powershell/contract.ts.
package contract

import (
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/schema"
)

// PowerShell is which of this worker's conversations are offered PowerShell (host-internal), by conversation ID: those
// working on Windows (their location's machine) with the shell tool (`bash`) enabled.
var PowerShell = func() *contracts.Contract {
	contract, err := contracts.Local("p2.powershell", contracts.LocalDeclaration{
		// 2: per conversation location.
		Version: 2,
		Params:  contracts.Params{},
		Members: []contracts.LocalEntry{contracts.Mem("offered", p2runtime.Contract(p2runtime.Record(schema.Bool())))},
	})
	if err != nil {
		panic(err)
	}
	return contract
}()

// ExtensionName is the extension offering Durable's `powershell` tool.
const ExtensionName = "p2-powershell"

// ToolName is the tool's name.
const ToolName = "powershell"
