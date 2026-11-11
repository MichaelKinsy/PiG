package hosts

import (
	"encoding/json"
	"html"
	"net/http"
	"path"
	"regexp"
	"strconv"
	"strings"

	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/apps/p2/web"
)

// Ports apps/p2/src/hosts/web.ts: serves the browser host: the page, the current web bundle's manifest and content-named
// files, and the installable app's files. The bundle is the one Pi's esbuild makes of the vendored sources, embedded
// (package web, D471); plugin reload is designed out (D473), so there are no previous bundle generations to keep.

// IsPublicFile is whether path is one of the app's public files, served without signing in (`isPublicFile`): adding the
// app to a home screen fetches them without the page's cookies (Chrome fetches the web manifest without credentials, iOS
// often fetches the touch icon on its own). They hold nothing private.
func IsPublicFile(path string) bool {
	_, ok := web.LookupPublicFile(path)
	return ok
}

// pagePath is a page path of the app: a session, or one of its conversations; for the owner also a list (`/list/<key>`) and the
// new-session screen (`/new`).
var pagePath = regexp.MustCompile(`^/(?:s/[0-9a-f-]+(?:/c/[1-9][0-9]*)?|list/[^/]+|new)$`)

// IsAppPage is whether path is one of the app's pages (where a browser loads the app): `/`, or a path of pagePath.
func IsAppPage(path string) bool { return path == "/" || pagePath.MatchString(path) }

// guestPagePath is a page path of a guest's sessions: a session, or one of its conversations.
var guestPagePath = regexp.MustCompile(`^/s/([^/]+)(?:/c/[1-9][0-9]*)?$`)

// guestPage is whether path is a page of guest's sessions: `/`, a session shared with it, or one of its conversations.
func guestPage(path string, guest p2runtime.GuestPrincipal) bool {
	if path == "/" {
		return true
	}
	match := guestPagePath.FindStringSubmatch(path)
	if match == nil {
		return false
	}
	_, shared := guest.Sessions[match[1]]
	return shared
}

// escapeAttribute escapes a value for a double-quoted HTML attribute (`escapeAttribute`).
var escapeAttribute = strings.NewReplacer("&", "&amp;", `"`, "&quot;", "<", "&lt;", ">", "&gt;")

// webPage is the app page. The inline style and theme script run before any CSS so the first paint has the right
// background; the bundle's stylesheet follows them, so its rules win. The module script runs after parsing, wherever it
// stands.
func webPage(nonce string, manifest web.Manifest, brand web.Brand, sprite *spriteHub, shellOrigin string) string {
	// The Radius shell that frames a relayed page, named for the shell bridge (src/web/shell-bridge.ts), which posts only to it.
	shell := ""
	if shellOrigin != "" {
		shell = "\n\t\t<!-- The Radius shell framing this page (src/web/shell-bridge.ts). -->\n\t\t<meta name=\"p2-shell-origin\" content=\"" + escapeAttribute.Replace(shellOrigin) + "\" />"
	}
	style := ""
	if manifest.Style != nil {
		style = "\n\t\t<link rel=\"stylesheet\" href=\"" + *manifest.Style + "\" />"
	}
	// With a sprite source the page names the current sprite (`data-brand-sprite`: its SpriteInfo, "" for none) for the brand's mark, which then
	// follows the selection (SpriteEventsPath), and the favicon is the sprite.
	spriteAttribute, icon := "", `<link rel="icon" href="/icons/favicon.svg" type="image/svg+xml" />`
	if sprite != nil {
		spriteAttribute = ` data-brand-sprite="` + html.EscapeString(sprite.info()) + `"`
		if asset, ok := sprite.source.Current(); ok {
			icon = `<link rel="icon" href="` + SpriteAssetPath + `?v=` + html.EscapeString(asset.Key) + `" type="image/svg+xml" />`
		}
	}
	return `<!doctype html>
<html lang="en" data-web-version="` + manifest.Version + `"` + spriteAttribute + `>
	<head>
		<meta charset="UTF-8" />
		<meta name="viewport" content="width=device-width, initial-scale=1, viewport-fit=cover, interactive-widget=resizes-content" />
		<meta name="color-scheme" content="light dark" />
		<meta name="theme-color" media="(prefers-color-scheme: light)" content="` + brand.Light + `" />
		<meta name="theme-color" media="(prefers-color-scheme: dark)" content="` + brand.Dark + `" />
		<!-- Installable as a home-screen app; no service worker, so nothing is cached offline. -->
		<link rel="manifest" href="/manifest.webmanifest" />
		` + icon + `
		<link rel="apple-touch-icon" href="/icons/apple-touch-icon.png" />
		<meta name="apple-mobile-web-app-title" content="` + brand.Name + `" />
		<meta name="mobile-web-app-capable" content="yes" />
		<meta name="apple-mobile-web-app-capable" content="yes" />` + shell + `
		<title>` + brand.Name + `</title>
		<style>
			:root {
				color-scheme: light;
				background: ` + brand.Light + `;
			}
			:root[data-theme="dark"] {
				color-scheme: dark;
				background: ` + brand.Dark + `;
			}
		</style>
		<script nonce="` + nonce + `">
			let theme;
			try {
				theme = localStorage.getItem("pi-theme");
			} catch {}
			document.documentElement.dataset.theme = document.documentElement.dataset.colorScheme =
				theme === "light" || theme === "dark"
					? theme
					: matchMedia("(prefers-color-scheme: dark)").matches
						? "dark"
						: "light";
		</script>` + style + `
	</head>
	<body>
		<div id="root"></div>
		<script type="module" src="` + manifest.Script + `"></script>
	</body>
</html>
`
}

// Web serves the browser host (`createWeb`).
type Web struct {
	// Nonce is the page CSP's script nonce, written on the page's inline script.
	Nonce  string
	Bundle *web.Bundle
	// Sprite is the PiG sprite hook (nil: Pi's page).
	Sprite *spriteHub
}

// GuestMayLoad is whether a guest may load request: GET/HEAD of its session's pages, the app's public files, the bundle
// manifest and the bundle's files. Encoded or traversing paths are refused before normalizing.
func (w *Web) GuestMayLoad(request *http.Request, guest p2runtime.GuestPrincipal) bool {
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		return false
	}
	pathname := request.URL.EscapedPath()
	if strings.ContainsAny(pathname, `%\`) {
		return false
	}
	// posix.normalize: a path it would change is refused (a trailing slash it keeps names no page or file anyway).
	if pathname != "/" && path.Clean(pathname) != pathname {
		return false
	}
	if guestPage(pathname, guest) || pathname == web.ManifestPath || IsPublicFile(pathname) {
		return true
	}
	name, ok := strings.CutPrefix(pathname, web.BundlePath)
	if !ok {
		return false
	}
	_, found := w.Bundle.Files[name]
	return found
}

func sendText(response http.ResponseWriter, status int, text string) {
	response.Header().Set("Content-Type", "text/plain")
	response.WriteHeader(status)
	_, _ = response.Write([]byte(text))
}

// Handle serves request: the app page, the manifest, a bundle file or a public file, else 404. shellOrigin is the Radius
// shell that frames a relayed request (`RequestOrigin.RelayShellOrigin`), named in app pages for the shell bridge.
func (w *Web) Handle(response http.ResponseWriter, request *http.Request, shellOrigin string) {
	path := request.URL.EscapedPath()
	head := request.Method == http.MethodHead
	if request.Method != http.MethodGet && !head {
		sendText(response, http.StatusNotFound, "Not found")
		return
	}
	header := response.Header()
	write := func(body []byte) {
		if !head {
			_, _ = response.Write(body)
		}
	}
	if IsAppPage(path) {
		header.Set("Content-Type", "text/html; charset=utf-8")
		response.WriteHeader(http.StatusOK)
		write([]byte(webPage(w.Nonce, w.Bundle.Manifest(), w.Bundle.Brand, w.Sprite, shellOrigin)))
		return
	}
	if path == web.ManifestPath {
		manifest, err := json.Marshal(w.Bundle.Manifest())
		if err != nil {
			sendText(response, http.StatusInternalServerError, "Could not load the UI")
			return
		}
		header.Set("Content-Type", "application/json")
		response.WriteHeader(http.StatusOK)
		write(manifest)
		return
	}
	if name, ok := strings.CutPrefix(path, web.BundlePath); ok {
		if file, found := w.Bundle.Files[name]; found {
			// Named by content: a page may keep it forever. Private: only this browser's caches.
			header.Set("Content-Type", file.Type)
			header.Set("Content-Length", strconv.Itoa(file.Size))
			header.Set("Cache-Control", "private, max-age=31536000, immutable")
			response.WriteHeader(http.StatusOK)
			write(file.Body())
			return
		}
	}
	if found, ok := web.LookupPublicFile(path); ok {
		body, err := w.Bundle.ReadPublicFile(found)
		if err != nil {
			sendText(response, http.StatusInternalServerError, "Could not load the UI")
			return
		}
		// Home screens and browsers may keep them for a day; a changed icon shows after that.
		cache := found.Cache
		if cache == "" {
			cache = "public, max-age=86400"
		}
		header.Set("Content-Type", found.Type)
		header.Set("Content-Length", strconv.Itoa(len(body)))
		header.Set("Cache-Control", cache)
		response.WriteHeader(http.StatusOK)
		write(body)
		return
	}
	sendText(response, http.StatusNotFound, "Not found")
}
