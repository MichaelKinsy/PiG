package runtime

import (
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/schema"
)

// Ports apps/p2/src/runtime/file-index.ts: the file index contract (plugins/file-index serves it) lives here, not in the
// plugin: the chat UIs use it when the plugin is installed and must not make chat depend on it.

// FileItemSchema is one file or directory of a completion.
var FileItemSchema = schema.Obj(schema.Properties{schema.Prop("path", schema.Str()), schema.Prop("dir", schema.Bool())})

// CompleteSchema is a completion request: what the user typed after `@` (or the path-like token for Tab), relative to
// the session directory.
var CompleteSchema = schema.Obj(schema.Properties{
	schema.Prop("query", schema.Str(schema.MaxLength(200))),
	schema.Opt("limit", schema.Int(schema.Minimum(1), schema.Maximum(100))),
	schema.Opt("kind", schema.Union([]schema.Schema{Literal("any"), Literal("file"), Literal("dir")})),
})

// CompleteResultSchema is a completion's answer; `truncated`: the index hit its bounds (entries, bytes or directories
// walked), so results may miss files.
var CompleteResultSchema = schema.Obj(schema.Properties{
	schema.Prop("items", schema.Arr(FileItemSchema)),
	schema.Prop("truncated", schema.Bool()),
})

// ListSchema is a directory listing request: a relative directory below the session directory; "" is its root.
var ListSchema = schema.Obj(schema.Properties{schema.Prop("path", schema.Str(schema.MaxLength(4096)))})

// ListResultSchema is a directory listing.
var ListResultSchema = schema.Obj(schema.Properties{
	schema.Prop("entries", schema.Arr(schema.Obj(schema.Properties{schema.Prop("name", schema.Str()), schema.Prop("dir", schema.Bool())}))),
	schema.Prop("truncated", schema.Bool()),
})

// FileIndexCache is one conversation's file index (`p2.file-index-cache`, worker-internal, shared by its viewers): built
// lazily, rebuilt on changes.
var FileIndexCache = mustContract(contracts.Local("p2.file-index-cache", contracts.LocalDeclaration{
	Version: 1,
	Params:  contracts.Params{contracts.Prop("conversation", contracts.Int(contracts.Min(1)))},
	Members: []contracts.LocalEntry{
		contracts.Mem("changes", Contract(schema.Num())),
		contracts.Mem("complete", contracts.NewMethod(Contract(CompleteSchema), Contract(CompleteResultSchema))),
		contracts.Mem("list", contracts.NewMethod(Contract(ListSchema), Contract(ListResultSchema))),
	},
}))

// FileIndex is `@` and Tab file completion (`p2.file-index`, version 1): the session directory's files and
// directories, ranked like pi's `@` completion. Cwd-relative only, the workspace's confinement and private-path
// exclusion; participants only (holding it leases the workspace watch). `changes` advances when files change: clients
// query again.
var FileIndex = mustContract(contracts.Remote("p2.file-index", contracts.RemoteDeclaration{
	Version: 1,
	Params: contracts.Params{
		contracts.Prop("session", contracts.Str()),
		contracts.Prop("conversation", contracts.Int(contracts.Min(1))),
	},
	HostAddress: SessionHost,
	Variants: []contracts.VariantEntry{
		contracts.Var("reader", contracts.VariantDeclaration{
			State: []contracts.Property{StateProp("changes", schema.Num())},
			Methods: []contracts.MethodEntry{
				contracts.Meth("complete", contracts.NewMethod(Contract(CompleteSchema), Contract(CompleteResultSchema))),
				contracts.Meth("list", contracts.NewMethod(Contract(ListSchema), Contract(ListResultSchema))),
			},
		}),
	},
}))
