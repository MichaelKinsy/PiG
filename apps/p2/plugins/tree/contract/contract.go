// Package contract ports apps/p2/plugins/tree/contract.ts: the session's branches as one tree of messages, switching
// the branch the session continues in, and the branches' labels (docs/tree.md).
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
	obj     = func(properties schema.Properties) schema.Schema { return schema.Obj(properties) }
	prop    = schema.Prop
	union   = func(branches ...schema.Schema) schema.Schema { return schema.Union(branches) }
	minimum = func() schema.Schema { return schema.Int(schema.Minimum(1)) }
)

// PreviewLength is how much of a message a node previews, and the longest branch label.
const PreviewLength = 200

// nodeFields are a tree node's fields in `SessionTree` v1.
func nodeFields() schema.Properties {
	return schema.Properties{
		// The entry's ID (Session-global, so unique across branches).
		prop("entry", minimum()),
		// The node before it on its branch, or null for the first message.
		prop("parent", union(minimum(), schema.Nul())),
		prop("role", union(p2runtime.Literal("user"), p2runtime.Literal("assistant"))),
		// The message's text, at most PreviewLength characters.
		prop("text", schema.Str()),
	}
}

// treeFields are the tree's fields in `SessionTree` v1.
func treeFields() schema.Properties {
	return schema.Properties{
		// The current main conversation.
		prop("main", minimum()),
		// The current branch's last node: where the next message continues; null before the first message.
		prop("current", union(minimum(), schema.Nul())),
	}
}

// TreeNodeSchema is one message of the tree: a user message or an assistant answer with text. `conversation` is the top
// level conversation storing it: the branch whose label it shows under, the first of its own nodes.
var TreeNodeSchema = obj(append(nodeFields(), prop("conversation", minimum())))

// BranchLabelSchema is a branch's label; null shows the session title (conversation 1's default, `MainDoc`).
var BranchLabelSchema = union(schema.Str(), schema.Nul())

// TreeSchema is the tree: the nodes in entry ID order (a parent always before its children), the top level
// conversations and their labels in creation order, the main conversation and the current node.
var TreeSchema = obj(schema.Properties{
	prop("nodes", schema.Arr(TreeNodeSchema)),
	prop("branches", schema.Arr(obj(schema.Properties{prop("conversation", minimum()), prop("label", BranchLabelSchema)}))),
	prop("main", minimum()),
	prop("current", union(minimum(), schema.Nul())),
})

// treeV1Schema is the tree of `SessionTree` v1: no branches, nodes without their conversation.
var treeV1Schema = obj(append(schema.Properties{prop("nodes", schema.Arr(obj(nodeFields())))}, treeFields()...))

// TreeNode is a node of the tree (TreeNodeSchema).
type TreeNode struct {
	Entry        int64  `json:"entry"`
	Parent       *int64 `json:"parent"`
	Role         string `json:"role"`
	Text         string `json:"text"`
	Conversation int64  `json:"conversation"`
}

var sessionTreeV1 = must(contracts.Remote("p2.tree", contracts.RemoteDeclaration{
	Version:     1,
	Params:      contracts.Params{contracts.Prop("session", contracts.Str())},
	HostAddress: p2runtime.SessionHost,
	Variants: []contracts.VariantEntry{
		contracts.Var("owner", contracts.VariantDeclaration{Methods: []contracts.MethodEntry{
			contracts.Meth("tree", contracts.NewMethod(contracts.Schema{}, c(treeV1Schema))),
			// Continues the session at `entry` (pi's `/tree`): after it for an answer, before it for a user message,
			// whose text comes back for the editor (null otherwise). Switches to the branch ending there, else forks a
			// new one (a new empty conversation before the first message), labelled with the chosen message's start. A
			// run of the conversation left goes on in the background. Returns the new main conversation.
			contracts.Meth("navigate", contracts.NewMethod(
				c(obj(schema.Properties{prop("entry", minimum())})),
				c(obj(schema.Properties{prop("conversation", minimum()), prop("text", union(schema.Str(), schema.Nul()))})),
			)),
			// Continues the session at the end of the branch `conversation` (a sidebar row); a run goes on.
			contracts.Meth("select", contracts.NewMethod(c(obj(schema.Properties{prop("conversation", minimum())})))),
		}}),
	},
}))

// SessionTree is the session's branches, for the owner (worker). 2: branch labels (`Tree.branches`,
// `TreeNode.conversation`) and `rename`: renames the branch `conversation`, 1 to PreviewLength characters after
// trimming; null or empty resets conversation 1 to the session title, other branches need a label. The provider still
// serves 1 (its `accepts`).
var SessionTree = must(contracts.Evolve(sessionTreeV1, func(e *contracts.RemoteEditor) *contracts.RemoteEditor {
	return e.Result("tree", c(TreeSchema)).
		AddMethod("owner", "rename", c(obj(schema.Properties{prop("conversation", minimum()), prop("label", BranchLabelSchema)})), c(schema.Nul()))
}))
