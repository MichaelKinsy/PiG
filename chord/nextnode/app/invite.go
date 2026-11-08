package app

import (
	"sync"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/next/registries"
	chordhttp "github.com/MichaelKinsy/PiG/chord/nextnode/http"
	"github.com/MichaelKinsy/PiG/internal/jsclock"
)

// The invite plugin (slice 13 §3, decision "The app kit, point 2"): the owner (`AppInfo.owner`, the principal the app names)
// makes an invite link for a principal of the app it chooses (`POST /_chord/invites`, owner only: `{ principal, label }` →
// `{ path }`); the invitee opens it, its page posts the token (the URL's fragment, never logged) to `POST /_chord/invite`, which
// consumes it (single use, 7 days) and answers with a browser session for that principal (§2's sessions). Invites wait in memory:
// a restart drops the ones not used.
//
// Ports packages/chord/src/node/app/invite.ts (Pi main c23b9ec1).

const (
	// inviteMs is how long an invite is valid: 7 days.
	inviteMs = 7 * 24 * 60 * 60_000
	// InvitePath is the invite link's path: its page, and the post that consumes it.
	InvitePath = "/_chord/invite"
	// InvitesPath is where the owner makes invites.
	InvitesPath = "/_chord/invites"
)

// invite is an invite waiting: the principal it signs in, its label, its expiry.
type invite struct {
	principal data.Principal
	label     string
	expires   float64
}

// InviteFacet is the invite plugin's server facet; the owner (AppInfo.owner) makes the invites.
func InviteFacet() host.Facet {
	var mu sync.Mutex
	// By their token's hash: what the map holds is no credential.
	invites := map[string]invite{}
	take := func(hash string) (invite, bool) {
		mu.Lock()
		defer mu.Unlock()
		found, ok := invites[hash]
		delete(invites, hash)
		return found, ok
	}
	return host.DefineFacet(func(_ host.FacetHost, self host.FacetSelf) error {
		return self.Contribute(registries.Registry[any, any](chordhttp.Http), chordhttp.HttpContribution{Routes: []chordhttp.HttpRouteEntry{
			chordhttp.RouteFunc("POST "+InvitesPath, func(request chordhttp.HttpRequest, ctx host.Ctx) (chordhttp.HttpResponse, error) {
				owner, err := appOwner(ctx)
				if err != nil {
					return chordhttp.HttpResponse{}, err
				}
				if !data.SamePrincipal(ctx.Principal(), owner, ctx) {
					return chordhttp.HttpResponse{Kind: chordhttp.KindError, Status: 403}, nil
				}
				// A trust boundary: the owner's request, checked for its shape.
				body, err := request.JSON()
				if err != nil {
					return chordhttp.HttpResponse{}, err
				}
				object, _ := body.(*delta.JsonObject)
				if object == nil {
					return chordhttp.HttpResponse{Kind: chordhttp.KindError, Status: 400}, nil
				}
				principal, isObject := object.Value("principal").(*delta.JsonObject)
				label, isText := object.Value("label").(string)
				if !isObject || !isText {
					return chordhttp.HttpResponse{Kind: chordhttp.KindError, Status: 400}, nil
				}
				token := NewToken()
				mu.Lock()
				invites[HashToken(token)] = invite{principal: principal, label: label, expires: jsclock.DateNow() + inviteMs}
				mu.Unlock()
				return chordhttp.HttpResponse{Kind: chordhttp.KindJSON, Value: delta.JsonObjectOf("path", InvitePath+"#"+token)}, nil
			}),
			{Key: "GET " + InvitePath, Route: chordhttp.HttpRouteOptions{Public: true, Handler: func(request chordhttp.HttpRequest, _ host.Ctx) (chordhttp.HttpResponse, error) {
				return chordhttp.HttpResponse{Kind: chordhttp.KindHTML, Body: FragmentPage(InvitePath, request.Nonce())}, nil
			}}},
			{Key: "POST " + InvitePath, Route: chordhttp.HttpRouteOptions{Public: true, Handler: func(request chordhttp.HttpRequest, ctx host.Ctx) (chordhttp.HttpResponse, error) {
				// A trust boundary: anyone may post here.
				body, err := request.JSON()
				if err != nil {
					return chordhttp.HttpResponse{}, err
				}
				token, ok := postedToken(body)
				if !ok {
					return chordhttp.HttpResponse{Kind: chordhttp.KindError, Status: 403}, nil
				}
				found, ok := take(HashToken(token))
				if !ok || found.expires <= jsclock.DateNow() {
					return chordhttp.HttpResponse{Kind: chordhttp.KindError, Status: 403}, nil
				}
				issued, err := issueSession(ctx, found.principal, found.label, SessionBrowser, request.Provenance().Kind != chordhttp.ProvenanceLocal)
				if err != nil {
					return chordhttp.HttpResponse{}, err
				}
				return chordhttp.HttpResponse{
					Kind: chordhttp.KindJSON, Value: "signed in",
					Headers: []chordhttp.ResponseHeader{{Name: "Set-Cookie", Values: []string{issued.cookie}}},
				}, nil
			}}},
		}})
	})
}

// appOwner is AppInfo's owner principal, as ctx sees it.
func appOwner(ctx host.Ctx) (data.Principal, error) {
	info, err := ctx.Get(AppInfo, delta.NewJsonObject(0), host.GetOptions{})
	if err != nil {
		return nil, err
	}
	defer info.Release()
	value, err := info.Member("owner").Value()
	if err != nil {
		return nil, err
	}
	owner, _ := value.(*delta.JsonObject)
	return owner, nil
}
