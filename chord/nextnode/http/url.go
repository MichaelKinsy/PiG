package http

import (
	nethttp "net/http"
	"net/netip"
	neturl "net/url"
	"strconv"
	"strings"
)

// The URL parser's part of what the component reads (Pi calls `new URL(...)` on `http://<Host><target>`; the WHATWG URL
// parser is Pi's runtime, Go has none): the path with `\` as `/`, its dot segments resolved (also the percent-encoded
// `%2e` ones), its characters percent-encoded as the path and query encode sets say, and the checks of the authority the
// listener's refusals depend on (a malformed `Host` 400, one with userinfo 403). Go-only file.

// webURL is the serialized URL of request: its target (the request line's, for a server's request) as the URL parser
// serializes it, over the `Host` the client sent.
func webURL(request *nethttp.Request) string {
	host, ok := requestHeader(request, "host")
	if !ok || host == "" {
		host = "invalid"
	}
	target := request.RequestURI
	if !strings.HasPrefix(target, "/") {
		target = "/"
		if request.URL != nil {
			if path := request.URL.EscapedPath(); path != "" {
				target = path
			}
			if request.URL.RawQuery != "" {
				target += "?" + request.URL.RawQuery
			}
		}
	}
	return "http://" + host + serializeTarget(target)
}

// parseWireURL is `new URL("http://" + host + target)` for an origin-form target: the URL as serialized, or the status that
// refuses it at the wire (400 when it does not parse, 403 for userinfo).
func parseWireURL(host, target string) (string, int) {
	if strings.Contains(host, "@") {
		return "", 403
	}
	if !validAuthority(host) {
		return "", 400
	}
	return "http://" + host + serializeTarget(target), 0
}

// serializeTarget is the path and query of an origin-form target as the URL parser writes them: the fragment dropped, `\`
// as `/` in the path, dot segments resolved, each part percent-encoded by its set.
func serializeTarget(target string) string {
	if at := strings.IndexByte(target, '#'); at >= 0 {
		target = target[:at]
	}
	path, query, hasQuery := strings.Cut(target, "?")
	out := encodeSet(normalizePath(path), pathEncode)
	if hasQuery {
		out += "?" + encodeSet(query, queryEncode)
	}
	return out
}

// normalizePath resolves the dot segments of an absolute path (`\` counts as `/`).
func normalizePath(path string) string {
	path = strings.ReplaceAll(path, "\\", "/")
	parts := strings.Split(path[1:], "/")
	out := make([]string, 0, len(parts))
	for index, part := range parts {
		last := index == len(parts)-1
		switch dotKind(part) {
		case 1:
			if last {
				out = append(out, "")
			}
		case 2:
			if len(out) > 0 {
				out = out[:len(out)-1]
			}
			if last {
				out = append(out, "")
			}
		default:
			out = append(out, part)
		}
	}
	return "/" + strings.Join(out, "/")
}

// dotKind is 1 for a single-dot segment (`.`, `%2e`), 2 for a double-dot one, else 0.
func dotKind(segment string) int {
	switch strings.ToLower(segment) {
	case ".", "%2e":
		return 1
	case "..", ".%2e", "%2e.", "%2e%2e":
		return 2
	}
	return 0
}

func pathEncode(b byte) bool {
	return b <= 0x20 || b == '"' || b == '#' || b == '<' || b == '>' || b == '?' || b == '`' || b == '{' || b == '}' || b >= 0x7f
}

func queryEncode(b byte) bool {
	return b <= 0x20 || b == '"' || b == '#' || b == '<' || b == '>' || b == '\'' || b >= 0x7f
}

// encodeSet percent-encodes the bytes of text that encode selects.
func encodeSet(text string, encode func(byte) bool) string {
	clean := true
	for i := 0; i < len(text); i++ {
		if encode(text[i]) {
			clean = false
			break
		}
	}
	if clean {
		return text
	}
	const hex = "0123456789ABCDEF"
	var out strings.Builder
	for i := 0; i < len(text); i++ {
		b := text[i]
		if encode(b) {
			out.WriteByte('%')
			out.WriteByte(hex[b>>4])
			out.WriteByte(hex[b&15])
		} else {
			out.WriteByte(b)
		}
	}
	return out.String()
}

// validAuthority is whether the URL parser accepts host (`name`, `name:port`, `[v6]:port`) as the host of an http URL.
func validAuthority(authority string) bool {
	host, port := authority, ""
	if strings.HasPrefix(authority, "[") {
		end := strings.IndexByte(authority, ']')
		if end < 0 {
			return false
		}
		host = authority[:end+1]
		rest := authority[end+1:]
		if rest != "" {
			if rest[0] != ':' {
				return false
			}
			port = rest[1:]
		}
		if _, err := netip.ParseAddr(host[1 : len(host)-1]); err != nil {
			return false
		}
	} else if at := strings.IndexByte(authority, ':'); at >= 0 {
		host, port = authority[:at], authority[at+1:]
	}
	if port != "" {
		value, err := strconv.Atoi(port)
		if err != nil || value < 0 || value > 65535 {
			return false
		}
		for i := 0; i < len(port); i++ {
			if port[i] < '0' || port[i] > '9' {
				return false
			}
		}
	}
	if strings.HasPrefix(host, "[") {
		return true
	}
	if host == "" {
		return false
	}
	for i := 0; i < len(host); i++ {
		if forbiddenHostByte(host[i]) {
			return false
		}
	}
	return true
}

// forbiddenHostByte is a forbidden domain code point of the URL standard.
func forbiddenHostByte(b byte) bool {
	if b <= 0x20 || b == 0x7f {
		return true
	}
	switch b {
	case '#', '%', '/', ':', '<', '>', '?', '@', '[', '\\', ']', '^', '|':
		return true
	}
	return false
}

// parseURLString is s as a *url.URL.
func parseURLString(s string) (*neturl.URL, error) { return neturl.Parse(s) }
