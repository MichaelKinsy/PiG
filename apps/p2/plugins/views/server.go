package views

import (
	"context"
	"io"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/MichaelKinsy/PiG/apps/p2/plugins/artifacts"
	sessioncontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions/contract"
	"github.com/MichaelKinsy/PiG/apps/p2/plugins/views/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/schema"
)

// Ports apps/p2/plugins/views/server.ts: the owner's library of saved views, and `/view-frame`, which serves a custom
// view's HTML into its sandboxed frame with the bridge and the artifacts' diagnostics on top.

func objectOf(value any) *delta.JsonObject {
	object, _ := value.(*delta.JsonObject)
	return object
}

func must(contract *contracts.Contract, err error) *contracts.Contract {
	if err != nil {
		panic(err)
	}
	return contract
}

var nameArgs = schema.Obj(schema.Properties{schema.Prop("name", schema.Str(schema.Pattern(contract.VIDSource)))})

// ViewLibrary is the library (server-internal, one per server): every principal's `SavedViews` shares it.
var ViewLibrary = must(contracts.Local("p2.view-library", contracts.LocalDeclaration{
	Version: 1,
	Params:  contracts.Params{},
	Members: []contracts.LocalEntry{
		contracts.Mem("saved", p2runtime.Contract(schema.Arr(contract.SavedInfoSchema))),
		contracts.Mem("get", contracts.NewMethod(p2runtime.Contract(nameArgs), p2runtime.Contract(schema.Nullable(contract.SavedViewSchema)))),
		contracts.Mem("save", contracts.NewMethod(p2runtime.Contract(contract.SavedViewSchema), p2runtime.Contract(contract.SavedInfoSchema))),
		contracts.Mem("remove", contracts.NewMethod(p2runtime.Contract(nameArgs))),
	},
}))

func infoJSON(info SavedInfo) *delta.JsonObject {
	object := delta.JsonObjectOf("name", info.Name, "title", info.Title)
	if info.Description != "" {
		object.Set("description", info.Description)
	}
	object.Set("kind", info.Kind)
	object.Set("saved", info.Saved)
	return object
}

func infosJSON(infos []SavedInfo) []any {
	list := make([]any, len(infos))
	for i, info := range infos {
		list[i] = infoJSON(info)
	}
	return list
}

func savedJSON(view SavedView) (delta.JsonValue, error) { return jsonOf(view) }

// savedOf is a saved view from its wire form.
func savedOf(value delta.JsonValue) (SavedView, error) {
	var view SavedView
	text, err := jsonText(value)
	if err != nil {
		return view, err
	}
	return view, jsonUnmarshal(text, &view)
}

func serveLibrary(facet host.FacetHost) error {
	return facet.Serve(ViewLibrary, host.LocalFactory(func(_ *delta.JsonObject, self host.Self) (contracts.Implementation, error) {
		server := self.Get(p2runtime.ServerLocal, delta.NewJsonObject(0))
		opened := self.Open(func(context.Context) (any, error) {
			info, err := server.Member("info").Value()
			if err != nil {
				return nil, err
			}
			directory, _ := objectOf(info).Value("dataDir").(string)
			library := NewLibrary(directory)
			return library, library.Load()
		}, func(any, host.NativeCloseReason, context.Context) error { return nil })
		library := func() (*Library, error) {
			value, err := opened.Value()
			if err != nil {
				return nil, err
			}
			return value.(*Library), nil
		}
		saved := p2runtime.FollowNative(self,
			func(changed func()) func() {
				if current, err := library(); err == nil {
					return current.Subscribe(changed)
				}
				return func() {}
			},
			func() (delta.JsonValue, error) {
				current, err := library()
				if err != nil {
					return nil, err
				}
				return infosJSON(current.Saved()), nil
			})
		name := func(args delta.JsonValue) string {
			text, _ := objectOf(args).Value("name").(string)
			return text
		}
		return contracts.Implementation{Members: contracts.VariantImplementation{
			State: map[string]chord.Watchable[delta.JsonValue]{"saved": saved},
			Methods: map[string]contracts.MethodFunction{
				"get": func(args delta.JsonValue, _ context.Context) (delta.JsonValue, error) {
					current, err := library()
					if err != nil {
						return nil, err
					}
					view, err := current.Get(name(args))
					if view == nil || err != nil {
						return nil, err
					}
					return savedJSON(*view)
				},
				"save": func(args delta.JsonValue, _ context.Context) (delta.JsonValue, error) {
					current, err := library()
					if err != nil {
						return nil, err
					}
					view, err := savedOf(args)
					if err != nil {
						return nil, err
					}
					info, err := current.Save(view)
					if err != nil {
						return nil, err
					}
					return infoJSON(info), nil
				},
				"remove": func(args delta.JsonValue, _ context.Context) (delta.JsonValue, error) {
					current, err := library()
					if err != nil {
						return nil, err
					}
					return nil, current.Remove(name(args))
				},
			},
		}}, nil
	}), host.ServeOptions{})
}

func serveSavedViews(facet host.FacetHost) error {
	return facet.Serve(contract.SavedViews, host.RemoteFactory(func(_ *delta.JsonObject, principal data.Principal, self host.Self) (contracts.Implementation, error) {
		who, err := p2runtime.FromChord(principal)
		if err != nil {
			return contracts.Implementation{}, p2runtime.Forbidden("saved views are the owner's")
		}
		library := self.Get(ViewLibrary, delta.NewJsonObject(0))
		if p2runtime.IsOwner(who) {
			return contracts.Implementation{Variant: "owner", Members: contracts.VariantImplementation{
				State: map[string]chord.Watchable[delta.JsonValue]{"saved": self.Derive([]any{library.Member("saved")}, func(values []any) (delta.JsonValue, error) { return values[0], nil })},
				Methods: map[string]contracts.MethodFunction{
					"remove": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
						return library.Call("remove", args, ctx)
					},
				},
			}}, nil
		}
		if _, worker := who.(p2runtime.WorkerPrincipal); !worker {
			return contracts.Implementation{}, p2runtime.Forbidden("saved views are the owner's")
		}
		return contracts.Implementation{Variant: "worker", Members: contracts.VariantImplementation{
			Methods: map[string]contracts.MethodFunction{
				"list": func(_ delta.JsonValue, _ context.Context) (delta.JsonValue, error) {
					value, err := library.Member("saved").Value()
					if list, ok := value.([]any); ok {
						return append([]any{}, list...), err
					}
					return value, err
				},
				"get": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					return library.Call("get", args, ctx)
				},
				"save": func(args delta.JsonValue, ctx context.Context) (delta.JsonValue, error) {
					return library.Call("save", args, ctx)
				},
			},
		}}, nil
	}), host.ServeOptions{})
}

// frameCSP is as the artifacts' previews: scripts, but no app origin, no frames, forms or plugins.
const frameCSP = "default-src 'none'; script-src 'unsafe-inline' 'wasm-unsafe-eval' https:; style-src 'unsafe-inline' https:; img-src data: blob: https:; font-src data: https:; media-src data: blob: https:; connect-src https:; worker-src blob:; frame-src 'none'; object-src 'none'; base-uri 'none'; form-action 'none'; sandbox allow-scripts allow-modals allow-pointer-lock"

var headRE = regexp.MustCompile(`(?i)<head(?:\s[^>]*)?>`)

// injectBridge is html with the bridge's script first in its head (or first of all without one).
func injectBridge(html string) string {
	script := "<script>" + viewBridgeScript + "</script>"
	if head := headRE.FindStringIndex(html); head != nil {
		return html[:head[1]] + script + html[head[1]:]
	}
	return script + html
}

func archived(row delta.JsonValue) bool { return objectOf(row).Value("archivedAt") != nil }

func serveFrames(facet host.FacetHost) error {
	return p2runtime.Contribute(facet, "p2.view-frames", func(self host.StoreSelf) error {
		server := p2runtime.ServerOf(self)
		catalog := self.Get(sessioncontract.Catalog, delta.NewJsonObject(0))
		serve := func(response http.ResponseWriter, request *http.Request, access p2runtime.RequestAccess, outer host.Ctx) error {
			query := request.URL.Query()
			session := query.Get("session")
			if !p2runtime.CanView(access.Principal, session) || request.Header.Get("Sec-Fetch-Site") == "cross-site" {
				response.WriteHeader(http.StatusForbidden)
				_, err := io.WriteString(response, "Forbidden")
				return err
			}
			notFound := func() error {
				response.WriteHeader(http.StatusNotFound)
				_, err := io.WriteString(response, "View not found")
				return err
			}
			view := query.Get("view")
			version, err := strconv.ParseFloat(strings.TrimSpace(query.Get("version")), 64)
			if !contract.ViewID.MatchString(view) || err != nil || version != math.Trunc(version) || version < 1 || version > 1<<53-1 {
				return notFound()
			}
			parts, err := server()
			if err != nil {
				return notFound()
			}
			// The bytes are the session worker's, read in a root of the server's own after the viewer was authorized.
			read, err := parts.Root(func(ctx host.Ctx) (any, error) {
				row, err := catalog.Call("get", delta.JsonObjectOf("id", session), ctx)
				if err != nil || row == nil || archived(row) {
					return nil, errUnknownSession
				}
				source, err := ctx.Get(contract.ViewSource, delta.JsonObjectOf("session", session), host.GetOptions{})
				if err != nil {
					return nil, err
				}
				return source.Call("read", delta.JsonObjectOf("view", view, "version", version), ctx)
			}, outer)
			if err != nil {
				return notFound()
			}
			source := objectOf(read)
			title, _ := source.Value("title").(string)
			html, _ := source.Value("html").(string)
			page := artifacts.InstrumentArtifact(p2runtime.ArtifactInfo{Name: view, Title: title, Type: p2runtime.ArtifactHTML, Version: int(version)}, injectBridge(html))
			policy := frameCSP + "; frame-ancestors 'self'"
			if access.Origin.RelayShellOrigin != "" {
				policy += " " + access.Origin.RelayShellOrigin
			}
			header := response.Header()
			header.Set("Content-Security-Policy", policy)
			header.Set("Content-Type", "text/html; charset=utf-8")
			header.Set("Content-Length", strconv.Itoa(len(page)))
			header.Set("Cross-Origin-Resource-Policy", "same-origin")
			// Versions are immutable, but a closed and reopened view reuses its ID: never cache.
			header.Set("Cache-Control", "no-store")
			response.WriteHeader(http.StatusOK)
			if request.Method == http.MethodHead {
				return nil
			}
			_, err = io.WriteString(response, page)
			return err
		}
		p2runtime.ContributeKeyed(self, p2runtime.Routes,
			p2runtime.Entry("GET /view-frame", p2runtime.RouteHandler(serve)),
			p2runtime.Entry("HEAD /view-frame", p2runtime.RouteHandler(serve)),
		)
		return nil
	})
}

// Server is the plugin's server facet.
var Server = p2runtime.DefineFacet(func(facet host.FacetHost, _ host.FacetSelf) error {
	for _, serve := range []func(host.FacetHost) error{serveLibrary, serveSavedViews, serveFrames} {
		if err := serve(facet); err != nil {
			return err
		}
	}
	return nil
})
