// Package hosts is p2's processes: the server (HTTP and WebSocket accept, session workers, the server facets) and the
// session worker. Ports apps/p2/src/hosts.
package hosts

import (
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
)

// Ports apps/p2/src/hosts/principals.ts: where an HTTP request came from and whether a browser sent it for another site.

// ProxyMarkerHeader is the historical wire name; ANY value marks a proxied request. It is not an authentication secret.
const ProxyMarkerHeader = "X-P2-Proxy-Secret"

// ParseAllowedHosts reads hosts reachable through a trusted HTTPS tunnel, e.g. `P2_ALLOWED_HOSTS=p2.example.ngrok.app`.
func ParseAllowedHosts(value string) []string {
	hosts := []string{}
	for _, part := range strings.Split(value, ",") {
		if host := strings.ToLower(strings.TrimSpace(part)); host != "" {
			hosts = append(hosts, host)
		}
	}
	return hosts
}

// localAddress is the address the connection arrived on (the server listens on loopback only).
func localAddress(request *http.Request) (host string, port int, ok bool) {
	address, _ := request.Context().Value(http.LocalAddrContextKey).(net.Addr)
	if address == nil {
		return "", 0, false
	}
	hostText, portText, err := net.SplitHostPort(address.String())
	if err != nil {
		return "", 0, false
	}
	port, err = strconv.Atoi(portText)
	return hostText, port, err == nil
}

// RequestOrigin is where a request came from. The server listens on loopback only. Local hosts are served over http; an
// allowed tunnel host is https (the tunnel terminates TLS; app authentication gates access). Any other Host header is
// refused (DNS rebinding), and forwarded requests never pass as local. nil: refused.
func RequestOrigin(request *http.Request, allowedHosts []string) *p2runtime.RequestOrigin {
	address, port, ok := localAddress(request)
	if !ok || address != "127.0.0.1" {
		return nil
	}
	host := strings.ToLower(request.Host)
	if host == "127.0.0.1:"+strconv.Itoa(port) || host == "localhost:"+strconv.Itoa(port) {
		header := request.Header
		if len(header.Values(ProxyMarkerHeader)) > 0 || header.Get("X-Forwarded-For") != "" || header.Get("X-Forwarded-Host") != "" ||
			header.Get("X-Forwarded-Proto") != "" || header.Get("Forwarded") != "" {
			return nil
		}
		return &p2runtime.RequestOrigin{Origin: "http://" + host, Remote: false, Port: port}
	}
	for _, allowed := range allowedHosts {
		if allowed == host {
			return &p2runtime.RequestOrigin{Origin: "https://" + host, Remote: true, Port: port}
		}
	}
	return nil
}

// SameOrigin refuses a browser request from another origin (a cross-site form, fetch or WebSocket).
func SameOrigin(request *http.Request, origin p2runtime.RequestOrigin) bool {
	values := request.Header.Values("Origin")
	return len(values) == 0 || values[0] == origin.Origin
}

var frameDestinations = map[string]bool{"iframe": true, "frame": true, "embed": true, "object": true, "fencedframe": true}

// ForeignBrowserRequest reports whether a browser sent this request for another site (Fetch Metadata): a cross-site or
// same-site (another local port) page, link, form, fetch or WebSocket, or a frame or embed another site holds. Such a
// request never gets the loopback owner: a page anywhere could otherwise frame the owner UI (clickjacking) or drive it.
// Requests without the headers (the TUI, scripts, older browsers) are unaffected; the page's CSP `frame-ancestors` still
// refuses framing there.
func ForeignBrowserRequest(request *http.Request) bool {
	site, hasSite := request.Header["Sec-Fetch-Site"]
	if hasSite && site[0] != "same-origin" && site[0] != "none" {
		return true
	}
	dest := request.Header.Get("Sec-Fetch-Dest")
	return frameDestinations[dest] && !(hasSite && site[0] == "same-origin")
}

// pageRoutes are the app pages a link may open: the app, a session or conversation, a share's join page, owner sign-in.
var pageRoutes = regexp.MustCompile(`^/(?:$|s/|join/|owner$)`)

// PageNavigation reports a top-level page load (link, address bar, bookmark, home-screen app launch): the only foreign
// browser request that may authenticate. It cannot read anything back into the foreign page, and opens the app as usual.
// Only browsers set `navigate` (a page's fetch cannot); frames say `iframe`/`frame`. Chrome on Android launches an
// installed app (WebAPK) with `Sec-Fetch-Site: cross-site`, `Sec-Fetch-Dest: empty` and no cookies, so `empty` counts as a
// document too.
func PageNavigation(request *http.Request) bool {
	dest := request.Header.Get("Sec-Fetch-Dest")
	path := "/"
	if parsed, err := url.ParseRequestURI(request.RequestURI); err == nil {
		path = parsed.Path
	}
	return request.Method == http.MethodGet && request.Header.Get("Sec-Fetch-Mode") == "navigate" &&
		(dest == "document" || dest == "empty") && pageRoutes.MatchString(path)
}

// ContentSecurityPolicy is the CSP of every response (as Pi Mobile's): the app's own scripts (the page's inline ones carry
// the nonce), its WebSocket, and frames only from its own origin (artifact previews). Nothing may frame it but the Radius
// shell that relays it. origin nil: the request's origin is unknown.
func ContentSecurityPolicy(nonce string, origin *p2runtime.RequestOrigin) string {
	socketOrigin, ancestors := "", "'none'"
	if origin != nil {
		socketOrigin = strings.Replace(origin.Origin, "http", "ws", 1)
		if origin.RelayShellOrigin != "" {
			ancestors = origin.RelayShellOrigin
		}
	}
	return strings.Join([]string{
		"default-src 'none'",
		"script-src 'self' 'nonce-" + nonce + "' 'wasm-unsafe-eval'",
		strings.TrimRight("connect-src 'self' "+socketOrigin, " "),
		"style-src 'self' 'unsafe-inline'",
		"img-src 'self' data: blob:",
		"media-src 'self' data: blob:",
		"font-src 'self' data:",
		"worker-src 'self' blob:",
		"manifest-src 'self'",
		"frame-src 'self'",
		"base-uri 'none'",
		"form-action 'none'",
		"frame-ancestors " + ancestors,
	}, "; ")
}
