package main

import "github.com/MichaelKinsy/PiG/internal/modelgen"

type modelsDevReasoningOption = modelgen.ModelsDevReasoningOption

func getEffortThinkingLevelMap(options []modelsDevReasoningOption) map[string]*string {
	return modelgen.GetEffortThinkingLevelMap(options)
}
