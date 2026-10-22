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
	// Port is the serialized port: "" when absent or the default of a special scheme.
	Port string
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

// Href is `new URL(raw).toString()`.
func (u URL) Href() string { return u.href(u.Pathname, u.query) }

// String is the href, as `String(url)` and a template literal give it.
func (u URL) String() string { return u.Href() }

// WithoutFragment is `url.hash = ""`: the URL without its fragment, an empty one ("#") included.
func (u URL) WithoutFragment() URL {
	u.Hash, u.fragment = "", ""
	return u
}

// href is the URL serializer over the serialized path and query: a URL without a host whose path starts with "//" writes "/." before the path, so the path cannot
// read back as an authority.
func (u URL) href(pathname, query string) string {
	prefix := u.Protocol + u.authority
	if u.authority == "" && strings.HasPrefix(pathname, "//") {
		prefix += "/."
	}
	return prefix + pathname + query + u.fragment
}

// Origin is `url.origin`: scheme, host and port of a special URL other than file; for a blob URL, the origin of the http or https URL its path holds; and "null" for
// every other URL, file included, whose origin is opaque.
func (u URL) Origin() string {
	switch u.Protocol {
	case "http:", "https:", "ws:", "wss:", "ftp:":
		if u.Port != "" {
			return u.Protocol + "//" + u.Hostname + ":" + u.Port
		}
		return u.Protocol + "//" + u.Hostname
	case "blob:":
		if inner, err := ParseURL(u.Pathname); err == nil && (inner.Protocol == "http:" || inner.Protocol == "https:") {
			return inner.Origin()
		}
	}
	return "null"
}

// WithPort is `url.port = port` for a port given as decimal digits: the default port of a special scheme is dropped, and a URL without a host, or a file URL, cannot have one.
func (u URL) WithPort(port string) URL {
	if u.Hostname == "" || u.Protocol == "file:" || !strings.HasPrefix(u.authority, "//") {
		return u
	}
	switch {
	case u.Protocol == "http:" && port == "80", u.Protocol == "ws:" && port == "80", u.Protocol == "https:" && port == "443", u.Protocol == "wss:" && port == "443", u.Protocol == "ftp:" && port == "21":
		port = ""
	}
	u.Port = port
	u.authority = "//" + credentials(u.Username, u.Password) + u.Hostname
	if port != "" {
		u.authority += ":" + port
	}
	return u
}

// WithoutHash is `url.hash = ""`: the URL without its fragment.
func (u URL) WithoutHash() URL {
	u.Hash, u.fragment = "", ""
	return u
}

// WithoutCredentials is `url.username = ""; url.password = ""`: the URL without its userinfo. A URL that has none, such as every file URL, is unchanged.
func (u URL) WithoutCredentials() URL {
	if u.Username == "" && u.Password == "" {
		return u
	}
	u.authority = "//" + strings.TrimPrefix(u.authority, "//"+credentials(u.Username, u.Password))
	u.Username, u.Password = "", ""
	return u
}

// WithQuery is `url.search = "?" + query` for a query already percent-encoded: the URL with that query.
func (u URL) WithQuery(query string) URL {
	u.Query = query
	u.query = "?" + query
	return u
}

// WithoutQuery is `url.search = ""`: the URL without its query, so its href has no "?".
func (u URL) WithoutQuery() URL {
	u.Query, u.query = "", ""
	return u
}

// WithPathname is `url.pathname = path; url.toString()`: the href with its path replaced. The path is used as given, as setting it would encode.
// The setter does nothing to a URL with an opaque path (no authority, and a path that does not start with "/"), so its href is unchanged.
func (u URL) WithPathname(path string) string {
	if u.authority == "" && !strings.HasPrefix(u.Pathname, "/") {
		return u.Href()
	}
	return u.href(encodeURLPart(path, false), u.query)
}

// ResolveAbsolutePath is `new URL(path, base).toString()` for a path that starts with a single "/" and is already in its serialized form (no dot segments, nothing the
// path percent-encode set changes): the scheme, userinfo and host of the base, the path, no query and no fragment. A file base whose first path segment starts with a
// drive letter ("C:", exactly two characters as in ada 4, Node 24.22.1 and 26.12.1; Node 22.19 and 24.19 test only a segment's first two) keeps that segment unless the path starts with a drive letter itself. It fails where the
// constructor throws: a base whose path is opaque (no authority and no leading slash) cannot be a base.
func (u URL) ResolveAbsolutePath(path string) (string, error) {
	if u.authority == "" && !strings.HasPrefix(u.Pathname, "/") {
		return "", errInvalidURL
	}
	if u.Protocol == "file:" && !startsWithWindowsDriveLetter(path[min(1, len(path)):]) {
		first, _, _ := strings.Cut(strings.TrimPrefix(u.Pathname, "/"), "/")
		if isNormalizedWindowsDriveLetter(first) {
			path = "/" + first + path
		}
	}
	return u.Protocol + u.authority + path, nil
}

// startsWithWindowsDriveLetter is the URL standard's "starts with a Windows drive letter": a drive letter followed by the end, "/", "\", "?" or "#".
func startsWithWindowsDriveLetter(s string) bool {
	return len(s) >= 2 && isWindowsDriveLetter(s[:2]) && (len(s) == 2 || strings.IndexByte(`/\?#`, s[2]) >= 0)
}

// WithSearch is `url.search = query; url.toString()`: the setter removes the query for "", otherwise drops one leading "?" and then every tab and newline (an empty
// query then serializes as "?"), applies the query percent-encode set (the special one for http, https, ws, wss, ftp and file), and keeps the path and the fragment.
func (u URL) WithSearch(query string) string {
	if query == "" {
		return u.href(u.Pathname, "")
	}
	extra := "\"#<>"
	if isSpecialScheme(u.Protocol) {
		extra += "'"
	}
	return u.href(u.Pathname, "?"+encodeURLSet(removeTabAndNewline(strings.TrimPrefix(query, "?")), extra))
}

// removeTabAndNewline is the URL parser's removal of every ASCII tab and newline from its input, which applies to a setter's value as well.
func removeTabAndNewline(value string) string {
	return strings.Map(func(r rune) rune {
		if r == '\t' || r == '\n' || r == '\r' {
			return -1
		}
		return r
	}, value)
}

func isSpecialScheme(protocol string) bool {
	switch protocol {
	case "http:", "https:", "ws:", "wss:", "ftp:", "file:":
		return true
	}
	return false
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
		result := URL{Protocol: href.Protocol, Username: href.Username, Password: href.Password, Hostname: href.Hostname, Port: href.Port, Pathname: href.Pathname, Query: href.Query}
		result.authority = "//" + credentials(href.Username, href.Password) + hostPort(href)
		if href.HasQuery {
			result.query = "?" + href.Query
		}
		// url.hash is the encoded fragment; HTTPURL.Hash keeps the raw one.
		if href.HasFragment {
			result.fragment = "#" + href.Fragment
			if href.Fragment != "" {
				result.Hash = result.fragment
			}
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
		result.Port = strings.TrimPrefix(serializedPort, ":")
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
		// The opaque-path state writes a space that directly precedes "?" or "#" as "%20".
		if (result.query != "" || result.fragment != "") && strings.HasSuffix(result.Pathname, " ") {
			result.Pathname = strings.TrimSuffix(result.Pathname, " ") + "%20"
		}
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
