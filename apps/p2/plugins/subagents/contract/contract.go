// Package contract ports the part of apps/p2/plugins/subagents/contract.ts that the sessions plugin reads: a child's
// schema and the server's index of every session's children (`ChildStore`).
package contract

import (
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/schema"
)

var c = p2runtime.Contract

// ChildSchema is a subagent (or a code mode script's one-shot agent) as the sidebar lists it.
var ChildSchema = schema.Obj(schema.Properties{
	// A subagent's name; a one-shot agent's label.
	schema.Prop("name", schema.Str()),
	// `agent`: a code mode script's one-shot agent, read-only and listed on the script's tool card, not the sidebar.
	schema.Opt("kind", p2runtime.Literal("agent")),
	// A one-shot agent's nested call ID (`<script call ID>/c<n>`), as the script's tool card lists it.
	schema.Opt("callId", schema.Str()),
	// An archived subagent: hidden in the sidebar's archived row; still usable.
	schema.Opt("archived", schema.Bool()),
	schema.Prop("conversationId", schema.Num()),
	schema.Prop("model", schema.Str()),
	schema.Prop("thinkingLevel", schema.Str()),
	schema.Prop("tools", schema.Arr(schema.Str())),
	// A subagent's own location (environment and cwd, e.g. a git worktree); absent: it follows the session.
	schema.Opt("location", p2runtime.LocationSchema),
	schema.Prop("lastEntryId", schema.Num()),
})

// ChildrenSchema is every session's children by session ID.
var ChildrenSchema = p2runtime.Record(schema.Arr(ChildSchema))

// ChildStore is the persistent sidebar metadata of subagents (`p2.child-store`, version 2: a child's own location
// instead of its own cwd); transcripts remain in the worker's Durable storage.
var ChildStore = func() *contracts.Contract {
	contract, err := contracts.Local("p2.child-store", contracts.LocalDeclaration{
		Version: 2,
		Params:  contracts.Params{},
		Members: []contracts.LocalEntry{
			contracts.Mem("children", c(ChildrenSchema)),
			contracts.Mem("report", contracts.NewMethod(c(schema.Obj(schema.Properties{
				schema.Prop("session", schema.Str()), schema.Prop("generation", schema.Num()), schema.Prop("children", schema.Arr(ChildSchema)),
			})))),
		},
	})
	if err != nil {
		panic(err)
	}
	return contract
}()
