package subprocess

import (
	jsonv2 "encoding/json/v2"

	json "github.com/MichaelKinsy/PiG/extensions/sdk/json"
	"github.com/MichaelKinsy/PiG/internal/jsonopts"
	"github.com/MichaelKinsy/PiG/internal/jsonwire"
)

// decodeEnvelope decodes one protocol frame into env, which is empty. encoding/json/v2 reads the frame under the JSON.parse options in one pass; the
// sdk decoder reads the frames v2 cannot give the same Envelope for or fails on, so each frame decodes as it always has and a frame that does not
// decode reports the sdk decoder's error:
//   - a lone UTF-16 surrogate escape in a string, which the sdk decoder keeps and v2 replaces with U+FFFD;
//   - any error of v2, whose text the sdk decoder words differently.
//
// A member name matches exactly under v2, where the sdk decoder also matched it case-insensitively; every SDK writes the protocol's own spelling.
func decodeEnvelope(data []byte, env *Envelope) error {
	if !jsonwire.HasLoneSurrogateEscape(data) {
		if jsonv2.Unmarshal(data, env, jsonopts.Parse) == nil {
			return nil
		}
		*env = Envelope{}
	}
	return json.Unmarshal(data, env)
}
