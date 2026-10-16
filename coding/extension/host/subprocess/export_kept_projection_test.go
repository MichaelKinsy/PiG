package subprocess

import (
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// EncodeKeptProjectionForTest exposes encodeKeptProjection to the external test package, which can import internal/codingagent.
var EncodeKeptProjectionForTest = encodeKeptProjection

// RaceEnabledForTest tells the external test package whether the race detector instruments this binary, which adds allocations.
const RaceEnabledForTest = raceEnabled

// MarshalProjectionForTest is the encoding of the projected part of preview with each source as its record, as json.Marshal writes it whole.
func MarshalProjectionForTest(sources []json.RawMessage, preview extension.BoundaryContextPreview) ([]byte, error) {
	return json.Marshal(boundaryProjectedWire[projectedEntryWire]{projectedEntries(sources, preview), preview.ContextMessages, preview.LLMMessages})
}
