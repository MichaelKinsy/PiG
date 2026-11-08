package app

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"sync"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/next/registries"
	chordhttp "github.com/MichaelKinsy/PiG/chord/nextnode/http"
	"github.com/MichaelKinsy/PiG/internal/jsclock"
)

// The owner (slice 13 §2, decisions "The app kit, point 3: authentication" and "The kit's owner is a principal the app chooses"):
// the owner is the principal the app names (`principal.owner`); no owner by loopback alone. At each start the kit makes a
// one-time owner link (a random token, single use, valid 10 minutes, the previous one dropped); the start script prints it. Its
// page reads the token from the URL's fragment (never sent to a server, so never in a log) and posts it; the post consumes it and
// sets a browser session cookie for the owner. The owner token file (mode 0600) carries a device session of the owner for
// command-line clients on the same machine.
//
// Ports packages/chord/src/node/app/owner.ts (Pi main c23b9ec1).

// ownerLinkMs is how long an owner link is valid: 10 minutes.
const ownerLinkMs = 10 * 60_000

// OwnerLink is the owner link's one-time token: made at each start (the previous one dropped), consumed by its first use.
type OwnerLink struct {
	mu      sync.Mutex
	hash    string
	has     bool
	expires float64
}

// Renew is a fresh token (the previous one no longer valid), valid for 10 minutes.
func (l *OwnerLink) Renew() string {
	token := NewToken()
	l.mu.Lock()
	defer l.mu.Unlock()
	l.hash, l.has = HashToken(token), true
	l.expires = jsclock.DateNow() + ownerLinkMs
	return token
}

// Consume is whether token is the current one and has not expired; it is used up either way when it matches.
func (l *OwnerLink) Consume(token string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.has || !MatchesHash(token, l.hash) {
		return false
	}
	l.has = false
	return jsclock.DateNow() < l.expires
}

// OwnerPath is the path of the owner link's page and post.
const OwnerPath = "/_chord/owner"

// OwnerFacet is the kit's facet for the owner link: `GET /_chord/owner`, a page posting the fragment's token, and `POST
// /_chord/owner`, which consumes it and answers with a session cookie of owner.
func OwnerFacet(link *OwnerLink, owner data.Principal) host.Facet {
	return host.DefineFacet(func(_ host.FacetHost, self host.FacetSelf) error {
		return self.Contribute(registries.Registry[any, any](chordhttp.Http), chordhttp.HttpContribution{Routes: []chordhttp.HttpRouteEntry{
			{Key: "GET " + OwnerPath, Route: chordhttp.HttpRouteOptions{Public: true, Handler: func(request chordhttp.HttpRequest, _ host.Ctx) (chordhttp.HttpResponse, error) {
				return chordhttp.HttpResponse{Kind: chordhttp.KindHTML, Body: FragmentPage(OwnerPath, request.Nonce())}, nil
			}}},
			{Key: "POST " + OwnerPath, Route: chordhttp.HttpRouteOptions{Public: true, Handler: func(request chordhttp.HttpRequest, ctx host.Ctx) (chordhttp.HttpResponse, error) {
				body, err := request.JSON()
				if err != nil {
					return chordhttp.HttpResponse{}, err
				}
				// A trust boundary: anyone may post here.
				token, ok := postedToken(body)
				if !ok || !link.Consume(token) {
					return chordhttp.HttpResponse{Kind: chordhttp.KindError, Status: 403}, nil
				}
				issued, err := issueSession(ctx, owner, "owner browser", SessionBrowser, request.Provenance().Kind != chordhttp.ProvenanceLocal)
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

// postedToken is the `token` string of a posted JSON body.
func postedToken(body any) (string, bool) {
	object, ok := body.(*delta.JsonObject)
	if !ok {
		return "", false
	}
	token, ok := object.Value("token").(string)
	return token, ok
}

// issuedSession is what Sessions' `issue` answered.
type issuedSession struct{ id, token, cookie string }

// issueSession issues a session of principal on the kit's Sessions in ctx.
func issueSession(ctx context.Context, principal data.Principal, label string, kind SessionKind, secure bool) (issuedSession, error) {
	sessions, err := ctx.(host.Ctx).Get(Sessions, delta.NewJsonObject(0), host.GetOptions{})
	if err != nil {
		return issuedSession{}, err
	}
	defer sessions.Release()
	args := delta.JsonObjectOf("principal", principal, "label", label, "kind", string(kind))
	if secure {
		args.Set("secure", true)
	}
	result, err := sessions.Call("issue", args, ctx)
	if err != nil {
		return issuedSession{}, err
	}
	object := result.(*delta.JsonObject)
	return issuedSession{id: object.Value("id").(string), token: object.Value("token").(string), cookie: object.Value("cookie").(string)}, nil
}

// FragmentPage is a sign-in page for a link with a token in its fragment (the owner link, an invite): its script (the response's
// nonce) posts the token to path, then opens the app.
func FragmentPage(path, nonce string) string {
	quoted, _ := json.Marshal(path)
	return strings.Join([]string{
		"<!doctype html><title>Sign in</title><p id=status>Signing in…</p>",
		`<script nonce="` + nonce + `">`,
		"const token = location.hash.slice(1); history.replaceState(null, '', location.pathname);",
		"fetch(" + string(quoted) + ", { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify({ token }) })",
		"  .then((r) => { if (r.ok) location.replace('/'); else document.getElementById('status').textContent = 'This link is used up or expired.'; });",
		"</script>",
	}, "\n")
}

// OwnerTokenFile is the owner token file at path: kept when its token still authenticates as owner (its session exists, its
// secret matches, it has not expired, it names owner; its mode set to 0600 again), else a new device session of owner written
// there (mode 0600, in one step), the old file's session revoked when it names another principal (the owner changed). It answers
// its token, for the start script.
func OwnerTokenFile(path string, owner data.Principal, h host.Host, ctx context.Context) (string, error) {
	existing, found, err := ReadSecret(path)
	if err != nil {
		return "", err
	}
	result, err := h.Root(func(ctx host.Ctx) (any, error) {
		sessions, err := ctx.Get(Sessions, delta.NewJsonObject(0), host.GetOptions{})
		if err != nil {
			return nil, err
		}
		defer sessions.Release()
		if found {
			// `verify` gives what `issue` stored: a principal of the app's type, or null.
			verified, err := sessions.Call("verify", existing, ctx)
			if err != nil {
				return nil, err
			}
			principal, _ := verified.(*delta.JsonObject)
			if data.SamePrincipal(principal, owner, ctx) {
				if err := os.Chmod(path, 0o600); err != nil {
					return nil, err
				}
				return existing, nil
			}
			// The kit made that session for the file; replacing the file ends it.
			if principal != nil {
				if _, err := sessions.Call("revoke", existing[:strings.IndexByte(existing, '.')], ctx); err != nil {
					return nil, err
				}
			}
		}
		issued, err := issueSession(ctx, owner, "owner token file", SessionDevice, false)
		if err != nil {
			return nil, err
		}
		mode := SecretCreate
		if found {
			mode = SecretReplace
		}
		if _, err := WriteSecret(path, issued.token, mode); err != nil {
			return nil, err
		}
		return issued.token, nil
	}, ctx, host.RootOptions{})
	if err != nil {
		return "", err
	}
	return result.(string), nil
}
