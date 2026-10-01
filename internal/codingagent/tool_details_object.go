package codingagent

import "github.com/MichaelKinsy/PiG/internal/orderedjson"

// detailsObject returns a tool result's `details` with an object held as raw JSON (what an extension process supplies, or a session file holds, so its members keep their order) decoded into a map. Every other value is returned as it is.
func detailsObject(details any) any {
	if object, ok := orderedjson.Map(details); ok {
		return object
	}
	return details
}
