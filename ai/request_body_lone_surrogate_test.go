package ai

import "testing"

// JSON.stringify writes a lone UTF-16 surrogate as its escape. Pi sends strings it does not sanitize (the Codex instructions, a reasoning field) that way;
// encoding/json writes U+FFFD for each byte of the WTF-8 form a Go string holds, so the request body is encoded with the JSON.stringify encoder.
func TestRequestBodyWritesALoneSurrogateAsItsEscape(t *testing.T) {
	lone := "a\xed\xa0\xbdb"
	reasoning := lone
	for name, test := range map[string]struct {
		body any
		want string
	}{
		"codex instructions":     {respRequest{Model: "m", Instructions: lone, codex: true}, `{"model":"m","stream":false,"instructions":"a\ud83db","input":null}`},
		"responses instructions": {respRequest{Model: "m", Instructions: lone}, ``},
		"assistant reasoning":    {oaiMessage{Role: "assistant", ReasoningContent: &reasoning}, `{"role":"assistant","content":null,"reasoning_content":"a\ud83db"}`},
		"pair is one character":  {oaiMessage{Role: "user", Content: "😀 \u2028 <&>"}, "{\"role\":\"user\",\"content\":\"😀 \u2028 <&>\"}"},
	} {
		got, err := marshalRequestBody(test.body)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if test.want != "" && string(got) != test.want {
			t.Errorf("%s: body = %s, want %s", name, got, test.want)
		}
		if test.want == "" && !containsEscape(string(got)) {
			t.Errorf("%s: body = %s, want the escape \\ud83d", name, got)
		}
	}
}

func containsEscape(body string) bool {
	for i := 0; i+6 <= len(body); i++ {
		if body[i:i+6] == `\ud83d` {
			return true
		}
	}
	return false
}
