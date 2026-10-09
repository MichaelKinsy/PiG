package nodeurl

// Implements the WHATWG URL parser (https://url.spec.whatwg.org/#concept-basic-url-parser) for an http or https input without a base: the subset behind Node's `URL.canParse(raw)` and the protocol, hostname, port, search and hash of `new URL(raw)`.

import (
	"slices"
	"strconv"
	"strings"
)

// HTTPURL is the part of a WHATWG http or https URL that Node's `new URL(value)` exposes as protocol, hostname, port, pathname, search and hash. Pathname is Node's: dot segments (including "%2e") and backslashes are resolved and the path percent-encode set is applied. Search and Hash keep the raw query and fragment after the "?" or "#": the parser's percent-encoding is not applied, so they equal Node's values only in being empty or not.
type HTTPURL struct {
	Protocol string
	Hostname string
	Port     string
	Pathname string
	Search   string
	Hash     string
}

// ParseHTTPURL is `URL.canParse(raw)` and `new URL(raw)` for an http or https URL without a base. It fails for a URL the parser rejects and for any other scheme, which the callers treat alike: they accept only `http:` and `https:`.
func ParseHTTPURL(raw string) (HTTPURL, error) {
	parsed, err := ParseHTTPHref(raw)
	return parsed.HTTPURL, err
}

// HTTPHref is an http or https URL with the serialization of `new URL(raw)`.
type HTTPHref struct {
	HTTPURL
	// Href is the serialized URL and Origin its origin, as `new URL(raw).href` and `.origin` give them: dot segments
	// resolved, the path, query and fragment percent-encoded, the default port and an empty path normalized.
	Href   string
	Origin string
	// The embedded Pathname is `new URL(raw).pathname`.
	// Username and Password are the percent-encoded userinfo. Query and Fragment are the encoded text after "?" and
	// "#", with HasQuery and HasFragment telling an empty one from an absent one.
	Username, Password string
	Query, Fragment    string
	HasQuery           bool
	HasFragment        bool
}

// ParseHTTPHref is ParseHTTPURL with the serialized URL, origin, pathname, userinfo, query and fragment.
func ParseHTTPHref(raw string) (HTTPHref, error) {
	return parseSpecialHref(raw, "http", "https")
}

// parseSpecialHref is the WHATWG parser for a special URL other than file: the scheme must be one of schemes, which share the authority grammar and
// differ only in a default port.
func parseSpecialHref(raw string, schemes ...string) (HTTPHref, error) {
	input := PrepareInput(raw)
	scheme, rest, found := strings.Cut(input, ":")
	if !found || !isScheme(scheme) {
		return HTTPHref{}, errInvalidURL
	}
	scheme = strings.ToLower(scheme)
	if !slices.Contains(schemes, scheme) {
		return HTTPHref{}, errInvalidURL
	}
	// Special authority ignore slashes state: any run of "/" and "\" before the authority is skipped.
	rest = strings.TrimLeftFunc(rest, func(r rune) bool { return r == '/' || r == '\\' })
	authorityEnd := strings.IndexAny(rest, "/\\?#")
	if authorityEnd < 0 {
		authorityEnd = len(rest)
	}
	authority, tail := rest[:authorityEnd], rest[authorityEnd:]
	userinfo := ""
	if at := strings.LastIndexByte(authority, '@'); at >= 0 {
		userinfo = authority[:at]
		authority = authority[at+1:]
	}
	host, port := authority, ""
	if bracket := strings.LastIndexByte(authority, ']'); strings.HasPrefix(authority, "[") && bracket >= 0 {
		host, port = authority[:bracket+1], authority[bracket+1:]
		if port != "" && port[0] != ':' {
			return HTTPHref{}, errInvalidURL
		}
		port = strings.TrimPrefix(port, ":")
	} else if colon := strings.IndexByte(authority, ':'); colon >= 0 {
		host, port = authority[:colon], authority[colon+1:]
	}
	if host == "" {
		return HTTPHref{}, errInvalidURL
	}
	hostname, err := SpecialHost(host)
	if err != nil || hostname == "" {
		return HTTPHref{}, errInvalidURL
	}
	normalizedPort, err := specialPort(scheme, port)
	if err != nil {
		return HTTPHref{}, err
	}
	result := HTTPHref{HTTPURL: HTTPURL{Protocol: scheme + ":", Hostname: hostname, Port: normalizedPort}}
	fragment, hasFragment := "", false
	if hash := strings.IndexByte(tail, '#'); hash >= 0 {
		fragment, hasFragment = tail[hash+1:], true
		if fragment != "" {
			result.Hash = "#" + fragment
		}
		tail = tail[:hash]
	}
	query, hasQuery := "", false
	if mark := strings.IndexByte(tail, '?'); mark >= 0 {
		query, hasQuery = tail[mark+1:], true
		if query != "" {
			result.Search = "?" + query
		}
	}
	result.Pathname = SpecialPath(tail, false)
	result.Pathname = encodeURLPart(result.Pathname, false)
	result.Origin = result.Protocol + "//" + hostPort(result)
	href := result.Origin
	name, password, _ := strings.Cut(userinfo, ":")
	result.Username, result.Password = encodeURLPart(name, true), encodeURLPart(password, true)
	if result.Username != "" || result.Password != "" {
		credentials := result.Username
		if result.Password != "" {
			credentials += ":" + result.Password
		}
		href = result.Protocol + "//" + credentials + "@" + hostPort(result)
	}
	href += result.Pathname
	result.HasQuery, result.HasFragment = hasQuery, hasFragment
	if hasQuery {
		result.Query = encodeURLSet(query, "\"#<>'")
		href += "?" + result.Query
	}
	if hasFragment {
		result.Fragment = encodeURLSet(fragment, "\"<>`")
		href += "#" + result.Fragment
	}
	result.Href = href
	return result, nil
}

func hostPort(u HTTPHref) string {
	if u.Port == "" {
		return u.Hostname
	}
	return u.Hostname + ":" + u.Port
}

// encodeURLSet percent-encodes C0 controls, space, every byte above 0x7e and the bytes of extra, as the WHATWG query
// and fragment percent-encode sets do.
func encodeURLSet(value, extra string) string {
	const hex = "0123456789ABCDEF"
	var out strings.Builder
	for i := range len(value) {
		c := value[i]
		if c <= 0x20 || c >= 0x7f || strings.IndexByte(extra, c) >= 0 {
			out.WriteByte('%')
			out.WriteByte(hex[c>>4])
			out.WriteByte(hex[c&15])
		} else {
			out.WriteByte(c)
		}
	}
	return out.String()
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
	if value == map[string]int{"http": 80, "https": 443, "ws": 80, "wss": 443, "ftp": 21}[scheme] {
		return "", nil
	}
	return strconv.Itoa(value), nil
}
