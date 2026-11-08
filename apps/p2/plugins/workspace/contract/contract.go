// Package contract ports apps/p2/plugins/workspace/contract.ts and protocol.ts: the workspace panel's contracts (a
// conversation's working directory, read through its execution environment by its worker) and the helpers that check
// what a request names (a leaf package other plugins import).
package contract

import (
	"strings"

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
	c   = p2runtime.Contract
	str = schema.Str
	obj = func(properties schema.Properties) schema.Schema { return schema.Obj(properties) }
	// literal is a `kind` discriminator.
	literal = func(value string) schema.Schema { return p2runtime.Literal(value) }
)

var (
	pathSchema   = str(schema.MaxLength(4096))
	commitSchema = str(schema.Pattern("^[0-9a-f]{7,64}$"))
)

// QuerySchema is a read-only view of the workspace the panel asks for.
var QuerySchema = schema.Union([]schema.Schema{
	obj(schema.Properties{schema.Prop("kind", literal("list")), schema.Prop("path", pathSchema)}),
	// `commit`: the file as of that commit instead of the working tree.
	obj(schema.Properties{schema.Prop("kind", literal("file")), schema.Prop("path", pathSchema), schema.Opt("commit", commitSchema)}),
	// Every file path below the root, for filtering by name: tracked and untracked, without ignored ones.
	obj(schema.Properties{schema.Prop("kind", literal("files"))}),
	obj(schema.Properties{schema.Prop("kind", literal("status"))}),
	// Literal text search below `path` ("" = everywhere), skipping ignored files. `include`/`exclude`: comma-separated
	// file globs relative to `path`; see WorkspaceGlobs.
	obj(schema.Properties{
		schema.Prop("kind", literal("search")),
		schema.Prop("path", pathSchema),
		schema.Prop("text", str(schema.MinLength(1), schema.MaxLength(200))),
		schema.Opt("include", str(schema.MaxLength(500))),
		schema.Opt("exclude", str(schema.MaxLength(500))),
	}),
	// `context`: unchanged lines around each change (default 3); `full` makes the patch the whole file.
	obj(schema.Properties{
		schema.Prop("kind", literal("diff")),
		schema.Prop("path", pathSchema),
		schema.Opt("full", schema.Bool()),
		schema.Opt("context", schema.Int(schema.Minimum(0), schema.Maximum(1000))),
		schema.Opt("commit", commitSchema),
	}),
	// The current branch's history, newest first.
	obj(schema.Properties{
		schema.Prop("kind", literal("log")),
		schema.Prop("skip", schema.Int(schema.Minimum(0), schema.Maximum(100000))),
		schema.Prop("limit", schema.Int(schema.Minimum(1), schema.Maximum(500))),
	}),
	// One commit's message and changed files.
	obj(schema.Properties{schema.Prop("kind", literal("commit")), schema.Prop("commit", commitSchema)}),
	// Where the workspace is open now, and the session's waiting or failed move.
	obj(schema.Properties{schema.Prop("kind", literal("place"))}),
})

var (
	// resultSchema is `unsafe<WorkspaceResult>(unknown())`.
	resultSchema = schema.Unknown()
	// ReadSchema is a bounded byte range of one workspace file.
	ReadSchema = obj(schema.Properties{
		schema.Prop("path", pathSchema),
		schema.Opt("start", schema.Int(schema.Minimum(0))),
		schema.Opt("end", schema.Int(schema.Minimum(0))),
		schema.Opt("suffix", schema.Int(schema.Minimum(1))),
	})
)

// WriteSchema is one chunk of an upload into a workspace folder; chunks arrive in order.
var WriteSchema = obj(schema.Properties{
	schema.Prop("path", pathSchema),
	schema.Prop("name", str(schema.MinLength(1), schema.MaxLength(255))),
	schema.Prop("token", str(schema.Pattern("^[0-9a-f-]{36}$"))),
	schema.Prop("offset", schema.Int(schema.Minimum(0))),
	schema.Prop("data", str(schema.MaxLength(1400000))),
	schema.Prop("final", schema.Bool()),
	schema.Opt("abort", schema.Bool()),
})

var inputParams = contracts.Params{
	contracts.Prop("session", contracts.Str()),
	contracts.Prop("conversation", contracts.Int(contracts.Min(1))),
}

// WorkspaceWrites serializes upload writes across conversations, through the environment at the conversation's
// location (`p2.workspace-writes`; version 3: a location (environment and cwd) instead of a cwd on the session's machine).
var WorkspaceWrites = must(contracts.Local("p2.workspace-writes", contracts.LocalDeclaration{
	Version: 3,
	Params:  contracts.Params{},
	Members: []contracts.LocalEntry{
		contracts.Mem("write", contracts.NewMethod(c(obj(schema.Properties{
			schema.Prop("location", p2runtime.LocationSchema), schema.Prop("upload", WriteSchema),
		})))),
	},
}))

// WorkspaceBackend is the shared worker implementation, one environment (at the conversation's location) and watch per
// conversation (`p2.workspace-backend`).
var WorkspaceBackend = must(contracts.Local("p2.workspace-backend", contracts.LocalDeclaration{
	Version: 1,
	Params:  contracts.Params{contracts.Prop("conversation", contracts.Int(contracts.Min(1)))},
	Members: []contracts.LocalEntry{
		contracts.Mem("changes", c(schema.Num())),
		contracts.Mem("query", contracts.NewMethod(c(QuerySchema), c(resultSchema))),
		contracts.Mem("read", contracts.NewMethod(c(ReadSchema), c(schema.Unknown()))),
		contracts.Mem("write", contracts.NewMethod(c(WriteSchema))),
	},
}))

// Workspace: holding it leases the filesystem watch; read-only guests cannot get it (`p2.workspace`).
var Workspace = must(contracts.Remote("p2.workspace", contracts.RemoteDeclaration{
	Version:     1,
	Params:      inputParams,
	HostAddress: p2runtime.SessionHost,
	Variants: []contracts.VariantEntry{
		contracts.Var("reader", contracts.VariantDeclaration{
			State:   []contracts.Property{p2runtime.StateProp("changes", schema.Num())},
			Methods: []contracts.MethodEntry{contracts.Meth("query", contracts.NewMethod(c(QuerySchema), c(resultSchema)))},
		}),
	},
}))

// WorkspaceTransfer is the HTTP transport only, reachable from the server host, never browsers
// (`p2.workspace-transfer`).
var WorkspaceTransfer = must(contracts.Remote("p2.workspace-transfer", contracts.RemoteDeclaration{
	Version:     1,
	Params:      inputParams,
	HostAddress: p2runtime.SessionHost,
	Variants: []contracts.VariantEntry{
		contracts.Var("server", contracts.VariantDeclaration{
			State: []contracts.Property{},
			Methods: []contracts.MethodEntry{
				contracts.Meth("read", contracts.NewMethod(c(ReadSchema), c(schema.Unknown()))),
				contracts.Meth("write", contracts.NewMethod(c(WriteSchema))),
			},
		}),
	},
}))

// WorkspacePath is a request path relative to the workspace root, as POSIX segments: "" is the root. It rejects absolute
// paths, `.`/`..`/empty segments, NUL, backslashes, `~` prefixes and anything inside `.git` (repository config can hold
// credentials). Lexical only; the worker additionally confines canonical paths.
func WorkspacePath(value string) (string, bool) {
	if p2runtime.JSLength(value) > 4096 {
		return "", false
	}
	if value == "" {
		return "", true
	}
	if strings.ContainsAny(value, "\x00\\") || strings.HasPrefix(value, "/") || strings.HasPrefix(value, "~") {
		return "", false
	}
	segments := strings.Split(value, "/")
	for _, segment := range segments {
		if segment == "" || segment == "." || segment == ".." {
			return "", false
		}
	}
	if segments[0] == ".git" {
		return "", false
	}
	return value, true
}
