package codemode

import (
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/factoryload"
)

// loadExtension runs the codemode factory through the production loader, as a Session's extension set does.
func loadExtension(options Options) (extension.Extension, error) {
	return loadFactory(CreateCodemodeExtension(options))
}

func loadFactory(factory extension.ExtensionFactory) (extension.Extension, error) {
	return factoryload.LoadExtensionFromFactory(factory, ".", extension.CreateEventBus(), extension.CreateExtensionRuntime(), "builtin:codemode")
}

// registeredDefinition is the codemode tool definition an extension loaded from the factory registered.
func registeredDefinition(ext extension.Extension) extension.ToolDefinition {
	tool, _ := ext.RegisteredTool(ToolName)
	return tool.Definition
}
