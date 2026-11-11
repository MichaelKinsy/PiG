package host

// pi: packages/chord/test/examples/29-registries.ts

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/registries"
)

// --- 29-registries.ts ----------------------------------------------------------------------------------------------

// exHandler is a route's handler: it returns the response, or an error where Pi's throws.
type exHandler func(path string) (string, error)

// exRoutes is a plugin's contribution to the http registry: its routes, in the order it declares them.
type exRoutes struct {
	Routes []registries.RecordEntry[exHandler]
}

func TestExample29Registries(t *testing.T) {
	// 1. The token, shared by every plugin (bundled like a contract): routes in, a router out.
	http, err := registries.DefineRegistry[any, any]("example.http", registries.DefineRegistryOptions{Version: 1})
	must(t, err)
	// The HTTP server: it reads the router at each request and guards the contribution it calls.
	server := exLocal("example.server", contracts.Params{},
		contracts.Mem("request", contracts.NewMethod(exObj(exProp("path", exStr())), exStr())))
	// A plugin whose routes are known only at runtime: it contributes a writable it sets.
	mounts := exLocal("example.mounts", contracts.Params{}, contracts.Mem("mount", contracts.NewMethod(exObj(exProp("path", exStr())))))
	var logs exCollector
	// 2. The defining plugin serves the registry: the first route by plugin name wins, a duplicate is reported.
	httpPlugin := DefineFacet(func(h FacetHost, _ FacetSelf) error {
		if err := h.Serve(registries.Registry[any, any](http), registries.RegistryProvider[any, any]{
			Compose: func(table registries.RegistryTable[any], composeContext registries.ComposeContext) (any, error) {
				typed := make(registries.RegistryTable[exRoutes], len(table))
				for i, entry := range table {
					typed[i] = registries.RegistryEntry[exRoutes]{Plugin: entry.Plugin, Type: entry.Type, Version: entry.Version, Value: entry.Value.(exRoutes)}
				}
				return registries.KeyedEntries(typed, func(value exRoutes) []registries.RecordEntry[exHandler] { return value.Routes }, composeContext.Report), nil
			},
		}.Erase(), ServeOptions{}); err != nil {
			return err
		}
		return h.Serve(server, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			router := self.Registry(registries.Registry[any, any](http))
			return exImpl(nil, map[string]contracts.MethodFunction{
				"request": exMethod(func(args *delta.JsonObject, _ context.Context) (delta.JsonValue, error) {
					path := exParam(args, "path")
					value := router.Value()
					route, ok := value.(*registries.KeyedItems[exHandler]).Get(path)
					if !ok {
						return "404", nil
					}
					// A contribution is another plugin's code: catch, report its plugin, go on.
					response, err := route.Value(path)
					if err != nil {
						logs.add(fmt.Sprintf("route %s of %s threw: %v", path, route.Plugin, err))
						return "500", nil
					}
					return response, nil
				}),
			}), nil
		}, ServeOptions{})
	})
	// 3. Two plugins contribute from their facets: plain values, in the table while the plugin is loaded.
	notesPlugin := DefineFacet(func(_ FacetHost, self FacetSelf) error {
		return self.Contribute(registries.Registry[any, any](http), exRoutes{Routes: []registries.RecordEntry[exHandler]{
			{Key: "/notes", Value: func(string) (string, error) { return "notes", nil }},
			{Key: "/broken", Value: func(string) (string, error) {
				return "", errors.New("Expected property name or '}' in JSON at position 1")
			}},
		}})
	})
	searchPlugin := DefineFacet(func(_ FacetHost, self FacetSelf) error {
		return self.Contribute(registries.Registry[any, any](http), exRoutes{Routes: []registries.RecordEntry[exHandler]{
			{Key: "/search", Value: func(string) (string, error) { return "results", nil }},
			{Key: "/notes", Value: func(string) (string, error) { return "search's notes", nil }},
		}})
	})
	// 4. A host-lifetime object contributes a writable: its changes recompose the router.
	mountsPlugin := DefineFacet(func(h FacetHost, _ FacetSelf) error {
		return h.Serve(mounts, func(_ *delta.JsonObject, self Self) (contracts.Implementation, error) {
			routes := self.Writable(exRoutes{})
			self.(StoreSelf).Contribute(registries.Registry[any, any](http), routes)
			return exImpl(nil, map[string]contracts.MethodFunction{
				"mount": exMethod(func(args *delta.JsonObject, _ context.Context) (delta.JsonValue, error) {
					path := exParam(args, "path")
					current := routes.Value().(exRoutes)
					err := routes.Set(exRoutes{Routes: append(append([]registries.RecordEntry[exHandler](nil), current.Routes...),
						registries.RecordEntry[exHandler]{Key: path, Value: func(string) (string, error) { return "mounted " + path, nil }})})
					return nil, err
				}),
			}), nil
		}, ServeOptions{})
	})
	var conflicts exCollector
	errs := &hostErrors{}
	host := newHost(t, HostOptions{OnError: func(err HostError) {
		if err.Kind == "registry_conflict" {
			conflicts.add(fmt.Sprintf("%s %v", err.Registry.Message, err.Registry.Plugins))
			return
		}
		errs.push(err)
	}})
	// 5. A reader before `load` is refused at once (`not_ready`, "call host.load first"); `load` composes every registry
	// before any object.
	_, err = host.Registry(background, registries.Registry[any, any](http), GetOptions{})
	if chordErr := exCode(t, err, data.CodeNotReady, ""); !strings.Contains(chordErr.Message, "call host.load first") {
		t.Fatalf("before load: %v", err)
	}
	mustLoad(t)(host.Load([]HostFacet{
		{Name: "@example/http", Facet: httpPlugin}, {Name: "@example/notes", Facet: notesPlugin},
		{Name: "@example/search", Facet: searchPlugin}, {Name: "@example/mounts", Facet: mountsPlugin},
	}, background))
	value, err := host.Registry(background, registries.Registry[any, any](http), GetOptions{})
	must(t, err)
	read := struct{ value registries.RegistryValue[any] }{value}
	routerNow := func() []string {
		value := read.value.Value()
		var out []string
		for key, item := range value.(*registries.KeyedItems[exHandler]).All() {
			out = append(out, key+" ("+item.Plugin+")")
		}
		return out
	}
	if got, want := routerNow(), []string{"/notes (@example/notes)", "/broken (@example/notes)", "/search (@example/search)"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("routes: %v, want %v", got, want)
	}
	if got := conflicts.all(); len(got) != 1 || !strings.Contains(got[0], `"/notes" is contributed twice`) || !strings.Contains(got[0], "@example/notes @example/search") {
		t.Fatalf("conflicts = %v", got)
	}
	// 6. Requests: a duplicate went to the first plugin by name; a throwing contribution is guarded at the use site.
	serverRef := exGet(t, host, server, jsonObject())
	exEqual(t, "/notes", exCall(t, serverRef, "request", jsonObject("path", "/notes")), "notes")
	exEqual(t, "/broken", exCall(t, serverRef, "request", jsonObject("path", "/broken")), "500")
	if got := logs.all(); len(got) != 1 || !strings.HasPrefix(got[0], "route /broken of @example/notes threw: ") {
		t.Fatalf("guarded: %v", got)
	}
	// 7. A watch of the composed value gets each new router; mounting recomposes it.
	watch, err := read.value.Watch(background)
	must(t, err)
	var routers exCollector
	watch.Start(func(_ context.Context, call chord.NativeWatchCall[any]) error {
		if call.Kind == chord.WatchCallValue {
			var keys []string
			for key := range call.Value.(*registries.KeyedItems[exHandler]).All() {
				keys = append(keys, key)
			}
			routers.add(strings.Join(keys, ","))
		}
		return nil
	})
	mountsRef := exGet(t, host, mounts, jsonObject())
	exCall(t, mountsRef, "mount", jsonObject("path", "/files"))
	loggedRouters := -1
	scopeEventually(t, "the router with /files", func() bool {
		if n := len(routers.all()); n != loggedRouters {
			loggedRouters = n
			t.Logf("routers: %v", routers.all())
		}
		return slices_Contains(routers.all(), "/files,/notes,/broken,/search")
	})
	exEqual(t, "/files", exCall(t, serverRef, "request", jsonObject("path", "/files")), "mounted /files")
	_, _ = watch.Stop()
	exCloseHost(t, host)
	if errs.Len() > 0 {
		t.Fatalf("background errors: %v", errs.All())
	}
}
