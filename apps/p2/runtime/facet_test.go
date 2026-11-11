package runtime_test

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
)

// p2's defineFacet (Pi's apps/p2/src/runtime/chord.ts, Pi main 682c7b93c; apps/p2/docs/rooms.md section 1, "Deny by default"): the
// factory of a remote type a facet serves never runs for a view principal unless the type is ForViews, however open the factory is.

func openType(name string) *contracts.Contract {
	contract, err := contracts.Remote(name, contracts.RemoteDeclaration{
		Version: 1, Params: contracts.Params{}, HostAddress: p2runtime.ServerHost,
		Variants: []contracts.VariantEntry{contracts.Var("any", contracts.VariantDeclaration{Methods: []contracts.MethodEntry{contracts.Meth("ping", contracts.NewMethod())}})},
	})
	if err != nil {
		panic(err)
	}
	return contract
}

func TestDefineFacetDeniesViewPrincipalsUnlessTheTypeIsForViews(t *testing.T) {
	plain, forViews := openType("p2.test-plain"), p2runtime.ForViews(openType("p2.test-for-views"))
	// An open factory: it admits every principal.
	open := host.RemoteFactory(func(*delta.JsonObject, data.Principal, host.Self) (contracts.Implementation, error) {
		return contracts.Implementation{Variant: "any", Members: contracts.VariantImplementation{Methods: map[string]contracts.MethodFunction{
			"ping": func(delta.JsonValue, context.Context) (delta.JsonValue, error) { return nil, nil },
		}}}, nil
	})
	server := p2test.NewTestHost(t, p2test.TestHostOptions{Facets: []host.Facet{p2runtime.DefineFacet(func(facet host.FacetHost, _ host.FacetSelf) error {
		if err := facet.Serve(plain, open, host.ServeOptions{}); err != nil {
			return err
		}
		return facet.Serve(forViews, open, host.ServeOptions{})
	})}})
	frame := server.Client(p2runtime.ViewPrincipal{Session: "s1", View: "room", Opening: "o1", Mode: p2runtime.ViewWrite, ForName: "x", ForRole: "owner", Link: "l1", Editor: "e1"})
	ref, err := frame.Hold(p2test.Background, plain, delta.NewJsonObject(0), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	failure, err := p2test.FailureOf(ref)
	if err != nil {
		t.Fatal(err)
	}
	p2test.ExpectChord(t, failure, data.CodeForbidden)
	if held, err := p2test.HoldIn(frame, forViews, delta.NewJsonObject(0), "any"); err != nil {
		t.Fatalf("a view principal holds a ForViews type: %v", err)
	} else {
		held.Release()
	}
	owner := server.Client(p2runtime.OwnerPrincipal{Access: p2runtime.AccessLocal})
	if held, err := p2test.HoldIn(owner, plain, delta.NewJsonObject(0), "any"); err != nil {
		t.Fatalf("the owner holds the plain type: %v", err)
	} else {
		held.Release()
	}
}
