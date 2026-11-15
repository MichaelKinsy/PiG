//go:build !pig_strip_mistral_conversations

package ai

// mistralToolWireKinds is the tools of BenchmarkToolDeclarationWire as mistral-conversations sends them.
func mistralToolWireKinds(tools []ToolSchema) []toolWireKind {
	mistral := make([]mistralTool, len(tools))
	for i, tool := range tools {
		mistral[i] = mistralTool{Type: "function", Function: mistralToolFn{Name: tool.Name, Description: tool.Description, Parameters: tool.Parameters, parameterOrder: tool.parameterOrder}}
	}
	return []toolWireKind{{"mistral-conversations", mistral}}
}
