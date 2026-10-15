package codemode

// pi: packages/coding-agent/src/extensions/codemode/tool.ts

// pi: packages/coding-agent/src/extensions/codemode/index.ts

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/factoryload"
)

// index.ts:31-45: createCodemodeExtension(options) returns an ExtensionFactory that registers the `codemode` tool with `defaultActive: false`;
// the factory is reusable, and each call builds the registration from the options given when it was created.
// mutation-checked: registering the tool active fails the first check; ignoring DisableModels (index.ts `models: options.models ?? true`) fails the second.
func TestCreateCodemodeExtensionFactoryRegistersTheToolInactive(t *testing.T) {
	factory := CreateCodemodeExtension(Options{})
	for range 2 {
		ext, err := loadFactory(factory)
		if err != nil {
			t.Fatal(err)
		}
		tool, ok := ext.RegisteredTool(ToolName)
		if !ok || len(ext.RegisteredTools()) != 1 {
			t.Fatalf("tools = %v", ext.RegisteredTools())
		}
		if tool.Definition.DefaultActive == nil || *tool.Definition.DefaultActive {
			t.Fatal("codemode must be registered inactive (defaultActive: false)")
		}
	}
}

func TestCreateCodemodeExtensionFactoryTakesItsOptionsFromCreation(t *testing.T) {
	served, err := loadFactory(CreateCodemodeExtension(Options{}))
	if err != nil {
		t.Fatal(err)
	}
	disabled, err := loadFactory(CreateCodemodeExtension(Options{DisableModels: true}))
	if err != nil {
		t.Fatal(err)
	}
	const modelsLine = "- `models`: classifiers and image generation."
	if !strings.Contains(registeredDefinition(served).Description, modelsLine) {
		t.Fatal("a factory created without DisableModels must describe the models namespace")
	}
	if strings.Contains(registeredDefinition(disabled).Description, modelsLine) {
		t.Fatal("DisableModels must leave the models namespace out of the tool description")
	}
}

// index.ts createCodemodeExtension: `getMode: () => options.mode ?? readMode(pi)` and `getInlineBudget: () => options.inlineBudget ?? readInlineBudget(pi)`
// read codemode.mode and codemode.inlineBudget through the API's getSettings on every use, so a factory loaded by the production loader follows the
// host's current settings, and an option given to the factory overrides them.
func TestCreateCodemodeExtensionFactoryReadsTheHostSettingsOnEveryUse(t *testing.T) {
	load := func(options Options, current *map[string]any) extension.ToolDefinition {
		runtime := extension.CreateExtensionRuntime()
		ext, err := factoryload.LoadExtensionFromFactory(CreateCodemodeExtension(options), ".", extension.CreateEventBus(), runtime, "builtin:codemode")
		if err != nil {
			t.Fatal(err)
		}
		runtime.ExtensionActions = extension.ExtensionActions{GetSettings: func() extension.Settings { return extension.Settings{"codemode": *current} }}
		return registeredDefinition(ext)
	}
	hidden := func(definition extension.ToolDefinition) []string {
		return definition.PrepareLoadout(overrideLoadout()).HiddenDeclarations
	}

	current := map[string]any{"mode": "only"}
	definition := load(Options{}, &current)
	if got := hidden(definition); len(got) != 1 || got[0] != "echo" {
		t.Fatalf("settings mode only: hidden = %v, want [echo]", got)
	}
	// The setting is read again on the next use, not captured when the factory ran.
	current = map[string]any{"mode": "on"}
	if got := hidden(definition); len(got) != 0 {
		t.Fatalf("settings changed to on: hidden = %v, want none", got)
	}
	// An option given to the factory wins over the setting.
	current = map[string]any{"mode": "on"}
	if got := hidden(load(Options{Mode: ModeOnly}, &current)); len(got) != 1 {
		t.Fatalf("option only over a setting of on: hidden = %v, want [echo]", got)
	}
	// The inline budget follows the setting too: a budget of zero lists no tool section even in mode only.
	current = map[string]any{"mode": "only", "inlineBudget": float64(0)}
	if got := load(Options{}, &current).PrepareLoadout(overrideLoadout()).Descriptions[ToolName]; strings.Contains(got, "### `echo`") {
		t.Fatalf("settings inlineBudget 0 still lists echo:\n%s", got)
	}
	current = map[string]any{"mode": "only", "inlineBudget": float64(100000)}
	if got := load(Options{}, &current).PrepareLoadout(overrideLoadout()).Descriptions[ToolName]; !strings.Contains(got, "### `echo`") {
		t.Fatalf("settings inlineBudget 100000 does not list echo:\n%s", got)
	}
}
