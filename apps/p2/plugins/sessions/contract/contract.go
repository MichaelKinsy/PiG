// Package contract ports apps/p2/plugins/sessions/contract.ts: the session catalog and the sessions as a principal sees
// them (a leaf package other plugins import for the contracts, their schemas and the registries of the lifecycle).
package contract

import (
	"context"

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
	arr     = func(item schema.Schema) schema.Schema { return schema.Arr(item) }
	union   = func(branches ...schema.Schema) schema.Schema { return schema.Union(branches) }
	method  = func(sides ...schema.Schema) *contracts.Method {
		contractSides := make([]contracts.Schema, len(sides))
		for i, side := range sides {
			contractSides[i] = c(side)
		}
		return contracts.NewMethod(contractSides...)
	}
)

func minimumOne() schema.Schema { return schema.Int(schema.Minimum(1)) }

// sessionFields are a session row's fields before `/tree` (`Sessions` v3, `Catalog` v2).
func sessionFields() schema.Properties {
	return schema.Properties{
		prop("id", str()),
		opt("env", union(str(), nul())),
		prop("cwd", str()),
		prop("title", str()),
		prop("createdAt", num()),
		prop("updatedAt", num()),
		prop("archivedAt", union(num(), nul())),
		prop("pinned", boolean()),
		prop("model", p2runtime.ModelRefSchema),
		prop("thinkingLevel", str()),
	}
}

func with(base schema.Properties, more ...schema.Property) schema.Properties {
	return append(append(schema.Properties{}, base...), more...)
}

var sessionSchemaV3 = obj(sessionFields())

// branchSchemaV4 is `Sessions` v4 and `Catalog` v3: branch labels were always text.
var branchSchemaV4 = obj(schema.Properties{prop("conversation", minimumOne()), prop("label", str())})
var sessionSchemaV4 = obj(with(sessionFields(), prop("main", minimumOne()), prop("branches", arr(branchSchemaV4))))

// BranchSchema is a `/tree` branch as the sidebar lists it: its conversation and its stored label (`MainDoc`); null
// shows the session title (conversation 1 until it is renamed).
var BranchSchema = obj(schema.Properties{prop("conversation", minimumOne()), prop("label", union(str(), nul()))})

// Branch is a `/tree` branch (BranchSchema); Label nil shows the session title.
type Branch struct {
	Conversation int
	Label        *string
}

// SessionSchema is a session row: `main` is the current main conversation (1, or the `/tree` branch the session
// continues in, docs/tree.md) and `branches` the top-level conversations, newest first; empty until the worker
// reported them.
var SessionSchema = obj(with(sessionFields(), prop("main", minimumOne()), prop("branches", arr(BranchSchema))))

// SessionArchived is cleanup completed before an archive call returns (e.g. session-owned port forwards).
var SessionArchived = p2runtime.NewKeyedRegistry[func(ctx context.Context, session string) error]("p2.session-archived")

// SessionCopied is what a copy of a session takes over from its source (`/clone`, `/fork`; e.g. its project), called
// once the copy's row exists and before its copying starts.
var SessionCopied = p2runtime.NewKeyedRegistry[func(ctx context.Context, source, copy string) error]("p2.session-copied")

// MaxTitle is Pi Mobile's MAX_SESSION_TITLE.
const MaxTitle = 120

var idSchema = obj(schema.Properties{prop("id", str())})
var titleSchema = str(schema.MinLength(1), schema.MaxLength(MaxTitle))

// CreateSchema is what `Sessions.create` takes before client-chosen IDs.
var CreateSchema = obj(schema.Properties{
	opt("env", union(str(schema.Pattern("^[a-z0-9][a-z0-9_-]{0,31}$")), nul())),
	opt("createDirectory", boolean()),
	prop("cwd", str(schema.MinLength(1))),
	opt("title", str()),
	opt("model", p2runtime.ModelRefSchema),
	opt("thinkingLevel", p2runtime.ThinkingLevelSchema),
})

// SessionIDSchema is a session ID: a UUID, chosen by the server or by the client (so a page can navigate before the
// answer).
var SessionIDSchema = str(schema.Pattern("^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$"))

// CreateArgsSchema is `Sessions.create` from version 6: `id`, the new session's ID chosen by the caller (default a new
// one).
var CreateArgsSchema = obj(with(p2runtime.PropertiesOf(CreateSchema), opt("id", SessionIDSchema)))

// OperationKindSchema is a lifecycle operation: what runs as a job after its method answered.
var OperationKindSchema = union(p2runtime.Literal("relocate"), p2runtime.Literal("clone"), p2runtime.Literal("fork"), p2runtime.Literal("archive"))

// OperationSchema is a lifecycle operation of a session in progress or failed (`Sessions.operations`): `session` the
// session it acts on, `target` the new session a clone or fork makes (its row exists from the start; its worker starts
// once the copy is done). `copying`: a clone's or fork's files are being copied (the target's worker waits). A failed
// one stays until it is dismissed, with why it failed; what it changed was rolled back (a failed copy's row is
// archived).
var OperationSchema = union(
	obj(schema.Properties{
		prop("phase", p2runtime.Literal("running")),
		prop("id", str()),
		prop("kind", OperationKindSchema),
		prop("session", str()),
		prop("target", union(str(), nul())),
		prop("copying", boolean()),
	}),
	obj(schema.Properties{
		prop("phase", p2runtime.Literal("failed")),
		prop("id", str()),
		prop("kind", OperationKindSchema),
		prop("session", str()),
		prop("target", union(str(), nul())),
		prop("error", str()),
	}),
)

// ArchivedStarts: archived sessions whose worker may start anyway, without resuming Durable work: rebuilding their
// search index (`p2.archived-starts`; server, host-internal; the search plugin's `rebuildIndex`, as Pi Mobile's).
var ArchivedStarts = must(contracts.Local("p2.archived-starts", contracts.LocalDeclaration{
	Version: 1,
	Params:  contracts.Params{},
	Members: []contracts.LocalEntry{
		contracts.Mem("allowed", c(arr(str()))),
		contracts.Mem("allow", method(obj(schema.Properties{prop("session", str())}))),
		contracts.Mem("end", method(obj(schema.Properties{prop("session", str())}))),
	},
}))

var catalogCreate = obj(schema.Properties{
	// A chosen ID (the caller's, a clone's or a fork's); default a new one.
	opt("id", SessionIDSchema),
	opt("env", union(str(), nul())),
	prop("cwd", str()),
	prop("title", str()),
	// The title was given, not the placeholder: automatic titles never replace it.
	opt("titleEdited", boolean()),
	prop("model", p2runtime.ModelRefSchema),
	prop("thinkingLevel", p2runtime.ThinkingLevelSchema),
})

func updateFields() schema.Properties {
	return schema.Properties{
		prop("id", str()),
		opt("cwd", str()),
		opt("env", union(str(), nul())),
		opt("title", str()),
		opt("pinned", boolean()),
		opt("archived", boolean()),
		opt("model", p2runtime.ModelRefSchema),
		opt("thinkingLevel", p2runtime.ThinkingLevelSchema),
	}
}

// catalogV2 is the catalog before `/tree`, the base of its history (2: thinking levels are an enum).
var catalogV2 = must(contracts.Local("p2.catalog", contracts.LocalDeclaration{
	Version: 2,
	Params:  contracts.Params{},
	Members: []contracts.LocalEntry{
		contracts.Mem("sessions", c(arr(sessionSchemaV3))),
		contracts.Mem("get", method(idSchema, sessionSchemaV3)),
		contracts.Mem("create", method(catalogCreate, sessionSchemaV3)),
		// Applies indexed activity without replacing an explicit title or moving time backwards.
		contracts.Mem("activity", method(obj(schema.Properties{prop("id", str()), prop("timestamp", num()), opt("firstUserText", str())}))),
		contracts.Mem("update", method(obj(updateFields()), sessionSchemaV3)),
	},
}))

func evolveLocal(previous *contracts.Contract, edit func(*contracts.LocalEditor) *contracts.LocalEditor) *contracts.Contract {
	return must(contracts.Evolve(previous, edit))
}

func evolveRemote(previous *contracts.Contract, edit func(*contracts.RemoteEditor) *contracts.RemoteEditor) *contracts.Contract {
	return must(contracts.Evolve(previous, edit))
}

// catalogV3 (3): `main` and `branches`.
var catalogV3 = evolveLocal(catalogV2, func(e *contracts.LocalEditor) *contracts.LocalEditor {
	return e.State("sessions", c(arr(sessionSchemaV4))).
		Result("get", c(sessionSchemaV4)).
		Result("create", c(sessionSchemaV4)).
		Method("update", c(obj(with(updateFields(), opt("main", minimumOne()), opt("branches", arr(branchSchemaV4))))), c(sessionSchemaV4))
})

// Catalog is the session catalog (`p2.catalog`, server, SQLite): every session, archived ones included.
// Host-internal. 4: a branch's label may be null (the session title). The provider still serves 2 and 3 (its
// `accepts`).
var Catalog = evolveLocal(catalogV3, func(e *contracts.LocalEditor) *contracts.LocalEditor {
	return e.State("sessions", c(arr(SessionSchema))).
		Result("get", c(SessionSchema)).
		Result("create", c(SessionSchema)).
		Method("update", c(obj(with(updateFields(), opt("main", minimumOne()), opt("branches", arr(BranchSchema))))), c(SessionSchema))
})

// sessionsV3 is Sessions as a principal sees them, before `/tree`: the base of the history (2: thinking levels are an
// enum; configureSelf takes either field alone. 3: `clone` and `fork` (owner)).
var sessionsV3 = must(contracts.Remote("p2.sessions", contracts.RemoteDeclaration{
	Version:     3,
	Params:      contracts.Params{},
	HostAddress: p2runtime.ServerHost,
	Variants: []contracts.VariantEntry{
		contracts.Var("owner", contracts.VariantDeclaration{
			State: []contracts.Property{
				p2runtime.StateProp("sessions", arr(sessionSchemaV3)),
				// Working directory suggested for a new session: the most recently active one, or the home directory.
				p2runtime.StateProp("defaultCwd", str()),
				p2runtime.StateProp("defaultEnv", union(str(), nul())),
			},
			Methods: []contracts.MethodEntry{
				contracts.Meth("create", method(CreateSchema, sessionSchemaV3)),
				contracts.Meth("relocate", method(obj(schema.Properties{
					prop("id", str()), prop("cwd", str(schema.MinLength(1))), prop("env", union(str(), nul())),
				}))),
				contracts.Meth("rename", method(obj(schema.Properties{prop("id", str()), prop("title", titleSchema)}))),
				contracts.Meth("pin", method(obj(schema.Properties{prop("id", str()), prop("pinned", boolean())}))),
				contracts.Meth("archive", method(idSchema)),
				contracts.Meth("restore", method(idSchema)),
				// A copy of the session as it is now (pi's `/clone`): its store with every conversation, and its
				// artifacts, downloads and uploads, under a new ID. Refused while the session's worker has work running.
				contracts.Meth("clone", method(idSchema, sessionSchemaV3)),
				// A new session holding the active context of `conversation` before the entry `before` (pi's `/fork`
				// from a user message: that message is not part of it). The main conversation or one of its `/tree`
				// branches.
				contracts.Meth("fork", method(obj(schema.Properties{
					prop("id", str()), prop("conversation", minimumOne()), prop("before", minimumOne()),
				}), sessionSchemaV3)),
			},
		}),
		// A guest sees its one session while it is not archived.
		contracts.Var("guest", contracts.VariantDeclaration{
			State: []contracts.Property{p2runtime.StateProp("sessions", arr(sessionSchemaV3))},
		}),
		contracts.Var("worker", contracts.VariantDeclaration{
			State: []contracts.Property{p2runtime.StateProp("session", union(sessionSchemaV3, nul()))},
			Methods: []contracts.MethodEntry{
				contracts.Meth("renameSelf", method(obj(schema.Properties{prop("title", titleSchema)}), obj(schema.Properties{prop("previous", str())}))),
				// The root conversation's configuration changed (either field alone): the catalog entry follows.
				contracts.Meth("configureSelf", method(obj(schema.Properties{opt("model", p2runtime.ModelRefSchema), opt("thinkingLevel", p2runtime.ThinkingLevelSchema)}))),
			},
		}),
	},
}))

// sessionsV4 (4): rows carry `main` and `branches`; the worker's `treeSelf`.
var sessionsV4 = evolveRemote(sessionsV3, func(e *contracts.RemoteEditor) *contracts.RemoteEditor {
	return e.State("owner", "sessions", c(arr(sessionSchemaV4))).
		State("guest", "sessions", c(arr(sessionSchemaV4))).
		State("worker", "session", c(union(sessionSchemaV4, nul()))).
		Result("create", c(sessionSchemaV4)).
		Result("clone", c(sessionSchemaV4)).
		Result("fork", c(sessionSchemaV4)).
		AddMethod("worker", "treeSelf", c(obj(schema.Properties{prop("main", minimumOne()), prop("branches", arr(branchSchemaV4))})), c(nul()))
})

// sessionsV5 (5): a branch's label may be null (the session title).
var sessionsV5 = evolveRemote(sessionsV4, func(e *contracts.RemoteEditor) *contracts.RemoteEditor {
	return e.State("owner", "sessions", c(arr(SessionSchema))).
		State("guest", "sessions", c(arr(SessionSchema))).
		State("worker", "session", c(union(SessionSchema, nul()))).
		Result("create", c(SessionSchema)).
		Result("clone", c(SessionSchema)).
		Result("fork", c(SessionSchema)).
		Args("treeSelf", c(obj(schema.Properties{prop("main", minimumOne()), prop("branches", arr(BranchSchema))})))
})

// Sessions is the sessions as a principal sees them (`p2.sessions`). The owner lists and manages all; a guest sees its
// own session; a worker sees and renames only its own session (its principal names it). The provider still serves 3, 4
// and 5 (its `accepts`): pages and TUIs loaded before a deploy keep working.
//
// 6, client-chosen IDs and lifecycle operations as jobs: `create` takes the new session's `id`, `clone` and `fork` the
// copy's (`copy`), so a page navigates at once. `clone`, `fork`, `relocate` and `archive` answer once their effect is
// visible (the copy's row, the archived row) or started (a move), and their long part runs as a job, shown in
// `operations` (running, then gone or failed with its error) until `dismissOperation`. A `relocate` the worker refuses
// (busy) is a failed operation.
//
// The worker's `treeSelf`: the session's main conversation or its branches changed (a `/tree` switch or fork, a branch
// renamed), or the worker started; the catalog entry follows, so clients open the main one and list the branches.
var Sessions = evolveRemote(sessionsV5, func(e *contracts.RemoteEditor) *contracts.RemoteEditor {
	return e.Args("create", c(CreateArgsSchema)).
		Args("clone", c(obj(schema.Properties{prop("id", str()), opt("copy", SessionIDSchema)}))).
		Args("fork", c(obj(schema.Properties{
			prop("id", str()), prop("conversation", minimumOne()), prop("before", minimumOne()), opt("copy", SessionIDSchema),
		}))).
		AddState("owner", "operations", c(arr(OperationSchema))).
		AddMethod("owner", "dismissOperation", c(obj(schema.Properties{prop("operation", str())})), c(nul()))
})

var location = obj(schema.Properties{prop("id", str()), prop("cwd", str(schema.MinLength(1))), prop("env", union(str(), nul()))})

// SessionOperations are the sessions' lifecycle operations (`p2.session-operations`; server, host-internal; a
// host-lifetime object, so its job runs persist): each method answers once its visible effect committed and runs the
// rest as a job (`operations`). `move` is the agent's relocation (`SessionMoves`): it waits for the job and answers why
// a busy worker refused (nothing recorded).
var SessionOperations = must(contracts.Local("p2.session-operations", contracts.LocalDeclaration{
	Version: 1,
	Params:  contracts.Params{},
	Members: []contracts.LocalEntry{
		contracts.Mem("operations", c(arr(OperationSchema))),
		contracts.Mem("move", method(location, obj(schema.Properties{prop("refused", union(str(), nul()))}))),
		contracts.Mem("relocate", method(location)),
		contracts.Mem("clone", method(obj(schema.Properties{prop("id", str()), prop("copy", SessionIDSchema)}), SessionSchema)),
		contracts.Mem("fork", method(obj(schema.Properties{
			prop("id", str()), prop("conversation", minimumOne()), prop("before", minimumOne()), prop("copy", SessionIDSchema),
		}), SessionSchema)),
		contracts.Mem("archive", method(obj(schema.Properties{prop("id", str())}))),
		contracts.Mem("restore", method(obj(schema.Properties{prop("id", str())}))),
		contracts.Mem("dismiss", method(obj(schema.Properties{prop("operation", str())}))),
	},
}))

// SessionMoves is moving a session to another location (`p2.session-moves`; server, host-internal):
// `Sessions.relocate` for the owner, and the locations plugin's `location_move` once the main agent's turn has ended.
// Validates the destination (throws when it is not a directory or its environment is not configured), then moves the
// catalog entry and the running worker in place. `refused` is why a busy worker could not be fenced (nothing moved; try
// again once it is idle), else null.
var SessionMoves = must(contracts.Local("p2.session-moves", contracts.LocalDeclaration{
	Version: 1,
	Params:  contracts.Params{},
	Members: []contracts.LocalEntry{
		contracts.Mem("relocate", method(
			obj(schema.Properties{prop("id", str()), prop("cwd", str(schema.MinLength(1))), prop("env", union(str(), nul()))}),
			obj(schema.Properties{prop("refused", union(str(), nul()))}),
		)),
	},
}))

// SessionTransfer is moving history between session workers for `Sessions.fork` (`p2.session-transfer`, the server
// only): the active context before an entry as entry drafts, the session's files copied into the fork's directory, and
// seeding a new session's main conversation with them before anyone sends to it. 3: `copyFiles`.
var SessionTransfer = must(contracts.Remote("p2.session-transfer", contracts.RemoteDeclaration{
	Version:     3,
	Params:      contracts.Params{contracts.Prop("session", contracts.Str())},
	HostAddress: p2runtime.SessionHost,
	Variants: []contracts.VariantEntry{
		contracts.Var("server", contracts.VariantDeclaration{Methods: []contracts.MethodEntry{
			// Durable `EntryDraft`s: kind, data, the model messages with context edits applied, `head: "self"` first.
			contracts.Meth("history", method(
				obj(schema.Properties{prop("conversation", minimumOne()), prop("before", minimumOne())}),
				obj(schema.Properties{prop("entries", arr(schema.Unknown()))}),
			)),
			// Appends `entries` to the main conversation in one commit; refused when it has history already.
			contracts.Meth("seed", method(obj(schema.Properties{prop("entries", arr(schema.Unknown()))}))),
			// Copies this session's files (the kit's `Files`: index and blobs) into the directory of the new session
			// `to`, which must not exist yet (its worker waits while a fork copies).
			contracts.Meth("copyFiles", method(obj(schema.Properties{prop("to", str())}))),
		}}),
	},
}))

var lockOrNone = union(p2runtime.SessionLockSchema, nul())

// SessionLocks is every session's lock (`p2.session-locks`; server, host-internal, persisted): set by the owner,
// followed by MCP placement and workers.
var SessionLocks = must(contracts.Local("p2.session-locks", contracts.LocalDeclaration{
	Version: 1,
	Params:  contracts.Params{},
	Members: []contracts.LocalEntry{
		contracts.Mem("locks", c(p2runtime.Record(p2runtime.SessionLockSchema))),
		contracts.Mem("set", method(obj(schema.Properties{prop("session", str()), prop("lock", lockOrNone)}))),
	},
}))

// SessionLock is a session's lock as the owner and the session's worker see it (`p2.session-lock`): the owner reads
// and sets every session's; a worker reads its own session's (its principal names it).
var SessionLock = must(contracts.Remote("p2.session-lock", contracts.RemoteDeclaration{
	Version:     1,
	Params:      contracts.Params{},
	HostAddress: p2runtime.ServerHost,
	Variants: []contracts.VariantEntry{
		contracts.Var("owner", contracts.VariantDeclaration{
			State:   []contracts.Property{p2runtime.StateProp("locks", p2runtime.Record(p2runtime.SessionLockSchema))},
			Methods: []contracts.MethodEntry{contracts.Meth("set", method(obj(schema.Properties{prop("session", str()), prop("lock", lockOrNone)})))},
		}),
		contracts.Var("worker", contracts.VariantDeclaration{State: []contracts.Property{p2runtime.StateProp("lock", lockOrNone)}}),
	},
}))
