package host

import (
	"testing"

	"github.com/MichaelKinsy/PiG/chord/next/data"
)

// RootOptions.Anonymous wins over a Principal given with it (Pi's explicit `principal: undefined` is no choice of a principal).
func TestHostRootAnonymousWinsOverAPrincipalGivenWithIt(t *testing.T) {
	host := newHost(t, HostOptions{Principal: serverPrincipal(), OnError: func(HostError) {}})
	defer closeHosts(t, host)
	mustLoad(t)(host.Load(nil, background))
	var seen data.Principal
	_, err := host.Root(func(ctx Ctx) (any, error) {
		seen = ctx.Principal()
		return nil, nil
	}, background, RootOptions{Principal: jsonObject("kind", "owner"), Anonymous: true})
	must(t, err)
	if seen != nil {
		t.Fatalf("Anonymous with a Principal acts as %v, want none", seen)
	}
}
