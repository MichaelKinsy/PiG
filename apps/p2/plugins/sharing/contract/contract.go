// Package contract ports apps/p2/plugins/sharing/contract.ts: share links, guests and their grants, the guests connected
// now, and the owner's sharing of one session (a leaf package other plugins import for the contracts).
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
	obj    = func(properties schema.Properties) schema.Schema { return schema.Obj(properties) }
	prop   = schema.Prop
	opt    = schema.Opt
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

// ModeSchema is what a guest may do in a session: `view` reads it, `full` also sends.
var ModeSchema = union(p2runtime.Literal("view"), p2runtime.Literal("full"))

// LinkSchema is a share link: its ID, the session and mode it gives, and its token (the URL's last segment).
var LinkSchema = obj(schema.Properties{
	prop("id", str()),
	prop("session", str()),
	prop("mode", ModeSchema),
	prop("token", str()),
})

// GuestGrantSchema is a guest's current principal: the sessions shared with it now (see `GuestPrincipal`).
var GuestGrantSchema = obj(schema.Properties{
	prop("kind", p2runtime.Literal("guest")),
	prop("id", str()),
	prop("name", str()),
	opt("email", str()),
	prop("home", str()),
	prop("sessions", p2runtime.Record(ModeSchema)),
	prop("revision", schema.Num()),
})

// PersonSchema is a person a session is shared with by email.
var PersonSchema = obj(schema.Properties{prop("id", str()), prop("name", str()), prop("session", str()), prop("mode", ModeSchema)})

// ConnectedSchema is a guest whose browser is connected now.
var ConnectedSchema = obj(schema.Properties{
	prop("id", str()),
	prop("name", str()),
	prop("sessions", p2runtime.Record(ModeSchema)),
})

// Shares is the share links, guests and their grants (`p2.shares`, version 1; server, SQLite `shares.sqlite`).
// Host-internal. A guest holds one grant per session shared with it; every change of its grants (share, join, removal,
// link reset, archive, restore) bumps its revision and invalidates the objects of the older principal.
//
// Sessions are shared two ways, into the same grants: by links, for browsers on loopback or a tunnel host (a session has
// at most one link per mode; joining mints a cookie, the name is self-chosen; the same browser joining more links gains
// their sessions), and by email, for people who reach the brain through Radius (only offered while it is connected to
// Radius; Radius verifies the email of whoever opens the brain, and admits only emails the brain reported, `emails`).
var Shares = must(contracts.Local("p2.shares", contracts.LocalDeclaration{
	Version: 1,
	Params:  contracts.Params{},
	Members: []contracts.LocalEntry{
		// Every link (small: at most two per session).
		contracts.Mem("links", c(arr(LinkSchema))),
		// Normalized emails sessions that are not archived are shared with: whom Radius may admit.
		contracts.Mem("emails", c(arr(str()))),
		// Every grant to a person by email that is not removed (archived sessions included).
		contracts.Mem("people", c(arr(PersonSchema))),
		// The principal of a guest cookie value (`<id>.<secret>`), or null.
		contracts.Mem("authenticate", method(obj(schema.Properties{prop("cookie", str())}), union(GuestGrantSchema, nul()))),
		// The principal of a person Radius authenticated by their normalized email, or null.
		contracts.Mem("authenticateEmail", method(obj(schema.Properties{prop("email", str())}), union(GuestGrantSchema, nul()))),
		// A guest's current principal, or null (nothing shared with it now).
		contracts.Mem("grant", method(obj(schema.Properties{prop("id", str())}), union(GuestGrantSchema, nul()))),
		// The live link of a token, or null (reset, archived).
		contracts.Mem("active", method(obj(schema.Properties{prop("token", str())}), union(LinkSchema, nul()))),
		contracts.Mem("link", method(obj(schema.Properties{
			prop("session", str()),
			prop("mode", ModeSchema),
			prop("reset", schema.Bool()),
			opt("remoteConfirmed", schema.Bool()),
		}))),
		// Shares `session` with the person of `email` (as the owner wrote it), or changes their mode.
		contracts.Mem("share", method(obj(schema.Properties{
			prop("session", str()), prop("email", str()), prop("mode", ModeSchema), opt("remoteConfirmed", schema.Bool()),
		}))),
		contracts.Mem("remove", method(obj(schema.Properties{prop("session", str()), prop("guestId", str())}))),
		// Joins through a link: the browser's guest (its cookie) gains the link's session; otherwise a new guest is created
		// and `cookie` is its value. Fails when the link ended or the guest was removed from that session.
		contracts.Mem("join", method(
			obj(schema.Properties{prop("token", str()), prop("name", str()), opt("cookie", str())}),
			obj(schema.Properties{prop("session", str()), opt("cookie", str())}),
		)),
	},
}))

// Presence is the guests whose browsers are connected now (`p2.share-presence`, version 2: derived from the references
// to `GuestPresence`, no enter and leave; server, in memory).
var Presence = must(contracts.Local("p2.share-presence", contracts.LocalDeclaration{
	Version: 2,
	Params:  contracts.Params{},
	Members: []contracts.LocalEntry{contracts.Mem("guests", c(p2runtime.Record(ConnectedSchema)))},
}))

// GuestPresence is held by a guest's browser for its page's lifetime: it marks the guest connected (`p2.guest-presence`).
// Nothing for the owner.
var GuestPresence = must(contracts.Remote("p2.guest-presence", contracts.RemoteDeclaration{
	Version:     1,
	Params:      contracts.Params{},
	HostAddress: p2runtime.ServerHost,
	Variants: []contracts.VariantEntry{
		contracts.Var("owner", contracts.VariantDeclaration{}),
		contracts.Var("guest", contracts.VariantDeclaration{}),
	},
}))

// Sharing is the sharing of one session (`p2.sharing`, owner only): its links, the people it is shared with by email, and
// connected guests. `peopleUrl` is where those people open the session (the brain's page in Radius), or null while
// sharing with people is unavailable: the brain is not connected to Radius, or does not know its address there yet.
var Sharing = must(contracts.Remote("p2.sharing", contracts.RemoteDeclaration{
	Version:     1,
	Params:      contracts.Params{contracts.Prop("session", contracts.Str())},
	HostAddress: p2runtime.ServerHost,
	Variants: []contracts.VariantEntry{
		contracts.Var("owner", contracts.VariantDeclaration{
			State: []contracts.Property{
				// The remote environments (comma-separated) the session's conversations work on, or null: this host.
				p2runtime.StateProp("env", union(str(), nul())),
				p2runtime.StateProp("links", arr(obj(schema.Properties{prop("mode", ModeSchema), prop("token", str()), prop("url", str())}))),
				p2runtime.StateProp("guests", arr(obj(schema.Properties{prop("id", str()), prop("name", str()), prop("mode", ModeSchema)}))),
				p2runtime.StateProp("people", arr(obj(schema.Properties{prop("id", str()), prop("name", str()), prop("mode", ModeSchema)}))),
				p2runtime.StateProp("peopleUrl", union(str(), nul())),
			},
			Methods: []contracts.MethodEntry{
				// Creates the link of `mode`, or with `reset` replaces it (new token) and ends its guests' access.
				contracts.Meth("link", method(obj(schema.Properties{prop("mode", ModeSchema), opt("reset", schema.Bool()), opt("remoteConfirmed", schema.Bool())}))),
				// Shares the session with the person of `email`, or changes their mode. They open it through Radius, signed in
				// with that email.
				contracts.Meth("share", method(obj(schema.Properties{prop("email", str()), prop("mode", ModeSchema), opt("remoteConfirmed", schema.Bool())}))),
				// Ends one guest's or person's access to the session; a link keeps working for others.
				contracts.Meth("remove", method(obj(schema.Properties{prop("guestId", str())}))),
			},
		}),
	},
}))
