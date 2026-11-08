// Package contract ports apps/p2/plugins/tree/contract.ts: `/tree` (`docs/tree.md`), the session's branches as one tree of
// messages, switching the branch the session continues in, and the branches' labels. Each branch is a top-level Durable
// conversation forked from another at an entry. A leaf package other plugins import for the contract.
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
	c      = p2runtime.Contract
	str    = schema.Str
	nul    = schema.Nul
	prop   = schema.Prop
	obj    = func(properties schema.Properties) schema.Schema { return schema.Obj(properties) }
	arr    = func(item schema.Schema) schema.Schema { return schema.Arr(item) }
	union  = func(branches ...schema.Schema) schema.Schema { return schema.Union(branches) }
	method = func(sides ...schema.Schema) *contracts.Method {
		contractSides := make([]contracts.Schema, len(sides))
		for i, side := range sides {
			contractSides[i] = c(side)
		}
		return contracts.NewMethod(contractSides...)
	}
)

func minimumOne() schema.Schema { return schema.Int(schema.Minimum(1)) }

// PreviewLength is how much of a message a node previews, and the longest branch label.
const PreviewLength = 200

// nodeFields are a tree node's fields in `SessionTree` v1: the entry's ID (Session-global, so unique across branches), the
// node before it on its branch (null for the first message), its role and its text (at most PreviewLength characters).
func nodeFields() schema.Properties {
	return schema.Properties{
		prop("entry", minimumOne()),
		prop("parent", union(minimumOne(), nul())),
		prop("role", union(p2runtime.Literal("user"), p2runtime.Literal("assistant"))),
		prop("text", str()),
	}
}

// TreeNodeSchema is one message of the tree: a user message or an assistant answer with text, and the top-level
// conversation storing it (the branch whose label it shows under, the first of its own nodes).
var TreeNodeSchema = obj(append(nodeFields(), prop("conversation", minimumOne())))

// BranchLabelSchema is a branch's label; null shows the session title (conversation 1's default, `MainDoc`).
var BranchLabelSchema = union(str(), nul())

// treeFields are the tree's fields in `SessionTree` v1: the current main conversation, and the current branch's last node
// (where the next message continues; null before the first message).
func treeFields() schema.Properties {
	return schema.Properties{
		prop("main", minimumOne()),
		prop("current", union(minimumOne(), nul())),
	}
}

// TreeSchema is the tree: the nodes in entry ID order (a parent always comes before its children), the top-level
// conversations and their labels in creation order, and `main` and `current`.
var TreeSchema = obj(append(schema.Properties{
	prop("nodes", arr(TreeNodeSchema)),
	prop("branches", arr(obj(schema.Properties{prop("conversation", minimumOne()), prop("label", BranchLabelSchema)}))),
}, treeFields()...))

// sessionTreeV1 is the session's branches, for the owner (worker): the base of the history, before branch labels.
var sessionTreeV1 = must(contracts.Remote("p2.tree", contracts.RemoteDeclaration{
	Version:     1,
	Params:      contracts.Params{contracts.Prop("session", contracts.Str())},
	HostAddress: p2runtime.SessionHost,
	Variants: []contracts.VariantEntry{
		contracts.Var("owner", contracts.VariantDeclaration{Methods: []contracts.MethodEntry{
			contracts.Meth("tree", contracts.NewMethod(contracts.Schema{}, c(obj(append(schema.Properties{prop("nodes", arr(obj(nodeFields())))}, treeFields()...))))),
			// Continues the session at `entry` (pi's `/tree`): after it for an answer, before it for a user message, whose text
			// comes back for the editor (null otherwise). Switches to the branch ending there, else forks a new one (a new
			// empty conversation before the first message), labelled with the chosen message's start. A run of the
			// conversation left goes on in the background. Returns the new main conversation.
			contracts.Meth("navigate", method(
				obj(schema.Properties{prop("entry", minimumOne())}),
				obj(schema.Properties{prop("conversation", minimumOne()), prop("text", union(str(), nul()))}),
			)),
			// Continues the session at the end of the branch `conversation` (a sidebar row); a run goes on.
			contracts.Meth("select", method(obj(schema.Properties{prop("conversation", minimumOne())}))),
		}}),
	},
}))

// SessionTree is the session's branches, for the owner (worker). 2: branch labels (`Tree.branches`,
// `TreeNode.conversation`) and `rename`: renames the branch `conversation`, 1 to PreviewLength characters after trimming;
// null or empty resets conversation 1 to the session title, other branches need a label. The provider still serves 1 (its
// `accepts`).
var SessionTree = must(contracts.Evolve(sessionTreeV1, func(e *contracts.RemoteEditor) *contracts.RemoteEditor {
	return e.Result("tree", c(TreeSchema)).
		AddMethod("owner", "rename", c(obj(schema.Properties{prop("conversation", minimumOne()), prop("label", BranchLabelSchema)})), c(nul()))
}))
