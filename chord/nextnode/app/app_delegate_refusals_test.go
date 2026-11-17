package app

import (
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
)

// Not upstream's (D605 claim 5): the server host's delegate to the plugins' Node host takes every principal (`Authorize` is always
// true) over a trusted link whose principal is the server's (`Trusted: true`), so the gate of a hold that goes over it is whatever the
// two hosts' tables say, not an authorization of the principal.
const secretPlugin = `import { ChordError, constant, defineFacet, hostKind, remote, str } from "@test/app/v1";
const Server = hostKind("server");
const Secret = remote("probe.secret", { version: 1, params: {}, hostAddress: () => Server(), variants: { main: { state: { who: str() } } } });
export default defineFacet((host) => host.serve(Secret, (_params, principal, _self) => {
	if (principal?.kind !== "owner") throw new ChordError({ code: "forbidden", message: "probe.secret: the owner's only" });
	return { variant: "main", members: { who: constant(JSON.stringify(principal)) } };
}));
`

func serverRemote(t *testing.T, name string) *contracts.Contract {
	t.Helper()
	return mustRemote(name, contracts.RemoteDeclaration{
		Version: 1, Params: contracts.Params{},
		HostAddress: func(*delta.JsonObject) (contracts.Address, error) { return Server.Address(nil), nil },
		Variants:    []contracts.VariantEntry{contracts.Var("main", contracts.VariantDeclaration{State: []contracts.Property{contracts.Prop("who", contracts.Str())}})},
	})
}

// heldAs is what holding the remote type contract on the app's server host as principal comes to inside its root scope (which
// releases the reference): the error of the Get, or the value of the member `who` once live.
func heldAs(t *testing.T, s started, principal data.Principal, contract *contracts.Contract) (who delta.JsonValue, err error) {
	t.Helper()
	_, err = s.app.Host.Root(func(ctx host.Ctx) (any, error) {
		ref, err := ctx.Get(contract, delta.NewJsonObject(0), host.GetOptions{})
		if err != nil {
			return nil, err
		}
		deadline := time.Now().Add(20 * time.Second)
		for time.Now().Before(deadline) && who == nil {
			who, _ = ref.Member("who").Value()
			time.Sleep(10 * time.Millisecond)
		}
		return nil, nil
	}, background, host.RootOptions{Principal: principal})
	return who, err
}

// What the delegate lets through is judged by the plugin's own object with the caller's principal, as in Pi's one host: the owner's
// hold of the plugin's object binds and the object saw the owner; a user's and the server's are refused by the object's own check
// (`forbidden`), not by the delegate, whose `Authorize` takes everyone.
func TestTheServersDelegateHandsThePluginsObjectTheCallersOwnPrincipalAndItsOwnCheckRefusesAnother(t *testing.T) {
	s := startTestApp(t, map[string]map[string]string{"secret": {"server.ts": secretPlugin}}, nil)
	who, err := heldAs(t, s, testOwner, serverRemote(t, "probe.secret"))
	if err != nil || who != `{"kind":"owner"}` {
		t.Fatalf("the owner's hold: who = %v, %v, want the owner", who, err)
	}
	for name, principal := range map[string]data.Principal{"user": delta.JsonObjectOf("kind", "user", "id", "u1"), "server": testServer} {
		if _, err := heldAs(t, s, principal, serverRemote(t, "probe.secret")); err == nil || !strings.Contains(err.Error(), "probe.secret: the owner's only") {
			t.Fatalf("a %s's hold: %v, want the object's own refusal", name, err)
		}
	}
}
