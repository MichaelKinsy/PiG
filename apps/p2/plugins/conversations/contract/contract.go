// Package contract ports apps/p2/plugins/conversations/contract.ts: peer conversations between a server's top-level sessions
// (a leaf package other plugins import for the contracts and their schemas).
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
	arr     = func(item schema.Schema) schema.Schema { return schema.Arr(item) }
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

// QuerySchema is what a session asks: the list of the other sessions, a message to one, or its latest completed answer.
var QuerySchema = union(
	obj(schema.Properties{prop("action", literal("list"))}),
	obj(schema.Properties{
		prop("action", literal("send")),
		prop("to", str(schema.MinLength(1), schema.MaxLength(200))),
		prop("message", str(schema.MinLength(1), schema.MaxLength(32768))),
		opt("followUp", boolean()),
	}),
	obj(schema.Properties{prop("action", literal("answer")), prop("to", str(schema.MinLength(1), schema.MaxLength(200)))}),
)

// AnswerSchema is a session's latest completed answer: whether it is working, and whether the text is cut.
var AnswerSchema = obj(schema.Properties{
	opt("text", str()),
	prop("busy", boolean()),
	prop("truncated", boolean()),
	prop("limited", boolean()),
})

var replySchema = union(
	obj(schema.Properties{
		prop("action", literal("list")),
		prop("self", str()),
		prop("peers", arr(obj(schema.Properties{
			prop("id", str()),
			prop("title", str()),
			prop("model", str()),
			prop("state", union(literal("working"), literal("idle"), literal("stopped"))),
		}))),
		prop("truncated", boolean()),
	}),
	obj(schema.Properties{prop("action", literal("send")), prop("to", str()), prop("submissionId", num())}),
	obj(append(schema.Properties{prop("action", literal("answer")), prop("to", str())}, p2runtime.PropertiesOf(AnswerSchema)...)),
)

func originProp() schema.Property { return opt("origin", literal("owner")) }

// ConversationBroker is the server-local broker. The sender's identity comes from the authenticated worker, never the
// prompt text.
var ConversationBroker = must(contracts.Local("p2.conversation-broker", contracts.LocalDeclaration{
	Version: 1,
	Params:  contracts.Params{},
	Members: []contracts.LocalEntry{
		contracts.Mem("query", method(
			obj(schema.Properties{prop("source", str()), prop("requestId", str()), originProp(), prop("query", QuerySchema)}),
			replySchema,
		)),
	},
}))

// Conversations: only a current worker generation can broker a root tool invocation.
//
// 2: request IDs up to 500 characters (Pi Mobile's limit: nested code mode calls make long ones). 3: `origin` is the sending
// turn's (`originOf`): `"owner"` or absent, no `"peer"`.
var Conversations = must(contracts.Remote("p2.conversations", contracts.RemoteDeclaration{
	Version:     3,
	Params:      contracts.Params{},
	HostAddress: p2runtime.ServerHost,
	Variants: []contracts.VariantEntry{
		contracts.Var("worker", contracts.VariantDeclaration{Methods: []contracts.MethodEntry{
			contracts.Meth("query", method(
				obj(schema.Properties{
					prop("conversation", p2runtime.Literal(1)),
					prop("requestId", str(schema.MinLength(1), schema.MaxLength(500))),
					originProp(),
					prop("query", QuerySchema),
				}),
				replySchema,
			)),
		}}),
	},
}))

// PeerConversation is the server-to-worker ingress; the server has already authorized and attributed the sender. `origin` is
// the sending turn's, which the recipient's entry records (`originOf`).
var PeerConversation = must(contracts.Remote("p2.peer-conversation", contracts.RemoteDeclaration{
	Version:     1,
	Params:      contracts.Params{contracts.Prop("session", contracts.Str())},
	HostAddress: p2runtime.SessionHost,
	Variants: []contracts.VariantEntry{
		contracts.Var("broker", contracts.VariantDeclaration{Methods: []contracts.MethodEntry{
			contracts.Meth("send", method(
				obj(schema.Properties{
					prop("source", str()), prop("title", str()), prop("requestId", str()), prop("message", str()),
					opt("followUp", boolean()), originProp(),
				}),
				obj(schema.Properties{prop("submissionId", num())}),
			)),
			contracts.Meth("answer", contracts.NewMethod(contracts.Schema{}, c(AnswerSchema))),
		}}),
	},
}))
