package contracts

import "math"

// Ports packages/chord/src/contracts/message.ts (a message as data: its namespace, key and parameters, which each
// client renders in its own language, its schema, and the check of one plugin code hands Chord). Declared here, below
// the data layer, because a contract holds messages too: a flag's label is one; a denial carries one.

// MessageParam is a parameter of a message as data: plain JSON (string, float64, bool, or nil), so it crosses links.
type MessageParam = any

// MessageData is a message as data: its namespace, its key and its parameters; plain JSON (strings, numbers, booleans,
// nil), so it crosses links.
type MessageData struct {
	Namespace string `json:"namespace"`
	Key       string `json:"key"`
	Params    []any  `json:"params"`
}

// MessageSchema is the schema of a message as data, for contract members.
var MessageSchema = Obj(
	Prop("namespace", Str()),
	Prop("key", Str()),
	Prop("params", Arr(Union([]Schema{
		Str(), Num(), Bool(), Nul(),
	}))),
)

// IsMessageData reports whether value is a valid MessageData: a non-nil Params slice whose elements are each a string,
// a finite float64 (or int), a bool, or nil.
func IsMessageData(value any) bool {
	var msg MessageData
	switch v := value.(type) {
	case MessageData:
		msg = v
	case *MessageData:
		if v == nil {
			return false
		}
		msg = *v
	default:
		return false
	}
	if msg.Params == nil {
		return false
	}
	for _, param := range msg.Params {
		switch p := param.(type) {
		case nil, string, bool:
		case float64:
			if math.IsNaN(p) || math.IsInf(p, 0) {
				return false
			}
		case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		default:
			return false
		}
	}
	return true
}
