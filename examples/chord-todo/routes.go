package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/next/registries"
	chordhttp "github.com/MichaelKinsy/PiG/chord/nextnode/http"
)

// routes is the lists plugin on the server: a JSON API for command-line clients. A route runs as whoever sent the request (the kit
// answered 401 before this code runs when nobody signed in), so its `ctx.Get` reaches the list's worker as them. A Go app's server
// facet is a Go facet the app loads (`Facets`): a plugin directory is TypeScript the Node host evaluates, and its routes are code
// of the server's own process.
func routes() host.Facet {
	return host.DefineFacet(func(_ host.FacetHost, self host.FacetSelf) error {
		return self.Contribute(registries.Registry[any, any](chordhttp.Http), chordhttp.HttpContribution{Routes: []chordhttp.HttpRouteEntry{
			chordhttp.RouteFunc("GET /api/lists/:list", func(request chordhttp.HttpRequest, ctx host.Ctx) (chordhttp.HttpResponse, error) {
				list, err := ctx.Get(todos, delta.JsonObjectOf("list", request.Params()["list"]), host.GetOptions{})
				if err != nil {
					return chordhttp.HttpResponse{}, err
				}
				items, err := list.Member("items").Value()
				return chordhttp.HttpResponse{Kind: chordhttp.KindJSON, Value: items}, err
			}),
			chordhttp.RouteFunc("POST /api/lists/:list", func(request chordhttp.HttpRequest, ctx host.Ctx) (chordhttp.HttpResponse, error) {
				list, err := ctx.Get(todos, delta.JsonObjectOf("list", request.Params()["list"]), host.GetOptions{})
				if err != nil {
					return chordhttp.HttpResponse{}, err
				}
				text, err := request.JSON()
				if err != nil {
					return chordhttp.HttpResponse{}, err
				}
				if _, err := list.Call("add", fmt.Sprint(text), ctx); err != nil {
					return chordhttp.HttpResponse{}, err
				}
				return chordhttp.HttpResponse{Kind: chordhttp.KindJSON, Value: "added"}, nil
			}),
		}})
	})
}

// routesStep is chapter III: a command-line client on this machine sends the owner token file as a bearer token; the route's
// `ctx.Get` acts as the request's principal, so the todo is the owner's.
func (s *session) routesStep() error {
	text, err := os.ReadFile(s.app.OwnerTokenFile)
	if err != nil {
		return err
	}
	s.token = strings.TrimSpace(string(text))
	cli := s.cli()
	_, posted, err := s.request(http.MethodPost, "/api/lists/groceries", []byte(`"butter"`), map[string]string{"Authorization": cli["Authorization"], "Content-Type": "application/json"})
	if err != nil {
		return err
	}
	var answer string
	if err := json.Unmarshal(posted, &answer); err != nil {
		return err
	}
	fmt.Fprintln(s.out, "3. POST /api/lists/groceries:", answer)
	_, listed, err := s.request(http.MethodGet, "/api/lists/groceries", nil, cli)
	if err != nil {
		return err
	}
	items, err := itemsOf(json.RawMessage(listed))
	if err != nil {
		return err
	}
	fmt.Fprintln(s.out, "   GET /api/lists/groceries:", show(items))
	status, _, err := s.request(http.MethodGet, "/api/lists/groceries", nil, nil)
	fmt.Fprintln(s.out, "   GET without a token:", status)
	return err
}
