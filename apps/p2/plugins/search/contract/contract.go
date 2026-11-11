// Package contract ports apps/p2/plugins/search/contract.ts: the message index (server, SQLite), the workers' feed into it,
// owner search, unread answers and the rebuild's scan (a leaf package other plugins import).
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
	obj     = func(properties schema.Properties) schema.Schema { return schema.Obj(properties) }
	arr     = func(item schema.Schema) schema.Schema { return schema.Arr(item) }
	union   = func(branches ...schema.Schema) schema.Schema { return schema.Union(branches) }
	literal = func(value string) schema.Schema { return p2runtime.Literal(value) }
)

// role is a message's author: a person (or peer) or the model.
func role() schema.Schema { return union(literal("user"), literal("assistant")) }

// IndexedMessageSchema is one committed user or assistant message of a session conversation, as indexed.
var IndexedMessageSchema = obj(schema.Properties{
	schema.Prop("conversationId", num()),
	schema.Prop("entryId", num()),
	schema.Prop("role", role()),
	schema.Prop("timestamp", num()),
	schema.Prop("text", str()),
})

// messageHitProperties are a message match's properties: a bounded snippet around the match.
func messageHitProperties() schema.Properties {
	return schema.Properties{
		schema.Prop("session", str()),
		schema.Prop("conversationId", num()),
		schema.Prop("entryId", num()),
		schema.Prop("role", role()),
		schema.Prop("text", str()),
	}
}

var messageHitSchema = obj(messageHitProperties())

// SearchHitSchema is a message match with its session's title.
var SearchHitSchema = obj(append(messageHitProperties(), schema.Prop("title", str())))

// SearchResultsSchema is a search's answer: the sessions whose title or working directory contains the query, in sidebar
// order, and the best message matches first (at most 41: the drawer shows 40 and whether there are more).
var SearchResultsSchema = obj(schema.Properties{
	schema.Prop("sessions", arr(str())),
	schema.Prop("messages", arr(SearchHitSchema)),
})

var querySchema = obj(schema.Properties{
	schema.Prop("query", str(schema.MaxLength(500))),
	// Search archived sessions instead of active ones.
	schema.Opt("archived", schema.Bool()),
})

func batchProperties() schema.Properties {
	return schema.Properties{
		schema.Opt("conversation", num(schema.Default(1))),
		schema.Opt("activityAt", num()),
		schema.Prop("messages", schema.Arr(IndexedMessageSchema, schema.MaxItems(1000))),
		// The cursor after this batch: entries up to here are indexed (unchanged until the last page of a range).
		schema.Prop("through", num()),
	}
}

var batchSchema = obj(batchProperties())

// HistoryHitsSchema is a conversation's own matches, for the history tools: entry, role and a bounded snippet, whether
// there are more, and the entry ID up to which the conversation is indexed.
var HistoryHitsSchema = obj(schema.Properties{
	schema.Prop("hits", arr(obj(schema.Properties{
		schema.Prop("entryId", num()),
		schema.Prop("role", role()),
		schema.Prop("snippet", str()),
	}))),
	schema.Prop("more", schema.Bool()),
	schema.Prop("indexedThrough", num()),
})

func historyQueryProperties() schema.Properties {
	return schema.Properties{
		// Keywords as the model wrote them (see `FTSQuery`); never raw FTS syntax.
		schema.Prop("query", str(schema.MaxLength(500))),
		schema.Prop("conversation", num()),
		// Only entries with smaller IDs.
		schema.Opt("before", num()),
		schema.Prop("limit", schema.Int(schema.Minimum(1), schema.Maximum(20))),
	}
}

var historyQuerySchema = obj(historyQueryProperties())

// RebuildSchema is a session's index rebuild: running, or failed with why (kept until the next rebuild of that session).
var RebuildSchema = union(
	obj(schema.Properties{schema.Prop("session", str()), schema.Prop("phase", literal("running"))}),
	obj(schema.Properties{schema.Prop("session", str()), schema.Prop("phase", literal("failed")), schema.Prop("error", str())}),
)

// entryIDsSchema is root conversation entry IDs by session.
var entryIDsSchema = p2runtime.Record(num())

func with(first schema.Property, rest schema.Properties) schema.Properties {
	return append(schema.Properties{first}, rest...)
}

// SearchIndex is the full-text index of every session's messages (root and children) (`p2.search-index`: server,
// host-internal, SQLite `search.sqlite`). Derived and rebuildable: a schema change drops it, and workers catch up from their
// cursors (0) at their next start.
var SearchIndex = must(contracts.Local("p2.search-index", contracts.LocalDeclaration{
	Version: 1,
	Params:  contracts.Params{},
	Members: []contracts.LocalEntry{
		// The entry ID up to which the chosen conversation is indexed (0: nothing).
		contracts.Mem("cursor", contracts.NewMethod(c(obj(schema.Properties{schema.Prop("session", str()), schema.Opt("conversation", num(schema.Default(1)))})), c(num()))),
		// Commits a batch (idempotent by entry ID) and advances the cursor; returns once committed.
		contracts.Mem("index", contracts.NewMethod(c(obj(with(schema.Prop("session", str()), batchProperties()))))),
		// Drops a session's messages and cursor (its worker re-indexes from the start).
		contracts.Mem("reset", contracts.NewMethod(c(obj(schema.Properties{schema.Prop("session", str())})))),
		// Each session's latest answer in its root conversation (its assistant entry ID), as indexed.
		contracts.Mem("answers", c(entryIDsSchema)),
		// Up to which root entry the owner has read each session (persisted; owners only, see `SessionReads`).
		contracts.Mem("reads", c(entryIDsSchema)),
		contracts.Mem("markRead", contracts.NewMethod(c(obj(schema.Properties{schema.Prop("session", str()), schema.Prop("through", num())})))),
		// One conversation's matches (history tools); bounds before ranking and the limit.
		contracts.Mem("history", contracts.NewMethod(c(obj(with(schema.Prop("session", str()), historyQueryProperties()))), c(HistoryHitsSchema))),
		// Message matches among `sessions` (literal terms, all required).
		contracts.Mem("messages", contracts.NewMethod(c(obj(schema.Properties{schema.Prop("query", str()), schema.Prop("sessions", arr(str()))})), c(arr(messageHitSchema)))),
		// The index rebuilds running or failed (a job each: they survive a server restart).
		contracts.Mem("rebuilds", c(arr(RebuildSchema))),
		// Starts re-indexing `session` from the start (`Search.rebuildIndex`); refuses an unknown session.
		contracts.Mem("rebuild", contracts.NewMethod(c(obj(schema.Properties{schema.Prop("session", str())})))),
	},
}))

// SearchFeed is a session worker's feed into the index (`p2.search-feed`: worker principals only, scoped to their session).
// Acknowledged catch-up: the worker scans its immutable history from the cursor and sends pages; each call returns after the
// server committed it, and the cursor moves only after a whole range committed, so an interrupted scan is retried.
var SearchFeed = must(contracts.Remote("p2.search-feed", contracts.RemoteDeclaration{
	Version:     1,
	Params:      contracts.Params{},
	HostAddress: p2runtime.ServerHost,
	Variants: []contracts.VariantEntry{
		contracts.Var("worker", contracts.VariantDeclaration{
			State: []contracts.Property{},
			Methods: []contracts.MethodEntry{
				contracts.Meth("cursor", contracts.NewMethod(c(union(schema.Nul(), obj(schema.Properties{schema.Prop("conversation", num())}))), c(num()))),
				contracts.Meth("index", contracts.NewMethod(c(batchSchema))),
				// Searches one of this worker's own conversations (the history tools); the session is the worker's.
				contracts.Meth("history", contracts.NewMethod(c(historyQuerySchema), c(HistoryHitsSchema))),
			},
		}),
	},
}))

// searchV1 is `Search` as version 1 had it.
var searchV1 = must(contracts.Remote("p2.search", contracts.RemoteDeclaration{
	Version:     1,
	Params:      contracts.Params{},
	HostAddress: p2runtime.ServerHost,
	Variants: []contracts.VariantEntry{
		contracts.Var("owner", contracts.VariantDeclaration{
			State: []contracts.Property{},
			Methods: []contracts.MethodEntry{
				contracts.Meth("search", contracts.NewMethod(c(querySchema), c(SearchResultsSchema))),
				// Re-indexes one session from the start (Pi Mobile's `rebuildIndex`): its worker stops, its index entries go,
				// and a fresh worker scans its history; an archived session's worker scans without resuming work and stops
				// again.
				contracts.Meth("rebuildIndex", contracts.NewMethod(c(obj(schema.Properties{schema.Prop("session", str())})))),
			},
		}),
	},
}))

// searchV2 is `Search` as version 2 had it: `rebuildIndex` answers once the rebuild started (a job); `rebuilds` shows it
// running, and why it failed.
var searchV2 = must(contracts.Evolve(searchV1, func(e *contracts.RemoteEditor) *contracts.RemoteEditor {
	return e.AddState("owner", "rebuilds", c(arr(RebuildSchema)))
}))

// Search is search over session titles, working directories and message text (owner only). 3: `sessions` limits a search
// to those sessions before ranking and the 41-hit limit (a sidebar list's own sessions). The provider still serves 1 and 2.
var Search = must(contracts.Evolve(searchV2, func(e *contracts.RemoteEditor) *contracts.RemoteEditor {
	properties := p2runtime.PropertiesOf(querySchema)
	properties = append(properties, schema.Opt("sessions", schema.Arr(schema.Str(schema.MaxLength(64)), schema.MaxItems(10000))))
	return e.Args("search", c(obj(properties)))
}))

// SessionReads is unread answers (owners only): a session is unread while its latest root answer (`answers`) is newer than
// what an owner has read (`reads`), on any device. A browser marks a session read when its root conversation is shown.
var SessionReads = must(contracts.Remote("p2.session-reads", contracts.RemoteDeclaration{
	Version:     1,
	Params:      contracts.Params{},
	HostAddress: p2runtime.ServerHost,
	Variants: []contracts.VariantEntry{
		contracts.Var("owner", contracts.VariantDeclaration{
			State: []contracts.Property{p2runtime.StateProp("answers", entryIDsSchema), p2runtime.StateProp("reads", entryIDsSchema)},
			Methods: []contracts.MethodEntry{
				contracts.Meth("markRead", contracts.NewMethod(c(obj(schema.Properties{schema.Prop("session", str()), schema.Prop("through", num())})))),
			},
		}),
	},
}))

// IndexScan is a session worker's indexer as the server's rebuild waits for it (`p2.search-index-scan`: the server's broker
// principal only).
var IndexScan = must(contracts.Remote("p2.search-index-scan", contracts.RemoteDeclaration{
	Version:     1,
	Params:      contracts.Params{contracts.Prop("session", contracts.Str())},
	HostAddress: p2runtime.SessionHost,
	Variants: []contracts.VariantEntry{
		contracts.Var("server", contracts.VariantDeclaration{
			State: []contracts.Property{},
			Methods: []contracts.MethodEntry{
				// Resolves once this worker's indexer has completed its first full scan.
				contracts.Meth("scanned", contracts.NewMethod()),
			},
		}),
	},
}))
