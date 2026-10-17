package configschema

import (
	"bytes"
	"fmt"
	"strings"
	"sync"

	"github.com/MichaelKinsy/PiG/internal/lazyregexp"
)

const (
	// schemaBaseURL + "/" + Artifact.Path is the schema $id: Pi 1.2.0 generate-schemas.ts:21 publishes them under https://pi.dev/schemas.
	schemaBaseURL    = "https://pi.dev"
	schemaDraft      = "https://json-schema.org/draft/2020-12/schema"
	generatedComment = "This file is generated from TypeScript source. Do not edit it manually; run npm run generate:schemas."
)

// Artifact is one generated schema file and the shared sub-schemas its generator turns into $defs.
type Artifact struct {
	Path        string
	Schema      *Schema
	Definitions []Definition
}

// Definition is one named $defs entry.
type Definition struct {
	Name   string
	Schema *Schema
}

var annotationKeys = map[string]bool{"default": true, "deprecated": true, "description": true, "examples": true, "readOnly": true, "title": true, "writeOnly": true}

// definitionName holds the pattern of a $defs name. definitionNamePattern creates it on first use, so package initialization allocates
// nothing for it.
var definitionName struct {
	once    sync.Once
	pattern *lazyregexp.Regexp
}

func definitionNamePattern() *lazyregexp.Regexp {
	definitionName.once.Do(func() { definitionName.pattern = lazyregexp.New(`^[A-Za-z][A-Za-z0-9_-]*$`) })
	return definitionName.pattern
}

func splitAnnotations(s *Schema) (shape, annotations []Opt) {
	for _, o := range s.kv {
		if annotationKeys[o.K] {
			annotations = append(annotations, o)
		} else {
			shape = append(shape, o)
		}
	}
	return shape, annotations
}

func compact(v any, buf *bytes.Buffer) {
	switch t := v.(type) {
	case *Schema:
		buf.WriteString("{")
		for i, o := range t.kv {
			if i > 0 {
				buf.WriteString(",")
			}
			writeString(buf, o.K)
			buf.WriteString(":")
			compact(o.V, buf)
		}
		buf.WriteString("}")
	case []any:
		buf.WriteString("[")
		for i, item := range t {
			if i > 0 {
				buf.WriteString(",")
			}
			compact(item, buf)
		}
		buf.WriteString("]")
	default:
		marshal(v, "", buf)
	}
}

func shapeKey(shape []Opt) string {
	var buf bytes.Buffer
	compact(&Schema{kv: shape}, &buf)
	return buf.String()
}

// addDefinitions replaces every repeated schema whose shape equals a definition with a local $ref that keeps the use's annotations
// (generate-schemas.ts addDefinitions).
func addDefinitions(schema *Schema, definitions []Definition) (*Schema, error) {
	type prepared struct {
		name  string
		shape []Opt
		key   string
	}
	var defs []prepared
	for _, d := range definitions {
		if !definitionNamePattern().MatchString(d.Name) {
			return nil, fmt.Errorf("invalid schema definition name: %s", d.Name)
		}
		shape, _ := splitAnnotations(d.Schema)
		defs = append(defs, prepared{d.Name, shape, shapeKey(shape)})
	}
	referenced := map[string]bool{}
	var replace func(value any, excluded string) any
	replace = func(value any, excluded string) any {
		switch t := value.(type) {
		case []any:
			out := make([]any, len(t))
			for i, item := range t {
				out[i] = replace(item, excluded)
			}
			return out
		case *Schema:
			shape, annotations := splitAnnotations(t)
			key := shapeKey(shape)
			for _, d := range defs {
				if d.name != excluded && d.key == key {
					referenced[d.name] = true
					return ref(d.name, annotations)
				}
			}
			out := &Schema{}
			for _, o := range t.kv {
				if annotationKeys[o.K] {
					out.kv = append(out.kv, o)
				} else {
					out.kv = append(out.kv, Opt{o.K, replace(o.V, excluded)})
				}
			}
			return out
		}
		return value
	}
	out := replace(schema, "").(*Schema)
	compactDefs := &Schema{}
	for _, d := range defs {
		compactDefs.kv = append(compactDefs.kv, Opt{d.name, replace(&Schema{kv: d.shape}, d.name)})
	}
	var unused []string
	for _, d := range defs {
		if !referenced[d.name] {
			unused = append(unused, d.name)
		}
	}
	if len(unused) > 0 {
		return nil, fmt.Errorf("unused schema definitions: %s", strings.Join(unused, ", "))
	}
	out.kv = append(out.kv, Opt{"$defs", compactDefs})
	return out, nil
}

// Render serializes one artifact the way generate-schemas.ts serializeSchema does.
func (a Artifact) Render() (string, error) {
	schema := a.Schema
	if len(a.Definitions) > 0 {
		var err error
		if schema, err = addDefinitions(schema, a.Definitions); err != nil {
			return "", err
		}
	}
	root := &Schema{kv: []Opt{{"$schema", schemaDraft}, {"$id", schemaBaseURL + "/" + a.Path}, {"$comment", generatedComment}}}
	root.kv = append(root.kv, schema.kv...)
	var buf bytes.Buffer
	marshal(root, "", &buf)
	buf.WriteString("\n")
	return buf.String(), nil
}
