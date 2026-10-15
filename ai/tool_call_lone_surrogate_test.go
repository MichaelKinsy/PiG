package ai

import "testing"

// A lone surrogate reaches the arguments text in internal/jsstring's WTF-8 form. Pi's JSON.parse keeps it as one UTF-16
// unit and JSON.stringify writes it as \ud83d (Node 24: JSON.stringify(JSON.parse('"\ud83d"')) is "\ud83d"), in a
// top-level string, an array and an object alike. Found by hunt-ai-wire.
func TestToolCallArgumentsKeepALoneSurrogate(t *testing.T) {
	lone := string([]byte{0xed, 0xa0, 0xbd}) // U+D83D in WTF-8
	for in, want := range map[string]string{
		`"` + lone + `"`:       `"\ud83d"`,
		`["` + lone + `"]`:     `["\ud83d"]`,
		`{"k":"` + lone + `"}`: `{"k":"\ud83d"}`,
	} {
		var call ToolCall
		call.SetStreamingArguments(in)
		if got, _ := call.ArgumentsJSON(); string(got) != want {
			t.Errorf("%q: arguments %s, want %s", in, got, want)
		}
	}
}
