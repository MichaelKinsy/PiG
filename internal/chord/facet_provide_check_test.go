package chord

import (
	"context"
	"fmt"
	"slices"
	"testing"
)

type memberlessService struct{}

type remoteMember struct{}

func (*remoteMember) Count() int { return 1 }

// provideTrace provides implementation from a facet's setup and records, in order, what the provide call, the facet's
// activation and the host creation report.
func provideTrace[T any](t *testing.T, def ServiceDefinition[T], implementation T) []string {
	t.Helper()
	var trace []string
	facet := DefineFacet(Facet{Id: "f", Setup: func(env *FacetEnvironment) error {
		if err := ProvideService(env, def, implementation); err != nil {
			trace = append(trace, "provide threw: "+err.Error())
		} else {
			trace = append(trace, "provide ok")
		}
		return env.OnActivate(func(context.Context) error { trace = append(trace, "activate"); return nil })
	}})
	host, err := CreateFacetHost(t.Context(), FacetOptions{Facets: []Facet{facet}})
	if err != nil {
		return append(trace, "host threw: "+err.Error())
	}
	trace = append(trace, "host ok")
	if err := host.Dispose(t.Context()); err != nil {
		t.Fatal(err)
	}
	return trace
}

// Pi packages/chord/src/facets/host.ts:531-545: env.provide checks only that the implementation is an object, for local
// and remote services alike ("Service <id> implementation must be an object"). A remote implementation's members are
// classified when the host installs it (provider.ts:121), so a memberless or unexposable implementation is accepted by
// provide and fails createFacetHost. Each expected trace is what Pi's facet host produces for the same setup under Node.
func TestFacetProvideChecksOnlyForAnObjectAndTheHostClassifiesTheMembers(t *testing.T) {
	remoteNil := DefineService[Reader]("test.r.nil")
	localNil := DefineService[HostValues]("test.l.nil", ServiceOptions{Local: true})
	memberless := DefineService[*memberlessService]("test.r.memberless")
	unexposable := DefineService[*remoteMember]("test.r.unexposable")
	for _, c := range []struct {
		name  string
		trace []string
		want  []string
	}{
		{"remote nil", provideTrace(t, remoteNil, nil), []string{"provide threw: Service test.r.nil implementation must be an object", "activate", "host ok"}},
		{"local nil", provideTrace(t, localNil, nil), []string{"provide threw: Service test.l.nil implementation must be an object", "activate", "host ok"}},
		{"remote memberless", provideTrace(t, memberless, &memberlessService{}), []string{"provide ok", "host threw: Remote service test.r.memberless has no members"}},
		{"remote unexposable member", provideTrace(t, unexposable, &remoteMember{}), []string{"provide ok", "host threw: Remote service member test.r.unexposable.count is not remotely exposable"}},
	} {
		if !slices.Equal(c.trace, c.want) {
			t.Errorf("%s:\n got %s\nwant %s", c.name, fmt.Sprintf("%q", c.trace), fmt.Sprintf("%q", c.want))
		}
	}
}

func spawnTrace[T any](t *testing.T, def ServiceDefinition[T], key string, implementation T) []string {
	t.Helper()
	var trace []string
	facet := DefineFacet(Facet{Id: "f", Setup: func(env *FacetEnvironment) error {
		spawner, err := ProvideMany(env, def)
		if err != nil {
			return err
		}
		return env.OnActivate(func(context.Context) error {
			if _, err := spawner.Spawn(key, implementation); err != nil {
				trace = append(trace, "spawn threw: "+err.Error())
			} else {
				trace = append(trace, "spawn ok")
			}
			return nil
		})
	}})
	host, err := CreateFacetHost(t.Context(), FacetOptions{Facets: []Facet{facet}})
	if err != nil {
		return append(trace, "host threw: "+err.Error())
	}
	trace = append(trace, "host ok")
	if err := host.Dispose(t.Context()); err != nil {
		t.Fatal(err)
	}
	return trace
}

// Pi packages/chord/src/facets/host.ts:550-556: a provideMany spawn checks the key, then that the implementation is an
// object for local and remote services alike ("Facet service <id> implementation must be an object"), then classifies a
// remote implementation. Each expected trace is what Pi's facet host produces for the same spawn under Node.
func TestFacetSpawnChecksKeyThenObjectThenRemoteMembers(t *testing.T) {
	remote := DefineService[Reader]("test.many.remote")
	local := DefineService[HostValues]("test.many.local", ServiceOptions{Local: true})
	memberless := DefineService[*memberlessService]("test.many.memberless")
	for _, c := range []struct {
		name  string
		trace []string
		want  []string
	}{
		{"remote nil", spawnTrace(t, remote, "k", nil), []string{"spawn threw: Facet service test.many.remote implementation must be an object", "host ok"}},
		{"remote memberless", spawnTrace(t, memberless, "k", &memberlessService{}), []string{"spawn threw: Remote service test.many.memberless has no members", "host ok"}},
		{"local nil", spawnTrace(t, local, "k", nil), []string{"spawn threw: Facet service test.many.local implementation must be an object", "host ok"}},
		{"empty key nil", spawnTrace(t, remote, "", nil), []string{"spawn threw: Facet service instance key must not be empty", "host ok"}},
	} {
		if !slices.Equal(c.trace, c.want) {
			t.Errorf("%s:\n got %s\nwant %s", c.name, fmt.Sprintf("%q", c.trace), fmt.Sprintf("%q", c.want))
		}
	}
}

// Pi packages/chord/src/facets/host.ts:533 and :552 reject an implementation whose typeof is not "object", null, and an
// array, with the provide and spawn messages above. In Go a struct, a non-nil pointer or a non-nil map is an object;
// a string, a number, a slice, a function and a nil map are not.
func TestFacetProvideRejectsAnImplementationThatIsNotAnObject(t *testing.T) {
	local := DefineService[any]("test.l.any", ServiceOptions{Local: true})
	many := DefineService[any]("test.many.any", ServiceOptions{Local: true})
	notObject := []struct {
		name  string
		value any
	}{
		{"string", "x"},
		{"number", 1},
		{"boolean", true},
		{"slice", []any{}},
		{"function", func() {}},
		{"nil map", map[string]any(nil)},
	}
	for _, c := range notObject {
		if got, want := provideTrace(t, local, c.value), []string{"provide threw: Service test.l.any implementation must be an object", "activate", "host ok"}; !slices.Equal(got, want) {
			t.Errorf("provide %s: got %q, want %q", c.name, got, want)
		}
		if got, want := spawnTrace(t, many, "k", c.value), []string{"spawn threw: Facet service test.many.any implementation must be an object", "host ok"}; !slices.Equal(got, want) {
			t.Errorf("spawn %s: got %q, want %q", c.name, got, want)
		}
	}
	for _, c := range []struct {
		name  string
		value any
	}{{"struct", memberlessService{}}, {"pointer", &memberlessService{}}, {"map", map[string]any{}}} {
		if got, want := provideTrace(t, local, c.value), []string{"provide ok", "activate", "host ok"}; !slices.Equal(got, want) {
			t.Errorf("provide %s: got %q, want %q", c.name, got, want)
		}
	}
}
