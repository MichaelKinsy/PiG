package app

import (
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/next/registries"
	chordhttp "github.com/MichaelKinsy/PiG/chord/nextnode/http"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// The ephemeral login plugin (slice 13 §3, decision "The app kit, point 2"): an IRC-like "pick a name and you're in".
// `POST /_chord/login` (public: `{ name }`) issues an ephemeral session for `{ kind: "user", id: <fresh>, name }` (the app's
// principal type must allow it) and answers with its cookie. No account is stored. The session ends with the browser (its cookie
// has no lifetime) or the server (ephemeral sessions are dropped whenever it starts).
//
// Ports packages/chord/src/node/app/ephemeral.ts (Pi main c23b9ec1).

// LoginPath is where the ephemeral login takes a name.
const LoginPath = "/_chord/login"

// maxLoginName is the longest name taken, in UTF-16 code units.
const maxLoginName = 40

// EphemeralUser is the principal an ephemeral login issues a session for: `{ kind: "user", id, name }`.
type EphemeralUser struct {
	Kind string
	ID   string
	Name string
}

// Principal is the user as a principal (a JSON object).
func (u EphemeralUser) Principal() *delta.JsonObject {
	return delta.JsonObjectOf("kind", u.Kind, "id", u.ID, "name", u.Name)
}

// EphemeralLoginFacet is the ephemeral login plugin's server facet.
func EphemeralLoginFacet() host.Facet {
	return host.DefineFacet(func(_ host.FacetHost, self host.FacetSelf) error {
		return self.Contribute(registries.Registry[any, any](chordhttp.Http), chordhttp.HttpContribution{Routes: []chordhttp.HttpRouteEntry{
			{Key: "POST " + LoginPath, Route: chordhttp.HttpRouteOptions{Public: true, Handler: func(request chordhttp.HttpRequest, ctx host.Ctx) (chordhttp.HttpResponse, error) {
				// A trust boundary: anyone may log in under a name.
				body, err := request.JSON(4096)
				if err != nil {
					return chordhttp.HttpResponse{}, err
				}
				name := ""
				if object, ok := body.(*delta.JsonObject); ok {
					if text, ok := object.Value("name").(string); ok {
						name = jsstring.Slice(jsstring.Trim(text), 0, maxLoginName)
					}
				}
				if name == "" {
					return chordhttp.HttpResponse{Kind: chordhttp.KindError, Status: 400}, nil
				}
				principal := EphemeralUser{Kind: "user", ID: NewToken(12), Name: name}.Principal()
				issued, err := issueSession(ctx, principal, name, SessionEphemeral, request.Provenance().Kind != chordhttp.ProvenanceLocal)
				if err != nil {
					return chordhttp.HttpResponse{}, err
				}
				return chordhttp.HttpResponse{
					Kind: chordhttp.KindJSON, Value: principal,
					Headers: []chordhttp.ResponseHeader{{Name: "Set-Cookie", Values: []string{issued.cookie}}},
				}, nil
			}}},
		}})
	})
}
