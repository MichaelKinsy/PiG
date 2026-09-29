package nodeurl

// Ports packages/ai/src/api/mistral-conversations.ts (WHATWG URL serialization at the fetch boundary).

import (
	"net/url"
	"strings"
)

func resolveDirectoryPath(base, relative string, file bool) string {
	path := strings.TrimRight(SpecialPath(base, file), "/") + "/" + relative
	return encodeURLPart(SpecialPath(path, file), false)
}

// encodeURLPart applies WHATWG's path or userinfo percent-encode set. Percent signs are preserved even when they do not start a valid escape.
func encodeURLPart(value string, userinfo bool) string {
	const hex = "0123456789ABCDEF"
	var out strings.Builder
	for i := range len(value) {
		c := value[i]
		encode := c <= 0x20 || c >= 0x7f || strings.ContainsRune("\"#<>?^`{}", rune(c))
		if userinfo && strings.ContainsRune("/:;=@[\\]|", rune(c)) {
			encode = true
		}
		if encode {
			out.WriteByte('%')
			out.WriteByte(hex[c>>4])
			out.WriteByte(hex[c&15])
		} else {
			out.WriteByte(c)
		}
	}
	return out.String()
}

// RequestURL represents an already-resolved WHATWG URL without net/url rewriting its serialized spelling. Opaque retains path characters and malformed percent escapes that Go's RawPath cannot represent; Host and Path still identify the request destination.
func RequestURL(serialized string) (*url.URL, error) {
	parsed, err := url.Parse(serialized)
	if err == nil && parsed.String() == serialized {
		return parsed, nil
	}
	scheme, rest, ok := strings.Cut(serialized, "://")
	if !ok || !isScheme(scheme) {
		return nil, errInvalidURL
	}
	authority, path, _ := strings.Cut(rest, "/")
	host := authority
	var user *url.Userinfo
	if at := strings.LastIndexByte(authority, '@'); at >= 0 {
		name, password, hasPassword := strings.Cut(authority[:at], ":")
		name = string(percentDecode(name))
		if hasPassword {
			user = url.UserPassword(name, string(percentDecode(password)))
		} else {
			user = url.User(name)
		}
		host = authority[at+1:]
	}
	if host == "" && scheme != "file" {
		return nil, errInvalidURL
	}
	path = "/" + path
	return &url.URL{Scheme: scheme, Host: host, User: user, Path: string(percentDecode(path)), RawPath: path, Opaque: "//" + authority + path}, nil
}
