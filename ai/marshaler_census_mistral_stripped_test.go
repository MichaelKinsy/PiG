//go:build pig_strip_mistral_conversations

package ai

// The Mistral receivers of the marshaler census in a build that strips mistral-conversations: the census parses every source file whatever its
// build tags, so their methods are still found, and no value of them exists to probe.
const censusStrippedMistral = "a request type of mistral-conversations, which this build strips (marshaler_census_mistral_test.go probes it)"

func init() {
	marshalerCensus["ai.mistralToolFn"] = censusRow{other: censusStrippedMistral}
	marshalerCensus["ai.mistralWireMessages"] = censusRow{other: censusStrippedMistral}
}
