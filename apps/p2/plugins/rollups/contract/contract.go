// Package contract ports apps/p2/plugins/rollups/contract.ts: rollups made from code, a saved script over every session
// that returns a sidebar section (a flat list of sessions, groups of sessions, or only a link to its list), refreshed
// automatically, on an interval or by hand (a leaf package other plugins import).
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

var (
	c       = p2runtime.Contract
	str     = schema.Str
	num     = schema.Num
	nul     = schema.Nul
	boolean = schema.Bool
	obj     = func(properties schema.Properties) schema.Schema { return schema.Obj(properties) }
	prop    = schema.Prop
	opt     = schema.Opt
	union   = func(branches ...schema.Schema) schema.Schema { return schema.Union(branches) }
	literal = p2runtime.Literal[string]
)

// MemberSchema is a session a rollup collected: its ID, or its ID with `at`, the time its window compares and its list
// sorts by (default the session's last activity), and `keep`, shown whatever its time (as a pinned session).
var MemberSchema = union(
	str(schema.MinLength(1), schema.MaxLength(200)),
	obj(schema.Properties{prop("id", str(schema.MinLength(1), schema.MaxLength(200))), opt("at", num()), opt("keep", boolean())}),
)

var members = schema.Arr(MemberSchema, schema.MaxItems(5000))

// RollupTargetSchema is where a group's "+" starts a session: in a folder (env null: this device), or in a project by
// its name.
var RollupTargetSchema = union(
	obj(schema.Properties{prop("kind", literal("folder")), prop("env", union(str(), nul())), prop("cwd", str(schema.MinLength(1)))}),
	obj(schema.Properties{prop("kind", literal("project")), prop("project", str(schema.MinLength(1)))}),
)

// RollupGroupSchema is a group a rollup returned; `keep`: shown even without a session in its window.
var RollupGroupSchema = obj(schema.Properties{
	prop("key", str(schema.MinLength(1), schema.MaxLength(200))),
	prop("name", str(schema.MinLength(1), schema.MaxLength(120))),
	opt("title", str(schema.MaxLength(500))),
	opt("keep", boolean()),
	opt("target", RollupTargetSchema),
	prop("members", members),
})

// RollupOutputSchema is what a rollup's script returns: a flat list of sessions, groups of sessions, or sessions shown
// only in its list.
var RollupOutputSchema = union(
	obj(schema.Properties{prop("layout", literal("sessions")), prop("members", members)}),
	obj(schema.Properties{prop("layout", literal("groups")), prop("groups", schema.Arr(RollupGroupSchema, schema.MaxItems(500)))}),
	obj(schema.Properties{prop("layout", literal("link")), prop("members", members)}),
)

// RollupWindowSchema is a rollup's window: one the owner may change in its options (the sidebar's windows), or `none`:
// every session it returned shows.
var RollupWindowSchema = union(literal("1d"), literal("3d"), literal("1w"), literal("forever"), literal("none"))

// RefreshSchema is when a rollup's script runs again: `auto` when what it read of the sessions changed (no tools),
// `every` so many minutes when that changed, `manual` only by hand. Every one can be refreshed by hand as well.
var RefreshSchema = union(
	obj(schema.Properties{prop("kind", literal("auto"))}),
	obj(schema.Properties{prop("kind", literal("every")), prop("minutes", schema.Int(schema.Minimum(1), schema.Maximum(1440)))}),
	obj(schema.Properties{prop("kind", literal("manual"))}),
)

// RollupKeySchema is a rollup key: lowercase, digits and dashes; its sidebar key is `rollup:<key>`.
var RollupKeySchema = str(schema.Pattern("^[a-z0-9][a-z0-9-]{0,39}$"))

// RollupDefinitionSchema is a rollup as saved: its key, its name in the sidebar, its window, when it refreshes, and its
// script.
var RollupDefinitionSchema = obj(schema.Properties{
	prop("key", RollupKeySchema),
	prop("name", str(schema.MinLength(1), schema.MaxLength(60))),
	prop("window", RollupWindowSchema),
	prop("refresh", RefreshSchema),
	prop("code", str(schema.MinLength(1), schema.MaxLength(100_000))),
})

// RollupEntrySchema is a saved rollup and its last result: `output` (null before its first good run; a failed run keeps
// the last good one), `checkedAt` (when the output was last confirmed current), `error` (why the last run failed),
// `reads` (what the last run read: `sessions.cwd`, `projects`, `now`; a failed run counts as having read every session
// field and the projects) and `fingerprint` (of those values then).
var RollupEntrySchema = obj(schema.Properties{
	prop("definition", RollupDefinitionSchema),
	prop("output", union(RollupOutputSchema, nul())),
	prop("checkedAt", union(num(), nul())),
	prop("error", union(str(), nul())),
	prop("reads", schema.Arr(str())),
	prop("fingerprint", union(str(), nul())),
})

// PreviewSchema is a trial run of a script against the sessions now: its output and what it read, or why it failed.
var PreviewSchema = union(
	obj(schema.Properties{prop("ok", literal2(true)), prop("output", RollupOutputSchema), prop("reads", schema.Arr(str()))}),
	obj(schema.Properties{prop("ok", literal2(false)), prop("error", str())}),
)

func literal2(value bool) schema.Schema { return p2runtime.Literal(value) }

// RollupSessionSchema is a session as a rollup's script sees it (`sessions`): the catalog row with its project, worker
// state and unread.
var RollupSessionSchema = obj(schema.Properties{
	prop("id", str()),
	prop("title", str()),
	prop("env", union(str(), nul())),
	prop("cwd", str()),
	prop("createdAt", num()),
	prop("updatedAt", num()),
	prop("archivedAt", union(num(), nul())),
	prop("pinned", boolean()),
	// `provider/modelId`.
	prop("model", str()),
	prop("project", union(str(), nul())),
	prop("state", union(literal("stopped"), literal("idle"), literal("working"))),
	prop("error", union(str(), nul())),
	prop("unread", boolean()),
})

var (
	keyArgs = obj(schema.Properties{prop("key", RollupKeySchema)})
	entries = schema.Arr(RollupEntrySchema)
	// Saves a rollup (a new one, or one of the same key replaced) and runs it once.
	save = contracts.NewMethod(c(RollupDefinitionSchema), c(RollupEntrySchema))
	// remove deletes a rollup.
	remove = contracts.NewMethod(c(keyArgs))
	// Runs a rollup now, whether or not what it read changed.
	refresh = contracts.NewMethod(c(keyArgs), c(RollupEntrySchema))
	// Runs a script against the sessions now without saving it.
	preview = contracts.NewMethod(c(obj(schema.Properties{prop("code", str(schema.MinLength(1), schema.MaxLength(100_000)))})), c(PreviewSchema))
)

// RollupStore is the saved rollups (`p2.rollup-store`; server, host-internal, persisted in the server's store) and
// running them.
var RollupStore = must(contracts.Local("p2.rollup-store", contracts.LocalDeclaration{
	Version: 1,
	Params:  contracts.Params{},
	Members: []contracts.LocalEntry{
		contracts.Mem("rollups", c(entries)),
		contracts.Mem("save", save), contracts.Mem("remove", remove), contracts.Mem("refresh", refresh), contracts.Mem("preview", preview),
	},
}))

// ROLLUPS_TOOL is the Familiar's tool for rollups (worker.ts), named in its tool lists.
const ROLLUPS_TOOL = "sidebar_rollups"

// SidebarRollups is the rollups for the owner's sidebar, and for the Familiar's `sidebar_rollups` tool (its worker only).
var SidebarRollups = must(contracts.Remote("p2.sidebar-rollups", contracts.RemoteDeclaration{
	Version:     1,
	Params:      contracts.Params{},
	HostAddress: p2runtime.ServerHost,
	Variants: []contracts.VariantEntry{
		contracts.Var("owner", contracts.VariantDeclaration{
			State:   []contracts.Property{p2runtime.StateProp("rollups", entries)},
			Methods: []contracts.MethodEntry{contracts.Meth("save", save), contracts.Meth("remove", remove), contracts.Meth("refresh", refresh), contracts.Meth("preview", preview)},
		}),
		contracts.Var("familiar", contracts.VariantDeclaration{
			Methods: []contracts.MethodEntry{
				contracts.Meth("list", contracts.NewMethod(contracts.Schema{}, c(entries))),
				contracts.Meth("save", save), contracts.Meth("remove", remove), contracts.Meth("refresh", refresh), contracts.Meth("preview", preview),
			},
		}),
	},
}))
