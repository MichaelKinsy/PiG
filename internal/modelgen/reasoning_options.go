package modelgen

// Ports packages/ai/scripts/models-dev-reasoning-options.ts
// Ports packages/ai/scripts/openrouter-reasoning-options.ts

// ModelsDevReasoningOption describes a raw models.dev reasoning control.
type ModelsDevReasoningOption struct {
	Type   string    `json:"type"`
	Values []*string `json:"values,omitempty"`
	Min    *float64  `json:"min,omitempty"`
	Max    *float64  `json:"max,omitempty"`
}

var thinkingLevels = [...]string{"minimal", "low", "medium", "high", "xhigh", "max"}

// GetEffortThinkingLevelMap exposes only verified efforts, preserving explicit null for unavailable levels. Toggle, budget, default and null values imply no selectable effort.
func GetEffortThinkingLevelMap(options []ModelsDevReasoningOption) map[string]*string {
	supported := map[string]bool{}
	for _, option := range options {
		if option.Type == "effort" {
			for _, value := range option.Values {
				if value != nil {
					supported[*value] = true
				}
			}
		}
	}
	known := supported["none"]
	for _, level := range thinkingLevels {
		known = known || supported[level]
	}
	if !known {
		return nil
	}
	result := map[string]*string{"off": nil}
	if supported["none"] {
		result["off"] = new("none")
	}
	for _, level := range thinkingLevels {
		result[level] = nil
		if supported[level] {
			result[level] = new(level)
		}
	}
	return result
}

// OpenRouterReasoningMetadata is the vendor's reasoning control metadata.
type OpenRouterReasoningMetadata struct {
	Mandatory        bool      `json:"mandatory,omitempty"`
	DefaultEnabled   bool      `json:"default_enabled,omitempty"`
	SupportedEfforts []*string `json:"supported_efforts,omitempty"`
	DefaultEffort    *string   `json:"default_effort,omitempty"`
}

// GetOpenRouterThinkingLevelMap converts vendor capabilities without inferring a request effort from the vendor default.
func GetOpenRouterThinkingLevelMap(reasoning *OpenRouterReasoningMetadata) map[string]*string {
	if reasoning == nil {
		return nil
	}
	result := GetEffortThinkingLevelMap([]ModelsDevReasoningOption{{Type: "effort", Values: reasoning.SupportedEfforts}})
	if result == nil {
		if reasoning.Mandatory {
			return map[string]*string{"off": nil}
		}
		return nil
	}
	result["off"] = new("none")
	if reasoning.Mandatory {
		result["off"] = nil
	}
	return result
}
