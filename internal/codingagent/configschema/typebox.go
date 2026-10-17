// Package configschema renders the JSON Schema artifacts of Pi's configuration files (models.json, settings.json, keybindings.json and
// themes) from Go schema trees. The trees mirror the TypeBox schemas of Pi's coding-agent and ai packages, and Render mirrors
// packages/coding-agent/scripts/generate-schemas.ts: node order, annotation placement and $defs extraction follow the TypeScript output.
package configschema

import (
	"bytes"
	"encoding/json"
	"strings"
)

// Schema is one JSON Schema node whose keys keep their insertion order, the order in which JSON.stringify writes a TypeBox schema.
type Schema struct{ kv []Opt }

// Opt is one schema keyword, in the order the TypeScript options object lists it.
type Opt struct {
	K string
	V any
}

// Prop is one object property.
type Prop struct {
	Name     string
	Schema   *Schema
	Optional bool
}

// P is a required property and Optional an optional one (Type.Optional).
func P(name string, s *Schema) Prop        { return Prop{Name: name, Schema: s} }
func Optional(name string, s *Schema) Prop { return Prop{Name: name, Schema: s, Optional: true} }

// D is the description option and Default the default option.
func D(text string) Opt      { return Opt{"description", text} }
func Default(v any) Opt      { return Opt{"default", v} }
func node(kv ...Opt) *Schema { return &Schema{kv: kv} }

func withOpts(s *Schema, opts []Opt) *Schema {
	for _, o := range opts {
		s.set(o.K, o.V)
	}
	return s
}

func (s *Schema) set(key string, value any) {
	for i := range s.kv {
		if s.kv[i].K == key {
			s.kv[i].V = value
			return
		}
	}
	s.kv = append(s.kv, Opt{key, value})
}

func (s *Schema) get(key string) (any, bool) {
	for _, o := range s.kv {
		if o.K == key {
			return o.V, true
		}
	}
	return nil, false
}

func (s *Schema) clone() *Schema { return &Schema{kv: append([]Opt(nil), s.kv...)} }

// With returns a copy of the schema with the options set (Type options passed as the last TypeBox argument).
func (s *Schema) With(opts ...Opt) *Schema { return withOpts(s.clone(), opts) }

func String(opts ...Opt) *Schema  { return withOpts(node(Opt{"type", "string"}), opts) }
func Number(opts ...Opt) *Schema  { return withOpts(node(Opt{"type", "number"}), opts) }
func Integer(opts ...Opt) *Schema { return withOpts(node(Opt{"type", "integer"}), opts) }
func Boolean(opts ...Opt) *Schema { return withOpts(node(Opt{"type", "boolean"}), opts) }
func Null(opts ...Opt) *Schema    { return withOpts(node(Opt{"type", "null"}), opts) }

// Literal is Type.Literal of a string, a boolean or a number.
func Literal(value any, opts ...Opt) *Schema {
	kind := "string"
	switch value.(type) {
	case bool:
		kind = "boolean"
	case int, float64:
		kind = "number"
	}
	return withOpts(node(Opt{"type", kind}, Opt{"const", value}), opts)
}

// Enum is Type.Enum over a list of string values.
func Enum(values []string, opts ...Opt) *Schema {
	list := make([]any, len(values))
	for i, v := range values {
		list[i] = v
	}
	return withOpts(node(Opt{"enum", list}), opts)
}

// Union is Type.Union.
func Union(alternatives []*Schema, opts ...Opt) *Schema {
	list := make([]any, len(alternatives))
	for i, a := range alternatives {
		list[i] = a
	}
	return withOpts(node(Opt{"anyOf", list}), opts)
}

func Array(items *Schema, opts ...Opt) *Schema {
	return withOpts(node(Opt{"type", "array"}, Opt{"items", items}), opts)
}

// Unknown is Type.Unknown, which accepts any value.
func Unknown(opts ...Opt) *Schema { return withOpts(node(), opts) }

// Record is Type.Record(Type.String(), value): an object whose every property matches value.
func Record(value *Schema, opts ...Opt) *Schema {
	return withOpts(node(Opt{"type", "object"}, Opt{"patternProperties", node(Opt{"^.*$", value})}), opts)
}

// Object is Type.Object: the required list holds the properties that are not Optional.
func Object(props []Prop, opts ...Opt) *Schema {
	var required []any
	properties := &Schema{}
	for _, p := range props {
		if !p.Optional {
			required = append(required, p.Name)
		}
		properties.kv = append(properties.kv, Opt{p.Name, p.Schema})
	}
	s := node(Opt{"type", "object"})
	if required != nil {
		s.kv = append(s.kv, Opt{"required", required})
	}
	s.kv = append(s.kv, Opt{"properties", properties})
	return withOpts(s, opts)
}

// RecordOf is Type.Record over the string literals keys: an object with one required property per key.
func RecordOf(keys []string, value *Schema, opts ...Opt) *Schema {
	props := make([]Prop, len(keys))
	for i, k := range keys {
		props[i] = P(k, value)
	}
	return Object(props, opts...)
}

// Partial is Type.Partial of an object: no property is required.
func Partial(s *Schema, opts ...Opt) *Schema {
	out := s.clone()
	kept := out.kv[:0]
	for _, o := range out.kv {
		if o.K != "required" {
			kept = append(kept, o)
		}
	}
	out.kv = kept
	return withOpts(out, opts)
}

// Ref is a local reference to a $defs entry; annotations follow the $ref like the generator's output.
func ref(name string, annotations []Opt) *Schema {
	return withOpts(node(Opt{"$ref", "#/$defs/" + name}), annotations)
}

// marshal writes JSON.stringify(value, null, 2) for a schema tree.
func marshal(v any, indent string, buf *bytes.Buffer) {
	switch t := v.(type) {
	case *Schema:
		if len(t.kv) == 0 {
			buf.WriteString("{}")
			return
		}
		buf.WriteString("{\n")
		for i, o := range t.kv {
			buf.WriteString(indent + "  ")
			writeString(buf, o.K)
			buf.WriteString(": ")
			marshal(o.V, indent+"  ", buf)
			if i < len(t.kv)-1 {
				buf.WriteString(",")
			}
			buf.WriteString("\n")
		}
		buf.WriteString(indent + "}")
	case []any:
		if len(t) == 0 {
			buf.WriteString("[]")
			return
		}
		buf.WriteString("[\n")
		for i, item := range t {
			buf.WriteString(indent + "  ")
			marshal(item, indent+"  ", buf)
			if i < len(t)-1 {
				buf.WriteString(",")
			}
			buf.WriteString("\n")
		}
		buf.WriteString(indent + "]")
	case string:
		writeString(buf, t)
	case nil:
		buf.WriteString("null")
	default:
		data, err := json.Marshal(t)
		if err != nil {
			panic(err)
		}
		buf.Write(data)
	}
}

// writeString writes a JSON string without the HTML escaping encoding/json applies, as JSON.stringify does.
func writeString(buf *bytes.Buffer, s string) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(s); err != nil {
		panic(err)
	}
	buf.WriteString(strings.TrimSuffix(b.String(), "\n"))
}
