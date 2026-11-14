package extension

import "testing"

// pi: runner.ts emitInput takes the text an `input` handler returns as the string it is, and agent-session.ts prompts with it. An extension
// in another process sends that string as JSON, and a lone UTF-16 surrogate arrives as its escape: the decode keeps the unit, as JSON.parse
// does, so the user message of the session, the RPC lines and the request hold what Pi's hold.
func TestInputEventResultKeepsALoneSurrogate(t *testing.T) {
	result, err := UnmarshalInputEventResult([]byte(`{"action":"transform","text":"UM[\ud83d] IN[\ud83d]","images":[{"type":"image","data":"D[\ud83d]","mimeType":"image/png"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	const unit = "\xed\xa0\xbd"
	transform, ok := result.(InputEventResultTransform)
	if !ok || transform.Text != "UM["+unit+"] IN["+unit+"]" {
		t.Fatalf("the result = %#v, want the text with both units", result)
	}
	if len(transform.Images) != 1 || transform.Images[0].Data != "D["+unit+"]" {
		t.Errorf("the images = %#v, want the data with the unit", transform.Images)
	}
}
