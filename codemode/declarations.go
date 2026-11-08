package codemode

import (
	"bytes"
	"encoding/json"
	"maps"
	"math"
	"net/url"
	"slices"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/internal/jsnumber"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

const (
	indent = "  "
	// maxRefExpansions is the number of local `$ref` expansions per rendered schema, so shared definitions cannot
	// blow up the output.
	maxRefExpansions = 32
	// maxTagChars is the longest property tag; longer ones (large defaults, long patterns) are left out.
	maxTagChars = 80
)

// tagKeywords are the JSON Schema keywords with a number or string value shown as `@keyword value` property tags.
var tagKeywords = []string{
	"minimum", "exclusiveMinimum", "maximum", "exclusiveMaximum", "multipleOf",
	"minLength", "maxLength", "pattern", "format", "minItems", "maxItems",
}

// isLowerBound and isUpperBound report the tag keywords whose values all hold at once, so only the largest (lower) or smallest
// (upper) matters. A switch, unlike a map, costs package initialization nothing.
func isLowerBound(keyword string) bool {
	switch keyword {
	case "minimum", "exclusiveMinimum", "minLength", "minItems":
		return true
	}
	return false
}

func isUpperBound(keyword string) bool {
	switch keyword {
	case "maximum", "exclusiveMaximum", "maxLength", "maxItems":
		return true
	}
	return false
}

// DefaultInputSchemaMaxChars is the largest rendered input type, in characters, before it becomes `unknown`.
//
// Ports packages/codemode/src/declarations.ts (DEFAULT_INPUT_SCHEMA_MAX_CHARS).
const DefaultInputSchemaMaxChars = 16_000

// RenderDeclarationsOptions selects what RenderDeclarations renders.
type RenderDeclarationsOptions struct {
	Tools   []Tool
	Globals []Tool
}

// RenderDeclarations renders TypeScript declarations for the script-visible API: tools become members of
// `declare const tools`, globals `declare function` statements, and `ns.member` globals members of `declare const ns`.
//
// Ports packages/codemode/src/declarations.ts (renderDeclarations).
func RenderDeclarations(options RenderDeclarationsOptions) string {
	var sections []string
	if len(options.Tools) > 0 {
		members := make([]string, len(options.Tools))
		for i, tool := range options.Tools {
			members[i] = docComment(tool.Description, indent) + indent + RenderToolSignature(tool, ToolRenderOptions{})
		}
		sections = append(sections, "declare const tools: {\n"+strings.Join(members, "\n")+"\n};")
	}
	var namespaces []string
	members := map[string][]string{}
	for _, global := range options.Globals {
		namespace, member, found := strings.Cut(global.Name, ".")
		if !found {
			sections = append(sections, renderGlobal("declare function "+global.Name, global, ""))
			continue
		}
		if _, seen := members[namespace]; !seen {
			namespaces = append(namespaces, namespace)
		}
		members[namespace] = append(members[namespace], renderGlobal(member, global, indent))
	}
	for _, namespace := range namespaces {
		sections = append(sections, "declare const "+namespace+": {\n"+strings.Join(members[namespace], "\n")+"\n};")
	}
	return strings.Join(sections, "\n\n")
}

// schemaOf decodes a schema; an absent or undecodable schema is undefined.
func schemaOf(raw json.RawMessage) (any, bool) {
	if len(raw) == 0 {
		return nil, false
	}
	v, err := decodeJSON(raw)
	if err != nil {
		return nil, false
	}
	return v, true
}

// ToolRenderOptions is the options object of renderToolSignature and renderToolSample.
type ToolRenderOptions struct {
	// InputMaxChars is the largest rendered input type, in characters, before it becomes `unknown`. Nil means
	// DefaultInputSchemaMaxChars.
	InputMaxChars *int
}

// SchemaToTypeOptions is the options object of schemaToType.
type SchemaToTypeOptions struct {
	// MaxChars is the largest rendered type, in characters, before it becomes `unknown`. Nil means no limit.
	MaxChars *int
}

// RenderToolSignature renders one tool as a member of the `tools` object: `name(args: T): Promise<R>;` with the
// name as the identifier scripts use. Input types longer than options.InputMaxChars (nil means
// DefaultInputSchemaMaxChars) render as `unknown`.
//
// Ports packages/codemode/src/declarations.ts (renderToolSignature).
func RenderToolSignature(tool Tool, options ToolRenderOptions) string {
	inputMaxChars := DefaultInputSchemaMaxChars
	if options.InputMaxChars != nil {
		inputMaxChars = *options.InputMaxChars
	}
	input := "unknown"
	if schema, ok := schemaOf(tool.InputSchema); ok {
		input = schemaToType(schema, &inputMaxChars)
	}
	return ToCodemodeIdentifier(tool.Name) + "(args: " + input + "): Promise<" + RenderToolOutputType(tool.OutputSchema) + ">;"
}

// RenderToolSample renders the description followed by the tool's declaration, for tool listings and `ALL_TOOLS`
// entries.
//
// Ports packages/codemode/src/declarations.ts (renderToolSample).
func RenderToolSample(tool Tool, options ToolRenderOptions) string {
	declaration := "declare const tools: { " + RenderToolSignature(tool, options) + " };"
	return jsstring.Trim(tool.Description) + "\n\ncodemode tool declaration:\n```ts\n" + declaration + "\n```"
}

// RenderToolOutputType returns the type a tool call resolves to: the output schema's type, and `unknown` without a
// schema.
//
// Ports packages/codemode/src/declarations.ts (renderToolOutputType).
func RenderToolOutputType(raw json.RawMessage) string {
	schema, defined := schemaOf(raw)
	if !defined {
		return "unknown"
	}
	return schemaToType(schema, nil)
}

func renderGlobal(head string, global Tool, indentation string) string {
	if global.Signature != nil {
		return docComment(global.Description, indentation) + indentation + head + *global.Signature + ";"
	}
	input, output := "unknown", "unknown"
	if schema, ok := schemaOf(global.InputSchema); ok {
		input = schemaToType(schema, nil)
	}
	if schema, ok := schemaOf(global.OutputSchema); ok {
		output = schemaToType(schema, nil)
	}
	return docComment(global.Description, indentation) + indentation + head + "(args: " + input + "): Promise<" + output + ">;"
}

// splitLines is text.split(/\r?\n/).
func splitLines(text string) []string {
	return strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
}

func docComment(description, indentation string) string {
	text := jsstring.Trim(description)
	if text == "" {
		return ""
	}
	lines := splitLines(strings.ReplaceAll(text, "*/", "*\\/"))
	if len(lines) == 1 {
		return indentation + "/** " + lines[0] + " */\n"
	}
	rendered := make([]string, len(lines))
	for i, line := range lines {
		if line != "" {
			line = " " + line
		}
		rendered[i] = indentation + " *" + line
	}
	return indentation + "/**\n" + strings.Join(rendered, "\n") + "\n" + indentation + " */\n"
}

func isIdentifier(name string) bool {
	if name == "" {
		return false
	}
	for i, char := range name {
		valid := char == '_' || char == '$' || (char >= 'A' && char <= 'Z') || (char >= 'a' && char <= 'z')
		if i > 0 {
			valid = valid || (char >= '0' && char <= '9')
		}
		if !valid {
			return false
		}
	}
	return true
}

func propertyKey(name string) string {
	if isIdentifier(name) {
		return name
	}
	return jsQuote(name)
}

func union(types []string) string {
	var unique []string
	for _, t := range types {
		if !slices.Contains(unique, t) {
			unique = append(unique, t)
		}
	}
	if slices.Contains(unique, "unknown") {
		return "unknown"
	}
	if len(unique) == 0 {
		return "never"
	}
	return strings.Join(unique, " | ")
}

// SchemaToType converts a JSON Schema to a TypeScript type expression: objects on one line
// (`{ a: string; b?: number; }`) with properties sorted by name, or one property per line with `//` comments when a
// property has a description or constraints the type cannot express (`// @integer @minimum 1 @default 20`); `Array<T>`
// for arrays. Local references (`#/$defs/...`, `#/definitions/...`) resolve against the schema; recursive and remote
// references render as `unknown`. A result longer than maxChars characters (options.MaxChars, nil means no limit) is
// rendered again without the constraint tags, and renders as `unknown` if it is still too long. A schema that is not
// valid JSON renders as `unknown`.
//
// Ports packages/codemode/src/declarations.ts (schemaToType).
func SchemaToType(schema json.RawMessage, options SchemaToTypeOptions) string {
	decoded, ok := schemaOf(schema)
	if !ok {
		return "unknown"
	}
	return schemaToType(decoded, options.MaxChars)
}

type schemaContext struct {
	root any
	// resolving holds the references being expanded on the current path, to stop at recursive types.
	resolving  map[string]bool
	expansions int
	// tags is whether property comments include constraint tags.
	tags bool
}

func schemaToType(schema any, maxChars *int) string {
	render := func(tags bool) string {
		return toType(schema, &schemaContext{root: schema, resolving: map[string]bool{}, tags: tags})
	}
	typ := render(true)
	if maxChars == nil || jsstring.Length(typ) <= *maxChars {
		return typ
	}
	if untagged := render(false); jsstring.Length(untagged) <= *maxChars {
		return untagged
	}
	return "unknown"
}

// ResolveSchemaRef returns the subschema a local `$ref` (`#`, `#/$defs/Item`) names in root, or false for remote references and references that name
// nothing. Pointer segments are URI-decoded and JSON Pointer unescaped (`~1` is `/`, `~0` is `~`). A segment with a malformed percent escape names
// nothing: schemas come from tools and MCP servers, so it must not fail the caller (Pi 6d3e342b2, #11803).
//
// Go mechanic: root and the result are decoded JSON: this package's ordered objects, or the `map[string]any` and `bool` of encoding/json. A
// `$ref` into an array element names nothing here, as `typeof current !== "object" || Array.isArray(current)` stops the walk there.
//
// Ports packages/codemode/src/declarations.ts (resolveSchemaRef).
func ResolveSchemaRef(ref string, root any) (any, bool) {
	segments, ok := refSegments(ref)
	if !ok {
		return nil, false
	}
	current := root
	for _, key := range segments {
		var found bool
		switch parent := current.(type) {
		case *object:
			current, found = parent.get(key)
		case map[string]any:
			current, found = parent[key]
		}
		if !found {
			return nil, false
		}
	}
	switch current.(type) {
	case bool, *object, map[string]any:
		return current, true
	}
	return nil, false
}

// ResolveSchemaRefJSON is [ResolveSchemaRef] on schema text: it returns the text of the subschema a local `$ref` names in root, with its members in the
// order root has them. It reads root once, as bytes, without building a tree or boxing tokens: the cost is the size of root, not its depth times its
// size. Text that is not valid JSON up to the end of the value it names resolves nothing.
func ResolveSchemaRefJSON(ref string, root json.RawMessage) (json.RawMessage, bool) {
	segments, ok := refSegments(ref)
	if !ok {
		return nil, false
	}
	current := root
	if len(segments) > 0 {
		scan := refScanner{text: root}
		start, end, found := scan.walk(segments)
		if scan.bad || !found {
			return nil, false
		}
		// The result does not alias root: callers own it.
		current = bytes.Clone(root[start:end])
	}
	for i, c := range current {
		switch c {
		case ' ', '\t', '\r', '\n':
			continue
		}
		rest := current[i:]
		if c == '{' || bytes.HasPrefix(rest, []byte("true")) || bytes.HasPrefix(rest, []byte("false")) {
			return current, true
		}
		break
	}
	return nil, false
}

// maxRefNesting is encoding/json's nesting limit for a value it decodes; the value a ref names is held to it, the values read past are not.
const maxRefNesting = 10000

// refScanner is a strict RFC 8259 reader over text with the acceptance of encoding/json: it reports malformed text through bad.
type refScanner struct {
	text []byte
	pos  int
	bad  bool
}

func (s *refScanner) space() {
	for s.pos < len(s.text) {
		switch s.text[s.pos] {
		case ' ', '\t', '\r', '\n':
			s.pos++
		default:
			return
		}
	}
}

// next returns the byte at pos after white space, or 0 and bad at the end of text.
func (s *refScanner) next() byte {
	s.space()
	if s.pos >= len(s.text) {
		s.bad = true
		return 0
	}
	return s.text[s.pos]
}

// walk reads one value: when it is an object it follows segments through its members and returns the range of the member they name. Like
// JSON.parse, a repeated name is the last one. A value that is not an object names nothing, and is read to its end.
func (s *refScanner) walk(segments []string) (start, end int, found bool) {
	if s.next() != '{' {
		if !s.bad {
			s.value(math.MaxInt)
		}
		return 0, 0, false
	}
	s.pos++
	if s.next() == '}' {
		s.pos++
		return 0, 0, false
	}
	for !s.bad {
		if s.next() != '"' {
			s.bad = true
			break
		}
		match := s.key(segments[0])
		if s.next() != ':' {
			s.bad = true
			break
		}
		s.pos++
		switch {
		case !match:
			s.value(math.MaxInt)
		case len(segments) > 1:
			start, end, found = s.walk(segments[1:])
		default:
			s.space()
			start = s.pos
			s.value(maxRefNesting)
			end, found = s.pos, true
		}
		if s.bad {
			break
		}
		switch s.next() {
		case ',':
			s.pos++
			continue
		case '}':
			s.pos++
			return start, end, found
		}
		s.bad = true
	}
	return 0, 0, false
}

// key reads the string at pos and reports whether it decodes to name.
func (s *refScanner) key(name string) bool {
	begin := s.pos
	plain := s.str()
	if s.bad {
		return false
	}
	if plain {
		return string(s.text[begin+1:s.pos-1]) == name
	}
	var decoded string
	return json.Unmarshal(s.text[begin:s.pos], &decoded) == nil && decoded == name
}

// str reads the string at pos. It reports whether its text is plain: ASCII without escapes, so its bytes are its value.
func (s *refScanner) str() (plain bool) {
	plain = true
	for s.pos++; s.pos < len(s.text); s.pos++ {
		switch c := s.text[s.pos]; {
		case c == '"':
			s.pos++
			return plain
		case c < 0x20:
			s.bad = true
			return false
		case c == '\\':
			plain = false
			s.pos++
			if s.pos >= len(s.text) {
				s.bad = true
				return false
			}
			switch s.text[s.pos] {
			case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
			case 'u':
				for range 4 {
					s.pos++
					if s.pos >= len(s.text) || !isHex(s.text[s.pos]) {
						s.bad = true
						return false
					}
				}
			default:
				s.bad = true
				return false
			}
		case c >= 0x80:
			plain = false
		}
	}
	s.bad = true
	return false
}

func isHex(c byte) bool {
	return '0' <= c && c <= '9' || 'a' <= c && c <= 'f' || 'A' <= c && c <= 'F'
}

func (s *refScanner) digits() bool {
	begin := s.pos
	for s.pos < len(s.text) && '0' <= s.text[s.pos] && s.text[s.pos] <= '9' {
		s.pos++
	}
	return s.pos > begin
}

// number reads a JSON number at pos.
func (s *refScanner) number() {
	if s.pos < len(s.text) && s.text[s.pos] == '-' {
		s.pos++
	}
	switch {
	case s.pos < len(s.text) && s.text[s.pos] == '0':
		s.pos++
	case s.pos < len(s.text) && '1' <= s.text[s.pos] && s.text[s.pos] <= '9':
		s.digits()
	default:
		s.bad = true
		return
	}
	if s.pos < len(s.text) && s.text[s.pos] == '.' {
		s.pos++
		if !s.digits() {
			s.bad = true
			return
		}
	}
	if s.pos < len(s.text) && (s.text[s.pos] == 'e' || s.text[s.pos] == 'E') {
		s.pos++
		if s.pos < len(s.text) && (s.text[s.pos] == '+' || s.text[s.pos] == '-') {
			s.pos++
		}
		if !s.digits() {
			s.bad = true
		}
	}
}

func (s *refScanner) literal(word string) {
	if len(s.text)-s.pos < len(word) || string(s.text[s.pos:s.pos+len(word)]) != word {
		s.bad = true
		return
	}
	s.pos += len(word)
}

// value reads one value at pos, up to limit levels of nesting. Whether each open level is an object is a bit of a stack, which stays on the
// goroutine stack until the nesting passes 256.
func (s *refScanner) value(limit int) {
	var inline [4]uint64
	objects := inline[:]
	depth := 0
	const (
		wantValue = iota
		wantKey
		afterValue
	)
	state := wantValue
	for !s.bad {
		switch state {
		case wantValue:
			switch c := s.next(); {
			case s.bad:
			case c == '{' || c == '[':
				s.pos++
				if depth++; depth > limit {
					s.bad = true
					return
				}
				if depth > len(objects)*64 {
					objects = append(objects, 0)
				}
				word, bit := (depth-1)/64, uint64(1)<<((depth-1)%64)
				if c == '{' {
					objects[word] |= bit
				} else {
					objects[word] &^= bit
				}
				closer := byte(']')
				state = wantValue
				if c == '{' {
					closer, state = '}', wantKey
				}
				if s.next() == closer {
					s.pos++
					depth--
					state = afterValue
				}
			case c == '"':
				s.str()
				state = afterValue
			case c == 't':
				s.literal("true")
				state = afterValue
			case c == 'f':
				s.literal("false")
				state = afterValue
			case c == 'n':
				s.literal("null")
				state = afterValue
			default:
				s.number()
				state = afterValue
			}
		case wantKey:
			if s.next() != '"' {
				s.bad = true
				return
			}
			s.str()
			if s.next() != ':' {
				s.bad = true
				return
			}
			s.pos++
			state = wantValue
		case afterValue:
			if depth == 0 {
				return
			}
			c := s.next()
			if s.bad {
				return
			}
			object := objects[(depth-1)/64]&(1<<((depth-1)%64)) != 0
			switch {
			case c == ',':
				s.pos++
				state = wantValue
				if object {
					state = wantKey
				}
			case object && c == '}', !object && c == ']':
				s.pos++
				depth--
			default:
				s.bad = true
			}
		}
	}
}

// refSegments are the members a local `$ref` (`#`, `#/$defs/Item`) walks through, URI-decoded and JSON Pointer unescaped. It reports false for a
// remote reference and for a segment with a malformed percent escape.
// pig divergence (D371): Pi main 6d3e342b2 follows local schema refs in tool search and codemode summaries; Pi 1.5.0 throws on a malformed ref.
func refSegments(ref string) ([]string, bool) {
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

func toType(schema any, ctx *schemaContext) string {
	switch s := schema.(type) {
	case bool:
		if s {
			return "unknown"
		}
		return "never"
	case *object:
		return objectSchemaType(s, ctx)
	}
	return "unknown"
}

func objectSchemaType(schema *object, ctx *schemaContext) string {
	if ref, ok := schema.vals["$ref"].(string); ok {
		if ctx.resolving[ref] || ctx.expansions >= maxRefExpansions {
			return "unknown"
		}
		target, ok := ResolveSchemaRef(ref, ctx.root)
		if !ok {
			return "unknown"
		}
		ctx.expansions++
		ctx.resolving[ref] = true
		defer delete(ctx.resolving, ref)
		return toType(target, ctx)
	}
	if c, ok := schema.get("const"); ok {
		return jsStringify(c)
	}
	if values, ok := schema.vals["enum"].([]any); ok {
		types := make([]string, len(values))
		for i, v := range values {
			types[i] = jsStringify(v)
		}
		return union(types)
	}
	variants, isVariants := schema.vals["anyOf"].([]any)
	if !isVariants {
		variants, isVariants = schema.vals["oneOf"].([]any)
	}
	if isVariants {
		types := make([]string, len(variants))
		for i, v := range variants {
			types[i] = toType(v, ctx)
		}
		return union(types)
	}
	if parts, ok := schema.vals["allOf"].([]any); ok {
		var rendered []string
		for _, part := range parts {
			if typ := toType(part, ctx); typ != "unknown" {
				if strings.Contains(typ, " | ") {
					typ = "(" + typ + ")"
				}
				rendered = append(rendered, typ)
			}
		}
		if len(rendered) == 0 {
			return "unknown"
		}
		return strings.Join(rendered, " & ")
	}

	typ, hasType := schema.get("type")
	if entries, ok := typ.([]any); ok {
		types := make([]string, len(entries))
		for i, entry := range entries {
			clone := &object{keys: schema.keys, vals: map[string]any{}}
			maps.Copy(clone.vals, schema.vals)
			clone.vals["type"] = entry
			types[i] = toType(clone, ctx)
		}
		return union(types)
	}
	if !hasType {
		_, hasProperties := schema.get("properties")
		_, hasAdditional := schema.get("additionalProperties")
		_, hasRequired := schema.get("required")
		if hasProperties || hasAdditional || hasRequired {
			return objectType(schema, ctx)
		}
		_, hasItems := schema.get("items")
		_, hasPrefix := schema.get("prefixItems")
		if hasItems || hasPrefix {
			return arrayType(schema, ctx)
		}
		return "unknown"
	}
	switch typ {
	case "string":
		return "string"
	case "number", "integer":
		return "number"
	case "boolean":
		return "boolean"
	case "null":
		return "null"
	case "array":
		return arrayType(schema, ctx)
	case "object":
		return objectType(schema, ctx)
	}
	return "unknown"
}

func arrayType(schema *object, ctx *schemaContext) string {
	items, hasItems := schema.get("items")
	tuple, itemsIsArray := items.([]any)
	if hasItems && !itemsIsArray {
		return "Array<" + toType(items, ctx) + ">"
	}
	if prefix, ok := schema.vals["prefixItems"].([]any); ok {
		tuple = prefix
	} else if !itemsIsArray {
		tuple = nil
	}
	if len(tuple) > 0 {
		types := make([]string, len(tuple))
		for i, item := range tuple {
			types[i] = toType(item, ctx)
		}
		return "[" + strings.Join(types, ", ") + "]"
	}
	return "unknown[]"
}

func descriptionOf(property any) string {
	if obj, ok := property.(*object); ok {
		if description, ok := obj.vals["description"].(string); ok {
			return jsstring.Trim(description)
		}
	}
	return ""
}

// splitLineTerminators is text.split(/[\r\n\u2028\u2029]/g): each terminator, \r\n as two, ends a line.
func splitLineTerminators(text string) []string {
	return strings.FieldsFunc(text, func(r rune) bool { return r == '\r' || r == '\n' || r == '\u2028' || r == '\u2029' })
}

// numberOf is a decoded JSON number's value, when v is one (`typeof value === "number"`).
func numberOf(v any) (float64, bool) {
	n, ok := v.(json.Number)
	if !ok {
		return 0, false
	}
	return jsnumber.FromJSON([]byte(n)), true
}

// constraintTags are the constraints of a property that its TypeScript type cannot express, as JSDoc-style tags:
// `@integer @minimum 1 @maximum 100 @default 20`. Empty when there are none.
//
// Ports packages/codemode/src/declarations.ts (constraintTags).
func constraintTags(property any) string {
	prop, ok := property.(*object)
	if !ok {
		return ""
	}
	// Optional fields such as Pydantic's `int | None` keep their constraints in the non-null branch. The branch's
	// constraints apply together with the property's, so both are read.
	variants, isArray := prop.vals["anyOf"].([]any)
	if !isArray {
		variants, _ = prop.vals["oneOf"].([]any)
	}
	var nonNull []any
	for _, variant := range variants {
		if obj, isObj := variant.(*object); !isObj || obj.vals["type"] != "null" {
			nonNull = append(nonNull, variant)
		}
	}
	sources := []*object{prop}
	if len(nonNull) == 1 {
		if branch, isObj := nonNull[0].(*object); isObj {
			sources = append(sources, branch)
		}
	}
	// Draft-04 and OpenAPI 3.0 make `minimum`/`maximum` exclusive with a boolean `exclusiveMinimum`/`exclusiveMaximum`.
	schemas := make([]map[string]any, len(sources))
	for i, source := range sources {
		schema := maps.Clone(source.vals)
		if source.vals["exclusiveMinimum"] == true {
			delete(schema, "minimum")
			setDefined(schema, "exclusiveMinimum", source.vals["minimum"])
		}
		if source.vals["exclusiveMaximum"] == true {
			delete(schema, "maximum")
			setDefined(schema, "exclusiveMaximum", source.vals["maximum"])
		}
		schemas[i] = schema
	}

	var tags []string
	for _, schema := range schemas {
		if isIntegerType(schema["type"]) {
			tags = append(tags, "@integer")
			break
		}
	}
	for _, keyword := range tagKeywords {
		if isLowerBound(keyword) || isUpperBound(keyword) {
			var best float64
			found := false
			for _, schema := range schemas {
				n, isNumber := numberOf(schema[keyword])
				switch {
				case !isNumber:
				case !found:
					best, found = n, true
				case isLowerBound(keyword):
					best = math.Max(best, n)
				default:
					best = math.Min(best, n)
				}
			}
			if found {
				tags = append(tags, "@"+keyword+" "+jsnumber.String(best))
			}
			continue
		}
		// `new Set(values)`: each distinct value once, in the order first seen, numbers by value.
		var seenNumbers []float64
		var seenStrings []string
		for _, schema := range schemas {
			value := schema[keyword]
			if n, isNumber := numberOf(value); isNumber {
				if slices.Contains(seenNumbers, n) {
					continue
				}
				seenNumbers = append(seenNumbers, n)
				tags = append(tags, "@"+keyword+" "+jsnumber.String(n))
			} else if s, isString := value.(string); isString {
				if slices.Contains(seenStrings, s) {
					continue
				}
				seenStrings = append(seenStrings, s)
				tags = append(tags, "@"+keyword+" "+s)
			}
		}
	}
	for _, schema := range schemas {
		if schema["uniqueItems"] == true {
			tags = append(tags, "@uniqueItems")
			break
		}
	}
	// Annotations, unlike constraints, come from the property itself.
	if value, defined := prop.vals["default"]; defined && value != nil {
		tags = append(tags, "@default "+jsStringify(value))
	}
	if prop.vals["deprecated"] == true {
		tags = append(tags, "@deprecated")
	}
	var shown []string
	for _, tag := range tags {
		if jsstring.Length(tag) <= maxTagChars {
			shown = append(shown, escapeLineTerminators(tag))
		}
	}
	return strings.Join(shown, " ")
}

// setDefined is `{ ...schema, key: value }` for a value that may be undefined: an undefined value reads as absent.
func setDefined(schema map[string]any, key string, value any) {
	if value == nil {
		if _, present := schema[key]; !present {
			return
		}
	}
	schema[key] = value
}

func isIntegerType(typ any) bool {
	if typ == "integer" {
		return true
	}
	list, _ := typ.([]any)
	return slices.Contains(list, any("integer"))
}

// lineTerminatorEscapes writes each line terminator as `\uXXXX`. A replacer works on bytes, so a lone surrogate (WTF-8) passes
// through unchanged. escapeLineTerminators makes it on first use, so package initialization allocates nothing for it.
var lineTerminatorEscapes struct {
	once     sync.Once
	replacer *strings.Replacer
}

func escapeLineTerminators(tag string) string {
	lineTerminatorEscapes.once.Do(func() {
		lineTerminatorEscapes.replacer = strings.NewReplacer("\r", `\u000d`, "\n", `\u000a`, "\u2028", `\u2028`, "\u2029", `\u2029`)
	})
	return lineTerminatorEscapes.replacer.Replace(tag)
}

func objectType(schema *object, ctx *schemaContext) string {
	properties, _ := schema.vals["properties"].(*object)
	if properties == nil {
		properties = &object{vals: map[string]any{}}
	}
	required := map[string]bool{}
	if list, ok := schema.vals["required"].([]any); ok {
		for _, r := range list {
			if name, ok := r.(string); ok {
				required[name] = true
			}
		}
	}
	names := slices.Clone(properties.keys)
	slices.SortFunc(names, compareUTF16)
	members := make([]string, len(names))
	for i, name := range names {
		optional := "?"
		if required[name] {
			optional = ""
		}
		members[i] = propertyKey(name) + optional + ": " + toType(properties.vals[name], ctx) + ";"
	}
	additional, hasAdditional := schema.get("additionalProperties")
	if hasAdditional && additional != false {
		typ := "unknown"
		if additional != true {
			typ = toType(additional, ctx)
		}
		members = append(members, "[key: string]: "+typ+";")
	} else if !hasAdditional && len(names) == 0 {
		members = append(members, "[key: string]: unknown;")
	}
	if len(members) == 0 {
		return "{}"
	}
	comments := make([][]string, len(names))
	commented := false
	for i, name := range names {
		for _, line := range splitLineTerminators(descriptionOf(properties.vals[name])) {
			if line = jsstring.Trim(line); line != "" {
				comments[i] = append(comments[i], line)
			}
		}
		if ctx.tags {
			if tags := constraintTags(properties.vals[name]); tags != "" {
				comments[i] = append(comments[i], tags)
			}
		}
		commented = commented || len(comments[i]) > 0
	}
	if !commented {
		return "{ " + strings.Join(members, " ") + " }"
	}
	lines := []string{"{"}
	for i := range names {
		for _, line := range comments[i] {
			lines = append(lines, indent+"// "+line)
		}
		lines = append(lines, indent+strings.ReplaceAll(members[i], "\n", "\n"+indent))
	}
	for _, member := range members[len(names):] {
		lines = append(lines, indent+member)
	}
	lines = append(lines, "}")
	return strings.Join(lines, "\n")
}
