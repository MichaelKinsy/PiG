package host

// Field survival of the registration types: every field a facet or a resource declaration is given comes back from the
// registry the host keeps it in. Pi reads these fields where the host stores them (host.ts install; object.ts
// resource), so a field dropped between the declaration and the registry changes what a reload keeps or replaces.

import (
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

// A FacetDefinition's Uses and Setup, and the facet's Name, survive Install into the host's facet registry.
func TestFacetDefinitionRegistrationKeepsUsesSetupAndName(t *testing.T) {
	uses := []*Contract{testContract("t.survive-a"), testContract("t.survive-b")}
	setups := 0
	definition := DefineFacet(func(FacetApi) error { setups++; return nil }, DefineFacetOptions{Uses: uses})
	if !reflect.DeepEqual(definition.Uses, uses) || definition.Setup == nil {
		t.Fatalf("DefineFacet dropped a field: Uses %v, Setup nil %v", definition.Uses, definition.Setup == nil)
	}
	host := quiet(t, nil)
	if err := host.Install(Facet{FacetDefinition: definition, Name: "survive"}, LoadOptions{}); err != nil {
		t.Fatal(err)
	}
	if setups != 1 {
		t.Fatalf("Setup ran %d times, want once", setups)
	}
	var registered Facet
	var found bool
	host.run(func() { registered, found = host.facets.Get("survive") })
	if !found || registered.Name != "survive" || !reflect.DeepEqual(registered.Uses, uses) {
		t.Fatalf("registered facet = %+v (found %v), want Name survive and Uses %v", registered, found, uses)
	}
	for i := range uses {
		if registered.Uses[i] != uses[i] {
			t.Fatalf("Uses[%d] is another contract than the one declared", i)
		}
	}
	if registered.Setup == nil {
		t.Fatal("the registered facet lost Setup")
	}
	if err := registered.Setup(FacetApi{}); err != nil || setups != 2 {
		t.Fatalf("registered Setup is not the declared function: err %v, runs %d", err, setups)
	}
}

// Every field of a ResourceDeclaration (Version, Deps, Exclusive, Open, Close) survives Self.Resource into the object's
// resource, and Open and Close are the declared functions.
func TestResourceDeclarationRegistrationKeepsVersionDepsExclusiveOpenAndClose(t *testing.T) {
	host := quiet(t, nil)
	log := &stringLog{}
	var dep, main *ResourceImpl
	local := emptyLocal("t.resource-survive")
	mustServe(t, host, local, func(_ any, _ Principal, self Self) (Implementation, error) {
		first, err := self.Resource("dep", ResourceDeclaration[any]{
			Version: 3,
			Open:    func(ResourceOpenOptions) (any, error) { log.add("open dep"); return "dep-value", nil },
		})
		if err != nil {
			return nil, err
		}
		second, err := self.Resource("main", ResourceDeclaration[any]{
			Version: 7, Deps: []any{first}, Exclusive: true,
			Open:  func(ResourceOpenOptions) (any, error) { log.add("open main"); return "main-value", nil },
			Close: func(value any) error { log.add("close " + value.(string)); return nil },
		})
		dep, main = resourceOf(first), resourceOf(second)
		return chordjson.ObjectOf(), err
	}, ServeOptions{})
	ref := mustRef(t, host, local, map[string]any{}, nil)
	if err := readyErr(t, ref); err != nil {
		t.Fatal(err)
	}
	if dep.spec.Version != 3 || dep.spec.Exclusive || len(dep.deps) != 0 || dep.spec.Close != nil {
		t.Fatalf("dep spec = %+v, deps %v: want Version 3, not Exclusive, no Deps, no Close", dep.spec, dep.deps)
	}
	if main.spec.Version != 7 || !main.spec.Exclusive || !main.exclusive() {
		t.Fatalf("main spec = %+v: want Version 7 and Exclusive", main.spec)
	}
	if len(main.deps) != 1 || main.deps[0] != any(dep) {
		t.Fatalf("main deps = %v, want the dep resource", main.deps)
	}
	if value, err := main.Value(); err != nil || value != "main-value" {
		t.Fatalf("main value = %v, %v", value, err)
	}
	if !log.equals("open dep", "open main") {
		t.Fatalf("open order = %v, want the dep before main", log.list())
	}
	ref.Release()
	waitFor(t, "main closed with its opened value", func() bool { return len(log.list()) == 3 })
	if !log.equals("open dep", "open main", "close main-value") {
		t.Fatalf("log = %v", log.list())
	}
}
