package nodeurl

import "strings"

// ResolveReference is `new URL(input, base)` for every base the parser reads: a special base (http, https, ws, wss, ftp, file), a non-special base with a path (an authority or a leading slash), and a base whose path is opaque (a URN), which only a fragment-only input can resolve against. An input with a scheme other than the base's, or any scheme against a non-special base, is parsed on its own with ParseURL. It fails where Node throws: a base the parser rejects, a relative input against an opaque base, or an input the parser rejects.
func ResolveReference(input, base string) (URL, error) {
	parsedBase, err := ParseURL(base)
	if err != nil {
		return URL{}, errInvalidURL
	}
	input = PrepareInput(input)
	special := isSpecialScheme(parsedBase.Protocol)
	if scheme, rest, found := strings.Cut(input, ":"); found && isScheme(scheme) {
		if !special || strings.ToLower(scheme)+":" != parsedBase.Protocol {
			return ParseURL(input)
		}
		// The special relative or authority state: an input with the base's scheme and no authority is relative.
		if len(rest) < 2 || !isSlash(rest[0]) || !isSlash(rest[1]) {
			input = rest
		} else {
			return ParseURL(input)
		}
	}
	if parsedBase.authority == "" && !strings.HasPrefix(parsedBase.Pathname, "/") {
		if strings.HasPrefix(input, "#") {
			return ParseURL(parsedBase.WithoutFragment().Href() + input)
		}
		return URL{}, errInvalidURL
	}
	slash := func(c byte) bool { return c == '/' || special && c == '\\' }
	prefix := parsedBase.Protocol + parsedBase.authority
	switch {
	case len(input) >= 2 && slash(input[0]) && slash(input[1]):
		return ParseURL(parsedBase.Protocol + input)
	case input != "" && slash(input[0]):
		// A file base whose first path segment is a Windows drive letter keeps it for an absolute path that does not name its own.
		if drive, _, _ := strings.Cut(strings.TrimPrefix(parsedBase.Pathname, "/"), "/"); parsedBase.Protocol == "file:" && isNormalizedWindowsDriveLetter(drive) && !startsWithWindowsDriveLetter(input[1:]) {
			return ParseURL(prefix + "/" + drive + input)
		}
		return ParseURL(prefix + input)
	case input == "":
		return ParseURL(prefix + parsedBase.Pathname + parsedBase.query)
	case input[0] == '?':
		return ParseURL(prefix + parsedBase.Pathname + input)
	case input[0] == '#':
		return ParseURL(prefix + parsedBase.Pathname + parsedBase.query + input)
	}
	return parsedBase.ResolveRelativePath(input)
}
