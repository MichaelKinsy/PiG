package nodeurl

// Ports packages/ai/src/api/mistral-conversations.ts (WHATWG URL serialization at the fetch boundary).

import "strings"

// ResolveDirectoryPath is `const base = new URL(raw); base.pathname = base.pathname.replace(/\/+$/u, "") + "/"; new URL(relativePath, base).toString()` for a path-relative reference: the base path becomes a
// directory (the pathname setter's encoding and dot-segment rules apply), the base query and fragment are dropped, and a base that is not a valid URL, or whose path is opaque, fails as the constructors do.
func ResolveDirectoryPath(raw, relativePath string) (string, error) {
	base, err := ParseURL(raw)
	if err != nil {
		return "", err
	}
	if base.authority == "" && !strings.HasPrefix(base.Pathname, "/") {
		return "", errInvalidURL
	}
	directory, err := ParseURL(base.WithPathname(trimTrailingSlashes(base.Pathname) + "/"))
	if err != nil {
		return "", err
	}
	resolved, err := directory.ResolveRelativePath(relativePath)
	if err != nil {
		return "", err
	}
	return resolved.Href(), nil
}

func trimTrailingSlashes(path string) string {
	for len(path) > 0 && path[len(path)-1] == '/' {
		path = path[:len(path)-1]
	}
	return path
}
