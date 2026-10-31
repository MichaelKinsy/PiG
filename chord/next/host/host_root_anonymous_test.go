package host

import (
	"testing"

	"github.com/MichaelKinsy/PiG/chord/next/data"
)

// Pi's `host.root(fn, context, { principal: undefined })` runs anonymous: an explicit `undefined` is not an omitted
// `principal`, which acts as the host's own. Go has no undefined, so RootOptions.Anonymous is that form (the HTTP component's
// scopes of requests nobody authenticated).
func TestHostRootAnonymousRunsWithNoPrincipalWhereAnOmittedOneActsAsTheHost(t *testing.T) {
	host := newHost(t, HostOptions{Principal: serverPrincipal(), OnError: func(HostError) {}})
	defer closeHosts(t, host)
	must(t, host.Load([]HostFacet{}, background))
	principalOf := func(options RootOptions) data.Principal {
		var seen data.Principal
		_, err := host.Root(func(ctx Ctx) (any, error) {
			seen = ctx.Principal()
			return nil, nil
		}, background, options)
		must(t, err)
		return seen
	}
	if got := principalOf(RootOptions{}); got == nil || principalKind(got) != "server" {
		t.Fatalf("an omitted principal acts as %v, want the host's", got)
	}
	if got := principalOf(RootOptions{Anonymous: true}); got != nil {
		t.Fatalf("an anonymous scope acts as %v", got)
	}
	owner := jsonObject("kind", "owner")
	if got := principalOf(RootOptions{Principal: owner, Anonymous: true}); got != nil {
		t.Fatalf("Anonymous with a Principal acts as %v, want none", got)
	}
}
