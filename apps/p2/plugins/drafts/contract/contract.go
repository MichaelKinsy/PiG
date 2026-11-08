// Package contract ports apps/p2/plugins/drafts/contract.ts: inputs withdrawn by someone else wait for their author
// (a leaf package the chat worker imports).
package contract

import (
	"strconv"

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
	c      = p2runtime.Contract
	method = func(sides ...schema.Schema) *contracts.Method {
		contractSides := make([]contracts.Schema, len(sides))
		for i, side := range sides {
			contractSides[i] = c(side)
		}
		return contracts.NewMethod(contractSides...)
	}
)

// AuthorSchema is who wrote an input: the owner or a guest, their ID and name.
var AuthorSchema = schema.Obj(schema.Properties{
	schema.Prop("kind", schema.Union([]schema.Schema{p2runtime.Literal("owner"), p2runtime.Literal("guest")})),
	schema.Prop("id", schema.Str()),
	schema.Prop("name", schema.Str()),
})

// DraftSchema is a restored draft: its ID and text.
var DraftSchema = schema.Obj(schema.Properties{schema.Prop("id", schema.Num()), schema.Prop("text", schema.Str())})

// Draft is a restored draft (DraftSchema).
type Draft struct {
	ID   float64
	Text string
}

// DraftsByKeySchema is the restored drafts by `<conversation>:<author id>`, oldest first.
var DraftsByKeySchema = p2runtime.Record(schema.Arr(DraftSchema))

// DraftKey is the key of an author's drafts in a conversation: `<conversation>:<author id>`.
func DraftKey(conversation int, authorID string) string {
	return strconv.Itoa(conversation) + ":" + authorID
}

// DraftStore: inputs withdrawn by someone else (Stop, a queue edit) wait here for their author (worker, in memory, for
// the worker's lifetime). Each is claimed once: the first of an author's tabs to claim it puts it into its composer.
var DraftStore = must(contracts.Local("p2.draft-store", contracts.LocalDeclaration{
	Version: 1,
	Params:  contracts.Params{},
	Members: []contracts.LocalEntry{
		contracts.Mem("drafts", c(DraftsByKeySchema)),
		contracts.Mem("add", method(schema.Obj(schema.Properties{
			schema.Prop("conversation", schema.Num()),
			schema.Prop("items", schema.Arr(schema.Obj(schema.Properties{
				schema.Prop("author", AuthorSchema), schema.Prop("text", schema.Str()),
			}))),
		}))),
		contracts.Mem("claim", method(
			schema.Obj(schema.Properties{
				schema.Prop("conversation", schema.Num()), schema.Prop("authorId", schema.Str()),
				schema.Prop("ids", schema.Arr(schema.Num())),
			}),
			schema.Obj(schema.Properties{schema.Prop("texts", schema.Arr(schema.Str()))}),
		)),
	},
}))

// Drafts is the caller's own restored drafts in one conversation (participants only). `claim` removes the drafts and
// returns the texts of those still there (another tab may have taken some).
var Drafts = must(contracts.Remote("p2.drafts", contracts.RemoteDeclaration{
	Version:     1,
	Params:      contracts.Params{contracts.Prop("session", contracts.Str()), contracts.Prop("conversation", contracts.Int())},
	HostAddress: p2runtime.SessionHost,
	Variants: []contracts.VariantEntry{
		contracts.Var("author", contracts.VariantDeclaration{
			State: []contracts.Property{p2runtime.StateProp("drafts", schema.Arr(DraftSchema))},
			Methods: []contracts.MethodEntry{
				contracts.Meth("claim", method(
					schema.Obj(schema.Properties{schema.Prop("ids", schema.Arr(schema.Num()))}),
					schema.Obj(schema.Properties{schema.Prop("texts", schema.Arr(schema.Str()))}),
				)),
			},
		}),
	},
}))
