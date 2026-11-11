// Package contract ports apps/p2/plugins/views/contract.ts: panes beside the transcript that the person and the agent
// share (a leaf package other plugins import for the contracts).
package contract

import (
	"regexp"

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
	num    = schema.Num
	prop   = schema.Prop
	opt    = schema.Opt
	method = func(sides ...schema.Schema) *contracts.Method {
		contractSides := make([]contracts.Schema, len(sides))
		for i, side := range sides {
			contractSides[i] = c(side)
		}
		return contracts.NewMethod(contractSides...)
	}
)

func obj(properties schema.Properties) schema.Schema { return schema.Obj(properties) }
func arr(item schema.Schema, options ...schema.ArrayOption) schema.Schema {
	return schema.Arr(item, options...)
}
func union(branches ...schema.Schema) schema.Schema { return schema.Union(branches) }

// VIDSource is VIEW_ID's source: a view's ID is a lowercase slug, unique in its session.
const VIDSource = "^[a-z0-9][a-z0-9_-]{0,63}$"

// ViewID is VIEW_ID.
var ViewID = regexp.MustCompile(VIDSource)

const (
	// Transcript is the ID where annotations name the view they mark up: it is no view of its own, but takes notes too.
	Transcript = "transcript"
	// MaxViewState is the largest state a view keeps, as JSON.
	MaxViewState = 256 * 1024
	// MaxViewHTML is the largest custom view's HTML, as UTF-8.
	MaxViewHTML = 2 * 1024 * 1024
	// MaxAnnotations is how many annotations a session keeps at once.
	MaxAnnotations = 200
	// ViewSources is where a custom view's HTML versions live in the session's files (path: the view's ID).
	ViewSources = "views"
	// ViewInput is the entry kind of a person's message that carried view context (selections, annotations) to the agent.
	ViewInput = "p2.view-input"
)

var id = str(schema.Pattern(VIDSource))

// ViewKindSchema is the kinds of views: a Markdown file of the workspace rendered, the working tree's changes, a custom
// HTML view.
var ViewKindSchema = schema.OneOf([]any{"markdown", "changes", "custom"})

// SelectionSchema is what the person selected in a view, as the view describes it: a summary the agent reads with their
// next message, and the view's own data. `sent`: their next message carried it already.
var SelectionSchema = obj(schema.Properties{
	prop("summary", str(schema.MinLength(1), schema.MaxLength(2000))),
	opt("data", schema.Unknown()),
	opt("sent", p2runtime.Literal(true)),
})

// ViewSchema is one view of a session.
var ViewSchema = obj(schema.Properties{
	prop("id", id),
	prop("kind", ViewKindSchema),
	prop("title", str(schema.MinLength(1), schema.MaxLength(120))),
	// `markdown`: the file, relative to the workspace; `changes`: only changes below this path.
	opt("path", str(schema.MaxLength(4096))),
	// `custom`: the version of its HTML in the session's files (`ViewSources`).
	opt("source", schema.Int(schema.Minimum(1))),
	opt("selection", SelectionSchema),
	// The custom view's last script errors in the browser, newest last: what the agent sees when it reads the view.
	opt("errors", arr(str(schema.MaxLength(1000)), schema.MaxItems(10))),
	// When the agent last asked to show it: devices bring it to the front (and show it again) when this grows.
	prop("focus", num()),
	prop("created", num()),
})

// strokeSchema is a stroke of a drawing: x, y pairs as fractions of the pane's width and height at the time it was drawn.
var strokeSchema = arr(num(schema.Minimum(-1), schema.Maximum(2)), schema.MaxItems(4000))

// AnnotationInputSchema is what an annotation says, as a person makes it: a comment on text, text to remove, or a drawing
// over a pane.
var AnnotationInputSchema = union(
	obj(schema.Properties{
		prop("kind", p2runtime.Literal("comment")),
		prop("view", id),
		prop("quote", str(schema.MinLength(1), schema.MaxLength(4000))),
		prop("note", str(schema.MinLength(1), schema.MaxLength(4000))),
	}),
	obj(schema.Properties{
		prop("kind", p2runtime.Literal("delete")),
		prop("view", id),
		prop("quote", str(schema.MinLength(1), schema.MaxLength(4000))),
	}),
	obj(schema.Properties{
		prop("kind", p2runtime.Literal("ink")),
		prop("view", id),
		prop("strokes", arr(strokeSchema, schema.MinItems(1), schema.MaxItems(100))),
		// The pane's aspect (width / height) when it was drawn: the drawing keeps its shape.
		prop("aspect", num(schema.Minimum(0.01), schema.Maximum(100))),
		// Text the drawing covers, as the pane found it under the strokes.
		opt("under", str(schema.MaxLength(4000))),
		opt("note", str(schema.MaxLength(4000))),
	}),
)

// AnnotationSchema is an annotation as the session keeps it: who made it, when, and whether a message carried it to the
// agent yet (open: the input's fields, flattened).
var AnnotationSchema = schema.Obj(schema.Properties{
	prop("id", str(schema.MinLength(1), schema.MaxLength(40))),
	prop("kind", schema.OneOf([]any{"comment", "delete", "ink"})),
	prop("view", id),
	opt("quote", str()),
	opt("note", str()),
	opt("strokes", arr(strokeSchema)),
	opt("aspect", num()),
	opt("under", str()),
	prop("author", str()),
	prop("created", num()),
	opt("sent", p2runtime.Literal(true)),
}, schema.Open())

// ArrangementSchema is how the agent last asked the person's panes to be laid out (`layout.ts`, `arrangementError`), and
// when: each device adopts it once.
var ArrangementSchema = obj(schema.Properties{prop("layout", schema.Unknown()), prop("stamp", num())})

// OpenSchema is what a person opens from the Views sheet: a Markdown file, the changes, or a view of the owner's library.
var OpenSchema = union(
	obj(schema.Properties{
		prop("kind", p2runtime.Literal("markdown")),
		prop("path", str(schema.MinLength(1), schema.MaxLength(4096))),
		opt("title", str(schema.MaxLength(120))),
	}),
	obj(schema.Properties{
		prop("kind", p2runtime.Literal("changes")),
		opt("path", str(schema.MaxLength(4096))),
		opt("title", str(schema.MaxLength(120))),
	}),
	obj(schema.Properties{prop("saved", id)}),
)

func viewsState() []contracts.Property {
	return []contracts.Property{
		p2runtime.StateProp("views", arr(ViewSchema)),
		p2runtime.StateProp("annotations", arr(AnnotationSchema)),
		p2runtime.StateProp("arrangement", schema.Nullable(ArrangementSchema)),
	}
}

func sessionParam() contracts.Params { return contracts.Params{contracts.Prop("session", contracts.Str())} }

// Views is a session's views and annotations as anyone who may view the session sees them; people who may send there also
// open, close and select in views, annotate, and drop what their next message would carry.
var Views = must(contracts.Remote("p2.views", contracts.RemoteDeclaration{
	Version:     1,
	Params:      sessionParam(),
	HostAddress: p2runtime.SessionHost,
	Variants: []contracts.VariantEntry{
		contracts.Var("reader", contracts.VariantDeclaration{State: viewsState()}),
		contracts.Var("editor", contracts.VariantDeclaration{State: viewsState(), Methods: []contracts.MethodEntry{
			contracts.Meth("open", method(OpenSchema, obj(schema.Properties{prop("id", id)}))),
			// Removes the view, its state and its annotations.
			contracts.Meth("close", method(obj(schema.Properties{prop("id", id)}))),
			// Sets (or with `null` clears) the person's selection in a view.
			contracts.Meth("select", method(obj(schema.Properties{prop("id", id), prop("selection", schema.Nullable(SelectionSchema))}))),
			contracts.Meth("annotate", method(AnnotationInputSchema, obj(schema.Properties{prop("id", str())}))),
			// Removes annotations: these, or every one without `ids`.
			contracts.Meth("unannotate", method(obj(schema.Properties{opt("ids", arr(str(), schema.MaxItems(MaxAnnotations)))}))),
			// Drops what the next message would carry: unsent annotations go, selections count as sent.
			contracts.Meth("discard", contracts.NewMethod()),
			// The custom view's script errors the browser saw (an empty list: none).
			contracts.Meth("report", method(obj(schema.Properties{prop("id", id), prop("errors", arr(str(schema.MaxLength(1000)), schema.MaxItems(10)))}))),
		}}),
	},
}))

// ViewState is one view's JSON state (null until something sets it): everyone who may view reads it, people who may send
// set it.
var ViewState = must(contracts.Remote("p2.view-state", contracts.RemoteDeclaration{
	Version:     1,
	Params:      contracts.Params{contracts.Prop("session", contracts.Str()), contracts.Prop("view", contracts.Str())},
	HostAddress: p2runtime.SessionHost,
	Variants: []contracts.VariantEntry{
		contracts.Var("reader", contracts.VariantDeclaration{State: []contracts.Property{p2runtime.StateProp("value", schema.Unknown())}}),
		contracts.Var("editor", contracts.VariantDeclaration{
			State:   []contracts.Property{p2runtime.StateProp("value", schema.Unknown())},
			Methods: []contracts.MethodEntry{contracts.Meth("set", method(obj(schema.Properties{prop("value", schema.Unknown())})))},
		}),
	},
}))

// ViewSource is a custom view's HTML for the server's `/view-frame` route (the worker owns the bytes; the route
// authorizes).
var ViewSource = must(contracts.Remote("p2.view-source", contracts.RemoteDeclaration{
	Version:     1,
	Params:      sessionParam(),
	HostAddress: p2runtime.SessionHost,
	Variants: []contracts.VariantEntry{
		contracts.Var("server", contracts.VariantDeclaration{Methods: []contracts.MethodEntry{
			contracts.Meth("read", method(
				obj(schema.Properties{prop("view", id), prop("version", schema.Int(schema.Minimum(1)))}),
				obj(schema.Properties{prop("title", str()), prop("html", str())}),
			)),
		}}),
	},
}))

// SavedViewSchema is a view in the owner's library: what opening it in any session starts from.
var SavedViewSchema = obj(schema.Properties{
	prop("name", id),
	prop("title", str(schema.MinLength(1), schema.MaxLength(120))),
	opt("description", str(schema.MaxLength(2000))),
	prop("kind", ViewKindSchema),
	opt("path", str(schema.MaxLength(4096))),
	// `custom`: its HTML.
	opt("html", str()),
	prop("state", schema.Unknown()),
	prop("saved", num()),
})

// SavedInfoSchema is a saved view without its HTML and state, for lists.
var SavedInfoSchema = obj(schema.Properties{
	prop("name", id),
	prop("title", str()),
	opt("description", str()),
	prop("kind", ViewKindSchema),
	prop("saved", num()),
})

// SavedViews is the owner's library of saved views (`<data>/views/<name>.json`, server): the owner lists and removes
// them; session workers list, read and save them for their agent's `view` tool and for the owner's Views sheet.
var SavedViews = must(contracts.Remote("p2.saved-views", contracts.RemoteDeclaration{
	Version:     1,
	Params:      contracts.Params{},
	HostAddress: p2runtime.ServerHost,
	Variants: []contracts.VariantEntry{
		contracts.Var("owner", contracts.VariantDeclaration{
			State:   []contracts.Property{p2runtime.StateProp("saved", arr(SavedInfoSchema))},
			Methods: []contracts.MethodEntry{contracts.Meth("remove", method(obj(schema.Properties{prop("name", id)})))},
		}),
		contracts.Var("worker", contracts.VariantDeclaration{Methods: []contracts.MethodEntry{
			contracts.Meth("list", contracts.NewMethod(contracts.Schema{}, c(arr(SavedInfoSchema)))),
			contracts.Meth("get", method(obj(schema.Properties{prop("name", id)}), schema.Nullable(SavedViewSchema))),
			contracts.Meth("save", method(SavedViewSchema, SavedInfoSchema)),
		}}),
	},
}))
