package ai

import (
	"encoding/json"

	sdkjson "github.com/MichaelKinsy/PiG/extensions/sdk/json"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// unmarshalKeepingSurrogates is json.Unmarshal for a record of a provider's stream. JSON.parse keeps a lone UTF-16 surrogate, which the name of a
// called tool can hold (the agent loop finds the tool by that name), and encoding/json reads one as U+FFFD: a record that holds the escape of a
// surrogate is read by the decoder that keeps the unit. A record that decoder refuses is read by encoding/json, so its error is the one callers
// inspect; target is then written twice, with the same members.
func unmarshalKeepingSurrogates(text []byte, target any) error {
	if jsstring.HasSurrogateEscape(text) && sdkjson.Unmarshal(text, target) == nil {
		return nil
	}
	return json.Unmarshal(text, target)
}
