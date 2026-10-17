package agent

import (
	"testing"

	json "github.com/MichaelKinsy/PiG/extensions/sdk/json"
	"github.com/MichaelKinsy/PiG/internal/jsonstringify"
)

// A member Pi wrote with a value PiG's field omits (an empty optional string, redacted: false, an empty text signature) is part of the object
// JSON.stringify writes back, so a message holding one writes its decoded text; the member scan must not accept it as PiG's own encoding.
func TestDecodedMessageKeepsMembersTheFieldsOmit(t *testing.T) {
	usage := `"usage":{"input":1,"output":1,"cacheRead":0,"cacheWrite":0,"totalTokens":2,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}}`
	for _, text := range []string{
		`{"role":"assistant","content":[],"api":"a","provider":"p","model":"m",` + usage + `,"stopReason":"stop","timestamp":1,"responseModel":""}`,
		`{"role":"assistant","content":[],"api":"a","provider":"p","model":"m",` + usage + `,"stopReason":"stop","timestamp":1,"responseId":""}`,
		`{"role":"assistant","content":[],"api":"a","provider":"p","model":"m",` + usage + `,"stopReason":"stop","timestamp":1,"errorMessage":""}`,
		`{"role":"assistant","content":[{"type":"thinking","thinking":"t","redacted":false}],"api":"a","provider":"p","model":"m",` + usage + `,"stopReason":"stop","timestamp":1}`,
		`{"role":"assistant","content":[{"type":"text","text":"t","textSignature":""}],"api":"a","provider":"p","model":"m",` + usage + `,"stopReason":"stop","timestamp":1}`,
	} {
		var message AgentMessage
		if err := json.Unmarshal([]byte(text), &message); err != nil {
			t.Fatal(err)
		}
		encoded, err := message.MarshalJSON()
		if err != nil {
			t.Fatal(err)
		}
		got, err := jsonstringify.Canonicalize(encoded)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != text {
			t.Errorf("wrote\n %s\nwant\n %s", got, text)
		}
	}
}
