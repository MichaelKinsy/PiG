package emptydrop

import "encoding/json"

type Capabilities struct {
	Tools   []string        `json:"tools,omitempty"` // want `field Tools: omitempty drops an explicit empty \[\]string`
	Extra   map[string]any  `json:"extra,omitempty"` // want `field Extra: omitempty drops`
	Name    string          `json:"name,omitempty"`
	Keep    []string        `json:"keep"`
	Skipped []string        `json:"-,omitempty"`
	Raw     json.RawMessage `json:"raw,omitempty"` // an encoded value keeps its explicit [] or {}
}
