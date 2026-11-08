package schema

// Ports packages/schema/src/resolver.ts
//
// Reference resolution of one compile: schema resources, anchors and JSON Pointers over the root schema and the registered documents. A resolver lives only while a schema compiles (or while its errors are explained).

import (
	"slices"
	"strconv"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/nodeurl"
)

// resolved is a schema and the base URI its references resolve against.
type resolved struct {
	node any
	base string
}

// defaultBase is the base URI of a root schema without `$id`; relative references resolve against it.
const defaultBase = "https://json-schema.invalid/root.json"

// resolver indexes the root schema and resolves references in it and in the registered documents.
type resolver struct {
	draft Draft
	// rootBase is the base URI the root schema is walked from; walkers apply the root's own `$id` through baseFor.
	rootBase string
	root     JsonSchema
	options  CompileOptions
	indexed  bool
	// baseOf maps a schema object to the base URI it was indexed under.
	baseOf map[nodeKey]string
	// dynamicAnchors maps the URI (with a `#name` fragment) of every `$dynamicAnchor` to its schema.
	dynamicAnchors map[string]any
	registry       map[string]JsonSchema
	resources      map[string]any
	anchors        map[string]any
	// rejectUnresolved compiles a reference that names nothing as `false` (UnresolvedRefsReject); unresolved holds the URIs of those references.
	rejectUnresolved bool
	unresolved       map[string]struct{}
}

func newResolver(root JsonSchema, options CompileOptions) *resolver {
	r := &resolver{
		draft:          Draft202012,
		rootBase:       defaultBase,
		root:           root,
		options:        options,
		baseOf:         map[nodeKey]string{},
		dynamicAnchors: map[string]any{},
		registry:       map[string]JsonSchema{},
		resources:      map[string]any{},
		anchors:        map[string]any{},
		unresolved:     map[string]struct{}{},
	}
	r.rejectUnresolved = options.UnresolvedRefs == UnresolvedRefsReject
	if options.Draft == Draft7 {
		r.draft = Draft7
	}
	return r
}

// baseFor is the base URI of node, whose parent has base: its `$id` changes it, unless draft 7 makes the `$id` inert. Once the schema is indexed, it is the indexed base (a resolved reference target already has its own).
func (r *resolver) baseFor(node jsonObject, base string) string {
	if indexed, ok := r.baseOf[node.identity()]; ok {
		return indexed
	}
	id, ok := node.stringMember("$id")
	if !ok {
		return base
	}
	// pig divergence (D302): index before applying an `$id`: a caller's base may already include it, and the index applies it once
	// (Pi main 2bd2c2d0a).
	if !r.indexed {
		r.ensureIndexed()
		if indexed, ok := r.baseOf[node.identity()]; ok {
			return indexed
		}
	}
	if r.draft == Draft7 {
		if _, hasRef := node.stringMember("$ref"); hasRef || strings.HasPrefix(id, "#") {
			return base
		}
	}
	return documentOf(resolveUri(id, base))
}

// indexedBase is the base URI node was indexed under.
func (r *resolver) indexedBase(node jsonObject) (string, bool) {
	base, ok := r.baseOf[node.identity()]
	return base, ok
}

// ensureIndexed indexes the root schema and the registered documents' URIs, once.
func (r *resolver) ensureIndexed() {
	if r.indexed {
		return
	}
	r.indexed = true
	uris := make([]string, 0, len(r.options.Schemas))
	for uri := range r.options.Schemas {
		uris = append(uris, uri)
	}
	slices.Sort(uris)
	for _, uri := range uris {
		r.registry[documentOf(resolveUri(uri, defaultBase))] = r.options.Schemas[uri]
	}
	r.resources[defaultBase] = r.root
	r.index(r.root, defaultBase)
}

// resolve returns the schema a reference names, with the base URI it compiles under. It fails the compile if it names nothing, or, with UnresolvedRefsReject, resolves to `false`, the schema no value passes.
func (r *resolver) resolve(ref, base string) resolved {
	r.ensureIndexed()
	if !r.rejectUnresolved {
		return r.find(ref, base)
	}
	target, ok := r.tryFind(ref, base)
	if !ok {
		r.unresolved[resolveUri(ref, base)] = struct{}{}
		return resolved{node: false, base: base}
	}
	return target
}

// isUnresolved reports whether ref, from base, named nothing and resolved to `false`.
func (r *resolver) isUnresolved(ref, base string) bool {
	_, ok := r.unresolved[resolveUri(ref, base)]
	return ok
}

// tryFind is find with the compile failure of a reference that names nothing as ok == false.
func (r *resolver) tryFind(ref, base string) (target resolved, ok bool) {
	defer func() {
		if recovered := recover(); recovered != nil {
			if _, isCompileError := recovered.(compileError); !isCompileError {
				panic(recovered)
			}
			ok = false
		}
	}()
	return r.find(ref, base), true
}

func (r *resolver) find(ref, base string) resolved {
	uri := resolveUri(ref, base)
	doc := documentOf(uri)
	fragment := ""
	if len(uri) > len(doc) {
		decoded, err := nodeurl.DecodeURIComponent(uri[len(doc)+1:])
		if err != nil {
			fail("URI malformed")
		}
		fragment = decoded
	}
	root, ok := r.resources[doc]
	if !ok {
		root, ok = r.load(doc)
	}
	if !ok {
		fail("Unresolved $ref %s (no schema registered for %s)", ref, doc)
	}
	if fragment == "" {
		return resolved{node: root, base: doc}
	}
	if strings.HasPrefix(fragment, "/") {
		return r.pointer(root, doc, fragment)
	}
	target, ok := r.anchors[doc+"#"+fragment]
	if !ok {
		fail("Unresolved $ref %s (no anchor %s in %s)", ref, fragment, doc)
	}
	o, _ := asObject(target)
	base2, found := r.indexedBase(o)
	if !found {
		base2 = doc
	}
	return resolved{node: target, base: base2}
}

// isResource reports whether node is the root of the schema resource at base.
func (r *resolver) isResource(node jsonObject, base string) bool {
	r.ensureIndexed()
	resource, ok := r.resources[base]
	if !ok {
		return false
	}
	o, isObj := asObject(resource)
	return isObj && o.identity() == node.identity()
}

var schemaKeywords = []string{
	"additionalProperties",
	"propertyNames",
	"contains",
	"not",
	"if",
	"then",
	"else",
	"unevaluatedProperties",
	"unevaluatedItems",
	"additionalItems",
	"contentSchema",
	"items",
}

var mapKeywords = []string{"properties", "patternProperties", "$defs", "definitions", "dependentSchemas", "dependencies"}

var arrayKeywords = []string{"allOf", "anyOf", "oneOf", "prefixItems", "items"}

// index walks only keywords that hold subschemas: an `$id` inside `enum`, `const` or an unknown keyword names nothing.
func (r *resolver) index(node any, base string) {
	o, ok := isSchemaObject(node)
	if !ok {
		return
	}
	if _, indexed := r.baseOf[o.identity()]; indexed {
		return
	}
	b := base
	// In draft 7 a `$ref` makes every sibling keyword, `$id` included, inert.
	_, hasRef := o.stringMember("$ref")
	refOnly := r.draft == Draft7 && hasRef
	if id, ok := o.stringMember("$id"); ok && !refOnly {
		if r.draft == Draft7 && strings.HasPrefix(id, "#") {
			r.anchors[documentOf(b)+id] = node
		} else {
			uri := resolveUri(id, b)
			b = documentOf(uri)
			r.resources[b] = node
			if len(uri) > len(b)+1 {
				r.anchors[uri] = node
			}
		}
	}
	r.baseOf[o.identity()] = b
	if refOnly {
		return
	}
	if r.draft != Draft7 {
		if anchor, ok := o.stringMember("$anchor"); ok {
			r.anchors[b+"#"+anchor] = node
		}
		if anchor, ok := o.stringMember("$dynamicAnchor"); ok {
			r.anchors[b+"#"+anchor] = node
			r.dynamicAnchors[b+"#"+anchor] = node
		}
	}
	for _, key := range schemaKeywords {
		r.index(o.value(key), b)
	}
	for _, key := range mapKeywords {
		if m, ok := o.objectMember(key); ok {
			for _, k := range m.keys() {
				r.index(m.value(k), b)
			}
		}
	}
	for _, key := range arrayKeywords {
		if list, ok := o.arrayMember(key); ok {
			for _, s := range list {
				r.index(s, b)
			}
		}
	}
}

// load indexes the registered document doc.
func (r *resolver) load(doc string) (JsonSchema, bool) {
	schema, ok := r.registry[doc]
	if !ok {
		return nil, false
	}
	r.resources[doc] = schema
	r.index(schema, doc)
	return schema, true
}

func (r *resolver) pointer(root any, base, fragment string) resolved {
	node := root
	b := base
	for _, raw := range strings.Split(fragment, "/")[1:] {
		key := strings.ReplaceAll(strings.ReplaceAll(raw, "~1", "/"), "~0", "~")
		next, ok := ownMember(node, key)
		if !ok {
			fail("Unresolved $ref #%s in %s", fragment, base)
		}
		node = next
		if o, isObj := asObject(node); isObj {
			if nb, found := r.indexedBase(o); found {
				b = nb
			}
		}
	}
	return resolved{node: node, base: b}
}

// ownMember is `Object.hasOwn(node, key) ? node[key] : undefined` for an object or an array.
func ownMember(node any, key string) (any, bool) {
	if o, ok := asObject(node); ok {
		return o.get(key)
	}
	if list, ok := node.([]any); ok {
		if key == "length" {
			return float64(len(list)), true
		}
		//portlint:allow numbers Atoi plus the Itoa round trip accepts only canonical array indices, the own index keys of Object.hasOwn(node, key) in resolver.ts pointer()
		if index, err := strconv.Atoi(key); err == nil && index >= 0 && index < len(list) && strconv.Itoa(index) == key {
			return list[index], true
		}
	}
	return nil, false
}

// resolveUri resolves ref against base as `new URL(ref, base).href` does. A base or reference the URL parser rejects (a relative reference against a URN, say) takes fragment-only references.
func resolveUri(ref, base string) string {
	if u, err := nodeurl.ResolveReference(ref, base); err == nil {
		return u.Href()
	}
	if strings.HasPrefix(ref, "#") {
		return documentOf(base) + ref
	}
	return ref
}

// documentOf is uri without its fragment.
func documentOf(uri string) string {
	if before, _, ok := strings.Cut(uri, "#"); ok {
		return before
	}
	return uri
}
