package ai

// convertTools runs ConvertResponsesTools with this provider's strict default and the given compat flags.
func (p *openAIResponsesProvider) convertTools(tools []ToolSchema, supportsStrictMode, supportsGrammar bool) ([]ResponsesTool, error) {
	return ConvertResponsesTools(tools, p.responsesToolOptions(responsesCompat{SupportsStrictMode: supportsStrictMode, SupportsOpenAIGrammarTools: supportsGrammar}))
}
