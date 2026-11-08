// Package schemaref resolves the local `$ref`s of a JSON schema. It imports nothing of the codemode engine, so tool search can follow a reference in a build
// that strips codemode.
package schemaref

import (
	"net/url"
	"strings"
	"unicode/utf8"
)

// Segments are the members a local `$ref` (`#`, `#/$defs/Item`) walks through, URI-decoded and JSON Pointer unescaped. It reports false for a
// remote reference and for a segment with a malformed percent escape.
func Segments(ref string) ([]string, bool) {
	if ref != "#" && !strings.HasPrefix(ref, "#/") {
		return nil, false
	}
	if ref == "#" {
		return nil, true
	}
	var segments []string
	//portlint:allow utf16units ref[2:] drops the two ASCII characters `#/` (declarations.ts resolveSchemaRef)
	for segment := range strings.SplitSeq(ref[2:], "/") {
		if segment == "" {
			continue
		}
		decoded, err := url.PathUnescape(segment)
		if err != nil || !utf8.ValidString(decoded) {
			return nil, false
		}
		segments = append(segments, strings.ReplaceAll(strings.ReplaceAll(decoded, "~1", "/"), "~0", "~"))
	}
	return segments, true
}

// Resolve is the subschema a local `$ref` names in a schema decoded to maps, as packages/codemode/src/declarations.ts resolveSchemaRef finds it: a
// boolean or an object, and nothing for a remote reference, a missing member or a step into an array.
func Resolve(ref string, root any) (any, bool) {
	segments, ok := Segments(ref)
	if !ok {
		return nil, false
	}
	current := root
	for _, key := range segments {
		parent, isObject := current.(map[string]any)
		if !isObject {
			return nil, false
		}
		var found bool
		if current, found = parent[key]; !found {
			return nil, false
		}
	}
	switch current.(type) {
	case bool, map[string]any:
		return current, true
	}
	return nil, false
}
