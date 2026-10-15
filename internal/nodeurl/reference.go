package nodeurl

// Implements the WHATWG URL parser's relative resolution against an http or https base (`new URL(input, base)`) and the application/x-www-form-urlencoded parser behind `URLSearchParams`.

import (
	"net/url"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// Origin is the URL's ASCII serialized origin, as Node's `url.origin` gives it for an http or https URL.
func (u HTTPURL) Origin() string {
	if u.Port == "" {
		return u.Protocol + "//" + u.Hostname
	}
	return u.Protocol + "//" + u.Hostname + ":" + u.Port
}

// ResolveURL is `new URL(input, base)` for a base with a special scheme other than file (http, https, ws, wss, ftp). An input naming another scheme, or a different special one, is parsed on its own with ParseURL; an input naming
// the base's scheme without an authority is relative to the base. A relative input resolves as the parser's relative state does: "//" and "\\" start an authority, "/" a path, "?" and "#" replace the query or the fragment, and
// anything else replaces the base path's last segment.
func ResolveURL(input, base string) (URL, error) {
	parsedBase, err := ParseURL(base)
	if err != nil || !isSpecialNetworkScheme(parsedBase.Protocol) {
		return URL{}, errInvalidURL
	}
	input = PrepareInput(input)
	if scheme, rest, found := strings.Cut(input, ":"); found && isScheme(scheme) {
		if strings.ToLower(scheme)+":" != parsedBase.Protocol {
			return ParseURL(input)
		}
		// The special relative or authority state: an input with the base's scheme and no authority is relative.
		input = rest
	}
	prefix := parsedBase.Protocol + parsedBase.authority
	switch {
	case len(input) >= 2 && isSlash(input[0]) && isSlash(input[1]):
		return ParseURL(parsedBase.Protocol + input)
	case input != "" && isSlash(input[0]):
		return ParseURL(prefix + input)
	case input == "":
		return ParseURL(prefix + parsedBase.Pathname + parsedBase.query)
	case input[0] == '?':
		return ParseURL(prefix + parsedBase.Pathname + input)
	case input[0] == '#':
		return ParseURL(prefix + parsedBase.Pathname + parsedBase.query + input)
	}
	return parsedBase.ResolveRelativePath(input)
}

func isSpecialNetworkScheme(protocol string) bool {
	switch protocol {
	case "http:", "https:", "ws:", "wss:", "ftp:":
		return true
	}
	return false
}

// SearchParams is `new URLSearchParams(query)` for a query without its leading "?": the application/x-www-form-urlencoded parser. Unlike url.ParseQuery it keeps a pair containing ";" and a malformed percent escape, which it leaves as written, and it decodes each name and value as UTF-8 with replacement.
func SearchParams(query string) url.Values {
	values := url.Values{}
	for _, pair := range SearchPairs(query) {
		values[pair[0]] = append(values[pair[0]], pair[1])
	}
	return values
}

// SearchPairs is the list `new URLSearchParams(query)` holds for a query without its leading "?": the decoded name and value of each pair, in order, with repeated names kept.
func SearchPairs(query string) [][2]string {
	var pairs [][2]string
	for sequence := range strings.SplitSeq(query, "&") {
		if sequence == "" {
			continue
		}
		name, value, _ := strings.Cut(sequence, "=")
		pairs = append(pairs, [2]string{formDecode(name), formDecode(value)})
	}
	return pairs
}

// formDecode replaces "+" with a space, percent-decodes the valid escapes and decodes the bytes as UTF-8 without BOM.
func formDecode(value string) string {
	value = strings.ReplaceAll(value, "+", " ")
	return jsstring.FromUTF8(percentDecode(value))
}
