package codemode

import (
	"cmp"
	"encoding/json"
	"maps"
	"net/url"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

const (
	indent = "  "
	// maxRefExpansions is the number of local `$ref` expansions per rendered schema, so shared definitions cannot
	// blow up the output.
	maxRefExpansions = 32
)

// DefaultInputSchemaMaxChars is the largest rendered input type, in characters, before it becomes `unknown`.
//
// Ports packages/codemode/src/declarations.ts (DEFAULT_INPUT_SCHEMA_MAX_CHARS).
const DefaultInputSchemaMaxChars = 16_000

// McpTypescriptPreamble holds TypeScript types for MCP results, from the MCP `CallToolResult` schema, so
// `CallToolResult<T>` declarations can refer to them.
//
// Ports packages/codemode/src/declarations.ts (MCP_TYPESCRIPT_PREAMBLE).
const McpTypescriptPreamble = `type Role = "user" | "assistant";
type MetaObject = Record<string, unknown>;
type Annotations = {
  audience?: Role[];
  priority?: number;
  lastModified?: string;
};
type Icon = {
  src: string;
  mimeType?: string;
  sizes?: string[];
  theme?: "light" | "dark";
};
type TextResourceContents = {
  uri: string;
  mimeType?: string;
  _meta?: MetaObject;
  text: string;
};
type BlobResourceContents = {
  uri: string;
  mimeType?: string;
  _meta?: MetaObject;
  blob: string;
};
type TextContent = {
  type: "text";
  text: string;
  annotations?: Annotations;
  _meta?: MetaObject;
};
type ImageContent = {
  type: "image";
  data: string;
  mimeType: string;
  annotations?: Annotations;
  _meta?: MetaObject;
};
type AudioContent = {
  type: "audio";
  data: string;
  mimeType: string;
  annotations?: Annotations;
  _meta?: MetaObject;
};
type ResourceLink = {
  icons?: Icon[];
  name: string;
  title?: string;
  uri: string;
  description?: string;
  mimeType?: string;
  annotations?: Annotations;
  size?: number;
  _meta?: MetaObject;
  type: "resource_link";
};
type EmbeddedResource = {
  type: "resource";
  resource: TextResourceContents | BlobResourceContents;
  annotations?: Annotations;
  _meta?: MetaObject;
};
type ContentBlock =
  | TextContent
  | ImageContent
  | AudioContent
  | ResourceLink
  | EmbeddedResource;
type CallToolResult<TStructured = { [key: string]: unknown }> = {
  _meta?: MetaObject;
  content: ContentBlock[];
  isError?: boolean;
  structuredContent?: TStructured;
  [key: string]: unknown;
};`

// RenderDeclarationsOptions selects what RenderDeclarations renders.
type RenderDeclarationsOptions struct {
	Tools   []Tool
	Globals []Tool
}

// RenderDeclarations renders TypeScript declarations for the script-visible API: tools become members of
// `declare const tools`, globals `declare function` statements, and `ns.member` globals members of `declare const ns`.
//
// Ports packages/codemode/src/declarations.ts (renderDeclarations).
func RenderDeclarations(options RenderDeclarationsOptions) (string, error) {
	var sections []string
	if len(options.Tools) > 0 {
		members := make([]string, len(options.Tools))
		for i, tool := range options.Tools {
			signature, err := RenderToolSignature(tool, ToolRenderOptions{})
			if err != nil {
				return "", err
			}
			members[i] = docComment(tool.Description, indent) + indent + signature
		}
		sections = append(sections, "declare const tools: {\n"+strings.Join(members, "\n")+"\n};")
	}
	var namespaces []string
	members := map[string][]string{}
	for _, global := range options.Globals {
		namespace, member, found := strings.Cut(global.Name, ".")
		if !found {
			rendered, err := renderGlobal("declare function "+global.Name, global, "")
			if err != nil {
				return "", err
			}
			sections = append(sections, rendered)
			continue
		}
		if _, seen := members[namespace]; !seen {
			namespaces = append(namespaces, namespace)
		}
		rendered, err := renderGlobal(member, global, indent)
		if err != nil {
			return "", err
		}
		members[namespace] = append(members[namespace], rendered)
	}
	for _, namespace := range namespaces {
		sections = append(sections, "declare const "+namespace+": {\n"+strings.Join(members[namespace], "\n")+"\n};")
	}
	return strings.Join(sections, "\n\n"), nil
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
// DefaultInputSchemaMaxChars) render as `unknown`. Tools whose output schema is an MCP `CallToolResult` render as `Promise<CallToolResult<T>>`,
// which needs McpTypescriptPreamble.
//
// Ports packages/codemode/src/declarations.ts (renderToolSignature).
func RenderToolSignature(tool Tool, options ToolRenderOptions) (string, error) {
	inputMaxChars := DefaultInputSchemaMaxChars
	if options.InputMaxChars != nil {
		inputMaxChars = *options.InputMaxChars
	}
	input := "unknown"
	if schema, ok := schemaOf(tool.InputSchema); ok {
		var err error
		if input, err = schemaToType(schema, &inputMaxChars); err != nil {
			return "", err
		}
	}
	output, err := RenderToolOutputType(tool.OutputSchema)
	if err != nil {
		return "", err
	}
	return ToCodemodeIdentifier(tool.Name) + "(args: " + input + "): Promise<" + output + ">;", nil
}

// RenderToolSample renders the description followed by the tool's declaration, for tool listings and `ALL_TOOLS`
// entries.
//
// Ports packages/codemode/src/declarations.ts (renderToolSample).
func RenderToolSample(tool Tool, options ToolRenderOptions) (string, error) {
	signature, err := RenderToolSignature(tool, options)
	if err != nil {
		return "", err
	}
	declaration := "declare const tools: { " + signature + " };"
	return jsstring.Trim(tool.Description) + "\n\ncodemode tool declaration:\n```ts\n" + declaration + "\n```", nil
}

// mcpStructured is McpStructuredContentSchema over decoded values: the structuredContent schema, `true` when it
// declares none, and whether the schema is a CallToolResult at all.
func mcpStructured(schema any, defined bool) (any, bool) {
	obj, ok := schema.(*object)
	if !defined || !ok {
		return nil, false
	}
	props, ok := obj.vals["properties"].(*object)
	if !ok {
		return nil, false
	}
	content, ok := props.vals["content"].(*object)
	if !ok || content.vals["type"] != "array" {
		return nil, false
	}
	if items, ok := content.vals["items"].(*object); !ok || items.vals["type"] != "object" {
		return nil, false
	}
	isError, ok := props.vals["isError"].(*object)
	if !ok || isError.vals["type"] != "boolean" {
		return nil, false
	}
	if meta, ok := props.vals["_meta"].(*object); !ok || meta.vals["type"] != "object" {
		return nil, false
	}
	switch structured := props.vals["structuredContent"].(type) {
	case *object, bool:
		return structured, true
	}
	return true, true
}

// McpStructuredContentSchema returns the `structuredContent` schema of an MCP `CallToolResult` output schema
// (detected by a `content` array of objects, boolean `isError`, and object `_meta`), the JSON text `true` when it
// declares none, or nil when the schema is not a `CallToolResult`. The result is compact JSON.
//
// Ports packages/codemode/src/declarations.ts (mcpStructuredContentSchema).
func McpStructuredContentSchema(schema json.RawMessage) json.RawMessage {
	decoded, defined := schemaOf(schema)
	structured, ok := mcpStructured(decoded, defined)
	if !ok {
		return nil
	}
	return json.RawMessage(jsStringify(structured))
}

// RenderToolOutputType returns the type a tool call resolves to: `CallToolResult<T>` for MCP output schemas (needs
// McpTypescriptPreamble), the schema's type otherwise, and `unknown` without a schema.
//
// Ports packages/codemode/src/declarations.ts (renderToolOutputType).
func RenderToolOutputType(raw json.RawMessage) (string, error) {
	schema, defined := schemaOf(raw)
	if structured, ok := mcpStructured(schema, defined); ok {
		typ, err := SchemaToType(json.RawMessage(jsStringify(structured)), SchemaToTypeOptions{})
		if err != nil {
			return "", err
		}
		if typ != "unknown" {
			return "CallToolResult<" + typ + ">", nil
		}
		return "CallToolResult", nil
	}
	if !defined {
		return "unknown", nil
	}
	return schemaToType(schema, nil)
}

func renderGlobal(head string, global Tool, indentation string) (string, error) {
	if global.Signature != nil {
		return docComment(global.Description, indentation) + indentation + head + *global.Signature + ";", nil
	}
	input, output := "unknown", "unknown"
	var err error
	if schema, ok := schemaOf(global.InputSchema); ok {
		if input, err = schemaToType(schema, nil); err != nil {
			return "", err
		}
	}
	if schema, ok := schemaOf(global.OutputSchema); ok {
		if output, err = schemaToType(schema, nil); err != nil {
			return "", err
		}
	}
	return docComment(global.Description, indentation) + indentation + head + "(args: " + input + "): Promise<" + output + ">;", nil
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
// property has a description; `Array<T>` for arrays. Local references (`#/$defs/...`, `#/definitions/...`) resolve
// against the schema; recursive and remote references render as `unknown`. A result longer than maxChars
// characters (options.MaxChars, nil means no limit) renders as `unknown`. A schema that is not valid JSON renders as `unknown`.
//
// Ports packages/codemode/src/declarations.ts (schemaToType).
func SchemaToType(schema json.RawMessage, options SchemaToTypeOptions) (string, error) {
	decoded, ok := schemaOf(schema)
	if !ok {
		return "unknown", nil
	}
	return schemaToType(decoded, options.MaxChars)
}

type schemaContext struct {
	root any
	// resolving holds the references being expanded on the current path, to stop at recursive types.
	resolving  map[string]bool
	expansions int
	// err is the first failure to decode a reference; the rendering carries on with `unknown` and schemaToType returns it.
	err error
}

func schemaToType(schema any, maxChars *int) (string, error) {
	ctx := &schemaContext{root: schema, resolving: map[string]bool{}}
	typ := toType(schema, ctx)
	if ctx.err != nil {
		return "", ctx.err
	}
	if maxChars != nil && jsstring.Length(typ) > *maxChars {
		return "unknown", nil
	}
	return typ, nil
}

// errURIMalformed is the failure to decode a percent-encoded $ref segment. Its message is that of the URIError
// upstream's decodeURIComponent throws, which callers surface as the error text.
var errURIMalformed = &URIError{Message: "URI malformed"}

// URIError is the error JavaScript's decodeURIComponent throws for a malformed percent-encoding (a `$ref` segment with one).
type URIError struct{ Message string }

func (e *URIError) Error() string { return e.Message }

// Name is the JavaScript error's `name`.
func (*URIError) Name() string { return "URIError" }

// resolveRef follows a local JSON pointer. A pointer segment that is not valid percent-encoding is an error, as
// upstream's decodeURIComponent throws a URIError.
func resolveRef(ref string, root any) (any, bool, error) {
	if ref != "#" && !strings.HasPrefix(ref, "#/") {
		return nil, false, nil
	}
	current := root
	if ref != "#" {
		for segment := range strings.SplitSeq(ref[2:], "/") {
			if segment == "" {
				continue
			}
			decoded, err := url.PathUnescape(segment)
			if err != nil || !utf8.ValidString(decoded) {
				return nil, false, errURIMalformed
			}
			key := strings.ReplaceAll(strings.ReplaceAll(decoded, "~1", "/"), "~0", "~")
			obj, ok := current.(*object)
			if !ok {
				return nil, false, nil
			}
			if current, ok = obj.get(key); !ok {
				return nil, false, nil
			}
		}
	}
	switch current.(type) {
	case bool, *object:
		return current, true, nil
	}
	return nil, false, nil
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
		target, ok, err := resolveRef(ref, ctx.root)
		if err != nil {
			ctx.err = cmp.Or(ctx.err, err)
			return "unknown"
		}
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
	described := false
	for _, name := range names {
		if descriptionOf(properties.vals[name]) != "" {
			described = true
			break
		}
	}
	if !described {
		return "{ " + strings.Join(members, " ") + " }"
	}
	lines := []string{"{"}
	for i, name := range names {
		for _, line := range splitLines(descriptionOf(properties.vals[name])) {
			if jsstring.Trim(line) != "" {
				lines = append(lines, indent+"// "+jsstring.Trim(line))
			}
		}
		lines = append(lines, indent+strings.ReplaceAll(members[i], "\n", "\n"+indent))
	}
	for _, member := range members[len(names):] {
		lines = append(lines, indent+member)
	}
	lines = append(lines, "}")
	return strings.Join(lines, "\n")
}
