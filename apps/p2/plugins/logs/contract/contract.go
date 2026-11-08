// Package contract ports apps/p2/plugins/logs/contract.ts: the owner's process logs.
package contract

import (
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/schema"
)

// LogLineSchema is a log line.
var LogLineSchema = schema.Obj(schema.Properties{
	schema.Prop("id", schema.Num()),
	schema.Prop("time", schema.Num()),
	schema.Prop("level", schema.Union([]schema.Schema{schema.Lit("stdout"), schema.Lit("stderr"), schema.Lit("event"), schema.Lit("error")})),
	schema.Prop("text", schema.Str()),
})

// LogSourcesSchema lists the sources with their line counts and last update.
var LogSourcesSchema = schema.Arr(schema.Obj(schema.Properties{
	schema.Prop("id", schema.Str()), schema.Prop("lines", schema.Num()), schema.Prop("updatedAt", schema.Num()),
}))

// Logs is the owner-only diagnostics (`p2.logs`, version 1). Changing source acquires a new view, not a new worker.
var Logs = func() *contracts.Contract {
	contract, err := contracts.Remote("p2.logs", contracts.RemoteDeclaration{
		Version:     1,
		Params:      contracts.Params{contracts.Prop("source", p2runtime.Contract(schema.Str(schema.MaxLength(128))))},
		HostAddress: p2runtime.ServerHost,
		Variants: []contracts.VariantEntry{
			contracts.Var("owner", contracts.VariantDeclaration{State: []contracts.Property{
				p2runtime.StateProp("sources", LogSourcesSchema),
				p2runtime.StateProp("lines", schema.Arr(LogLineSchema)),
			}}),
		},
	})
	if err != nil {
		panic(err)
	}
	return contract
}()
