// Package contract ports apps/p2/plugins/send-file/contract.ts: a sent file's text preview.
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

var c = p2runtime.Contract

// MaxSentPreview is how much of a sent file a text preview reads (the rest is a download): 256 KiB.
const MaxSentPreview = 256 * 1024

// SentFiles is a sent file's text preview for clients that cannot use the `/download` route (the TUI's worker has no
// cookie). Same authority as the route: anyone who may view the session. `text` is null for binary files (a NUL byte or
// invalid UTF-8 in the first `MaxSentPreview` bytes).
var SentFiles = must(contracts.Remote("p2.sent-files", contracts.RemoteDeclaration{
	Version:     1,
	Params:      contracts.Params{contracts.Prop("session", contracts.Str())},
	HostAddress: p2runtime.SessionHost,
	Variants: []contracts.VariantEntry{
		contracts.Var("reader", contracts.VariantDeclaration{Methods: []contracts.MethodEntry{
			contracts.Meth("preview", contracts.NewMethod(
				c(schema.Obj(schema.Properties{schema.Prop("id", schema.Str(schema.MaxLength(64)))})),
				c(schema.Obj(schema.Properties{
					schema.Prop("name", schema.Str()),
					schema.Prop("size", schema.Num()),
					schema.Prop("text", schema.Union([]schema.Schema{schema.Str(), schema.Nul()})),
					schema.Prop("truncated", schema.Bool()),
				})),
			)),
		}}),
	},
}))

// OfferedBy is the `ref` of the downloads conversation `conversation` offered (`send_file`), in the session's files.
func OfferedBy(conversation int) string { return "conversation:" + strconv.Itoa(conversation) }
