package harness

// Code generated from the encoding/json v1 decoders as they were before the jsontext conversion; the differential fuzz targets compare against it.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/MichaelKinsy/PiG/ai"
)

func callArgumentsV1(call ai.ToolCall) string {
	encoded, err := call.ArgumentsJSON()
	if err != nil {
		return ""
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return ""
	}
	var pairs []string
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return strings.Join(pairs, ", ")
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return strings.Join(pairs, ", ")
		}
		pairs = append(pairs, fmt.Sprint(key)+"="+jsStringEscapes(value))
	}
	return strings.Join(pairs, ", ")
}
