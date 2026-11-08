package runtime

import (
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/schema"
)

// Ports apps/p2/src/runtime/notices.ts: owner notices (a browser-safe contract), the errors and results the server
// reports to the owner (refused reloads, failed workers), served by the logs plugin and shown by every owner page and
// TUI.

// NoticesSchema is the list of notices.
var NoticesSchema = schema.Arr(schema.Obj(schema.Properties{
	schema.Prop("id", schema.Num()),
	schema.Prop("time", schema.Num()),
	schema.Prop("source", schema.Str()),
	schema.Prop("message", schema.Str()),
}))

// LogNotices is the owner's notices (`p2.log-notices`, version 1); the `dismiss` method removes one by id.
var LogNotices = mustContract(contracts.Remote("p2.log-notices", contracts.RemoteDeclaration{
	Version:     1,
	Params:      contracts.Params{},
	HostAddress: ServerHost,
	Variants: []contracts.VariantEntry{
		contracts.Var("owner", contracts.VariantDeclaration{
			State: []contracts.Property{StateProp("notices", NoticesSchema)},
			Methods: []contracts.MethodEntry{contracts.Meth("dismiss", contracts.NewMethod(
				Contract(schema.Obj(schema.Properties{schema.Prop("id", schema.Num())})),
			))},
		}),
	},
}))
