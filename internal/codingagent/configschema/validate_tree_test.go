package configschema

import (
	"fmt"
	"testing"
)

// walkSchemas visits every schema node of a tree: property and record values, array items and union alternatives. The
// holders of "properties" and "patternProperties" map names to schemas, so they are traversed and not visited.
func walkSchemas(s *Schema, path string, visit func(*Schema, string)) {
	visit(s, path)
	for _, o := range s.kv {
		switch value := o.V.(type) {
		case *Schema:
			if o.K == "properties" || o.K == "patternProperties" {
				for _, member := range value.kv {
					walkSchemas(member.V.(*Schema), path+"/"+o.K+"/"+member.K, visit)
				}
				continue
			}
			walkSchemas(value, path+"/"+o.K, visit)
		case []any:
			for i, item := range value {
				if alternative, ok := item.(*Schema); ok {
					walkSchemas(alternative, fmt.Sprintf("%s/%s/%d", path, o.K, i), visit)
				}
			}
		}
	}
}

// ValidateModelsConfig runs on every models.json Pig loads, and the validator panics on a keyword it does not model rather
// than accept it unchecked. A node is only checked when a document reaches it, so a keyword the oracle documents never reach
// would panic on the first user file that sets that property. Every node of the tree is checked here against each JSON kind,
// and a record must use the one pattern Type.Record(Type.String(), ...) renders.
func TestTheValidatorModelsEveryNodeOfTheModelsSchema(t *testing.T) {
	documents := []string{`null`, `true`, `0`, `1.5`, `""`, `"x"`, `[]`, `[1]`, `{}`, `{"a":1}`}
	nodes := 0
	walkSchemas(ModelsConfigSchema(), "#", func(node *Schema, path string) {
		nodes++
		if patterns, ok := node.get("patternProperties"); ok {
			if members := patterns.(*Schema).kv; len(members) != 1 || members[0].K != "^.*$" {
				t.Errorf("%s: patternProperties %v, want the single pattern ^.*$", path, members)
			}
		}
		for _, text := range documents {
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Errorf("%s on %s: %v", path, text, r)
					}
				}()
				var errs []schemaError
				node.check(mustDoc(t, text), nil, &errs)
			}()
		}
	})
	if nodes < 100 {
		t.Fatalf("walked %d nodes: the walk does not reach the tree", nodes)
	}
}

func mustDoc(t *testing.T, text string) jsonNode {
	t.Helper()
	node, err := parseJSONNode([]byte(text))
	if err != nil {
		t.Fatal(err)
	}
	return node
}

// A keyword or type the validator does not model must not be accepted silently.
func TestTheValidatorPanicsOnAKeywordOrTypeItDoesNotModel(t *testing.T) {
	for name, schema := range map[string]*Schema{"pattern keyword": String(Opt{"pattern", "^a$"}), "unknown type": node(Opt{"type", "bigint"})} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("%s was accepted without being checked", name)
				}
			}()
			var errs []schemaError
			schema.check(mustDoc(t, `"b"`), nil, &errs)
		}()
	}
}
