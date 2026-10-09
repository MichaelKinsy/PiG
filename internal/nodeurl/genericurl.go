package nodeurl

import (
	"net/netip"
	"strings"
)

// URL is the part of a WHATWG URL that Node's `new URL(value)` exposes as protocol, username, password, hostname, pathname and hash, for any scheme
// the parser can read without a base.
type URL struct {
	Protocol string
	Username string
	Password string
	Hostname string
	// Pathname is the serialized path: "/..." for a special URL or any URL with a leading slash, the opaque text after "scheme:" for a URL without
	// an authority or leading slash, and "" for a host with no path.
	Pathname string
	// Hash is "#" and the percent-encoded fragment, or "" when the fragment is absent or empty.
	Hash string
	// Query is the percent-encoded text after "?", as `url.search` gives it without the "?"; `new URLSearchParams(url.search)` and SearchParams(Query) hold the same pairs.
	Query string

	// authority, query and fragment are the serialized parts of the href around the path: "//user:pass@host:port", "?query" and "#fragment" (an
	// empty fragment stays "#", which Hash omits).
	authority, query, fragment string
}

// WithPathname is `url.pathname = path; url.toString()`: the href with its path replaced. The path is used as given, as setting it would encode.
func (u URL) WithPathname(path string) string {
	return u.Protocol + u.authority + encodeURLPart(path, false) + u.query + u.fragment
}

// ParseURL is `new URL(raw)` without a base, for every scheme. The special schemes other than file (http, https, ws, wss, ftp) go through the WHATWG
// special parser, file through the file URL parser, and every other scheme is a non-special URL: "//" starts an authority whose host is opaque (not
// lowercased, forbidden code points rejected, a bracketed IPv6 address allowed), a leading "/" starts a path, and anything else is an opaque path. It
// fails where Node throws: a missing or invalid scheme, an invalid host, a port that is not digits or is above 65535, or credentials or a port without
// a host.
func ParseURL(raw string) (URL, error) {
	input := PrepareInput(raw)
	scheme, rest, found := strings.Cut(input, ":")
	if !found || scheme == "" || !isScheme(scheme) {
		return URL{}, errInvalidURL
	}
	scheme = strings.ToLower(scheme)
	switch scheme {
	case "http", "https", "ws", "wss", "ftp":
		href, err := parseSpecialHref(input, "http", "https", "ws", "wss", "ftp")
		if err != nil {
			return URL{}, err
		}
		result := URL{Protocol: href.Protocol, Username: href.Username, Password: href.Password, Hostname: href.Hostname, Pathname: href.Pathname, Hash: href.Hash, Query: href.Query}
		result.authority = "//" + credentials(href.Username, href.Password) + hostPort(href)
		if href.HasQuery {
			result.query = "?" + href.Query
		}
		if href.HasFragment {
			result.fragment = "#" + href.Fragment
		}
		return result, nil
	case "file":
		host, pathname, err := parseFileURL(input)
		if err != nil {
			return URL{}, errInvalidURL
		}
		result := URL{Protocol: "file:", Hostname: host, Pathname: encodeURLPart(pathname, false), authority: "//" + host}
		if hash := strings.IndexByte(rest, '#'); hash >= 0 {
			fragment := encodeURLSet(rest[hash+1:], "\"<>`")
			result.fragment = "#" + fragment
			if fragment != "" {
				result.Hash = "#" + fragment
			}
			rest = rest[:hash]
		}
		if mark := strings.IndexByte(rest, '?'); mark >= 0 {
			result.Query = encodeURLSet(rest[mark+1:], "\"#<>'")
			result.query = "?" + result.Query
		}
		return result, nil
	}
	result := URL{Protocol: scheme + ":"}
	if hash := strings.IndexByte(rest, '#'); hash >= 0 {
		fragment := encodeURLSet(rest[hash+1:], "\"<>`")
		result.fragment = "#" + fragment
		if fragment != "" {
			result.Hash = "#" + fragment
		}
		rest = rest[:hash]
	}
	if mark := strings.IndexByte(rest, '?'); mark >= 0 {
		result.Query = encodeURLSet(rest[mark+1:], "\"#<>")
		result.query = "?" + result.Query
		rest = rest[:mark]
	}
	switch {
	case strings.HasPrefix(rest, "//"):
		rest = rest[2:]
		authority, path := rest, ""
		if slash := strings.IndexByte(rest, '/'); slash >= 0 {
			authority, path = rest[:slash], rest[slash:]
		}
		userinfo := ""
		if at := strings.LastIndexByte(authority, '@'); at >= 0 {
			userinfo, authority = authority[:at], authority[at+1:]
			if authority == "" {
				return URL{}, errInvalidURL
			}
		}
		host, port, hasPort := authority, "", false
		insideBrackets := false
		for i := range len(authority) {
			if c := authority[i]; c == '[' {
				insideBrackets = true
			} else if c == ']' {
				insideBrackets = false
			} else if c == ':' && !insideBrackets {
				host, port, hasPort = authority[:i], authority[i+1:], true
				break
			}
		}
		if host == "" && (userinfo != "" || hasPort) {
			return URL{}, errInvalidURL
		}
		serializedPort := ""
		if hasPort && port != "" {
			normalized, err := specialPort("", port)
			if err != nil {
				return URL{}, err
			}
			serializedPort = ":" + normalized
		}
		hostname, err := opaqueHost(host)
		if err != nil {
			return URL{}, err
		}
		result.Hostname = hostname
		name, password, _ := strings.Cut(userinfo, ":")
		result.Username, result.Password = encodeURLPart(name, true), encodeURLPart(password, true)
		result.authority = "//" + credentials(result.Username, result.Password) + result.Hostname + serializedPort
		if path != "" {
			result.Pathname = nonSpecialPath(path)
		}
	case strings.HasPrefix(rest, "/"):
		result.Pathname = nonSpecialPath(rest)
	default:
		result.Pathname = encodeC0Control(rest)
	}
	return result, nil
}

// nonSpecialPath serializes the path of a non-special URL: "/" separates segments, "." and ".." (also as %2e) resolve, and the path percent-encode
// set applies. A backslash is an ordinary character.
func nonSpecialPath(path string) string {
	segments := strings.Split(strings.TrimPrefix(path, "/"), "/")
	var out []string
	for i, segment := range segments {
		last := i == len(segments)-1
		switch {
		case isDoubleDot(segment):
			if len(out) > 0 {
				out = out[:len(out)-1]
			}
			if last {
				out = append(out, "")
			}
		case isSingleDot(segment):
			if last {
				out = append(out, "")
			}
		default:
			out = append(out, segment)
		}
	}
	return encodeURLPart("/"+strings.Join(out, "/"), false)
}

// encodeC0Control percent-encodes the C0 controls and every byte above 0x7e: the C0 control percent-encode set of an opaque host or path. A space stays.
func encodeC0Control(value string) string {
	const hex = "0123456789ABCDEF"
	var out strings.Builder
	for i := range len(value) {
		c := value[i]
		if c < 0x20 || c >= 0x7f {
			out.WriteByte('%')
			out.WriteByte(hex[c>>4])
			out.WriteByte(hex[c&15])
		} else {
			out.WriteByte(c)
		}
	}
	return out.String()
}

// credentials is the serialized "user:pass@" of an href, or "" without credentials.
func credentials(username, password string) string {
	if username == "" && password == "" {
		return ""
	}
	if password != "" {
		return username + ":" + password + "@"
	}
	return username + "@"
}

// opaqueHost is the host parser for a non-special URL: a bracketed IPv6 address, or text without forbidden host code points, percent-encoded
// with the C0 control percent-encode set.
func opaqueHost(host string) (string, error) {
	if strings.HasPrefix(host, "[") {
		if !strings.HasSuffix(host, "]") {
			return "", errInvalidURL
		}
		addr, err := netip.ParseAddr(host[1 : len(host)-1])
		if err != nil || !addr.Is6() || addr.Zone() != "" {
			return "", errInvalidURL
		}
		return "[" + serializeIPv6(addr) + "]", nil
	}
	if strings.ContainsAny(host, "\x00\t\n\r #/:<>?@[\\]^|") {
		return "", errInvalidURL
	}
	const hex = "0123456789ABCDEF"
	var out strings.Builder
	for i := range len(host) {
		if c := host[i]; c < 0x20 || c > 0x7e {
			out.WriteByte('%')
			out.WriteByte(hex[c>>4])
			out.WriteByte(hex[c&15])
		} else {
			out.WriteByte(c)
		}
	}
	return out.String(), nil
}
