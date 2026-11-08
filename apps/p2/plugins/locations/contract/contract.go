// Package contract ports apps/p2/plugins/locations/contract.ts and contract.worker.ts: the location tools' server side
// (`AgentLocations`, worker principals only) and the Durable document that keeps the main agent's move.
package contract

import (
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/schema"
)

var (
	str  = schema.Str
	obj  = func(properties schema.Properties) schema.Schema { return schema.Obj(properties) }
	prop = schema.Prop
	opt  = schema.Opt
	c    = p2runtime.Contract
)

func union(branches ...schema.Schema) schema.Schema { return schema.Union(branches) }

// EnvInfoSchema is one environment as an agent sees it: never keys or identity files. `gondolin` is a sandbox, `host`
// naming the SSH environment it runs on; conversations working there cannot leave it. `tested` is the owner's last test of
// this configuration (Envs), if any.
var EnvInfoSchema = obj(schema.Properties{
	prop("name", str()),
	prop("type", union(p2runtime.Literal("ssh"), p2runtime.Literal("gondolin"))),
	prop("host", str()),
	opt("user", str()),
	opt("port", schema.Num()),
	opt("tested", union(
		obj(schema.Properties{prop("ok", p2runtime.Literal(true)), prop("platform", str()), prop("arch", str())}),
		obj(schema.Properties{prop("ok", p2runtime.Literal(false)), prop("error", str())}),
	)),
})

// owner is the worker's attestation that the owner started the calling turn; only such turns use the location tools.
func owner() schema.Schema { return p2runtime.Literal("owner") }

func envOrNull() schema.Schema { return union(str(), schema.Nul()) }

func mustContract(contract *contracts.Contract, err error) *contracts.Contract {
	if err != nil {
		panic(err)
	}
	return contract
}

// AgentLocations is the location tools' server side (`p2.agent-locations`): the environments an agent may move to, their
// directories, and moving the main agent (its session) once its turn has ended.
var AgentLocations = mustContract(contracts.Remote("p2.agent-locations", contracts.RemoteDeclaration{
	Version:     1,
	Params:      contracts.Params{},
	HostAddress: p2runtime.ServerHost,
	Variants: []contracts.VariantEntry{contracts.Var("worker", contracts.VariantDeclaration{
		Methods: []contracts.MethodEntry{
			// The configured SSH environments and sandboxes (this device is always available besides them).
			contracts.Meth("envs", contracts.NewMethod(
				c(obj(schema.Properties{prop("origin", owner())})),
				c(obj(schema.Properties{prop("envs", schema.Arr(EnvInfoSchema))})),
			)),
			// Folders of `path` (absolute or `~`-relative) on `env` (null: this device), as the directory picker lists them.
			contracts.Meth("directories", contracts.NewMethod(
				c(obj(schema.Properties{prop("origin", owner()), prop("env", envOrNull()), prop("path", str(schema.MinLength(1)))})),
				c(p2runtime.DirectoryListingSchema),
			)),
			// Moves the worker's session (`SessionMoves.relocate`); `refused` is why its busy worker could not be fenced
			// (nothing moved). A session working in a sandbox only moves within it (only the owner's Relocate leaves it).
			contracts.Meth("move", contracts.NewMethod(
				c(obj(schema.Properties{prop("origin", owner()), prop("env", envOrNull()), prop("cwd", str(schema.MinLength(1)))})),
				c(obj(schema.Properties{prop("refused", envOrNull())})),
			)),
		},
	})},
}))
