// Package jsonopts is the one encoding/json/v2 option set that reads and writes JSON the way Pi's JSON.parse and JSON.stringify do.
// A call site joins them with its own options (jsonv2.JoinOptions) and never changes them for everyone: a site that must emit
// null for a nil slice, say, adds jsonv2.FormatNilSliceAsNull(true) itself.
package jsonopts

import (
	"encoding/json"
	"encoding/json/jsontext"
	jsonv2 "encoding/json/v2"
)

var (
	// Parse reads what JSON.parse accepts and gives the value JSON.parse gives:
	//   - a repeated member name is allowed and the last value wins (a typed decode sets the field again, a decode into any or a map replaces
	//     the entry); a decoder that keeps member order keeps the first position;
	//   - invalid UTF-8 in a string is replaced by U+FFFD, as Node does when it decodes bytes to a string before JSON.parse sees them;
	//   - member names match case-sensitively, as a JavaScript property read does, where encoding/json v1 folded case;
	//   - a repeated member replaces what a decode into any or a map read before it instead of merging into it (the v2 default merges a
	//     second object into the first and fails on an array after an object). This is the merge rule encoding/json v1 has, so a site that
	//     moves from json.Unmarshal keeps its result.
	//
	// A decode into a struct still merges a repeated object-valued member into the field and, as v1 did, keeps a field's value when a
	// later duplicate is null; JSON.parse replaces in both cases. A site that needs exact JSON.parse values for repeated members reads the
	// object member by member (see internal/orderedjson) and keeps the last value itself.
	//
	// Everything else is the v2 default, which already agrees with JSON.parse: a malformed document, trailing data and a control
	// character in a string are errors, a number is read as a float64 and one out of range is an error where JSON.parse gives
	// Infinity, and the nesting limit is 10000 levels.
	Parse = jsonv2.JoinOptions(
		jsontext.AllowDuplicateNames(true),
		jsontext.AllowInvalidUTF8(true),
		jsonv2.MatchCaseInsensitiveNames(false),
		json.MergeWithLegacySemantics(true),
	)

	// Stringify writes what JSON.stringify writes: Parse, and no escaping of <, > and & or of U+2028 and U+2029, which JSON.stringify
	// leaves as they are and encoding/json v1 escaped. Numbers already use the JavaScript Number-to-string form (1e21 and 1e-7 switch to
	// exponents, 1e20 and 1e-6 do not).
	//
	// Not covered, because encoding/json cannot express it: -0 is written "-0" where JSON.stringify writes 0; NaN and the infinities are
	// errors where JSON.stringify writes null; a lone UTF-16 surrogate cannot be held by a Go string, so a decoded "\ud800" is U+FFFD.
	Stringify = jsonv2.JoinOptions(
		Parse,
		jsontext.EscapeForHTML(false),
		jsontext.EscapeForJS(false),
	)
)
