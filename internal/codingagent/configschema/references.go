package configschema

import (
	"bytes"
	"regexp"
	"slices"
	"strings"
)

// Mirrors packages/coding-agent/scripts/generate-references.ts: the reference page of the models.json fields, rendered from the schema
// trees. The page is the same as Pi's except for the generated comment, which names PiG's generator.

// ModelsConfigurationReferencePath is the file name of the models.json reference page, as RenderReferences keys it.
const ModelsConfigurationReferencePath = "models-configuration.md"

const referencesGeneratedComment = "This file is generated from the Go schema trees of internal/codingagent/configschema. Do not edit it manually; run make generate-schemas."

// referenceSection is one section of a reference page.
type referenceSection struct {
	heading string
	intro   string
	// schema is the object schema whose properties become table rows.
	schema *Schema
	// links are the top-level fields whose type links to another section instead of listing nested fields.
	links map[string]string
	// collapsed are the top-level fields listed without their nested fields.
	collapsed []string
}

var simpleTypeName = regexp.MustCompile(`^[a-z]+$`)

// formatList is formatList of generate-references.ts: "a, b and c".
func formatList(items []string) string {
	if len(items) > 1 {
		return strings.Join(items[:len(items)-1], ", ") + " and " + items[len(items)-1]
	}
	if len(items) == 1 {
		return items[0]
	}
	return ""
}

func modelsConfigurationSections() []referenceSection {
	parts := newModelSchemaParts()
	sections := []referenceSection{
		{
			heading: "Providers",
			intro:   "Each key under `providers` is a provider ID. A built-in provider ID changes that provider. Any other ID adds a provider, which needs `baseUrl` and an `api` at provider or model level.",
			schema:  providerConfigSchema(parts),
			links:   map[string]string{"models": "#models", "modelOverrides": "#model-overrides", "compat": "#compatibility"},
		},
		{
			heading: "Models",
			intro:   "Each `models` entry adds a model to the provider. An entry whose `id` matches an existing model replaces it.",
			schema:  modelDefinitionSchema(parts),
			links:   map[string]string{"inputLimits": "#input-limits", "compat": "#compatibility"},
		},
		{
			heading: "Model overrides",
			intro:   "Each `modelOverrides` key is the ID of a built-in or extension model. Unknown IDs are ignored. Object fields, such as `cost`, merge with the model's value field by field; other fields replace it.",
			schema:  modelOverrideSchema(parts),
			links:   map[string]string{"inputLimits": "#input-limits", "compat": "#compatibility"},
		},
		{
			heading: "Input limits",
			intro:   "See [Describe model input and caching](models.md#describe-model-input-and-caching).",
			schema:  ModelInputLimitsSchema(),
		},
		{
			heading: "Compatibility",
			intro:   "`compat` describes verified differences from an API's standard request and response behavior. Model values override provider values per field. The fields that apply depend on the model's `api`.",
		},
	}
	// APIs grouped by the compatibility schema they share, in first-seen order.
	var schemaOrder []string
	apis := map[string][]string{}
	for _, entry := range CompatSchemasByApi() {
		if _, seen := apis[entry.Schema]; !seen {
			schemaOrder = append(schemaOrder, entry.Schema)
		}
		apis[entry.Schema] = append(apis[entry.Schema], entry.API)
	}
	for _, name := range schemaOrder {
		group := apis[name]
		section := referenceSection{
			heading:   "### `" + group[0] + "`",
			schema:    compatSchemaNamed(name),
			collapsed: []string{"openRouterRouting", "vercelGatewayRouting"},
		}
		if aliases := group[1:]; len(aliases) > 0 {
			quoted := make([]string, len(aliases))
			for i, alias := range aliases {
				quoted[i] = "`" + alias + "`"
			}
			section.intro = "Also applies to " + formatList(quoted) + "."
		}
		sections = append(sections, section)
	}
	return sections
}

// referenceType is referenceType of generate-references.ts.
func referenceType(schema *Schema) string {
	if v, ok := schema.get("const"); ok {
		return jsonText(v)
	}
	if v, ok := schema.get("enum"); ok {
		var values []string
		for _, item := range v.([]any) {
			values = append(values, jsonText(item))
		}
		return strings.Join(values, " | ")
	}
	if alternatives := schema.alternatives("anyOf"); len(alternatives) > 0 {
		var seen []string
		for _, alternative := range alternatives {
			if text := referenceType(alternative); !contains(seen, text) {
				seen = append(seen, text)
			}
		}
		return strings.Join(seen, " | ")
	}
	if kind, _ := schema.get("type"); kind == "array" {
		item := "unknown"
		if items := schema.child("items"); items != nil {
			item = referenceType(items)
		}
		if strings.Contains(item, " | ") {
			return "(" + item + ")[]"
		}
		return item + "[]"
	}
	if kind, ok := schema.get("type"); ok {
		return kind.(string)
	}
	return "unknown"
}

func contains(list []string, text string) bool {
	return slices.Contains(list, text)
}

func jsonText(v any) string {
	var buf bytes.Buffer
	compact(v, &buf)
	return buf.String()
}

// documentedObject is documentedObject of generate-references.ts: the nested object whose fields get their own rows, if any of them carries
// documentation, and the path suffix that reaches it through arrays.
func documentedObject(schema *Schema) (suffix string, object *Schema) {
	if kind, _ := schema.get("type"); kind == "array" {
		items := schema.child("items")
		if items == nil {
			return "", nil
		}
		inner, found := documentedObject(items)
		if found == nil {
			return "", nil
		}
		return "[]" + inner, found
	}
	object = schema
	if schema.child("properties") == nil {
		object = nil
		for _, branch := range schema.alternatives("anyOf") {
			if branch.child("properties") != nil {
				object = branch
				break
			}
		}
	}
	if object == nil {
		return "", nil
	}
	for _, field := range object.child("properties").kv {
		fieldSchema := field.V.(*Schema)
		_, described := fieldSchema.get("description")
		_, defaulted := fieldSchema.get("default")
		if described || defaulted {
			return "", object
		}
	}
	return "", nil
}

func escapeTableText(text string) string {
	return strings.ReplaceAll(strings.ReplaceAll(text, "|", `\|`), "<", "&lt;")
}

// referenceRows is referenceRows of generate-references.ts.
func referenceRows(schema *Schema, section referenceSection, prefix string) []string {
	var rows []string
	properties := schema.child("properties")
	if properties == nil {
		return nil
	}
	required := schema.strings("required")
	for _, o := range properties.kv {
		name, field := o.K, o.V.(*Schema)
		path := prefix + name
		link := ""
		if prefix == "" {
			link = section.links[name]
		}
		typeName := escapeTableText(referenceType(field))
		typeText := typeName
		if !simpleTypeName.MatchString(typeName) {
			typeText = "`" + typeName + "`"
		}
		defaultText := ""
		if v, ok := field.get("default"); ok {
			defaultText = "`" + escapeTableText(jsonText(v)) + "`"
		}
		var description []string
		if contains(required, name) {
			description = append(description, "Required.")
		}
		if v, ok := field.get("description"); ok {
			description = append(description, v.(string))
		}
		cell := typeText
		if link != "" {
			cell = "[" + typeText + "](" + link + ")"
		}
		rows = append(rows, "| `"+path+"` | "+cell+" | "+defaultText+" | "+escapeTableText(strings.Join(description, " "))+" |")
		if link != "" || (prefix == "" && contains(section.collapsed, name)) {
			continue
		}
		if suffix, nested := documentedObject(field); nested != nil {
			rows = append(rows, referenceRows(nested, section, path+suffix+".")...)
		}
	}
	return rows
}

// RenderReferences returns the reference pages by file name (generate-references.ts renderReferences).
func RenderReferences() map[string]string {
	blocks := []string{
		"# Models Configuration",
		"<!-- " + referencesGeneratedComment + " -->",
		strings.Join([]string{
			"This reference lists the fields of `models.json`. See [Configure a compatible endpoint](models.md#configure-a-compatible-endpoint) for examples and [Configuration](configuration.md#agent-directory) for the file location.",
			"Add `\"$schema\": \"https://pi.dev/schemas/models.schema.json\"` to `models.json` for editor validation and completion.",
		}, "\n\n"),
	}
	for _, section := range modelsConfigurationSections() {
		if strings.HasPrefix(section.heading, "#") {
			blocks = append(blocks, section.heading)
		} else {
			blocks = append(blocks, "## "+section.heading)
		}
		if section.intro != "" {
			blocks = append(blocks, section.intro)
		}
		if section.schema != nil {
			rows := referenceRows(section.schema, section, "")
			blocks = append(blocks, strings.Join(append([]string{"| Field | Type | Default | Description |", "|---|---|---|---|"}, rows...), "\n"))
		}
	}
	return map[string]string{ModelsConfigurationReferencePath: strings.Join(blocks, "\n\n") + "\n"}
}
