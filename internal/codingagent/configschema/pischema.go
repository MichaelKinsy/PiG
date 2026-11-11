package configschema

// Builders for pi-ai's own configuration schemas (compat-schema.ts, model-schema.ts), which Pi builds with pi-schema and no longer with
// TypeBox: pi-schema's builders written for the *Schema tree of this package. The leaves (str, num, int, bool, nul, arr, union) render the same
// node as their TypeBox counterparts String, Number, Integer, Boolean, Null, Array and Union, so those constructors serve both families; the
// builders below differ from TypeBox in what they write.

// OpenObject is openObject of packages/ai/src/providers/schema-builders.ts over pi-schema's obj(properties, {...options, open: true}): type,
// properties, then required when a property is not optional, then the options in the order given. An Opt{"additionalProperties", true} option
// says explicitly that other properties are allowed and is written after the others, as openObject appends it to the schema.
func OpenObject(props []Prop, opts ...Opt) *Schema {
	properties := &Schema{}
	var required []any
	for _, p := range props {
		properties.kv = append(properties.kv, Opt{p.Name, p.Schema})
		if !p.Optional {
			required = append(required, p.Name)
		}
	}
	s := node(Opt{"type", "object"}, Opt{"properties", properties})
	if required != nil {
		s.kv = append(s.kv, Opt{"required", required})
	}
	explicit := false
	for _, o := range opts {
		if o.K != "additionalProperties" {
			s.kv = append(s.kv, o)
			continue
		}
		if o.V != true {
			panic("configschema: OpenObject takes additionalProperties: true only")
		}
		explicit = true
	}
	if explicit {
		s.kv = append(s.kv, Opt{"additionalProperties", true})
	}
	return s
}

// StringLiteral is stringLiteral of schema-builders.ts: one string, {"type":"string","const":value}.
func StringLiteral(value string, opts ...Opt) *Schema {
	return withOpts(node(Opt{"type", "string"}, Opt{"const", value}), opts)
}

// StringRecord is stringRecord of schema-builders.ts: an object with any string keys and values of value,
// {"type":"object","patternProperties":{"^.*$":value}}.
func StringRecord(value *Schema, opts ...Opt) *Schema {
	return withOpts(node(Opt{"type", "object"}, Opt{"patternProperties", node(Opt{"^.*$", value})}), opts)
}

// OneOf is pi-schema's oneOf over string values: {"enum":[...]}.
func OneOf(values []string, opts ...Opt) *Schema {
	list := make([]any, len(values))
	for i, v := range values {
		list[i] = v
	}
	return withOpts(node(Opt{"enum", list}), opts)
}
