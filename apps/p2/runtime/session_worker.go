package runtime

import (
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/schema"
)

// Ports apps/p2/src/runtime/session-worker.ts: what the server tells and asks a session worker: its spawn parameters
// and the lifecycle calls a relocation makes. The worker serves WorkerBoundary (worker.ts); the sessions plugin on the
// server calls it.

// LocationSchema is where a conversation works (`LocationSchema` of location.ts): its cwd and its environment, a name
// or null for this machine.
var LocationSchema = schema.Obj(schema.Properties{
	schema.Prop("cwd", schema.Str()),
	schema.Prop("env", schema.Union([]schema.Schema{schema.Str(), schema.Nul()})),
})

// SessionInit is what the server tells a worker it spawns: Session from the sessions plugin, more keys from other
// plugins.
type SessionInit struct {
	ID  string
	Cwd string
	// Env is the session's environment, nil for this machine.
	Env           *string
	Model         durable.ModelRef
	ThinkingLevel ai.ModelThinkingLevel
	// Resume is false for an archived session started only to rebuild its search index: Durable work does not resume.
	// Nil is upstream's absent field: resume.
	Resume *bool
}

// WorkerBoundary is the worker's boundary contract (`p2.worker-boundary`, version 2), served by the session's worker
// to the server. `prepare` fences admissions if nothing runs (`accepted`); with `to` (a relocation), the
// `RelocationChecks` run under the fence too: a `refusal` lifts it again, before anything (the catalog included)
// moved. `cancel` lifts the fence. `relocate` runs after an accepted `prepare` and the catalog update: the worker
// follows the session to the location in place (`Harness.relocate`), then admits again. A failure leaves it fenced; the
// server restarts it.
var WorkerBoundary = mustContract(contracts.Remote("p2.worker-boundary", contracts.RemoteDeclaration{
	Version:     2,
	Params:      contracts.Params{contracts.Prop("session", contracts.Str())},
	HostAddress: SessionHost,
	Variants: []contracts.VariantEntry{
		contracts.Var("server", contracts.VariantDeclaration{
			Methods: []contracts.MethodEntry{
				contracts.Meth("prepare", contracts.NewMethod(
					Contract(schema.Union([]schema.Schema{schema.Nul(), schema.Obj(schema.Properties{schema.Prop("to", LocationSchema)})})),
					Contract(schema.Obj(schema.Properties{
						schema.Prop("accepted", schema.Bool()),
						schema.Prop("tasks", schema.Num()),
						schema.Prop("busy", schema.Arr(schema.Num())),
						schema.Opt("refusal", schema.Str()),
					})),
				)),
				contracts.Meth("cancel", contracts.NewMethod()),
				contracts.Meth("relocate", contracts.NewMethod(Contract(LocationSchema))),
			},
		}),
	},
}))
