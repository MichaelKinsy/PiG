package ai

import (
	"github.com/MichaelKinsy/PiG/internal/nodeurl"
)

// trustedURLHref is `new URL(value).href` for a verification URI the user's browser will open: ok is false where `new URL` throws or the protocol is
// not http: or https: (https: alone when httpsOnly). The WHATWG parser normalizes what Go's keeps as written: the scheme and host are lowercased, a
// default port, an empty port and an empty userinfo are dropped, an empty path becomes "/", and "https:host" and backslashes are read as authorities.
func trustedURLHref(value string, httpsOnly bool) (href string, ok bool) {
	parsed, err := nodeurl.ParseHTTPHref(value)
	if err != nil || (httpsOnly && parsed.Protocol != "https:") {
		return "", false
	}
	return parsed.Href, true
}
