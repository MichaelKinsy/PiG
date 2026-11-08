package runtime

import (
	"context"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
)

// Ports apps/p2/src/runtime/chord.ts: Chord v2's API with p2's principals. Upstream re-exports Chord's types
// specialised to `P2Principal` (Ctx, Host, Facet, Factory, Self, ...); Go's Chord host carries principals as
// data.Principal, and runtime.ToChord / FromChord (principals.go) convert at the boundary, so those aliases have no Go
// form. What remains is p2's two host kinds and the constructors every p2 host uses.

// ServerKind is the server: one host (`hostKind("server")`).
var ServerKind = mustKind("server")

// SessionKind is a session's worker: one per session (`hostKind("session", { id: str() })`).
var SessionKind = mustKind("session", contracts.Prop("id", contracts.Str()))

func mustKind(name string, params ...contracts.Property) *contracts.HostKind {
	kind, err := contracts.NewHostKind(name, params...)
	if err != nil {
		panic(err)
	}
	return kind
}

// ServerHost is where server objects live (a contract's `hostAddress`: `serverHost`).
func ServerHost(*delta.JsonObject) (contracts.Address, error) {
	return ServerKind.Address(delta.NewJsonObject(0)), nil
}

// SessionHost is where session objects live: the worker of the params' `session` (a contract's `hostAddress`:
// `sessionHost`).
func SessionHost(params *delta.JsonObject) (contracts.Address, error) {
	return SessionKind.Address(delta.JsonObjectOf("id", params.Value("session"))), nil
}

// NewHost is a p2 host (`createHost`): principals keyed and revoked the same way on every host. options' PrincipalKey and
// RevokedBy are set when absent; Principal is the host's own as a Chord principal (ToChord of a p2 Principal).
func NewHost(options host.HostOptions) (host.Host, error) {
	if options.PrincipalKey == nil {
		options.PrincipalKey = PrincipalKeyFunction
	}
	if options.RevokedBy == nil {
		options.RevokedBy = ChordRevokedBy
	}
	return host.CreateHost(options)
}

// Forbidden is Chord's `forbidden(message)`: what a plugin throws for a refused principal.
func Forbidden(message string) error {
	return data.NewChordError(data.ChordErrorInit{Code: data.CodeForbidden, Message: message}, nil)
}

// CtxOf is the Chord call context of a method handler's ctx.
func CtxOf(ctx context.Context) host.Ctx {
	if each, ok := ctx.(host.Ctx); ok {
		return each
	}
	return nil
}
