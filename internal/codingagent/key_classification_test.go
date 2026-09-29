package codingagent

// classifyKey supplies default bindings to the default-key fixture tables.
func classifyKey(data string) keyAction {
	return classifyKeyWithBindings(data, DefaultKeybindingsManager())
}
