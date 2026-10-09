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

// ResolveHTTPURL is `new URL(input, base)` for an http or https base. It fails when the parser throws and when the input names a scheme other than http or https.
func ResolveHTTPURL(input, base string) (HTTPURL, error) {
	parsedBase, err := ParseHTTPURL(base)
	if err != nil {
		return HTTPURL{}, err
	}
	input = PrepareInput(input)
	if scheme, rest, found := strings.Cut(input, ":"); found && isScheme(scheme) {
		scheme = strings.ToLower(scheme)
		if scheme+":" != parsedBase.Protocol {
			return ParseHTTPURL(input)
		}
		// The special relative or authority state: an input with the base's scheme and no authority is relative.
		input = rest
	}
	origin := parsedBase.Origin()
	switch {
	case len(input) >= 2 && isSlash(input[0]) && isSlash(input[1]):
		return ParseHTTPURL(parsedBase.Protocol + input)
	case input != "" && isSlash(input[0]):
		return ParseHTTPURL(origin + input)
	case input == "":
		return ParseHTTPURL(origin + parsedBase.Pathname + parsedBase.Search)
	case input[0] == '?':
		return ParseHTTPURL(origin + parsedBase.Pathname + input)
	case input[0] == '#':
		return ParseHTTPURL(origin + parsedBase.Pathname + parsedBase.Search + input)
	}
	// A path-relative input replaces the base path's last segment.
	return ParseHTTPURL(origin + parsedBase.Pathname[:strings.LastIndexByte(parsedBase.Pathname, '/')+1] + input)
}

// SearchParams is `new URLSearchParams(query)` for a query without its leading "?": the application/x-www-form-urlencoded parser. Unlike url.ParseQuery it keeps a pair containing ";" and a malformed percent escape, which it leaves as written, and it decodes each name and value as UTF-8 with replacement.
func SearchParams(query string) url.Values {
	values := url.Values{}
	for sequence := range strings.SplitSeq(query, "&") {
		if sequence == "" {
			continue
		}
		name, value, _ := strings.Cut(sequence, "=")
		name, value = formDecode(name), formDecode(value)
		values[name] = append(values[name], value)
	}
	return values
}

// formDecode replaces "+" with a space, percent-decodes the valid escapes and decodes the bytes as UTF-8 without BOM.
func formDecode(value string) string {
	value = strings.ReplaceAll(value, "+", " ")
	return jsstring.FromUTF8(percentDecode(value))
}
