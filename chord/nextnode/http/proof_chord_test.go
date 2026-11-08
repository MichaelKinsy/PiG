package http

import (
	"testing"

	"github.com/MichaelKinsy/PiG/chord/next/host"
)

// Proof of an HttpRoute (router.ts HttpRoute) as the composed router hands it out: the matched route's handler is the
// one contributed, with its params, and a route is private unless its options say Public (deny by default).

func TestAMatchedRouteIsTheContributedHandlerAndPrivateUnlessMarkedPublic(t *testing.T) {
	var private HttpRoute = func(request HttpRequest, ctx host.Ctx) (HttpResponse, error) {
		return HttpResponse{Kind: KindHTML, Body: "private"}, nil
	}
	router, conflicts := routerCompose(t, routerEntry{plugin: "@a/one", value: HttpContribution{Routes: []HttpRouteEntry{
		RouteFunc("GET /notes/:id", private),
		{Key: "GET /open", Route: HttpRouteOptions{Handler: routerRoute("open"), Public: true}},
	}}})
	if len(conflicts) != 0 {
		t.Fatalf("conflicts = %v", conflicts)
	}
	match, status := router.Route("GET", "/notes/7")
	if status != Found || match.Pattern != "GET /notes/:id" || match.Plugin != "@a/one" || match.Params["id"] != "7" {
		t.Fatalf("Route = %+v, %v", match, status)
	}
	if match.Public || match.Page || match.CrossSite {
		t.Fatalf("a route without options is %+v, want private, not a page, not cross-site", match)
	}
	response, err := match.Handler(nil, nil)
	if err != nil || response.Body != "private" {
		t.Fatalf("the matched handler answered %+v, %v", response, err)
	}
	open, status := router.Route("GET", "/open")
	if status != Found || !open.Public {
		t.Fatalf("Route(/open) = %+v, %v; want a public route", open, status)
	}
	if _, status := router.Route("GET", "/nope"); status == Found {
		t.Fatal("a path nobody contributed matched")
	}
}
