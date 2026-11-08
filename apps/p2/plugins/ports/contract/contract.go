// Package contract ports apps/p2/plugins/ports/contract.ts and protocol.ts: the port forwards' schemas, the server's
// `ForwardStore`, the owner's and the worker's `Ports`, and the forward origins that Radius routes.
package contract

import (
	"regexp"

	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/schema"
)

var (
	str  = schema.Str
	obj  = func(properties schema.Properties) schema.Schema { return schema.Obj(properties) }
	prop = schema.Prop
	opt  = schema.Opt
	c    = p2runtime.Contract
)

func union(branches ...schema.Schema) schema.Schema { return schema.Union(branches) }

func port() schema.Schema { return schema.Int(schema.Minimum(1), schema.Maximum(65535)) }

// MaxForwardName is the longest name of a forward.
const MaxForwardName = 40

// ForwardIDPattern is the shape of a forward's ID (`FORWARD_ID`): Radius puts it in a DNS label, so lowercase letters and
// digits only.
const ForwardIDPattern = "^[a-z0-9]{1,24}$"

var forwardID = regexp.MustCompile(ForwardIDPattern)

// IsForwardID is whether id is the shape of a forward's ID.
func IsForwardID(id string) bool { return forwardID.MatchString(id) }

// forwardBaseProperties are a stored forward's fields (`forwards.json`).
func forwardBaseProperties() schema.Properties {
	return schema.Properties{
		prop("id", str()),
		prop("port", port()),
		opt("name", str()),
		opt("public", schema.Bool()),
		// The session whose agent opened it (the `ports` tool); absent when the owner added it.
		opt("session", str()),
	}
}

// ForwardBaseSchema is a stored forward.
var ForwardBaseSchema = obj(forwardBaseProperties())

// ForwardSchema is a forward as shown to the owner and the agent: who opened it, and its addresses once known.
var ForwardSchema = obj(append(forwardBaseProperties(),
	opt("sessionTitle", str()),
	// Owner only, after signing in to Radius.
	opt("url", str()),
	// Anyone with the link; only for public forwards.
	opt("publicUrl", str()),
))

var forwardList = schema.Arr(ForwardSchema)

var radiusOriginSchema = union(str(), schema.Nul())

// QuerySchema is the `ports` tool's request from a session's agent; the server knows which session sends it.
var QuerySchema = union(
	obj(schema.Properties{prop("action", p2runtime.Literal("list"))}),
	obj(schema.Properties{
		prop("action", p2runtime.Literal("open")),
		prop("port", port()),
		opt("name", str(schema.MaxLength(MaxForwardName))),
		opt("public", schema.Bool()),
	}),
	obj(schema.Properties{prop("action", p2runtime.Literal("close")), prop("port", port())}),
)

// ReplySchema is the answer to a query.
var ReplySchema = obj(schema.Properties{
	prop("forwards", forwardList),
	// False while the brain has never been opened through Radius: forwards exist, their addresses are unknown.
	prop("addressesKnown", schema.Bool()),
	// `open`: the forward opened or updated.
	opt("forward", ForwardSchema),
	// `close`: whether the port was forwarded.
	opt("closed", schema.Bool()),
})

func mustContract(contract *contracts.Contract, err error) *contracts.Contract {
	if err != nil {
		panic(err)
	}
	return contract
}

// ForwardStore is the forwards (server, host-internal): their list and Radius origin; the store itself is the
// `ForwardsNative`.
var ForwardStore = mustContract(contracts.Local("p2.forward-store", contracts.LocalDeclaration{
	Version: 2,
	Params:  contracts.Params{},
	Members: []contracts.LocalEntry{
		contracts.Mem("list", c(forwardList)),
		contracts.Mem("radiusOrigin", c(radiusOriginSchema)),
	},
}))

// Ports is the owner's forwards and the worker's tool calls (`p2.ports`): one registry, the worker's identity fixes its
// session. 2: the worker's query has no `origin`: only turns the owner started reach the tool (its `origin: "owner"`).
var Ports = mustContract(contracts.Remote("p2.ports", contracts.RemoteDeclaration{
	Version:     2,
	Params:      contracts.Params{},
	HostAddress: p2runtime.ServerHost,
	Variants: []contracts.VariantEntry{
		contracts.Var("owner", contracts.VariantDeclaration{
			State: []contracts.Property{
				p2runtime.StateProp("forwards", forwardList),
				p2runtime.StateProp("radiusOrigin", radiusOriginSchema),
			},
			Methods: []contracts.MethodEntry{
				contracts.Meth("add", contracts.NewMethod(c(obj(schema.Properties{
					prop("port", port()),
					opt("name", str(schema.MaxLength(MaxForwardName))),
					opt("public", schema.Bool()),
				})))),
				contracts.Meth("remove", contracts.NewMethod(c(obj(schema.Properties{prop("id", str())})))),
				contracts.Meth("setPublic", contracts.NewMethod(c(obj(schema.Properties{prop("id", str()), prop("public", schema.Bool())})))),
			},
		}),
		contracts.Var("worker", contracts.VariantDeclaration{
			Methods: []contracts.MethodEntry{
				contracts.Meth("query", contracts.NewMethod(c(obj(schema.Properties{
					prop("conversation", p2runtime.Literal(1)),
					prop("query", QuerySchema),
				})), c(ReplySchema))),
			},
		}),
	},
}))

var radiusBrainOrigin = regexp.MustCompile(`^(https?://)([0-9a-f]{32})\.([^/]+)$`)

// ForwardOrigin is the forward's origin beside the brain's Radius origin, or its public one; "" and false when radiusOrigin
// is not a Radius brain origin or id is not a forward ID (`forwardOrigin`).
func ForwardOrigin(radiusOrigin, id string, public bool) (string, bool) {
	match := radiusBrainOrigin.FindStringSubmatch(radiusOrigin)
	if match == nil || !IsForwardID(id) {
		return "", false
	}
	prefix := "fwd"
	if public {
		prefix = "pub"
	}
	return match[1] + prefix + "--" + match[2] + "--" + id + "." + match[3], true
}

// ForwardsParts is what `ForwardStore` contributes to ForwardsNative once open: Forwards is read at each use.
type ForwardsParts struct {
	Forwards func() *Forwards
}

// ForwardsNative is the registry of the open forwards (`p2.forwards`): its value is `ForwardStore`'s contribution, nil
// while it is not open. The tool's server side, the Radius relay and the tests read the forwards here.
var ForwardsNative = p2runtime.NewNativeRegistry("p2.forwards")

// ServeForwardsNative serves the registry (the ports plugin's facet; test hosts).
func ServeForwardsNative(facet host.FacetHost) error {
	return p2runtime.ServeNative[ForwardsParts](facet, ForwardsNative)
}

// ForwardsOf is the forwards for a factory, read at each use: it declares the registry.
func ForwardsOf(self host.Self) func() (*ForwardsParts, error) {
	return p2runtime.NativeOf[ForwardsParts](self.Registry(ForwardsNative), "The forwards")
}
