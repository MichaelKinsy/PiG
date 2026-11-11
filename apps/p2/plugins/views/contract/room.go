package contract

import (
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/schema"
)

// Ports apps/p2/plugins/views/room-contract.ts: rooms of custom views (apps/p2/docs/rooms.md): what a view's frames share on the
// server (presence, messages, a base state, the clock, the agent's calls to methods a frame offers), how a page gets a frame its
// link, and what the server reads of the view from the session's worker.

// LegacyOpening is the opening of a view added before openings existed (contract.ts).
const LegacyOpening = "0"

func boolean(options ...schema.AnnotationOption) schema.Schema { return schema.Bool(options...) }
func integer(options ...schema.NumberOption) schema.Schema     { return schema.Int(options...) }
func unknown() schema.Schema                                   { return schema.Unknown() }

var viewParam = contracts.Prop("view", contracts.Str(contracts.Pattern(VIDSource)))

// The kinds of rooms: live (presence and messages), log (messages since a snapshot), text (server-applied edits).
var (
	// RoomKindSchema is the kinds of rooms.
	RoomKindSchema = schema.OneOf([]any{"live", "log", "text"})
	roleSchema     = schema.OneOf([]any{"owner", "guest"})
	modeSchema     = schema.OneOf([]any{"read", "write"})
	methodName     = str(schema.Pattern("^[A-Za-z_][A-Za-z0-9_]{0,63}$"))
)

// PeerSchema is one of a room's peers: a frame of the view on its own link (its ID is the server-minted link ID).
var PeerSchema = obj(schema.Properties{prop("id", str()), prop("name", str()), prop("role", roleSchema), prop("mode", modeSchema)})

// PresenceSchema is who is in the room, and the leader: the oldest present peer that may write (null: none).
var PresenceSchema = obj(schema.Properties{prop("peers", arr(PeerSchema)), prop("leader", schema.Nullable(str()))})

// MessageSchema is an unkeyed message as the room numbered it: `data` is JSON text (the kit stringifies and parses). `n`: text
// rooms: the editor's batch number, when the message is a batch of edits (`data`: the edits, as JSON).
var MessageSchema = obj(schema.Properties{prop("seq", integer()), prop("from", str()), prop("data", str()), opt("n", integer())})

// LogSchema is the window of the journal every peer holds: all its messages after `start` (older ones: `history`).
var LogSchema = obj(schema.Properties{prop("start", integer()), prop("messages", arr(MessageSchema))})

// KeyedSchema is a peer's newest message for one key: the object stays, a newer message sets its two leaves. `data`'s first
// character is a tag that differs from the previous value's (`0` or `1`), the JSON text follows.
var KeyedSchema = obj(schema.Properties{prop("seq", integer()), prop("data", str())})

// Tagged is data tagged to differ from previous (a keyed entry's current `data`), see KeyedSchema.
func Tagged(data string, previous *string) string {
	if previous != nil && len(*previous) > 0 && (*previous)[0] == '0' {
		return "1" + data
	}
	return "0" + data
}

// KeyedByPeerSchema is keyed messages by peer, then key.
var KeyedByPeerSchema = obj(schema.Properties{prop("peers", p2runtime.Record(p2runtime.Record(KeyedSchema)))})

// BaseSchema is what a log or text room starts from: the view's state at message `seq`; `epoch` grows when it was replaced.
var BaseSchema = obj(schema.Properties{prop("seq", integer()), prop("epoch", integer()), prop("value", unknown())})

// MethodInfoSchema is a method a frame offers the agent: its name, what it does, its arguments' JSON Schema. `idempotent`: safe
// to run again: a call whose peer left is given to the next leader.
var MethodInfoSchema = obj(schema.Properties{
	prop("name", methodName), prop("description", str(schema.MaxLength(2000))), prop("args", unknown()), opt("idempotent", boolean()),
})

// CallSchema is a call the agent made, for the peer it is pinned to.
var CallSchema = obj(schema.Properties{prop("id", str()), prop("method", str()), prop("args", unknown()), prop("peer", str())})

// RoomSchema is the room's own facts: the store's incarnation (a new store, after the last one ended, numbers from 0 again),
// its kind, last number, base, the leader's term, the calls waiting.
var RoomSchema = obj(schema.Properties{
	prop("store", str()), prop("kind", schema.Nullable(RoomKindSchema)), prop("seq", integer()), prop("term", integer()),
	prop("base", schema.Nullable(BaseSchema)), prop("calls", arr(CallSchema)),
})

// TextIDSchema is an element ID of a text room (articulated's `ElementId`).
var TextIDSchema = obj(schema.Properties{prop("bunchId", str(schema.MinLength(1), schema.MaxLength(64))), prop("counter", integer(schema.Minimum(0)))})

// TextEditSchema is one edit of a text room: insert `content` right after `before`, or delete what lies from `start` to `end`.
var TextEditSchema = union(
	obj(schema.Properties{
		prop("kind", p2runtime.Literal("insert")), prop("before", schema.Nullable(TextIDSchema)), prop("id", TextIDSchema),
		prop("content", str(schema.MinLength(1), schema.MaxLength(100000))), prop("inWord", boolean()),
	}),
	obj(schema.Properties{prop("kind", p2runtime.Literal("delete")), prop("start", TextIDSchema), prop("end", TextIDSchema), prop("length", integer(schema.Minimum(1)))}),
)

// OutgoingSchema is what a peer sends: messages with an optional key, their data as JSON text.
var OutgoingSchema = arr(obj(schema.Properties{opt("key", str(schema.MinLength(1), schema.MaxLength(64))), prop("data", str())}), schema.MaxItems(1000))

// JoinedSchema is what a join answers.
var JoinedSchema = obj(schema.Properties{prop("me", str()), prop("editor", str()), prop("from", integer())})

// HistoryArgsSchema is the arguments of `history`.
var HistoryArgsSchema = obj(schema.Properties{prop("after", integer()), prop("limit", integer(schema.Minimum(1), schema.Maximum(1000)))})

// HistorySchema is journal messages after a number; `more`: the journal has more after the last one.
var HistorySchema = obj(schema.Properties{prop("messages", arr(MessageSchema)), prop("more", boolean())})

// AnswerSchema is a call's answer: its result, or why it failed.
var AnswerSchema = obj(schema.Properties{prop("id", str()), opt("result", unknown()), opt("error", str(schema.MaxLength(4000)))})

// SnapshotSchema is a log room's snapshot.
var SnapshotSchema = obj(schema.Properties{prop("seq", integer()), prop("epoch", integer()), prop("term", integer()), prop("value", unknown())})

// EditBatchSchema is a batch of edits of a text room.
var EditBatchSchema = obj(schema.Properties{prop("n", integer(schema.Minimum(1))), prop("edits", arr(TextEditSchema, schema.MaxItems(1000)))})

// CallArgsSchema is a call's arguments.
var CallArgsSchema = obj(schema.Properties{prop("method", methodName), prop("args", unknown())})

// ViewRoom is a view's room (server): `peer` for the frames of one opening of the view (view principals), `worker` for the
// session's worker (the agent's `view_methods` and `view_call`). In memory; it ends a little after its last peer.
var ViewRoom = p2runtime.ForViews(must(contracts.Remote("p2.view-room", contracts.RemoteDeclaration{
	Version:     1,
	Params:      contracts.Params{contracts.Prop("session", contracts.Str()), viewParam, contracts.Prop("opening", contracts.Str())},
	HostAddress: p2runtime.ServerHost,
	Variants: []contracts.VariantEntry{
		contracts.Var("peer", contracts.VariantDeclaration{
			State: []contracts.Property{
				p2runtime.StateProp("room", RoomSchema), p2runtime.StateProp("presence", PresenceSchema),
				p2runtime.StateProp("log", LogSchema), p2runtime.StateProp("keyed", KeyedByPeerSchema),
			},
			Methods: []contracts.MethodEntry{
				// Joins (as a room of `kind`, if given: the first one present decides it): the caller's peer and editor IDs, and the
				// last number before it joined (a live room's joiner takes messages after it; keyed ones all). A frame that only
				// offers methods joins without a kind.
				contracts.Meth("join", method(obj(schema.Properties{opt("kind", RoomKindSchema)}), JoinedSchema)),
				// Numbers and adds messages; `ack`: the last number the caller applied.
				contracts.Meth("send", method(obj(schema.Properties{prop("messages", OutgoingSchema), opt("ack", integer())}))),
				contracts.Meth("ack", method(obj(schema.Properties{prop("seq", integer())}))),
				// Journal messages after `after`, at most `limit`; `more`: there are newer ones before the window.
				contracts.Meth("history", method(HistoryArgsSchema, HistorySchema)),
				// The server's clock, in milliseconds since the epoch.
				contracts.Meth("now", contracts.NewMethod(contracts.Schema{}, c(num()))),
				contracts.Meth("offer", method(obj(schema.Properties{prop("methods", arr(MethodInfoSchema, schema.MaxItems(100)))}))),
				contracts.Meth("answer", method(AnswerSchema)),
				// Log rooms, the leader: `value` is the room's state after message `seq`.
				contracts.Meth("snapshot", method(SnapshotSchema)),
				// Text rooms: a batch of edits, number `n` of the caller's editor.
				contracts.Meth("edit", method(EditBatchSchema)),
			},
		}),
		contracts.Var("worker", contracts.VariantDeclaration{Methods: []contracts.MethodEntry{
			// The methods the leader offers; fails `not_open` while no frame that may write shows the view.
			contracts.Meth("methods", contracts.NewMethod(contracts.Schema{}, c(arr(MethodInfoSchema)))),
			// Runs a method in the leader's frame; its result.
			contracts.Meth("call", method(CallArgsSchema, unknown())),
		}}),
	},
})))

// ViewLinks is a page's way to a frame's link: a token for one view of the session, for people who may view it.
var ViewLinks = must(contracts.Remote("p2.view-links", contracts.RemoteDeclaration{
	Version:     1,
	Params:      sessionParam(),
	HostAddress: p2runtime.ServerHost,
	Variants: []contracts.VariantEntry{
		contracts.Var("viewer", contracts.VariantDeclaration{Methods: []contracts.MethodEntry{
			// A token for `/chord/view` (single use, one minute) and the view's current opening; `editor`: the editor a
			// reconnecting frame had (from its first token).
			contracts.Meth("token", method(
				obj(schema.Properties{prop("view", str(schema.Pattern(VIDSource))), opt("editor", str())}),
				obj(schema.Properties{prop("token", str()), prop("opening", str()), prop("editor", str())}),
			)),
		}}),
	},
}))

// ViewOpenings is the current opening of each custom view of the session (worker, for the server only).
var ViewOpenings = must(contracts.Remote("p2.view-openings", contracts.RemoteDeclaration{
	Version:     1,
	Params:      sessionParam(),
	HostAddress: p2runtime.SessionHost,
	Variants: []contracts.VariantEntry{
		contracts.Var("server", contracts.VariantDeclaration{State: []contracts.Property{p2runtime.StateProp("openings", p2runtime.Record(str()))}}),
	},
}))

// BackedStateSchema is a view's state as its room sees it: the value, its revision, and whose write it was (the room's, or
// another's).
var BackedStateSchema = obj(schema.Properties{prop("value", unknown()), prop("revision", integer()), prop("by", schema.OneOf([]any{"room", "other"}))})

// ViewBacking is one opening of a custom view, as its room reads and writes it (worker, for the server only): the view's state,
// and the room's own document (a text room's text, IDs and watermarks), which nothing else writes.
var ViewBacking = must(contracts.Remote("p2.view-backing", contracts.RemoteDeclaration{
	Version:     1,
	Params:      contracts.Params{contracts.Prop("session", contracts.Str()), viewParam, contracts.Prop("opening", contracts.Str())},
	HostAddress: p2runtime.SessionHost,
	Variants: []contracts.VariantEntry{
		contracts.Var("server", contracts.VariantDeclaration{
			State: []contracts.Property{p2runtime.StateProp("current", BackedStateSchema), p2runtime.StateProp("room", unknown())},
			Methods: []contracts.MethodEntry{
				// Sets the view's state and the room's document (each if given), only while the state's revision is still
				// `revision` and the opening current; false otherwise (nothing written).
				contracts.Meth("write", method(obj(schema.Properties{prop("revision", integer()), opt("value", unknown()), opt("room", unknown())}), boolean())),
			},
		}),
	},
}))
