package runtime

import (
	"errors"
	"regexp"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/nodeurl"
)

// Ports apps/p2/src/runtime/owner-link.ts: owner links (`/owner#<token>`) and the origins they are for, checked by the
// owner-link CLI and the TUI launcher. URLs parse as WHATWG's `new URL` does (internal/nodeurl).

// OwnerOrigin parses value as the http(s) origin an owner link is for: no credentials, path, query or fragment, and plain
// http only for loopback. Shared by the shell CLI and the TUI; an invitation never goes in a path or query.
func OwnerOrigin(value string) (nodeurl.URL, error) {
	url, err := nodeurl.ParseURL(value)
	if err != nil {
		return nodeurl.URL{}, errors.New("Invalid URL; use an http(s) origin")
	}
	if !slices.Contains([]string{"http:", "https:"}, url.Protocol) || url.Username != "" || url.Password != "" ||
		url.Pathname != "/" || url.Query != "" || url.Hash != "" {
		return nodeurl.URL{}, errors.New("Use an http(s) origin URL without credentials, path or query")
	}
	if url.Protocol != "https:" && !slices.Contains([]string{"127.0.0.1", "localhost", "[::1]"}, url.Hostname) {
		return nodeurl.URL{}, errors.New("Remote connections require HTTPS")
	}
	return url, nil
}

var ownerFragment = regexp.MustCompile(`^#[a-f0-9]{64}$`)

// OwnerLinkToken is the token of the owner link value for origin: the fragment of `<origin>/owner#<64 hex digits>`.
func OwnerLinkToken(value string, origin nodeurl.URL) (string, error) {
	link, err := nodeurl.ParseURL(strings.TrimSpace(value))
	if err != nil {
		return "", errors.New("Invalid owner link")
	}
	// A query, even an empty one, is refused as Pi's `link.search` is: only an empty one is "" there too, so the parsed
	// query decides.
	if link.Origin() != origin.Origin() || link.Username != "" || link.Password != "" || link.Pathname != "/owner" ||
		link.Query != "" || !ownerFragment.MatchString(link.Hash) {
		return "", errors.New("Use an owner link for this origin with its token in the fragment")
	}
	return link.Hash[1:], nil
}
