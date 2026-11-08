// Package schemaref resolves local JSON Schema `$ref`s (`#`, `#/$defs/Item`). Tool search ranks tools without the code mode sandbox, so the resolver lives
// here, not in package codemode, which a pig_strip_codemode build leaves out; codemode re-exports it for decoded schemas of its own object type.
//
// Ports packages/codemode/src/declarations.ts (resolveSchemaRef), Pi 6d3e342b2 (#11803).
package schemaref

import (
	"net/url"
	"strings"
	"unicode/utf8"
)

// Segments are the members a local `$ref` (`#`, `#/$defs/Item`) walks through, URI-decoded and JSON Pointer unescaped (`~1` is `/`, `~0` is `~`). It
// reports false for a remote reference and for a segment with a malformed percent escape: schemas come from tools and MCP servers, so a malformed
// reference names nothing and must not fail the caller.
func Segments(ref string) ([]string, bool) {
	if ref != "#" && !strings.HasPrefix(ref, "#/") {
		return nil, false
	}
	if ref == "#" {
		return nil, true
	}
	var segments []string
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

// Resolve returns the subschema a local `$ref` names in root, a schema decoded by encoding/json (`map[string]any` and `bool`), or false for remote
// references and references that name nothing. A `$ref` into an array element names nothing, as `typeof current !== "object" || Array.isArray(current)`
// stops Pi's walk there.
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
		if current, ok = parent[key]; !ok {
			return nil, false
		}
	}
	switch current.(type) {
	case bool, map[string]any:
		return current, true
	}
	return nil, false
}
