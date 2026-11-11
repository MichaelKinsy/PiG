package artifacts

import (
	"errors"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/MichaelKinsy/PiG/apps/p2/plugins/artifacts/contract"
	sessioncontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/sessions/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/host"
)

// Ports apps/p2/plugins/artifacts/server.ts: the server's `GET /artifact` route.

const (
	executableCSP = "default-src 'none'; script-src 'unsafe-inline' 'wasm-unsafe-eval' https:; style-src 'unsafe-inline' https:; img-src data: blob: https:; font-src data: https:; media-src data: blob: https:; connect-src https:; worker-src blob:; frame-src 'none'; object-src 'none'; base-uri 'none'; form-action 'none'; sandbox allow-scripts allow-modals allow-pointer-lock"
	documentCSP   = "default-src 'none'; base-uri 'none'; form-action 'none'; sandbox"
)

// versionOf is `Number(text)` when it is a safe integer of at least 1, else false: an empty or blank text is 0, and a
// number is written in decimal, with an exponent, or with a 0x, 0o or 0b prefix.
func versionOf(text string) (int, bool) {
	text = strings.TrimFunc(text, isJSSpace)
	var number float64
	switch {
	case text == "":
		return 0, false
	case len(text) > 2 && text[0] == '0' && strings.ContainsRune("xXoObB", rune(text[1])):
		base := map[byte]int{'x': 16, 'X': 16, 'o': 8, 'O': 8, 'b': 2, 'B': 2}[text[1]]
		parsed, err := strconv.ParseUint(text[2:], base, 64)
		if err != nil {
			return 0, false
		}
		number = float64(parsed)
	default:
		// Only what JavaScript reads as a decimal literal: not `inf`, `nan`, hexadecimal floats or digit separators.
		for _, r := range text {
			if !strings.ContainsRune("0123456789+-.eE", r) {
				return 0, false
			}
		}
		parsed, err := strconv.ParseFloat(text, 64)
		if err != nil {
			return 0, false
		}
		number = parsed
	}
	if number != math.Trunc(number) || number < 1 || number > 1<<53-1 {
		return 0, false
	}
	return int(number), true
}

func archived(row delta.JsonValue) bool { return objectOf(row).Value("archivedAt") != nil }

func serveRoutes(facet host.FacetHost) error {
	return p2runtime.Contribute(facet, "p2.artifact-routes", func(self host.StoreSelf) error {
		server := p2runtime.ServerOf(self)
		catalog := self.Get(sessioncontract.Catalog, delta.NewJsonObject(0))
		serve := func(response http.ResponseWriter, request *http.Request, access p2runtime.RequestAccess, outer host.Ctx) error {
			query := request.URL.Query()
			session := query.Get("session")
			if !p2runtime.CanView(access.Principal, session) || request.Header.Get("Sec-Fetch-Site") == "cross-site" {
				response.Header().Set("Content-Type", "text/plain")
				response.WriteHeader(http.StatusForbidden)
				_, err := io.WriteString(response, "Forbidden")
				return err
			}
			headersSent := false
			err := func() error {
				name := query.Get("name")
				version, ok := versionOf(query.Get("version"))
				if !validName(name) || !ok {
					return errNotFound
				}
				parts, err := server()
				if err != nil {
					return err
				}
				// The bytes are the session worker's: read in a root of the server's own (its `ArtifactSource` answers only the
				// server), after the viewer was authorized above.
				read, err := parts.Root(func(ctx host.Ctx) (any, error) {
					// An unknown or archived session (whose worker never starts) is a 404 at once, never waited for.
					row, err := catalog.Call("get", delta.JsonObjectOf("id", session), ctx)
					if err != nil || archived(row) {
						return nil, errNotFound
					}
					source, err := ctx.Get(contract.ArtifactSource, delta.JsonObjectOf("session", session), host.GetOptions{})
					if err != nil {
						return nil, err
					}
					return source.Call("read", delta.JsonObjectOf("name", name, "version", float64(version)), ctx)
				}, outer)
				if err != nil {
					return err
				}
				artifact := objectOf(read)
				info, ok := p2runtime.ArtifactInfoOf(artifact.Value("info"))
				if !ok {
					return errNotFound
				}
				content, _ := artifact.Value("content").(string)
				download := query.Get("download") == "1"
				executable := info.Type == p2runtime.ArtifactHTML || info.Type == p2runtime.ArtifactSVG
				if executable && !download {
					content = InstrumentArtifact(info, content)
				}
				extension := string(info.Type)
				switch info.Type {
				case p2runtime.ArtifactMarkdown:
					extension = "md"
				case p2runtime.ArtifactText:
					extension = "txt"
				}
				header := response.Header()
				// `frame-ancestors` checks every ancestor: through Radius the app itself sits in the Radius shell's frame, so
				// `'self'` alone would block every preview there.
				policy := documentCSP
				if executable {
					policy = executableCSP
				}
				policy += "; frame-ancestors 'self'"
				if access.Origin.RelayShellOrigin != "" {
					policy += " " + access.Origin.RelayShellOrigin
				}
				header.Set("Content-Security-Policy", policy)
				switch info.Type {
				case p2runtime.ArtifactHTML:
					header.Set("Content-Type", "text/html; charset=utf-8")
				case p2runtime.ArtifactSVG:
					header.Set("Content-Type", "image/svg+xml; charset=utf-8")
				default:
					header.Set("Content-Type", "text/plain; charset=utf-8")
				}
				header.Set("Content-Length", strconv.Itoa(len(content)))
				if download {
					header.Set("Content-Disposition", `attachment; filename="`+info.Name+"."+extension+`"`)
				} else {
					header.Set("Content-Disposition", "inline")
				}
				header.Set("Cross-Origin-Resource-Policy", "same-origin")
				response.WriteHeader(http.StatusOK)
				headersSent = true
				if request.Method == http.MethodHead {
					return nil
				}
				_, err = io.WriteString(response, content)
				return err
			}()
			if err == nil {
				return nil
			}
			if headersSent {
				// `response.destroy()`: the transfer ends mid-body, the client sees a broken one.
				if conn, _, hijackErr := http.NewResponseController(response).Hijack(); hijackErr == nil {
					_ = conn.Close()
				}
				return nil
			}
			status := http.StatusNotFound
			var refused *ArtifactError
			if errors.As(err, &refused) {
				status = refused.Status
			}
			response.Header().Set("Content-Type", "text/plain")
			response.WriteHeader(status)
			_, writeErr := io.WriteString(response, "Artifact not found")
			return writeErr
		}
		p2runtime.ContributeKeyed(self, p2runtime.Routes,
			p2runtime.Entry("GET /artifact", p2runtime.RouteHandler(serve)),
			p2runtime.Entry("HEAD /artifact", p2runtime.RouteHandler(serve)),
		)
		return nil
	})
}

// Server is the plugin's server facet.
var Server = p2runtime.DefineFacet(func(facet host.FacetHost, _ host.FacetSelf) error {
	return serveRoutes(facet)
})
