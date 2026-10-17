package codingagent

import (
	"github.com/MichaelKinsy/PiG/internal/codingagent/configschema"
	"github.com/MichaelKinsy/PiG/tui"
)

// Mirrors packages/coding-agent/src/core/key-id-schema.ts and keybindings-schema.ts. The artifact lives here because it enumerates the keybinding
// definitions of this package, which the schema package (a dependency of models.json loading) must not import.

func keyIDSchema() *configschema.Schema {
	return configschema.String(configschema.Opt{K: "pattern", V: KeyIDPattern()}, configschema.D("Key identifier with optional ctrl, shift, alt, or super modifiers."))
}

func keybindingValueSchema(opts ...configschema.Opt) *configschema.Schema {
	return configschema.Union([]*configschema.Schema{keyIDSchema(), configschema.Array(keyIDSchema())}, opts...)
}

// KeybindingsSchemaArtifact is the schemas/keybindings.schema.json artifact that configschema.Render takes with its own four.
func KeybindingsSchemaArtifact() configschema.Artifact {
	props := []configschema.Prop{configschema.Optional("$schema", configschema.String())}
	for _, id := range tui.TUIKeybindingOrder {
		props = append(props, configschema.Optional(string(id), keybindingValueSchema(configschema.D(tui.TUIKeybindings[id].Description))))
	}
	km := DefaultKeybindingsManager()
	for _, id := range km.KeybindingIDs() {
		definition, _ := km.GetDefinition(id)
		props = append(props, configschema.Optional(id, keybindingValueSchema(configschema.D(definition.Description))))
	}
	return configschema.Artifact{
		Path:        configschema.KeybindingsArtifactPath,
		Schema:      configschema.Object(props, configschema.Opt{K: "additionalProperties", V: keybindingValueSchema()}),
		Definitions: []configschema.Definition{{Name: "KeybindingValue", Schema: keybindingValueSchema()}},
	}
}
