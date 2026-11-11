package modelgen

// Ports packages/ai/catalog/reasoning.ts

// ModelsDevReasoningOption describes a raw models.dev reasoning control: a toggle, effort values (a value is null in JSON when
// models.dev lists no verified value) or a token budget.
type ModelsDevReasoningOption struct {
	Type   string    `json:"type"`
	Values []*string `json:"values,omitempty"`
	Min    *float64  `json:"min,omitempty"`
	Max    *float64  `json:"max,omitempty"`
}

var thinkingLevels = [...]string{"minimal", "low", "medium", "high", "xhigh", "max"}

// GetEffortThinkingLevelMap converts models.dev verified effort values into Pi's selectable thinking levels. Values without a Pi
// equivalent (`default` and JSON `null`) are intentionally omitted. The map is nil when no level is verified: toggle, budget,
// default and null values imply no selectable effort.
func GetEffortThinkingLevelMap(options []ModelsDevReasoningOption) *Obj {
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
	if len(supported) == 0 {
		return nil
	}
	known := supported["none"]
	for _, level := range thinkingLevels {
		known = known || supported[level]
	}
	if !known {
		return nil
	}
	result := NewObj()
	if supported["none"] {
		result.Set("off", "none")
	} else {
		result.Set("off", nil)
	}
	for _, level := range thinkingLevels {
		if supported[level] {
			result.Set(level, level)
		} else {
			result.Set(level, nil)
		}
	}
	return result
}
