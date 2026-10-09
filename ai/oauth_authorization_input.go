package ai

import (
	"net/url"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/nodeurl"
)

// authorizationURLQuery is `new URL(value).searchParams` for a pasted value. ok is false when `new URL(value)` throws, so the caller treats the
// value as a code, a `code#state` pair or a query string.
func authorizationURLQuery(value string) (query url.Values, ok bool) {
	parsed, err := nodeurl.ParseURL(value)
	if err != nil {
		return nil, false
	}
	return nodeurl.SearchParams(parsed.Query), true
}

// authorizationFragmentPair is `value.split("#", 2)`: the text before the first "#" and the text between it and the next "#".
func authorizationFragmentPair(value string) (code, state string, ok bool) {
	code, rest, ok := strings.Cut(value, "#")
	if !ok {
		return "", "", false
	}
	state, _, _ = strings.Cut(rest, "#")
	return code, state, true
}

// authorizationQueryParams is `new URLSearchParams(value)`, which drops one leading "?".
func authorizationQueryParams(value string) url.Values {
	return nodeurl.SearchParams(strings.TrimPrefix(value, "?"))
}
