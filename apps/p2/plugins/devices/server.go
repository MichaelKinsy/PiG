// Package devices is p2's devices plugin (the server facet of apps/p2/plugins/devices/server.ts): owner sign-in from other
// devices by one-time tokens and signed cookies, the owner's Devices object, and sign-out of one or all devices. Its TUI
// facet (tui.ts) belongs to the TUI lane.
package devices

import (
	"context"
	"io"
	"net/http"
	"strings"

	"github.com/MichaelKinsy/PiG/apps/p2/hosts"
	"github.com/MichaelKinsy/PiG/apps/p2/plugins/devices/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/chord/next/state"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

// Ports apps/p2/plugins/devices/server.ts.

const maxClaimBytes = 1024

func objectOf(value any) *delta.JsonObject {
	object, _ := value.(*delta.JsonObject)
	return object
}

func forbid(response http.ResponseWriter, request *http.Request, message string) error {
	_, _ = io.Copy(io.Discard, request.Body)
	response.WriteHeader(http.StatusForbidden)
	_, err := io.WriteString(response, message)
	return err
}

func serveDeviceAccess(facet host.FacetHost) error {
	return facet.Serve(contract.DeviceAccess, host.LocalFactory(func(_ *delta.JsonObject, self host.Self) (contracts.Implementation, error) {
		server := self.Get(p2runtime.ServerLocal, delta.NewJsonObject(0))
		info := func() (*delta.JsonObject, error) {
			value, err := server.Member("info").Value()
			return objectOf(value), err
		}
		opened := self.Open(func(context.Context) (any, error) {
			value, err := info()
			if err != nil {
				return nil, err
			}
			directory, _ := value.Value("dataDir").(string)
			return contract.OpenOwnerAccess(directory, nil)
		}, func(value any, _ host.NativeCloseReason, _ context.Context) error {
			value.(*contract.OwnerAccess).Close()
			return nil
		})
		access := func() *contract.OwnerAccess {
			value, err := opened.Value()
			if err != nil {
				panic(err)
			}
			return value.(*contract.OwnerAccess)
		}
		store := self.(host.StoreSelf)
		store.Contribute(contract.DeviceAccesses, &contract.DeviceAccessParts{Access: access})
		// A local session counts only on loopback: a copied cookie on a tunnel host or through Radius is nobody.
		principal := func(request *http.Request, origin p2runtime.RequestOrigin) p2runtime.Principal {
			session := access().Session(request, origin.Port)
			switch {
			case session == nil:
			case session.Kind == contract.SessionDevice:
				return p2runtime.OwnerPrincipal{Access: p2runtime.AccessDevice, Device: session.ID}
			case !origin.Remote:
				return p2runtime.OwnerPrincipal{Access: p2runtime.AccessLocal}
			}
			return nil
		}
		p2runtime.ContributeKeyed(store, p2runtime.Authenticators, p2runtime.Entry("owner", p2runtime.Authenticator(
			func(request *http.Request, origin p2runtime.RequestOrigin, _ host.Ctx) (p2runtime.Principal, error) {
				return principal(request, origin), nil
			})))
		// A valid owner cookie takes precedence over a pre-existing guest cookie. Host checks still apply.
		p2runtime.ContributeKeyed(store, p2runtime.RequestResolvers, p2runtime.Entry("ownerCookie", p2runtime.RequestResolver(
			func(request *http.Request, _ host.Ctx) (*p2runtime.RequestAccess, error) {
				value, err := info()
				if err != nil {
					return nil, nil
				}
				var allowed []string
				list, _ := value.Value("allowedHosts").([]any)
				for _, each := range list {
					text, _ := each.(string)
					allowed = append(allowed, text)
				}
				origin := hosts.RequestOrigin(request, allowed)
				if origin == nil || !origin.Remote {
					return nil, nil
				}
				if owner := principal(request, *origin); owner != nil {
					return &p2runtime.RequestAccess{Origin: *origin, Principal: owner}, nil
				}
				return nil, nil
			})))
		// A local session in use does not expire: each app load renews it for another 30 days.
		p2runtime.ContributeKeyed(store, p2runtime.PageCookies, p2runtime.Entry("localSession", p2runtime.PageCookie(
			func(request *http.Request, access2 p2runtime.RequestAccess) string {
				if access2.Origin.Remote {
					return ""
				}
				if session := access().Session(request, access2.Origin.Port); session != nil && session.Kind == contract.SessionLocal {
					return access().LocalCookie(access2.Origin.Port, session.ID)
				}
				return ""
			})))
		// sameOriginPost is a same-origin POST that no other site's page made; refused (403) otherwise.
		sameOriginPost := func(response http.ResponseWriter, request *http.Request, origin p2runtime.RequestOrigin) (bool, error) {
			if p2runtime.FromOrigin(request, origin.Origin) && request.Header.Get("Sec-Fetch-Site") != "cross-site" {
				return true, nil
			}
			return false, forbid(response, request, "Invalid origin")
		}
		logout := func(response http.ResponseWriter, request *http.Request, access2 p2runtime.RequestAccess, all bool, ctx host.Ctx) error {
			if ok, err := sameOriginPost(response, request, access2.Origin); !ok {
				return err
			}
			if all && !p2runtime.IsOwner(access2.Principal) {
				return forbid(response, request, "Forbidden")
			}
			_, _ = io.Copy(io.Discard, request.Body)
			current := access()
			session := current.Session(request, access2.Origin.Port)
			var failure error
			if all {
				failure = current.Rotate()
			} else if session != nil {
				failure = current.Logout(*session)
			}
			// Local sessions share one principal: ending one leaves the others' live links alone.
			if all || (session != nil && session.Kind == contract.SessionDevice) {
				revoke := delta.NewJsonObject(0)
				if !all {
					revoke = delta.JsonObjectOf("device", session.ID)
				}
				if _, err := server.Call("revokeOwners", revoke, ctx); err != nil && failure == nil {
					failure = err
				}
			}
			if failure != nil {
				return failure
			}
			return p2runtime.SendJSON(response, http.StatusOK, map[string]any{"ok": true},
				map[string]string{"Set-Cookie": current.ClearCookie(access2.Origin.Port, access2.Origin.Remote)})
		}
		signIn := func(response http.ResponseWriter, request *http.Request, _ p2runtime.RequestAccess, _ host.Ctx) error {
			p2runtime.SignIn(response, request, http.StatusOK, p2runtime.SignInOptions{})
			return nil
		}
		p2runtime.ContributeKeyed(store, p2runtime.Routes,
			p2runtime.Entry("GET /owner", p2runtime.RouteHandler(signIn)),
			p2runtime.Entry("HEAD /owner", p2runtime.RouteHandler(signIn)),
			p2runtime.Entry("GET /owner/check", p2runtime.RouteHandler(func(response http.ResponseWriter, request *http.Request, access2 p2runtime.RequestAccess, _ host.Ctx) error {
				if !p2runtime.IsOwner(access2.Principal) {
					p2runtime.SignIn(response, request, http.StatusUnauthorized, p2runtime.SignInOptions{})
					return nil
				}
				return p2runtime.SendJSON(response, http.StatusOK, map[string]any{"owner": true}, nil)
			})),
			p2runtime.Entry("POST /owner/logout", p2runtime.RouteHandler(func(response http.ResponseWriter, request *http.Request, access2 p2runtime.RequestAccess, ctx host.Ctx) error {
				return logout(response, request, access2, false, ctx)
			})),
			p2runtime.Entry("POST /owner/logout-all", p2runtime.RouteHandler(func(response http.ResponseWriter, request *http.Request, access2 p2runtime.RequestAccess, ctx host.Ctx) error {
				return logout(response, request, access2, true, ctx)
			})),
			// Only a local owner's own request: no browser fetch of another origin, not even of this site.
			p2runtime.Entry("GET /owner-token", p2runtime.RouteHandler(func(response http.ResponseWriter, request *http.Request, access2 p2runtime.RequestAccess, _ host.Ctx) error {
				owner, isOwner := access2.Principal.(p2runtime.OwnerPrincipal)
				// Node joins repeated header lines with ", ".
				values := request.Header.Values("Sec-Fetch-Site")
				site := strings.Join(values, ", ")
				if !isOwner || owner.Access != p2runtime.AccessLocal || access2.Origin.Remote || (len(values) > 0 && site != "same-origin") {
					return forbid(response, request, "Forbidden")
				}
				return p2runtime.SendJSON(response, http.StatusOK, map[string]any{"token": access().IssueToken()}, nil)
			})),
			// On loopback the claim is a local session: the invitation came from a local owner to someone at this machine.
			p2runtime.Entry("POST /owner/claim", p2runtime.RouteHandler(func(response http.ResponseWriter, request *http.Request, access2 p2runtime.RequestAccess, _ host.Ctx) error {
				if ok, err := sameOriginPost(response, request, access2.Origin); !ok {
					return err
				}
				value, ok, err := readJSON(response, request)
				if !ok {
					return err
				}
				token, isString := fieldOf(value, "token").(string)
				if !isString || !access().Claim(token) {
					response.WriteHeader(http.StatusForbidden)
					_, err = io.WriteString(response, "Invalid one-time token")
					return err
				}
				cookie := access().LocalCookie(access2.Origin.Port, "")
				if access2.Origin.Remote {
					cookie = access().Cookie(access2.Origin.Port, true)
				}
				return p2runtime.SendJSON(response, http.StatusOK, map[string]any{"ok": true}, map[string]string{"Set-Cookie": cookie})
			})),
			// Local sign-in (runtime/local_owner.go): a challenge, then its answer under the owner key.
			p2runtime.Entry("POST "+p2runtime.LocalChallengePath, p2runtime.RouteHandler(func(response http.ResponseWriter, request *http.Request, access2 p2runtime.RequestAccess, _ host.Ctx) error {
				_, _ = io.Copy(io.Discard, request.Body)
				if access2.Origin.Remote {
					return forbid(response, request, "Forbidden")
				}
				if ok, err := sameOriginPost(response, request, access2.Origin); !ok {
					return err
				}
				return p2runtime.SendJSON(response, http.StatusOK, map[string]any{"challenge": access().IssueChallenge()}, nil)
			})),
			p2runtime.Entry("POST "+p2runtime.LocalClaimPath, p2runtime.RouteHandler(func(response http.ResponseWriter, request *http.Request, access2 p2runtime.RequestAccess, _ host.Ctx) error {
				if access2.Origin.Remote {
					return forbid(response, request, "Forbidden")
				}
				if ok, err := sameOriginPost(response, request, access2.Origin); !ok {
					return err
				}
				value, ok, err := readJSON(response, request)
				if !ok {
					return err
				}
				challenge, _ := fieldOf(value, "challenge").(string)
				proof, _ := fieldOf(value, "proof").(string)
				if !access().ProveLocal(challenge, proof, access2.Origin.Port) {
					response.WriteHeader(http.StatusForbidden)
					_, err = io.WriteString(response, "Invalid local sign-in")
					return err
				}
				return p2runtime.SendJSON(response, http.StatusOK, map[string]any{"ok": true},
					map[string]string{"Set-Cookie": access().LocalCookie(access2.Origin.Port, "")})
			})),
		)
		return contracts.Implementation{}, nil
	}), host.ServeOptions{})
}

func serveDevices(facet host.FacetHost) error {
	return facet.Serve(contract.Devices, host.RemoteFactory(func(_ *delta.JsonObject, principal data.Principal, self host.Self) (contracts.Implementation, error) {
		who, err := p2runtime.FromChord(principal)
		owner, isOwner := who.(p2runtime.OwnerPrincipal)
		if err != nil || !isOwner {
			return contracts.Implementation{}, p2runtime.Forbidden("Devices is owner-only")
		}
		self.Get(contract.DeviceAccess, delta.NewJsonObject(0))
		access := contract.DeviceAccessesOf(self)
		local := owner.Access == p2runtime.AccessLocal
		return contracts.Implementation{Variant: "owner", Members: contracts.VariantImplementation{
			State: map[string]chord.Watchable[delta.JsonValue]{"canMint": host.Expose(state.NewConstant(delta.JsonValue(local)))},
			Methods: map[string]contracts.MethodFunction{
				"token": func(delta.JsonValue, context.Context) (delta.JsonValue, error) {
					if !local {
						return nil, p2runtime.Forbidden("Generate owner links on the host with npm run owner-link")
					}
					parts, err := access()
					if err != nil {
						return nil, err
					}
					return delta.JsonObjectOf("token", parts.Access().IssueToken()), nil
				},
			},
		}}, nil
	}), host.ServeOptions{})
}

// readJSON reads a small JSON body; ok false (after answering 413/400) when it is too large or not JSON. A body past the
// limit is refused as soon as it is seen; the rest is not read.
func readJSON(response http.ResponseWriter, request *http.Request) (value any, ok bool, err error) {
	body, err := io.ReadAll(io.LimitReader(request.Body, maxClaimBytes+1))
	if err == nil && len(body) > maxClaimBytes {
		_, _ = io.Copy(io.Discard, request.Body)
		response.WriteHeader(http.StatusRequestEntityTooLarge)
		_, err = io.WriteString(response, "Too large")
		return nil, false, err
	}
	if err == nil {
		value, err = chordjson.Decode(body)
	}
	if err != nil {
		response.WriteHeader(http.StatusBadRequest)
		_, err = io.WriteString(response, "Invalid sign-in request")
		return nil, false, err
	}
	return value, true, nil
}

// fieldOf is the member key of value when value is an object (nil otherwise).
func fieldOf(value any, key string) any {
	if object := objectOf(value); object != nil {
		return object.Value(key)
	}
	return nil
}

// Server is the plugin's server facet.
var Server = p2runtime.DefineFacet(func(facet host.FacetHost, _ host.FacetSelf) error {
	if err := contract.ServeDeviceAccesses(facet); err != nil {
		return err
	}
	if err := serveDeviceAccess(facet); err != nil {
		return err
	}
	return serveDevices(facet)
})
