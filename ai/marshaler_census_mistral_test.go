//go:build !pig_strip_mistral_conversations

package ai

// The Mistral receivers of the marshaler census, with the probes of a build that links mistral-conversations.
func init() {
	marshalerCensus["ai.mistralToolFn"] = censusRow{probe: func() any {
		return mistralToolFn{Name: loneSurrogateProbe, Description: loneSurrogateProbe, Parameters: censusProbeSchema()}
	}, sameUnderEncodingJSON: true}
	marshalerCensus["ai.mistralWireMessages"] = censusRow{probe: func() any {
		return mistralWireMessages{{Role: "tool", Name: loneSurrogateProbe, Content: "r", ToolCallID: "call00001"}}
	}}
}
