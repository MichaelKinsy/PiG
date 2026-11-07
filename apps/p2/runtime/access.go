package runtime

import (
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
)

// Ports apps/p2/src/runtime/access.ts: who the browser is (a browser-safe contract): the owner, or a guest sessions
// were shared with. Served by the server host.

var modeSchema = contracts.Union([]contracts.Schema{contracts.Lit("view"), contracts.Lit("full")})

// GrantSchema is a guest's grant: every session shared with it (Sessions), and the one it lands in (Session, shared or
// joined last) with that session's Mode. Email is set for a person Radius verified; otherwise the name is
// self-chosen. Sessions is optional so that servers which share one session per guest (Radius Cloud Brain's copy of
// this contract) still serve the same UI; read modes through GrantMode.
var GrantSchema = contracts.Obj(
	contracts.Prop("id", contracts.Str()),
	contracts.Prop("name", contracts.Str()),
	contracts.Prop("session", contracts.Str()),
	contracts.Prop("mode", modeSchema),
	contracts.Prop("sessions", contracts.Opt(contracts.Map(modeSchema))),
	contracts.Prop("email", contracts.Opt(contracts.Str())),
)

// Grant is a guest's grant (GrantSchema). Sessions is nil where the grant has no `sessions` field.
type Grant struct {
	ID       string
	Name     string
	Session  string
	Mode     GuestMode
	Sessions map[string]GuestMode
	Email    *string
}

// GrantMode returns a guest's mode in session, and false when that session is not shared with it. A nil grant, or an
// absent session, is not shared.
func GrantMode(grant *Grant, session *string) (GuestMode, bool) {
	if grant == nil || session == nil {
		return "", false
	}
	if grant.Sessions != nil {
		mode, ok := grant.Sessions[*session]
		return mode, ok
	}
	if grant.Session == *session {
		return grant.Mode, true
	}
	return "", false
}

// Access is who the browser is, served by the server host (`p2.access`, version 1): the `owner` variant has no state,
// the `guest` variant has the guest's grant.
var Access = mustContract(contracts.Remote("p2.access", contracts.RemoteDeclaration{
	Version:     1,
	Params:      contracts.Params{},
	HostAddress: ServerHost,
	Variants: []contracts.VariantEntry{
		contracts.Var("owner", contracts.VariantDeclaration{State: []contracts.Property{}}),
		contracts.Var("guest", contracts.VariantDeclaration{State: []contracts.Property{contracts.Prop("grant", GrantSchema)}}),
	},
}))

func mustContract(contract *contracts.Contract, err error) *contracts.Contract {
	if err != nil {
		panic(err)
	}
	return contract
}
