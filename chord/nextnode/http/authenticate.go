package http

import (
	"context"

	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/registries"
)

// Ports packages/chord/src/next-node/http/authenticate.ts. Who someone is (decision "The HTTP server is a native object",
// replacing the `authenticate` option): the registry `Authenticators`, one function per contributor, asked in contributor
// order (plugin name: an order with no meaning, so an app needing precedence between credentials contributes one
// authenticator that decides); the first principal returned wins, a throwing one is reported and skipped.
//
// Forced Go differences. An authenticator is blocking (Pi's `Principal | undefined | Promise<...>`); it returns a nil
// principal for Pi's `undefined` and an error for a throw (a panic is recovered as one).

// Authenticator is who request is, or nil (anonymous here, maybe another authenticator knows).
type Authenticator func(request HttpRequest, ctx context.Context) (data.Principal, error)

// AuthenticatorEntry is an authenticator with its plugin, as the component asks it.
type AuthenticatorEntry struct {
	Plugin       string
	Authenticate Authenticator
}

// Authenticators is the registry of authenticators (served by `http.facet`).
var Authenticators = must(registries.DefineRegistry[Authenticator, []AuthenticatorEntry]("chord.http.authenticators", registries.DefineRegistryOptions{Version: 1}))

// composeAuthenticators composes `Authenticators`: every contribution in contributor order.
func composeAuthenticators(table registries.RegistryTable[Authenticator], _ registries.ComposeContext) ([]AuthenticatorEntry, error) {
	entries := make([]AuthenticatorEntry, len(table))
	for i, entry := range table {
		entries[i] = AuthenticatorEntry{Plugin: entry.Plugin, Authenticate: entry.Value}
	}
	return entries, nil
}

// authenticate is the principal of request: the first one an authenticator returns, in order; one that throws is reported
// through failed and skipped; none: nil (anonymous). Once ctx is aborted no further authenticator starts.
func authenticate(authenticators []AuthenticatorEntry, request HttpRequest, failed func(plugin string, err error), ctx context.Context) data.Principal {
	for _, entry := range authenticators {
		// A request gone or sealed starts no new work.
		if ctx.Err() != nil {
			return nil
		}
		principal, err := tryAuthenticate(entry.Authenticate, request, ctx)
		if err != nil {
			failed(entry.Plugin, err)
			continue
		}
		if principal != nil {
			return principal
		}
	}
	return nil
}

func tryAuthenticate(authenticator Authenticator, request HttpRequest, ctx context.Context) (principal data.Principal, err error) {
	defer recoverAs(&err)
	return authenticator(request, ctx)
}
