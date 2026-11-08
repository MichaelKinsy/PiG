// Package devices is p2's devices plugin (the server facet of apps/p2/plugins/devices/server.ts): owner sign-in from other
// devices by one-time tokens and signed cookies, the owner's Devices object, and sign-out of one or all devices. Its TUI
// facet (tui.ts) belongs to the TUI lane.
package devices

import (
	"context"
	"io"
	"net/http"
	"sync"

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

// retention hands the open owner access of a reloaded facet to the facet that replaces it.
type retention struct {
	mu   sync.Mutex
	kept map[string]*contract.OwnerAccess
}

var retained = &retention{kept: map[string]*contract.OwnerAccess{}}

func (r *retention) keep(access *contract.OwnerAccess) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.kept[access.Directory()] = access
	return true
}

func (r *retention) take(directory string) *contract.OwnerAccess {
	r.mu.Lock()
	defer r.mu.Unlock()
	access := r.kept[directory]
	delete(r.kept, directory)
	return access
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
			if kept := retained.take(directory); kept != nil {
				return kept, nil
			}
			return contract.OpenOwnerAccess(directory, nil)
		}, func(value any, reason host.NativeCloseReason, _ context.Context) error {
			access := value.(*contract.OwnerAccess)
			// Pi's reload of unchanged code leaves the object, and so its invitations and key, in place; a Go reload
			// re-evaluates the facet, so the next open takes the access over instead.
			if reason.Kind == host.NativeCloseReload && retained.keep(access) {
				return nil
			}
			access.Close()
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
		principal := func(request *http.Request, port int) p2runtime.Principal {
			if device := access().Device(request, port); device != nil {
				return p2runtime.OwnerPrincipal{Access: p2runtime.AccessDevice, Device: device.ID}
			}
			return nil
		}
		p2runtime.ContributeKeyed(store, p2runtime.Authenticators, p2runtime.Entry("owner", p2runtime.Authenticator(
			func(request *http.Request, origin p2runtime.RequestOrigin, _ host.Ctx) (p2runtime.Principal, error) {
				return principal(request, origin.Port), nil
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
				if owner := principal(request, origin.Port); owner != nil {
					return &p2runtime.RequestAccess{Origin: *origin, Principal: owner}, nil
				}
				return nil, nil
			})))
		logout := func(response http.ResponseWriter, request *http.Request, access2 p2runtime.RequestAccess, all bool, ctx host.Ctx) error {
			if !p2runtime.FromOrigin(request, access2.Origin.Origin) || request.Header.Get("Sec-Fetch-Site") == "cross-site" {
				return forbid(response, request, "Invalid origin")
			}
			if all && !p2runtime.IsOwner(access2.Principal) {
				return forbid(response, request, "Forbidden")
			}
			_, _ = io.Copy(io.Discard, request.Body)
			current := access()
			device := current.Device(request, access2.Origin.Port)
			var failure error
			if all {
				failure = current.Rotate()
			} else if device != nil {
				failure = current.Logout(*device)
			}
			if all || device != nil {
				revoke := delta.NewJsonObject(0)
				if !all {
					revoke = delta.JsonObjectOf("device", device.ID)
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
			p2runtime.SignIn(response, request, http.StatusOK)
			return nil
		}
		p2runtime.ContributeKeyed(store, p2runtime.Routes,
			p2runtime.Entry("GET /owner", p2runtime.RouteHandler(signIn)),
			p2runtime.Entry("HEAD /owner", p2runtime.RouteHandler(signIn)),
			p2runtime.Entry("GET /owner/check", p2runtime.RouteHandler(func(response http.ResponseWriter, request *http.Request, access2 p2runtime.RequestAccess, _ host.Ctx) error {
				if !p2runtime.IsOwner(access2.Principal) {
					p2runtime.SignIn(response, request, http.StatusUnauthorized)
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
			p2runtime.Entry("GET /owner-token", p2runtime.RouteHandler(func(response http.ResponseWriter, request *http.Request, access2 p2runtime.RequestAccess, _ host.Ctx) error {
				owner, isOwner := access2.Principal.(p2runtime.OwnerPrincipal)
				if !isOwner || owner.Access != p2runtime.AccessLocal || access2.Origin.Remote || request.Header.Get("Sec-Fetch-Site") == "cross-site" {
					return forbid(response, request, "Forbidden")
				}
				return p2runtime.SendJSON(response, http.StatusOK, map[string]any{"token": access().IssueToken()}, nil)
			})),
			p2runtime.Entry("POST /owner/claim", p2runtime.RouteHandler(func(response http.ResponseWriter, request *http.Request, access2 p2runtime.RequestAccess, _ host.Ctx) error {
				if !p2runtime.FromOrigin(request, access2.Origin.Origin) || request.Header.Get("Sec-Fetch-Site") == "cross-site" {
					return forbid(response, request, "Invalid origin")
				}
				// A body past the limit is refused as soon as it is seen; the rest is not read.
				body, err := io.ReadAll(io.LimitReader(request.Body, maxClaimBytes+1))
				if err == nil && len(body) > maxClaimBytes {
					_, _ = io.Copy(io.Discard, request.Body)
					response.WriteHeader(http.StatusRequestEntityTooLarge)
					_, err = io.WriteString(response, "Too large")
					return err
				}
				token, isString, ok := claimToken(body, err)
				if !ok {
					response.WriteHeader(http.StatusBadRequest)
					_, err = io.WriteString(response, "Invalid sign-in request")
					return err
				}
				if !isString || !access().Claim(token) {
					response.WriteHeader(http.StatusForbidden)
					_, err = io.WriteString(response, "Invalid one-time token")
					return err
				}
				return p2runtime.SendJSON(response, http.StatusOK, map[string]any{"ok": true},
					map[string]string{"Set-Cookie": access().Cookie(access2.Origin.Port, access2.Origin.Remote)})
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

// claimToken reads the sign-in body: its `token` (isString false when the body is no object with a string token), and
// whether it was valid JSON at all.
func claimToken(body []byte, readErr error) (token string, isString, ok bool) {
	if readErr != nil {
		return "", false, false
	}
	value, err := chordjson.Decode(body)
	if err != nil {
		return "", false, false
	}
	if object := objectOf(value); object != nil {
		token, isString = object.Value("token").(string)
	}
	return token, isString, true
}

// Server is the plugin's server facet.
var Server = host.DefineFacet(func(facet host.FacetHost, _ host.FacetSelf) error {
	if err := contract.ServeDeviceAccesses(facet); err != nil {
		return err
	}
	if err := serveDeviceAccess(facet); err != nil {
		return err
	}
	return serveDevices(facet)
})
