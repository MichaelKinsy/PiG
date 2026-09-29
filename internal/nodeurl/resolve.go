package nodeurl

// Ports packages/ai/src/api/mistral-conversations.ts (requestMistralStream URL resolution).

import (
	"net/url"
	"strconv"
	"strings"
)

// ResolveDirectoryPath resolves a relative URL path beneath a normalized directory base. It applies the WHATWG special-URL authority, host and path rules before resolution; the base query and fragment do not participate in the resulting path.
func ResolveDirectoryPath(raw, relativePath string) (string, error) {
	input := PrepareInput(raw)
	scheme, rest, found := strings.Cut(input, ":")
	if !found || !isScheme(scheme) {
		return "", errInvalidURL
	}
	scheme = strings.ToLower(scheme)
	if end := strings.IndexAny(rest, "?#"); end >= 0 {
		rest = rest[:end]
	}
	switch scheme {
	case "http", "https", "ws", "wss", "ftp":
		var err error
		input, err = normalizeSpecialAuthority(scheme, rest)
		if err != nil {
			return "", err
		}
		authority, path, _ := strings.Cut(strings.TrimPrefix(input, scheme+"://"), "/")
		return scheme + "://" + authority + resolveDirectoryPath("/"+path, relativePath, false), nil
	case "file":
		host, pathname, err := parseFileURL(input)
		if err != nil {
			return "", errInvalidURL
		}
		return "file://" + host + resolveDirectoryPath(pathname, relativePath, true), nil
	default:
		input = scheme + ":" + rest
	}
	base, err := url.Parse(input)
	if err != nil || !base.IsAbs() || base.Opaque != "" {
		return "", errInvalidURL
	}
	base.Scheme = scheme
	base.RawPath = strings.TrimRight(SpecialPath(base.EscapedPath(), scheme == "file"), "/") + "/"
	base.Path, err = url.PathUnescape(base.RawPath)
	if err != nil {
		return "", errInvalidURL
	}
	return base.ResolveReference(&url.URL{Path: relativePath}).String(), nil
}

func normalizeSpecialAuthority(scheme, rest string) (string, error) {
	rest = strings.TrimLeft(strings.ReplaceAll(rest, "\\", "/"), "/")
	authority, path, hasPath := strings.Cut(rest, "/")
	userinfo := ""
	if at := strings.LastIndexByte(authority, '@'); at >= 0 {
		user, password, _ := strings.Cut(authority[:at], ":")
		user, password = encodeURLPart(user, true), encodeURLPart(password, true)
		if user != "" || password != "" {
			userinfo = user
			if password != "" {
				userinfo += ":" + password
			}
			userinfo += "@"
		}
		authority = authority[at+1:]
	}
	host, port := authority, ""
	if strings.HasPrefix(authority, "[") {
		end := strings.IndexByte(authority, ']')
		if end < 0 {
			return "", errInvalidURL
		}
		host = authority[:end+1]
		if suffix := authority[end+1:]; suffix != "" {
			var ok bool
			port, ok = strings.CutPrefix(suffix, ":")
			if !ok {
				return "", errInvalidURL
			}
		}
	} else if colon := strings.LastIndexByte(authority, ':'); colon >= 0 {
		host, port = authority[:colon], authority[colon+1:]
	}
	if host == "" {
		return "", errInvalidURL
	}
	host, err := SpecialHost(host)
	if err != nil {
		return "", errInvalidURL
	}
	if port != "" {
		if strings.Trim(port, "0123456789") != "" {
			return "", errInvalidURL
		}
		value, err := strconv.ParseUint(port, 10, 16)
		if err != nil {
			return "", errInvalidURL
		}
		port = strconv.FormatUint(value, 10)
		if (scheme == "http" || scheme == "ws") && port == "80" || (scheme == "https" || scheme == "wss") && port == "443" || scheme == "ftp" && port == "21" {
			port = ""
		}
	}
	result := scheme + "://" + userinfo + host
	if port != "" {
		result += ":" + port
	}
	if hasPath {
		result += "/" + path
	}
	return result, nil
}
