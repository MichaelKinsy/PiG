// Package contract ports apps/p2/plugins/status/contract.ts: the live status of every session (a leaf package other
// plugins import for the contracts).
package contract

import (
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/schema"
)

func must(contract *contracts.Contract, err error) *contracts.Contract {
	if err != nil {
		panic(err)
	}
	return contract
}

var c = p2runtime.Contract

// WorkStepSchema is what a busy conversation does now: the model answers (`thinking`), tools of its current round run
// (their names, in call order, at least one), it compacts its context, or other work runs (`working`: hooks, waiting for
// its own tasks).
var WorkStepSchema = schema.Union([]schema.Schema{
	schema.Obj(schema.Properties{schema.Prop("kind", p2runtime.Literal("thinking"))}),
	schema.Obj(schema.Properties{
		schema.Prop("kind", p2runtime.Literal("tools")),
		schema.Prop("names", schema.Arr(schema.Str(), schema.MinItems(1))),
	}),
	schema.Obj(schema.Properties{schema.Prop("kind", p2runtime.Literal("compacting"))}),
	schema.Obj(schema.Properties{schema.Prop("kind", p2runtime.Literal("working"))}),
})

// WorkSchema is a busy conversation's work: since when it is busy (the worker's clock, ms), its tool rounds so far and
// its step.
var WorkSchema = schema.Obj(schema.Properties{
	schema.Prop("since", schema.Num()),
	schema.Prop("rounds", schema.Num()),
	schema.Prop("step", WorkStepSchema),
})

// StatusSchema is one session's status: `running` (a worker process runs for it), an error, its spend, `tasks` (live
// Durable tasks, foreground and background), `busy` (conversations with foreground work, ascending) and `work` (what each
// busy conversation does, by conversation ID).
var StatusSchema = schema.Obj(schema.Properties{
	schema.Prop("running", schema.Bool()),
	schema.Opt("error", schema.Str()),
	schema.Opt("usage", schema.Unknown()),
	schema.Prop("tasks", schema.Num()),
	schema.Prop("busy", schema.Arr(schema.Num())),
	schema.Prop("work", p2runtime.Record(WorkSchema)),
})

// StatusesSchema is every session's status by session ID.
var StatusesSchema = p2runtime.Record(StatusSchema)

// ReportFields is what a worker reports of its work: its live tasks, its busy conversations and what they do, and its
// spend.
func ReportFields() schema.Properties {
	return schema.Properties{
		schema.Prop("tasks", schema.Num()),
		schema.Prop("busy", schema.Arr(schema.Num())),
		schema.Prop("work", p2runtime.Record(WorkSchema)),
		schema.Opt("usage", schema.Unknown()),
	}
}

// StatusStore is the live status of every session (`p2.status-store`; server, in memory). Reports are fenced by worker
// generation. 2: `work`.
var StatusStore = must(contracts.Local("p2.status-store", contracts.LocalDeclaration{
	Version: 2,
	Params:  contracts.Params{},
	Members: []contracts.LocalEntry{
		contracts.Mem("statuses", c(StatusesSchema)),
		contracts.Mem("report", contracts.NewMethod(c(schema.Obj(append(ReportFields(),
			schema.Prop("session", schema.Str()), schema.Prop("generation", schema.Num())))))),
	},
}))

// StatusSchemaV1 is `StatusSchema` as 6e11a00a0 released it: no `work` (open pages and running workers built then still
// bind it).
var StatusSchemaV1 = schema.Obj(schema.Properties{
	schema.Prop("running", schema.Bool()),
	schema.Opt("error", schema.Str()),
	schema.Opt("usage", schema.Unknown()),
	schema.Prop("tasks", schema.Num()),
	schema.Prop("busy", schema.Arr(schema.Num())),
})

var sessionStatusV1 = must(contracts.Remote("p2.session-status", contracts.RemoteDeclaration{
	Version:     1,
	Params:      contracts.Params{},
	HostAddress: p2runtime.ServerHost,
	Variants: []contracts.VariantEntry{
		contracts.Var("reader", contracts.VariantDeclaration{State: []contracts.Property{p2runtime.StateProp("statuses", p2runtime.Record(StatusSchemaV1))}}),
		contracts.Var("reporter", contracts.VariantDeclaration{Methods: []contracts.MethodEntry{
			contracts.Meth("report", contracts.NewMethod(c(schema.Obj(schema.Properties{
				schema.Prop("tasks", schema.Num()),
				schema.Prop("busy", schema.Arr(schema.Num())),
				schema.Opt("usage", schema.Unknown()),
			})))),
		}}),
	},
}))

// SessionStatus: readers (owner) see every session's status; a worker reports its own session's (its principal names
// it). 2: what each busy conversation does (`work`). The provider still serves 1 (its `accepts`).
var SessionStatus = must(contracts.Evolve(sessionStatusV1, func(e *contracts.RemoteEditor) *contracts.RemoteEditor {
	return e.State("reader", "statuses", c(StatusesSchema)).Args("report", c(schema.Obj(ReportFields())))
}))

// StatusReport is the session worker's status push (`p2.status-report`; worker, local): reports the worker's work now
// (its tasks, busy conversations and spend) to the server and returns once the server committed it. A method that
// starts or stops work (`Chat.send`, `Chat.stop`) awaits it, so the sidebar's working dot is current when the method
// answers.
var StatusReport = must(contracts.Local("p2.status-report", contracts.LocalDeclaration{
	Version: 1,
	Params:  contracts.Params{},
	Members: []contracts.LocalEntry{contracts.Mem("push", contracts.NewMethod())},
}))

// WorkerControl is the owner-only retry of a failed session worker (`p2.worker-control`); reading status never starts
// it. `restart` answers once the new worker runs, or fails with why it could not start (`worker_failed`).
var WorkerControl = must(contracts.Remote("p2.worker-control", contracts.RemoteDeclaration{
	Version:     1,
	Params:      contracts.Params{contracts.Prop("session", contracts.Str())},
	HostAddress: p2runtime.ServerHost,
	Variants: []contracts.VariantEntry{
		contracts.Var("owner", contracts.VariantDeclaration{Methods: []contracts.MethodEntry{contracts.Meth("restart", contracts.NewMethod())}}),
	},
}))

// SessionMetrics is what a page shows about its session (`p2.session-metrics`): how many viewers (distinct `viewer`
// params holding it: a page holds one for its active session, child views included) and the session's spend.
var SessionMetrics = must(contracts.Remote("p2.session-metrics", contracts.RemoteDeclaration{
	Version: 1,
	Params: contracts.Params{
		contracts.Prop("session", contracts.Str()),
		contracts.Prop("viewer", contracts.Str(contracts.Min(1), contracts.Max(80))),
	},
	HostAddress: p2runtime.ServerHost,
	Variants: []contracts.VariantEntry{
		contracts.Var("reader", contracts.VariantDeclaration{State: []contracts.Property{
			p2runtime.StateProp("clients", schema.Num()),
			p2runtime.StateProp("usage", schema.Union([]schema.Schema{schema.Unknown(), schema.Nul()})),
		}}),
	},
}))
