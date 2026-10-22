package nodeurl

// Implements the serializer and ordered updates of `URLSearchParams` that `url.searchParams.set(...)` applies to a parsed URL.

import "strings"

// FormURLEncode is the application/x-www-form-urlencoded byte serializer that `URLSearchParams.toString()` uses: ASCII alphanumerics and "*-._" stay, a space becomes "+", and every other byte is percent-encoded.
func FormURLEncode(value string) string {
	const upperhex = "0123456789ABCDEF"
	var out strings.Builder
	out.Grow(len(value))
	for i := range len(value) {
		c := value[i]
		switch {
		case 'a' <= c && c <= 'z', 'A' <= c && c <= 'Z', '0' <= c && c <= '9', c == '*', c == '-', c == '.', c == '_':
			out.WriteByte(c)
		case c == ' ':
			out.WriteByte('+')
		default:
			out.WriteByte('%')
			out.WriteByte(upperhex[c>>4])
			out.WriteByte(upperhex[c&15])
		}
	}
	return out.String()
}

// WithSearchParam is `url.searchParams.set(name, value); url.toString()`: the existing pairs are parsed in order, the first pair named name takes the value and later ones are removed (or the pair is appended), and the
// query becomes the form serialization of the list. Setting a pair never leaves an empty query.
func (u URL) WithSearchParam(name, value string) string {
	type pair struct{ name, value string }
	var pairs []pair
	for sequence := range strings.SplitSeq(u.Query, "&") {
		if sequence == "" {
			continue
		}
		key, val, _ := strings.Cut(sequence, "=")
		pairs = append(pairs, pair{formDecode(key), formDecode(val)})
	}
	found := false
	kept := pairs[:0]
	for _, p := range pairs {
		if p.name == name {
			if found {
				continue
			}
			found = true
			p.value = value
		}
		kept = append(kept, p)
	}
	if !found {
		kept = append(kept, pair{name, value})
	}
	parts := make([]string, len(kept))
	for i, p := range kept {
		parts[i] = FormURLEncode(p.name) + "=" + FormURLEncode(p.value)
	}
	return u.href(u.Pathname, "?"+strings.Join(parts, "&"))
}

// ResolveRelativePath is `new URL(ref, base)` for a relative path reference, one that has no scheme and does not start with "/", "?" or "#": the base's path up to and including its last "/" followed by ref, parsed again
// as a URL (dot segments, percent-encoding), with the base's query and fragment dropped. A base whose path is opaque cannot be a base. An empty path counts as "/". A file base whose only path segment is a letter and
// ":" keeps that segment, as the parser's path shortening does for a Windows drive letter (ada 4, Node 24.22.1 and 26.12.1, take exactly two characters; Node 22.19 and 24.19 take any segment that starts with them).
func (u URL) ResolveRelativePath(ref string) (URL, error) {
	if u.authority == "" && !strings.HasPrefix(u.Pathname, "/") {
		return URL{}, errInvalidURL
	}
	directory := "/"
	if last := strings.LastIndexByte(u.Pathname, '/'); last >= 0 {
		directory = u.Pathname[:last+1]
	}
	if u.Protocol == "file:" && strings.Count(u.Pathname, "/") == 1 && isNormalizedWindowsDriveLetter(u.Pathname[1:]) {
		directory = u.Pathname + "/"
	}
	base := u
	base.fragment = ""
	return ParseURL(base.href(directory+ref, ""))
}
