package host

import (
	"reflect"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/registries"
	"github.com/MichaelKinsy/PiG/chord/next/state"
)

// Ports packages/chord/test/next/host/facet.test.ts. `loaded` of upstream's support builds a host and loads one facet
// named "@test/app"; its setup runs through `setUp`, which is what these cases assert, so each runs setUp directly. The
// two cases that are TypeScript type-level checks are designed out (owner class a): their Go carriers are named on the
// tests below and in the lane's rows.

// scopeFacet is `loaded(setup)`: the facet "@test/app" set up with a host whose references are none.
func scopeFacet(setup func(host FacetHost, self FacetSelf) error) (facetSetUp, error) {
	return setUp("@test/app", DefineFacet(setup), func(*contracts.Contract) state.SourceHandle { return nil })
}

func scopeInfo(t testing.TB) *contracts.Contract {
	t.Helper()
	contract, err := contracts.Local("app.info", contracts.LocalDeclaration{
		Version: 1, Params: contracts.Params{}, Members: []contracts.LocalEntry{contracts.Mem("n", contracts.Int())},
	})
	if err != nil {
		t.Fatal(err)
	}
	return contract
}

func TestFacetsDeclareOnlyWhileTheirSetupRuns(t *testing.T) {
	Info := scopeInfo(t)
	var later FacetHost
	if _, err := scopeFacet(func(host FacetHost, _ FacetSelf) error {
		later = host
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	err := later.Serve(Info, func() {}, ServeOptions{})
	if err == nil || err.Error() != "@test/app: host.serve is valid only in the facet's setup" {
		t.Fatalf("err = %v", err)
	}
}

// Carries "facets › only declare: a setup takes the host and a self that only contributes; no documents, routines or
// natives" (a type-level check upstream): the Go types have exactly these members, and the facet's setup is one function.
func TestFacetsOnlyDeclareASetupTakesTheHostAndASelfThatOnlyContributes(t *testing.T) {
	names := func(of reflect.Type) []string {
		var got []string
		for method := range of.Methods() {
			got = append(got, method.Name)
		}
		return got
	}
	if got := names(reflect.TypeFor[FacetSelf]()); !reflect.DeepEqual(got, []string{"Contribute"}) {
		t.Fatalf("FacetSelf members = %v, want only Contribute", got)
	}
	if got := names(reflect.TypeFor[FacetHost]()); !reflect.DeepEqual(got, []string{"References", "Serve"}) {
		t.Fatalf("FacetHost members = %v, want References and Serve (no state, on or onLive)", got)
	}
	setup, ok := reflect.TypeFor[Facet]().FieldByName("Setup")
	if !ok || reflect.TypeFor[Facet]().NumField() != 1 ||
		setup.Type != reflect.TypeFor[func(FacetHost, FacetSelf) error]() {
		t.Fatalf("Facet is not exactly its setup(host FacetHost, self FacetSelf): %v", reflect.TypeFor[Facet]())
	}
}

func TestFacetsContributeAtMostOneValuePerRegistryOnlyWhileTheirSetupRuns(t *testing.T) {
	Names, err := registries.DefineRegistry[string, int]("app.names", registries.DefineRegistryOptions{Version: 1})
	if err != nil {
		t.Fatal(err)
	}
	token := registries.Registry[any, any](Names)
	_, err = scopeFacet(func(_ FacetHost, self FacetSelf) error {
		if err := self.Contribute(token, "a"); err != nil {
			return err
		}
		return self.Contribute(token, "b")
	})
	if err == nil || err.Error() != "@test/app: registry app.names is contributed to twice" {
		t.Fatalf("err = %v, want the twice error", err)
	}
	var later FacetSelf
	if _, err := scopeFacet(func(host FacetHost, self FacetSelf) error {
		provider := registries.RegistryProvider[string, int]{
			Compose: func(table registries.RegistryTable[string], _ registries.ComposeContext) (int, error) {
				return len(table), nil
			},
		}
		later = self
		return host.Serve(token, provider.Erase(), ServeOptions{})
	}); err != nil {
		t.Fatal(err)
	}
	err = later.Contribute(token, "late")
	if err == nil || err.Error() != "@test/app: self.contribute is valid only in the facet's setup" {
		t.Fatalf("err = %v", err)
	}
}

// Carries "facets › are typed by exactly the app's principal type" (a type-level check upstream): Go has no principal type
// parameter on a facet (a principal is data.Principal), so a facet is the one value DefineFacet makes of its setup, and
// nothing about it is checked at runtime, as in Pi.
func TestFacetsAreTypedByExactlyTheAppsPrincipalType(t *testing.T) {
	called := false
	facet := DefineFacet(func(FacetHost, FacetSelf) error {
		called = true
		return nil
	})
	if reflect.TypeOf(facet) != reflect.TypeFor[Facet]() {
		t.Fatalf("DefineFacet returned %T", facet)
	}
	if _, err := setUp("@test/app", facet, nil); err != nil || !called {
		t.Fatalf("setUp = %v, called %v", err, called)
	}
}

// Own tests of setUp: it returns what the setup served and contributed, in order, by kind (Pi's `setUp` doc comment).

func TestSetUpReturnsServedEntriesAndContributionsInOrderByKind(t *testing.T) {
	Info := scopeInfo(t)
	Names, err := registries.DefineRegistry[string, int]("app.names", registries.DefineRegistryOptions{Version: 1})
	if err != nil {
		t.Fatal(err)
	}
	Other, err := registries.DefineRegistry[string, int]("app.other", registries.DefineRegistryOptions{Version: 1})
	if err != nil {
		t.Fatal(err)
	}
	linger := 60 * time.Second
	references := 0
	got, err := setUp("@test/app", DefineFacet(func(host FacetHost, self FacetSelf) error {
		host.References(Info)
		provider := registries.RegistryProvider[string, int]{
			Compose: func(table registries.RegistryTable[string], _ registries.ComposeContext) (int, error) {
				return len(table), nil
			},
		}.Erase()
		if err := host.Serve(Info, "factory", ServeOptions{Linger: &linger}); err != nil {
			return err
		}
		if err := host.Serve(registries.Registry[any, any](Names), provider, ServeOptions{}); err != nil {
			return err
		}
		if err := self.Contribute(registries.Registry[any, any](Other), 1); err != nil {
			return err
		}
		return self.Contribute(registries.Registry[any, any](Names), "x")
	}), func(*contracts.Contract) state.SourceHandle {
		references++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if references != 1 {
		t.Fatalf("the host's references was asked %d times, want once", references)
	}
	if len(got.served) != 2 || got.served[0].kind != servedType || got.served[0].contract != Info ||
		got.served[0].factory != "factory" || *got.served[0].options.Linger != 60*time.Second ||
		got.served[1].kind != servedRegistry || got.served[1].registry.Name != "app.names" || got.served[1].provider.Compose == nil {
		t.Fatalf("served = %+v", got.served)
	}
	if len(got.contributions) != 2 || got.contributions[0].registry.Name != "app.other" || got.contributions[0].value != 1 ||
		got.contributions[1].registry.Name != "app.names" || got.contributions[1].value != "x" {
		t.Fatalf("contributions = %+v", got.contributions)
	}
}

func TestSetUpRefusesWhatIsNeitherAContractNorARegistryAndKeepsTheSetupsError(t *testing.T) {
	_, err := scopeFacet(func(host FacetHost, _ FacetSelf) error { return host.Serve("not a type", nil, ServeOptions{}) })
	if err == nil || err.Error() != "@test/app: host.serve takes a contract or a registry, not string" {
		t.Fatalf("err = %v", err)
	}
	Names, _ := registries.DefineRegistry[string, int]("app.names", registries.DefineRegistryOptions{Version: 1})
	_, err = scopeFacet(func(host FacetHost, _ FacetSelf) error {
		return host.Serve(registries.Registry[any, any](Names), "not a provider", ServeOptions{})
	})
	if err == nil {
		t.Fatal("a registry served with something that is not its provider was accepted")
	}
}

// Carries "native.test.ts › facets › cannot open natives" (`expectTypeOf<FacetHost>().not.toHaveProperty("open")`, a
// type-level check upstream; owner class a). The mechanism is the method sets of the Go types: a facet's setup gets a
// FacetHost and a FacetSelf, and neither declares Open (nor State, On, OnLive: a facet has no natives, documents or
// routines), so a setup cannot open a native, and the one value its setup does reach, the facetScope behind them, has no
// Open either; Open is a member of Self alone, the `self` of an object's factory, which is where natives live (opened
// during an attempt's activation and closed when it ends).
func TestFacetsCannotOpenNativesFacetHostDeclaresNoOpenMember(t *testing.T) {
	has := func(of reflect.Type, name string) bool {
		_, found := of.MethodByName(name)
		return found
	}
	for _, of := range []reflect.Type{reflect.TypeFor[FacetHost](), reflect.TypeFor[FacetSelf](), reflect.TypeFor[*facetScope]()} {
		for _, member := range []string{"Open", "State", "On", "OnLive"} {
			if has(of, member) {
				t.Errorf("%v has a %s member: a facet could declare it", of, member)
			}
		}
	}
	if !has(reflect.TypeFor[Self](), "Open") {
		t.Error("Self has no Open: an object's factory could not open natives either")
	}
	// The setup the facet is given is typed by those two interfaces alone: it cannot reach Self's members.
	setup, _ := reflect.TypeFor[Facet]().FieldByName("Setup")
	if setup.Type != reflect.TypeFor[func(FacetHost, FacetSelf) error]() {
		t.Fatalf("a facet's setup is %v, want func(FacetHost, FacetSelf) error", setup.Type)
	}
}
