// Package contract ports apps/p2/plugins/artifacts/contract.ts: a session's artifacts (a leaf package other plugins import
// for the contracts).
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

var c = p2runtime.Contract

// ArtifactInfoSchema is one version of an artifact (the runtime's, which upstream re-exports from the API).
var ArtifactInfoSchema = p2runtime.ArtifactInfoSchema

// ArtifactList is the complete per-conversation index, independent of compaction/reset and transcript paging.
var ArtifactList = must(contracts.Remote("p2.artifact-list", contracts.RemoteDeclaration{
	Version:     1,
	Params:      contracts.Params{contracts.Prop("session", contracts.Str()), contracts.Prop("conversation", contracts.Int())},
	HostAddress: p2runtime.SessionHost,
	Variants: []contracts.VariantEntry{
		contracts.Var("reader", contracts.VariantDeclaration{State: []contracts.Property{
			p2runtime.StateProp("artifacts", schema.Arr(ArtifactInfoSchema)),
		}}),
	},
}))

// MaxArtifactPreview is how much of an artifact an inline preview reads (the rest is a download): 512 KiB.
const MaxArtifactPreview = 512 * 1024

// ArtifactContent is an artifact's content for clients that cannot use the `/artifact` route (the TUI's worker has no
// cookie): text and Markdown are shown inline, HTML and SVG as source. Same authority as the route: anyone who may view
// the session. At most `MaxArtifactPreview` characters; `truncated` says there is more.
var ArtifactContent = must(contracts.Remote("p2.artifact-content", contracts.RemoteDeclaration{
	Version:     1,
	Params:      contracts.Params{contracts.Prop("session", contracts.Str())},
	HostAddress: p2runtime.SessionHost,
	Variants: []contracts.VariantEntry{
		contracts.Var("reader", contracts.VariantDeclaration{Methods: []contracts.MethodEntry{
			contracts.Meth("read", contracts.NewMethod(
				c(schema.Obj(schema.Properties{
					schema.Prop("name", schema.Str(schema.MaxLength(64))),
					schema.Prop("version", schema.Int(schema.Minimum(1))),
				})),
				c(schema.Obj(schema.Properties{
					schema.Prop("info", ArtifactInfoSchema), schema.Prop("content", schema.Str()), schema.Prop("truncated", schema.Bool()),
				})),
			)),
		}}),
	},
}))

// ArtifactSource is an artifact's whole source for the server's `/artifact` route (the session's worker, the server only:
// the route authorizes the viewer, the worker owns the bytes). At most 2 MiB of UTF-8.
var ArtifactSource = must(contracts.Remote("p2.artifact-source", contracts.RemoteDeclaration{
	Version:     1,
	Params:      contracts.Params{contracts.Prop("session", contracts.Str())},
	HostAddress: p2runtime.SessionHost,
	Variants: []contracts.VariantEntry{
		contracts.Var("server", contracts.VariantDeclaration{Methods: []contracts.MethodEntry{
			contracts.Meth("read", contracts.NewMethod(
				c(schema.Obj(schema.Properties{
					schema.Prop("name", schema.Str(schema.MaxLength(64))),
					schema.Prop("version", schema.Int(schema.Minimum(1))),
				})),
				c(schema.Obj(schema.Properties{schema.Prop("info", ArtifactInfoSchema), schema.Prop("content", schema.Str())})),
			)),
		}}),
	},
}))
