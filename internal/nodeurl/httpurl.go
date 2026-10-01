package nodeurl

// Implements the WHATWG URL parser (https://url.spec.whatwg.org/#concept-basic-url-parser) for an http or https input without a base: the subset behind Node's `URL.canParse(raw)` and the protocol, hostname, port, search and hash of `new URL(raw)`.

import (
	"strconv"
	"strings"
)

// HTTPURL is the part of a WHATWG http or https URL that Node's `new URL(value)` exposes as protocol, hostname, port, search and hash. Search and Hash keep the raw query and fragment after the "?" or "#": the parser's percent-encoding is not applied, so they equal Node's values only in being empty or not.
type HTTPURL struct {
	Protocol string
	Hostname string
	Port     string
	Search   string
	Hash     string
}

// ParseHTTPURL is `URL.canParse(raw)` and `new URL(raw)` for an http or https URL without a base. It fails for a URL the parser rejects and for any other scheme, which the callers treat alike: they accept only `http:` and `https:`.
func ParseHTTPURL(raw string) (HTTPURL, error) {
	input := PrepareInput(raw)
	scheme, rest, found := strings.Cut(input, ":")
	if !found || !isScheme(scheme) {
		return HTTPURL{}, errInvalidURL
	}
	scheme = strings.ToLower(scheme)
	if scheme != "http" && scheme != "https" {
		return HTTPURL{}, errInvalidURL
	}
	// Special authority ignore slashes state: any run of "/" and "\" before the authority is skipped.
	rest = strings.TrimLeftFunc(rest, func(r rune) bool { return r == '/' || r == '\\' })
	authorityEnd := strings.IndexAny(rest, "/\\?#")
	if authorityEnd < 0 {
		authorityEnd = len(rest)
	}
	authority, tail := rest[:authorityEnd], rest[authorityEnd:]
	if at := strings.LastIndexByte(authority, '@'); at >= 0 {
		authority = authority[at+1:]
	}
	host, port := authority, ""
	if bracket := strings.LastIndexByte(authority, ']'); strings.HasPrefix(authority, "[") && bracket >= 0 {
		host, port = authority[:bracket+1], authority[bracket+1:]
		if port != "" && port[0] != ':' {
			return HTTPURL{}, errInvalidURL
		}
		port = strings.TrimPrefix(port, ":")
	} else if colon := strings.IndexByte(authority, ':'); colon >= 0 {
		host, port = authority[:colon], authority[colon+1:]
	}
	if host == "" {
		return HTTPURL{}, errInvalidURL
	}
	hostname, err := SpecialHost(host)
	if err != nil || hostname == "" {
		return HTTPURL{}, errInvalidURL
	}
	normalizedPort, err := specialPort(scheme, port)
	if err != nil {
		return HTTPURL{}, err
	}
	result := HTTPURL{Protocol: scheme + ":", Hostname: hostname, Port: normalizedPort}
	if hash := strings.IndexByte(tail, '#'); hash >= 0 {
		if fragment := tail[hash+1:]; fragment != "" {
			result.Hash = "#" + fragment
		}
		tail = tail[:hash]
	}
	if query := strings.IndexByte(tail, '?'); query >= 0 {
		if search := tail[query+1:]; search != "" {
			result.Search = "?" + search
		}
	}
	return result, nil
}

// specialPort is the port state of the parser: digits only, at most 65535, and the scheme's default port serializes as an empty port.
func specialPort(scheme, port string) (string, error) {
	if port == "" {
		return "", nil
	}
	value := 0
	for _, c := range []byte(port) {
		if c < '0' || c > '9' {
			return "", errInvalidURL
		}
		value = value*10 + int(c-'0')
		if value > 65535 {
			return "", errInvalidURL
		}
	}
	if value == map[string]int{"http": 80, "https": 443}[scheme] {
		return "", nil
	}
	return strconv.Itoa(value), nil
}
