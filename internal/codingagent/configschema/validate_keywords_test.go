package configschema

import (
	"fmt"
	"testing"
)

// Schema.Errors interprets only the keywords and values the models.json trees use. A pin bump that adds another one (additionalProperties false, a pattern other than "^.*$", a non-string const, minItems, ...) would be ignored at run time and accept a document Pi rejects, while the rendered artifact and the byte-equal test stay green. The differential reaches such a keyword only if one of its documents does; this test fails on the tree itself.
func TestModelsConfigSchemaUsesOnlyKeywordsTheValidatorChecks(t *testing.T) {
	annotations := map[string]bool{"description": true, "default": true, "title": true}
	var problems []string
	var walk func(s *Schema, path string)
	walk = func(s *Schema, path string) {
		if s == nil {
			return
		}
		kind, _ := s.get("type")
		for _, o := range s.kv {
			at := path + "/" + o.K
			switch o.K {
			case "type", "properties", "required", "anyOf", "items", "minLength", "minimum", "maximum", "exclusiveMinimum", "maxItems":
			case "enum":
				for _, value := range o.V.([]any) {
					if _, ok := value.(string); !ok {
						problems = append(problems, fmt.Sprintf("%s: enum value %#v is not a string", at, value))
					}
				}
			case "const":
				if _, ok := o.V.(string); !ok || kind != "string" {
					problems = append(problems, fmt.Sprintf("%s: const %#v under type %v", at, o.V, kind))
				}
			case "additionalProperties":
				if o.V != true {
					problems = append(problems, fmt.Sprintf("%s: additionalProperties %#v", at, o.V))
				}
			case "patternProperties":
				pattern := o.V.(*Schema)
				if len(pattern.kv) != 1 || pattern.kv[0].K != "^.*$" {
					problems = append(problems, fmt.Sprintf("%s: patterns %v", at, pattern.kv))
				}
			default:
				if !annotations[o.K] {
					problems = append(problems, at+": keyword the validator does not check")
				}
			}
			switch v := o.V.(type) {
			case *Schema:
				if o.K == "properties" || o.K == "patternProperties" {
					for _, member := range v.kv {
						walk(member.V.(*Schema), at+"/"+member.K)
					}
				} else {
					walk(v, at)
				}
			case []any:
				for i, item := range v {
					if child, ok := item.(*Schema); ok {
						walk(child, fmt.Sprintf("%s/%d", at, i))
					}
				}
			}
		}
	}
	walk(ModelsConfigSchema(), "#")
	for _, problem := range problems {
		t.Error(problem)
	}
}
