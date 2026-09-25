package nodeurl

import (
	"net/netip"
	"runtime"
	"strings"
	"unicode/utf8"

	"golang.org/x/net/idna"
)

// Error is an error Node's url.fileURLToPath throws, with its code. URIError
// comes from decodeURIComponent and has no code.
type Error struct {
	Code    string
	Message string
}

func (e *Error) Error() string { return e.Message }

var (
	errInvalidURL         = &Error{Code: "ERR_INVALID_URL", Message: "Invalid URL"}
	errInvalidURLScheme   = &Error{Code: "ERR_INVALID_URL_SCHEME", Message: "The URL must be of scheme file"}
	errPathNotAbsolute    = &Error{Code: "ERR_INVALID_FILE_URL_PATH", Message: "File URL path must be absolute"}
	errURIMalformed       = &Error{Message: "URI malformed"}
	errEncodedSeparators  = &Error{Code: "ERR_INVALID_FILE_URL_PATH", Message: `File URL path must not include encoded \ or / characters`}
	errEncodedSlash       = &Error{Code: "ERR_INVALID_FILE_URL_PATH", Message: "File URL path must not include encoded / characters"}
	whatwgDomainToUnicode = idna.New(
		idna.MapForLookup(),
		idna.Transitional(false),
		idna.StrictDomainName(false),
		idna.CheckHyphens(false),
	)
)

// FileURLToPath converts raw as Node's url.fileURLToPath(raw, {windows})
// does: raw is parsed as a WHATWG URL, and a Windows host names a UNC server
// in Unicode. It returns the Error Node throws for a URL that does not parse,
// is not a file URL, encodes a path separator, has a host on POSIX, or on
// Windows has neither a host nor a drive letter.
func FileURLToPath(raw string, windows bool) (string, error) {
	host, pathname, err := parseFileURL(raw)
	if err != nil {
		return "", err
	}
	if !windows {
		if host != "" {
			return "", &Error{Code: "ERR_INVALID_FILE_URL_HOST", Message: `File URL host must be "localhost" or empty on ` + nodePlatform()}
		}
		if containsEncoded(pathname, "2f") {
			return "", errEncodedSlash
		}
		return decodeURIComponent(pathname)
	}
	if containsEncoded(pathname, "2f") || containsEncoded(pathname, "5c") {
		return "", errEncodedSeparators
	}
	decoded, err := decodeURIComponent(strings.ReplaceAll(pathname, "/", `\`))
	if err != nil {
		return "", err
	}
	if host != "" {
		return `\\` + DomainToUnicode(host) + decoded, nil
	}
	if len(decoded) < 3 || decoded[1]|0x20 < 'a' || decoded[1]|0x20 > 'z' || decoded[2] != ':' {
		return "", errPathNotAbsolute
	}
	return decoded[1:], nil
}

// DomainToUnicode is Node's url.domainToUnicode for a host FileHost returned:
// an IP address is unchanged, and a domain's IDNA labels become Unicode.
func DomainToUnicode(host string) string {
	if strings.HasPrefix(host, "[") {
		return host
	}
	if _, err := netip.ParseAddr(host); err == nil {
		return host
	}
	unicode, err := whatwgDomainToUnicode.ToUnicode(host)
	if err != nil {
		return ""
	}
	return unicode
}

// parseFileURL runs the WHATWG URL parser on raw far enough to return a file
// URL's host and serialized path.
func parseFileURL(raw string) (host, pathname string, err error) {
	input := strings.TrimFunc(raw, func(r rune) bool { return r <= 0x20 })
	input = strings.Map(func(r rune) rune {
		if r == '\t' || r == '\n' || r == '\r' {
			return -1
		}
		return r
	}, input)
	colon := strings.IndexByte(input, ':')
	if colon <= 0 || !isScheme(input[:colon]) {
		return "", "", errInvalidURL
	}
	if !strings.EqualFold(input[:colon], "file") {
		return "", "", errInvalidURLScheme
	}
	rest := input[colon+1:]
	if len(rest) >= 2 && isSlash(rest[0]) && isSlash(rest[1]) {
		rest = rest[2:]
		end := strings.IndexAny(rest, `/\?#`)
		if end < 0 {
			end = len(rest)
		}
		// A drive letter where the host would be starts the path.
		if buffer := rest[:end]; !isWindowsDriveLetter(buffer) {
			if host, err = FileHost(buffer); err != nil {
				return "", "", errInvalidURL
			}
			rest = rest[end:]
		}
	}
	return host, "/" + strings.Join(parseFilePath(rest), "/"), nil
}

// parseFilePath is the WHATWG path state for a file URL: segments split on /
// and \, the query and fragment dropped, dot segments resolved, and a leading
// Windows drive letter normalized to X: and never removed by "..".
func parseFilePath(rest string) []string {
	if end := strings.IndexAny(rest, "?#"); end >= 0 {
		rest = rest[:end]
	}
	if rest != "" && isSlash(rest[0]) {
		rest = rest[1:]
	}
	segments := splitSegments(rest)
	var path []string
	for i, buffer := range segments {
		last := i == len(segments)-1
		switch {
		case isDoubleDot(buffer):
			// Shortening never removes a file URL's lone drive letter.
			if len(path) > 1 || len(path) == 1 && !isNormalizedWindowsDriveLetter(path[0]) {
				path = path[:len(path)-1]
			}
			if last {
				path = append(path, "")
			}
		case isSingleDot(buffer):
			if last {
				path = append(path, "")
			}
		default:
			if len(path) == 0 && isWindowsDriveLetter(buffer) {
				buffer = buffer[:1] + ":"
			}
			path = append(path, buffer)
		}
	}
	return path
}

// splitSegments splits rest on / and \, keeping empty segments.
func splitSegments(rest string) []string {
	var segments []string
	start := 0
	for i := range len(rest) {
		if isSlash(rest[i]) {
			segments = append(segments, rest[start:i])
			start = i + 1
		}
	}
	return append(segments, rest[start:])
}

func isSlash(c byte) bool { return c == '/' || c == '\\' }

func isScheme(s string) bool {
	for i := range len(s) {
		c := s[i] | 0x20
		switch {
		case c >= 'a' && c <= 'z':
		case i > 0 && (s[i] >= '0' && s[i] <= '9' || s[i] == '+' || s[i] == '-' || s[i] == '.'):
		default:
			return false
		}
	}
	return true
}

func isWindowsDriveLetter(s string) bool {
	return len(s) == 2 && (s[0]|0x20 >= 'a' && s[0]|0x20 <= 'z') && (s[1] == ':' || s[1] == '|')
}

func isNormalizedWindowsDriveLetter(s string) bool {
	return isWindowsDriveLetter(s) && s[1] == ':'
}

func isSingleDot(s string) bool { return s == "." || strings.EqualFold(s, "%2e") }

func isDoubleDot(s string) bool {
	switch strings.ToLower(s) {
	case "..", ".%2e", "%2e.", "%2e%2e":
		return true
	}
	return false
}

// containsEncoded reports whether pathname holds %<hex>, in either case.
func containsEncoded(pathname, hex string) bool {
	return strings.Contains(strings.ToLower(pathname), "%"+hex)
}

// decodeURIComponent is JavaScript's decodeURIComponent: every %XX becomes
// its byte, and a malformed escape or invalid UTF-8 is a URIError.
func decodeURIComponent(s string) (string, error) {
	if !strings.Contains(s, "%") {
		return s, nil
	}
	var out []byte
	for i := 0; i < len(s); i++ {
		if s[i] != '%' {
			out = append(out, s[i])
			continue
		}
		if i+2 >= len(s) || !isHex(s[i+1]) || !isHex(s[i+2]) {
			return "", errURIMalformed
		}
		out = append(out, unhex(s[i+1])<<4|unhex(s[i+2]))
		i += 2
	}
	if !utf8.Valid(out) {
		return "", errURIMalformed
	}
	return string(out), nil
}

// percentDecode is the WHATWG percent-decode: %XX becomes its byte, and a %
// without two hex digits stays.
func percentDecode(s string) []byte {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '%' && i+2 < len(s) && isHex(s[i+1]) && isHex(s[i+2]) {
			out = append(out, unhex(s[i+1])<<4|unhex(s[i+2]))
			i += 2
			continue
		}
		out = append(out, s[i])
	}
	return out
}

func isHex(c byte) bool {
	return c >= '0' && c <= '9' || c|0x20 >= 'a' && c|0x20 <= 'f'
}

func unhex(c byte) byte {
	if c <= '9' {
		return c - '0'
	}
	return c | 0x20 - 'a' + 10
}

// nodePlatform is process.platform for this build.
func nodePlatform() string {
	if runtime.GOOS == "windows" {
		return "win32"
	}
	return runtime.GOOS
}
