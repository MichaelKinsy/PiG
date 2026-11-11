package configschema

import (
	"strings"
	"testing"
)

// pi: packages/ai/src/providers/schema-builders.ts (openObject, stringLiteral, stringRecord) over pi-schema's obj, oneOf, str, num, ...
// Pi has no test file for the builders: the shapes below are what its schemas render (JSON.stringify(schema, null, 2)), which
// TestModelSchemaExportsMatchPi and TestCompatSchemaExportsMatchPi compare against Pi's own trees.

func compactText(s *Schema) string {
	text := schemaText(s)
	var out strings.Builder
	for line := range strings.SplitSeq(text, "\n") {
		out.WriteString(strings.TrimSpace(line))
	}
	return out.String()
}

func TestOpenObjectBuildsAnObjectThatAllowsOtherProperties(t *testing.T) {
	// obj(properties, {...annotations, open: true}): type, properties, required (only when a property is required), then the
	// annotations in the order given; an explicit additionalProperties: true option is appended after them.
	cases := []struct {
		name  string
		build func() *Schema
		want  string
	}{
		{"required and optional", func() *Schema {
			return OpenObject([]Prop{P("a", String()), Optional("b", Number()), P("c", Boolean())})
		}, `{"type": "object","properties": {"a": {"type": "string"},"b": {"type": "number"},"c": {"type": "boolean"}},"required": ["a","c"]}`},
		{"no required property", func() *Schema {
			return OpenObject([]Prop{Optional("a", String())})
		}, `{"type": "object","properties": {"a": {"type": "string"}}}`},
		{"no property", func() *Schema { return OpenObject(nil) }, `{"type": "object","properties": {}}`},
		{"annotations follow required", func() *Schema {
			return OpenObject([]Prop{P("a", String())}, D("An object."))
		}, `{"type": "object","properties": {"a": {"type": "string"}},"required": ["a"],"description": "An object."}`},
		{"explicit additionalProperties comes last", func() *Schema {
			return OpenObject([]Prop{P("a", String())}, Opt{"additionalProperties", true}, D("An object."))
		}, `{"type": "object","properties": {"a": {"type": "string"}},"required": ["a"],"description": "An object.","additionalProperties": true}`},
	}
	for _, c := range cases {
		if got := compactText(c.build()); got != c.want {
			t.Errorf("%s:\n got %s\nwant %s", c.name, got, c.want)
		}
	}
}

func TestStringLiteralStringRecordAndOneOfBuildTheirPiSchemaShapes(t *testing.T) {
	for name, c := range map[string]struct {
		got  *Schema
		want string
	}{
		"stringLiteral":           {StringLiteral("openai"), `{"type": "string","const": "openai"}`},
		"stringLiteral annotated": {StringLiteral("anthropic", D("d")), `{"type": "string","const": "anthropic","description": "d"}`},
		"stringRecord":            {StringRecord(String()), `{"type": "object","patternProperties": {"^.*$": {"type": "string"}}}`},
		"stringRecord annotated":  {StringRecord(Number(), D("d")), `{"type": "object","patternProperties": {"^.*$": {"type": "number"}},"description": "d"}`},
		"oneOf":                   {OneOf([]string{"text", "image"}), `{"enum": ["text","image"]}`},
		"oneOf annotated":         {OneOf([]string{"a"}, D("d")), `{"enum": ["a"],"description": "d"}`},
	} {
		if got := compactText(c.got); got != c.want {
			t.Errorf("%s:\n got %s\nwant %s", name, got, c.want)
		}
	}
}

// piAISchemaTrees are the pi-ai configuration schemas (compat-schema.ts and model-schema.ts), all built with openObject.
func piAISchemaTrees() map[string]*Schema {
	return map[string]*Schema{
		"ProviderCompatSchema":                ProviderCompatSchema(),
		"OpenAICompletionsCompatSchema":       OpenAICompletionsCompatSchema(),
		"OpenAIResponsesCompatSchema":         OpenAIResponsesCompatSchema(),
		"AnthropicMessagesCompatSchema":       AnthropicMessagesCompatSchema(),
		"BedrockCompatSchema":                 BedrockCompatSchema(),
		"MistralConversationsCompatSchema":    MistralConversationsCompatSchema(),
		"SystemOneCompatSchema":               SystemOneCompatSchema(),
		"OpenRouterRoutingSchema":             OpenRouterRoutingSchema(),
		"VercelGatewayRoutingSchema":          VercelGatewayRoutingSchema(),
		"AnthropicAllowedFallbackModelSchema": AnthropicAllowedFallbackModelSchema(),
		"ChatTemplateKwargValueSchema":        ChatTemplateKwargValueSchema(),
		"ThinkingLevelMapSchema":              ThinkingLevelMapSchema(),
		"ModelPromptCacheSchema":              ModelPromptCacheSchema(),
		"ModelCostRatesSchema":                ModelCostRatesSchema(),
		"ModelCostTierSchema":                 ModelCostTierSchema(),
		"ModelCostSchema":                     ModelCostSchema(),
		"ModelImageResizeOptionsSchema":       ModelImageResizeOptionsSchema(),
		"ModelImageInputLimitsSchema":         ModelImageInputLimitsSchema(),
		"ModelInputLimitsSchema":              ModelInputLimitsSchema(),
	}
}

// pi-schema's obj writes type, properties, required, the annotations: a TypeBox object wrote required before properties. Pi's generated
// schemas/models.schema.json moved every required list of a pi-ai object after its properties (Pi b556cfe5 diff of that file).
func TestPiAISchemaObjectsWriteTypePropertiesRequiredThenAnnotations(t *testing.T) {
	objects := 0
	for name, tree := range piAISchemaTrees() {
		walkSchemas(tree, name, func(node *Schema, path string) {
			if _, ok := node.get("properties"); !ok {
				return
			}
			objects++
			keys := make([]string, len(node.kv))
			for i, o := range node.kv {
				keys[i] = o.K
			}
			if len(keys) < 2 || keys[0] != "type" || keys[1] != "properties" {
				t.Errorf("%s: keys %v, want type then properties first", path, keys)
				return
			}
			rest := keys[2:]
			if len(rest) > 0 && rest[0] == "required" {
				rest = rest[1:]
			}
			for i, key := range rest {
				switch key {
				case "required":
					t.Errorf("%s: required after %v, want it right after properties", path, keys[:2+i])
				case "additionalProperties":
					if i != len(rest)-1 {
						t.Errorf("%s: additionalProperties is not the last key of %v", path, keys)
					}
					if v, _ := node.get("additionalProperties"); v != true {
						t.Errorf("%s: additionalProperties = %v, want true", path, v)
					}
				}
			}
		})
	}
	if objects < 50 {
		t.Fatalf("walked %d objects: the walk does not reach the trees", objects)
	}
}

func TestModelCostSchemaTextMatchesPiSchemaBuilders(t *testing.T) {
	want := `{"type": "object","properties": {"input": {"type": "number","description": "Input cost in USD per million tokens."},"output": {"type": "number","description": "Output cost in USD per million tokens."},"cacheRead": {"type": "number","description": "Cache-read cost in USD per million tokens."},"cacheWrite": {"type": "number","description": "Cache-write cost in USD per million tokens."},"tiers": {"type": "array","items": {"type": "object","properties": {"inputTokensAbove": {"type": "number","description": "Use this tier when total request input exceeds this token count."},"input": {"type": "number","description": "Input cost in USD per million tokens."},"output": {"type": "number","description": "Output cost in USD per million tokens."},"cacheRead": {"type": "number","description": "Cache-read cost in USD per million tokens."},"cacheWrite": {"type": "number","description": "Cache-write cost in USD per million tokens."}},"required": ["inputTokensAbove","input","output","cacheRead","cacheWrite"]},"description": "Request-wide pricing tiers. The highest matching input threshold applies to the full request."}},"required": ["input","output","cacheRead","cacheWrite"]}`
	if got := compactText(ModelCostSchema()); got != want {
		t.Errorf("ModelCostSchema:\n got %s\nwant %s", got, want)
	}
}

// pi: packages/coding-agent/src/core/pi-ai-schemas.ts PartialModelCostSchema: ModelCostSchema without its own `required`.
func TestPartialModelCostSchemaIsModelCostWithoutRequired(t *testing.T) {
	partial := PartialModelCostSchema()
	if _, ok := partial.get("required"); ok {
		t.Error("PartialModelCostSchema keeps required")
	}
	full := ModelCostSchema()
	var want []Opt
	for _, o := range full.kv {
		if o.K != "required" {
			want = append(want, o)
		}
	}
	if len(partial.kv) != len(want) {
		t.Fatalf("PartialModelCostSchema keys = %d, want %d", len(partial.kv), len(want))
	}
	for i, o := range partial.kv {
		if o.K != want[i].K {
			t.Errorf("key %d = %s, want %s", i, o.K, want[i].K)
		}
	}
	tiers, _ := partial.child("properties").child("tiers").child("items").get("required")
	if len(tiers.([]any)) != 5 {
		t.Errorf("the tiers of PartialModelCostSchema keep their required list: %v", tiers)
	}
	if _, ok := full.get("required"); !ok {
		t.Error("ModelCostSchema lost its required list: PartialModelCostSchema must not change it")
	}
}
