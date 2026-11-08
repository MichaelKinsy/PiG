// Package contract ports apps/p2/plugins/search/contract.ts: the full-text index of every session's messages, a session
// worker's feed into it, and the owner's search (a leaf package other plugins import for the contracts and their schemas).
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
	boolean = schema.Bool
	prop    = schema.Prop
	opt     = schema.Opt
	obj     = func(properties schema.Properties) schema.Schema { return schema.Obj(properties) }
	arr     = func(item schema.Schema, options ...schema.ArrayOption) schema.Schema {
		return schema.Arr(item, options...)
	}
	union   = func(branches ...schema.Schema) schema.Schema { return schema.Union(branches) }
	literal = func(value string) schema.Schema { return p2runtime.Literal(value) }
	method  = func(sides ...schema.Schema) *contracts.Method {
		contractSides := make([]contracts.Schema, len(sides))
		for i, side := range sides {
			contractSides[i] = c(side)
		}
		return contracts.NewMethod(contractSides...)
	}
)

// with is properties followed by extra (a spread).
func with(properties schema.Properties, extra ...schema.Property) schema.Properties {
	return append(append(schema.Properties{}, properties...), extra...)
}

var roleSchema = func() schema.Schema { return union(literal("user"), literal("assistant")) }

// IndexedMessageSchema is one committed user or assistant message of a session conversation, as indexed.
var IndexedMessageSchema = obj(schema.Properties{
	prop("conversationId", num()),
	prop("entryId", num()),
	prop("role", roleSchema()),
	prop("timestamp", num()),
	prop("text", str()),
})

var messageHitSchema = obj(schema.Properties{
	prop("session", str()),
	prop("conversationId", num()),
	prop("entryId", num()),
	prop("role", roleSchema()),
	// A bounded snippet around the match.
	prop("text", str()),
})

// SearchHitSchema is a message match with its session's title.
var SearchHitSchema = obj(with(p2runtime.PropertiesOf(messageHitSchema), prop("title", str())))

// SearchResultsSchema are the sessions whose title or working directory contains the query, in sidebar order, and the best
// message matches first (at most 41: the drawer shows 40 and whether there are more).
var SearchResultsSchema = obj(schema.Properties{
	prop("sessions", arr(str())),
	prop("messages", arr(SearchHitSchema)),
})

var querySchema = obj(schema.Properties{
	prop("query", str(schema.MaxLength(500))),
	// Search archived sessions instead of active ones.
	opt("archived", boolean()),
})

var batchSchema = obj(schema.Properties{
	opt("conversation", num(schema.Default(1))),
	opt("activityAt", num()),
	prop("messages", arr(IndexedMessageSchema, schema.MaxItems(1000))),
	// The cursor after this batch: entries up to here are indexed (unchanged until the last page of a range).
	prop("through", num()),
})

// HistoryHitsSchema are a conversation's own matches, for the history tools: entry, role and a bounded snippet, whether
// there are more, and the entry ID up to which the conversation is indexed (later entries are not searchable yet).
var HistoryHitsSchema = obj(schema.Properties{
	prop("hits", arr(obj(schema.Properties{
		prop("entryId", num()),
		prop("role", roleSchema()),
		prop("snippet", str()),
	}))),
	prop("more", boolean()),
	prop("indexedThrough", num()),
})

var historyQuerySchema = obj(schema.Properties{
	// Keywords as the model wrote them (see `ftsQuery`); never raw FTS syntax.
	prop("query", str(schema.MaxLength(500))),
	prop("conversation", num()),
	// Only entries with smaller IDs.
	opt("before", num()),
	prop("limit", schema.Int(schema.Minimum(1), schema.Maximum(20))),
})

// RebuildSchema is a session's index rebuild: running, or failed with why (kept until the next rebuild of that session).
var RebuildSchema = union(
	obj(schema.Properties{prop("session", str()), prop("phase", literal("running"))}),
	obj(schema.Properties{prop("session", str()), prop("phase", literal("failed")), prop("error", str())}),
)

// entryIdsSchema is root conversation entry IDs by session.
var entryIdsSchema = p2runtime.Record(num())

// SearchIndex is the full-text index of every session's messages (root and children) (`p2.search-index`; server,
// host-internal, SQLite `search.sqlite`). Derived and rebuildable: a schema change drops it, and workers catch up from
// their cursors (0) at their next start.
var SearchIndex = must(contracts.Local("p2.search-index", contracts.LocalDeclaration{
	Version: 1,
	Params:  contracts.Params{},
	Members: []contracts.LocalEntry{
		// The entry ID up to which the chosen conversation is indexed (0: nothing).
		contracts.Mem("cursor", method(obj(schema.Properties{prop("session", str()), opt("conversation", num(schema.Default(1)))}), num())),
		// Commits a batch (idempotent by entry ID) and advances the cursor; returns once committed.
		contracts.Mem("index", method(obj(with(schema.Properties{prop("session", str())}, p2runtime.PropertiesOf(batchSchema)...)))),
		// Drops a session's messages and cursor (its worker re-indexes from the start).
		contracts.Mem("reset", method(obj(schema.Properties{prop("session", str())}))),
		// Each session's latest answer in its root conversation (its assistant entry ID), as indexed.
		contracts.Mem("answers", c(entryIdsSchema)),
		// Up to which root entry the owner has read each session (persisted; owners only, see `SessionReads`).
		contracts.Mem("reads", c(entryIdsSchema)),
		contracts.Mem("markRead", method(obj(schema.Properties{prop("session", str()), prop("through", num())}))),
		// One conversation's matches (history tools); bounds before ranking and the limit.
		contracts.Mem("history", method(obj(with(schema.Properties{prop("session", str())}, p2runtime.PropertiesOf(historyQuerySchema)...)), HistoryHitsSchema)),
		// Message matches among `sessions` (literal terms, all required).
		contracts.Mem("messages", method(obj(schema.Properties{prop("query", str()), prop("sessions", arr(str()))}), arr(messageHitSchema))),
		// The index rebuilds running or failed (a job each: they survive a server restart).
		contracts.Mem("rebuilds", c(arr(RebuildSchema))),
		// Starts re-indexing `session` from the start (`Search.rebuildIndex`); refuses an unknown session.
		contracts.Mem("rebuild", method(obj(schema.Properties{prop("session", str())}))),
	},
}))

// SearchFeed is a session worker's feed into the index (worker principals only, scoped to their session). Acknowledged
// catch-up: the worker scans its immutable history from the cursor and sends pages; each call returns after the server
// committed it, and the cursor moves only after a whole range committed, so an interrupted scan is retried.
var SearchFeed = must(contracts.Remote("p2.search-feed", contracts.RemoteDeclaration{
	Version:     1,
	Params:      contracts.Params{},
	HostAddress: p2runtime.ServerHost,
	Variants: []contracts.VariantEntry{
		contracts.Var("worker", contracts.VariantDeclaration{Methods: []contracts.MethodEntry{
			contracts.Meth("cursor", method(union(schema.Nul(), obj(schema.Properties{prop("conversation", num())})), num())),
			contracts.Meth("index", method(batchSchema)),
			// Searches one of this worker's own conversations (the history tools); the session is the worker's.
			contracts.Meth("history", method(historyQuerySchema, HistoryHitsSchema)),
		}}),
	},
}))

// searchV1 is the search over session titles, working directories and message text (owner only): the base of the history.
var searchV1 = must(contracts.Remote("p2.search", contracts.RemoteDeclaration{
	Version:     1,
	Params:      contracts.Params{},
	HostAddress: p2runtime.ServerHost,
	Variants: []contracts.VariantEntry{
		contracts.Var("owner", contracts.VariantDeclaration{Methods: []contracts.MethodEntry{
			contracts.Meth("search", method(querySchema, SearchResultsSchema)),
			// Re-indexes one session from the start (Pi Mobile's `rebuildIndex`): its worker stops, its index entries go, and a
			// fresh worker scans its history; an archived session's worker scans without resuming work and stops again.
			contracts.Meth("rebuildIndex", method(obj(schema.Properties{prop("session", str())}))),
		}}),
	},
}))

// Search is the search over session titles, working directories and message text (owner only). 2: `rebuildIndex` answers
// once the rebuild started (a job); `rebuilds` shows it running, and why it failed. The provider still serves 1.
var Search = must(contracts.Evolve(searchV1, func(e *contracts.RemoteEditor) *contracts.RemoteEditor {
	return e.AddState("owner", "rebuilds", c(arr(RebuildSchema)))
}))
