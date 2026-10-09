package codemode

// pi: packages/coding-agent/src/extensions/codemode/tool.ts

// pi: packages/coding-agent/src/extensions/codemode/index.ts

import (
	"testing"
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
	if registeredDefinition(served).Description == registeredDefinition(disabled).Description {
		t.Fatal("DisableModels must change the tool description: the models namespace is left out")
	}
}
