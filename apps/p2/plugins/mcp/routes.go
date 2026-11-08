package mcp

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/MichaelKinsy/PiG/apps/p2/plugins/mcp/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/host"
)

// Ports apps/p2/plugins/mcp/routes.ts: the browser routes of MCP sign-ins (owners only). `GET /mcp/sign-in/<server>` begins a
// sign-in and redirects to the authorization server: the browser returns to a loopback callback on the p2 host when it runs
// there, else to `GET /mcp/callback` on the origin it reaches p2 through (Radius, a public host). The device owner's browser
// runs on the p2 host; for any other owner a probe page finds out first (`LoopbackProbe`).

// signInRoutes contributes the sign-in routes.
func signInRoutes(self host.StoreSelf) error {
	p2runtime.ContributeKeyed(self, p2runtime.Routes,
		p2runtime.Entry("GET /mcp/sign-in/*", p2runtime.RouteHandler(signInRoute)),
		p2runtime.Entry("GET /mcp/callback", p2runtime.RouteHandler(callbackRoute)),
	)
	return nil
}

// callbackPath is the path of the callback route on p2's own origin.
const callbackPath = "/mcp/callback"

func manager(ctx host.Ctx) (*host.Reference, error) {
	return ctx.Get(contract.McpManager, delta.NewJsonObject(0), host.GetOptions{})
}

// signInRoute begins the sign-in to the server the path names. `?via=loopback|callback` is the probe page's answer; without
// it, the device owner signs in through loopback and any other owner gets the probe page.
func signInRoute(response http.ResponseWriter, request *http.Request, access p2runtime.RequestAccess, ctx host.Ctx) error {
	owner, isOwner := access.Principal.(p2runtime.OwnerPrincipal)
	if !isOwner {
		refuse(response, access.Principal == nil)
		return nil
	}
	name, err := url.PathUnescape(strings.TrimPrefix(request.URL.EscapedPath(), "/mcp/sign-in/"))
	if err != nil {
		SendPage(response, http.StatusBadRequest, ResultPage("Unknown MCP server", "The sign-in link is malformed."))
		return nil
	}
	via := request.URL.Query().Get("via")
	local := owner.Access == p2runtime.AccessLocal && !access.Origin.Remote
	reference, err := manager(ctx)
	if err != nil {
		return err
	}
	if !local && via != "loopback" && via != "callback" {
		probed, err := reference.Call("probe", delta.JsonObjectOf("origin", access.Origin.Origin), ctx)
		if err != nil {
			return err
		}
		return SendProbePage(response, name, stringOf(objectOf(probed).Value("url")))
	}
	redirect := delta.JsonObjectOf("kind", "route", "url", access.Origin.Origin+callbackPath)
	if local || via == "loopback" {
		redirect = delta.JsonObjectOf("kind", "loopback")
	}
	started, err := reference.Call("beginSignIn", delta.JsonObjectOf("server", name, "redirect", redirect), ctx)
	if err != nil {
		SendPage(response, http.StatusBadGateway, ResultPage("Signing in to "+name+" failed", err.Error()))
		return nil
	}
	if objectOf(started).Value("kind") == "authorized" {
		SendPage(response, http.StatusOK, ResultPage("Signed in to "+name, "You may close this page."))
		return nil
	}
	response.Header().Set("Location", stringOf(objectOf(started).Value("url")))
	response.WriteHeader(http.StatusFound)
	return nil
}

// callbackRoute completes a route sign-in with the authorization response the browser brings back.
func callbackRoute(response http.ResponseWriter, request *http.Request, access p2runtime.RequestAccess, ctx host.Ctx) error {
	params := request.URL.Query()
	if !p2runtime.IsOwner(access.Principal) {
		status := http.StatusForbidden
		if access.Principal == nil {
			status = http.StatusUnauthorized
		}
		SendPage(response, status, ResultPage("Sign-in not finished",
			"This browser is not signed in to p2 here. Copy this page's full address from the address bar and paste it in p2's MCP panel."))
		return nil
	}
	if !params.Has("state") {
		SendPage(response, http.StatusBadRequest, ResultPage("Sign-in not finished", "The authorization response has no state."))
		return nil
	}
	call := delta.JsonObjectOf("state", params.Get("state"))
	if params.Has("code") {
		call.Set("code", params.Get("code"))
	}
	if params.Has("iss") {
		call.Set("iss", params.Get("iss"))
	}
	if params.Has("error") {
		message := params.Get("error")
		if params.Has("error_description") {
			message = params.Get("error_description")
		}
		call.Set("error", message)
	}
	reference, err := manager(ctx)
	if err != nil {
		return err
	}
	completed, err := reference.Call("completeSignIn", call, ctx)
	if err != nil {
		SendPage(response, http.StatusBadRequest, ResultPage("Signing in failed", err.Error()))
		return nil
	}
	SendPage(response, http.StatusOK, ResultPage("Signed in to "+stringOf(objectOf(completed).Value("server")), "You may close this page."))
	return nil
}

// refuse: guests and people without a sign-in get no sign-in.
func refuse(response http.ResponseWriter, anonymous bool) {
	status := http.StatusForbidden
	if anonymous {
		status = http.StatusUnauthorized
	}
	SendPage(response, status, ResultPage("Not signed in", "Only p2's owner signs in to MCP servers. Open p2 and sign in first."))
}
