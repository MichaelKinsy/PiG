package codingagent

import (
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

// The write card names its file from the path arguments alone, so the header reads the same from them as from the whole object: both path keys, a null or missing one, a wrong type, and arguments that are not an object.
func TestWriteHeaderFromThePathArgumentsEqualsTheHeaderFromTheWholeArguments(t *testing.T) {
	for _, raw := range []string{
		`{"path":"a/b.go","content":"x"}`,
		`{"file_path":"/abs/c.go","path":"ignored","content":"x"}`,
		`{"file_path":null,"path":"d.go","content":"x"}`,
		`{"file_path":"","path":"e.go"}`,
		`{"path":5,"content":"x"}`,
		`{"file_path":{"a":1},"content":"x"}`,
		`{"path":["a"]}`,
		`{"path":"é\u2028\u0000.go","content":"x"}`,
		`{"content":"only"}`,
		`{}`,
		`[1,2]`,
		`"text"`,
		`null`,
		`{"path":"unterminated`,
		``,
	} {
		for _, cwd := range []string{"", "/work"} {
			want := tui.FormatWriteHeader(json.RawMessage(raw), cwd)
			got := tui.FormatWriteHeader(pathRenderArgs(decodeRenderArgs(json.RawMessage(raw))), cwd)
			if got != want {
				t.Errorf("arguments %s, cwd %q: header %q, want %q", raw, cwd, got, want)
			}
		}
	}
}
