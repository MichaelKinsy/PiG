// Package contract ports the part of apps/p2/plugins/subagents/contract.ts that the sessions plugin reads: a child's
// schema and the server's index of every session's children (`ChildStore`).
package contract

import (
	"slices"
	"strings"

	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/schema"
)

var c = p2runtime.Contract

// childFields are a child's fields as 6e11a00a0 released them (`Subagents` 2, before its time and failure).
func childFields() schema.Properties {
	return schema.Properties{
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
	}
}

// ChildSchemaV2 is a child as 6e11a00a0 released it, before its time and failure.
var ChildSchemaV2 = schema.Obj(childFields())

// ChildSchema is a subagent (or a code mode script's one-shot agent) as the sidebar lists it.
var ChildSchema = schema.Obj(append(childFields(),
	// When its newest message was written (ms).
	schema.Opt("updatedAt", schema.Num()),
	// Why its latest run ended without an answer: Durable's run failure reason (e.g. `aborted`, `no_model`) and its
	// detail (an error's text; owners only). Absent after a run that answered.
	schema.Opt("failure", schema.Obj(schema.Properties{schema.Prop("reason", schema.Str()), schema.Opt("detail", schema.Str())})),
))

// ModelRefOf is a child's model text (`provider/modelId`; the model ID may hold more slashes) as a model reference.
func ModelRefOf(text string) (provider, modelID string, ok bool) {
	slash := strings.Index(text, "/")
	if slash <= 0 {
		return "", "", false
	}
	return text[:slash], text[slash+1:], true
}

// ChildrenSchema is every session's children by session ID.
var ChildrenSchema = p2runtime.Record(schema.Arr(ChildSchema))

// ChildStore is the persistent sidebar metadata of subagents (`p2.child-store`, version 3: a child's own location
// instead of its own cwd (2), its time and failure (3)); transcripts remain in the worker's Durable storage.
var ChildStore = func() *contracts.Contract {
	contract, err := contracts.Local("p2.child-store", contracts.LocalDeclaration{
		Version: 3,
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

// RemoteEnvironments are the remote environments a session's conversations work on (`remoteEnvironments`): the session's
// (a row's `env`), then its subagents' own (archived ones included: still usable), each once, in that order. A read/write
// guest can reach all of them. children is the session's `ChildSchema` list as JSON; both may be nil.
func RemoteEnvironments(session *delta.JsonObject, children []any) []string {
	environments := []string{}
	add := func(env any) {
		if text, ok := env.(string); ok && text != "" && !slices.Contains(environments, text) {
			environments = append(environments, text)
		}
	}
	if session != nil {
		add(session.Value("env"))
	}
	for _, each := range children {
		if child, ok := each.(*delta.JsonObject); ok {
			if location, ok := child.Value("location").(*delta.JsonObject); ok {
				add(location.Value("env"))
			}
		}
	}
	return environments
}

// ConversationLocation is where a conversation works (`conversationLocation`): a child's own location, else the session's
// (the main conversation's) as its cwd and env (null when none). Both may be nil (nil when neither is given).
func ConversationLocation(session, child *delta.JsonObject) *delta.JsonObject {
	if child != nil {
		if location, ok := child.Value("location").(*delta.JsonObject); ok {
			return location
		}
	}
	if session == nil {
		return nil
	}
	env := session.Value("env")
	return delta.JsonObjectOf("cwd", session.Value("cwd"), "env", env)
}

var subagentsV2 = func() *contracts.Contract {
	contract, err := contracts.Remote("p2.subagents", contracts.RemoteDeclaration{
		// 2: a child's own location instead of its own cwd.
		Version:     2,
		Params:      contracts.Params{},
		HostAddress: p2runtime.ServerHost,
		Variants: []contracts.VariantEntry{
			contracts.Var("reader", contracts.VariantDeclaration{
				State: []contracts.Property{p2runtime.StateProp("children", p2runtime.Record(schema.Arr(ChildSchemaV2)))},
			}),
			contracts.Var("reporter", contracts.VariantDeclaration{
				Methods: []contracts.MethodEntry{
					contracts.Meth("report", contracts.NewMethod(c(schema.Obj(schema.Properties{schema.Prop("children", schema.Arr(ChildSchemaV2))})))),
				},
			}),
		},
	})
	if err != nil {
		panic(err)
	}
	return contract
}()

// Subagents are the session's children, as workers report them and readers see them (server). 3: a child's time and
// failure. The provider still serves 2 (its `accepts`).
var Subagents = func() *contracts.Contract {
	contract, err := contracts.Evolve(subagentsV2, func(e *contracts.RemoteEditor) *contracts.RemoteEditor {
		return e.State("reader", "children", c(ChildrenSchema)).
			Args("report", c(schema.Obj(schema.Properties{schema.Prop("children", schema.Arr(ChildSchema))})))
	})
	if err != nil {
		panic(err)
	}
	return contract
}()

// SubagentControl changes a session's subagents from the browser (worker): the owner and read/write guests. `archive`
// archives a subagent (stopping its work first) or restores it; one-shot agents are read-only: they belong to the script
// waiting for their answer.
var SubagentControl = func() *contracts.Contract {
	contract, err := contracts.Remote("p2.subagent-control", contracts.RemoteDeclaration{
		Version:     1,
		Params:      contracts.Params{contracts.Prop("session", contracts.Str())},
		HostAddress: p2runtime.SessionHost,
		Variants: []contracts.VariantEntry{
			contracts.Var("participant", contracts.VariantDeclaration{
				Methods: []contracts.MethodEntry{
					contracts.Meth("archive", contracts.NewMethod(c(schema.Obj(schema.Properties{
						schema.Prop("conversation", schema.Num()), schema.Prop("archived", schema.Bool()),
					})))),
				},
			}),
		},
	})
	if err != nil {
		panic(err)
	}
	return contract
}()
