package extension

import "encoding/json"

// FindToolInput is upstream's FindToolInput (find.ts findSchema).
type FindToolInput struct {
	Pattern string   `json:"pattern"`
	Path    *string  `json:"path,omitempty"`
	Limit   *float64 `json:"limit,omitempty"`
	// Extra holds members the schema does not declare; upstream's input is a plain object that keeps them.
	Extra map[string]json.RawMessage `json:"-"`
}
