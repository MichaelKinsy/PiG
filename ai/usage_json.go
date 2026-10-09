package ai

import "encoding/json"

// MarshalJSON emits the upstream Usage wire shape.
func (usage Usage) MarshalJSON() ([]byte, error) {
	type wire struct {
		Input        int       `json:"input"`
		Output       int       `json:"output"`
		CacheRead    int       `json:"cacheRead"`
		CacheWrite   int       `json:"cacheWrite"`
		CacheWrite1h *int      `json:"cacheWrite1h,omitempty"`
		Reasoning    *int      `json:"reasoning,omitempty"`
		TotalTokens  int       `json:"totalTokens"`
		Cost         UsageCost `json:"cost"`
	}
	value := wire(usage)
	return json.Marshal(value)
}

// UnmarshalJSON decodes the upstream Usage wire shape.
func (usage *Usage) UnmarshalJSON(data []byte) error {
	type wire struct {
		Input        int       `json:"input"`
		Output       int       `json:"output"`
		CacheRead    int       `json:"cacheRead"`
		CacheWrite   int       `json:"cacheWrite"`
		CacheWrite1h *int      `json:"cacheWrite1h"`
		Reasoning    *int      `json:"reasoning"`
		TotalTokens  int       `json:"totalTokens"`
		Cost         UsageCost `json:"cost"`
	}
	var value wire
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	*usage = Usage(value)
	return nil
}
