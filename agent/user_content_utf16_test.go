package agent

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

func TestUserMessageJSONPreservesUTF16Units(t *testing.T) {
	// Pi session-manager.ts:439-451 retains valid content parsed from a Session file, including unpaired UTF-16 units.
	for _, tc := range []struct {
		name, input, output string
		units               []uint16
	}{
		{"empty", `""`, `""`, []uint16{}},
		{"lone-high", `"a\ud800b"`, `"a\ud800b"`, []uint16{'a', 0xd800, 'b'}},
		{"lone-low", `"a\udfffb"`, `"a\udfffb"`, []uint16{'a', 0xdfff, 'b'}},
		{"two-high", `"\ud800\ud800"`, `"\ud800\ud800"`, []uint16{0xd800, 0xd800}},
		{"pair", `"\ud83d\ude00"`, `"😀"`, []uint16{0xd83d, 0xde00}},
		{"scalar-replacement", `"a�b"`, `"a�b"`, []uint16{'a', 0xfffd, 'b'}},
		{"literal-escape", `"\\ud800"`, `"\\ud800"`, []uint16{'\\', 'u', 'd', '8', '0', '0'}},
	} {
		for _, blocks := range []bool{false, true} {
			name := tc.name + "/string"
			input, want := tc.input, tc.output
			if blocks {
				name = tc.name + "/blocks"
				input = `[{"type":"text","text":` + input + `}]`
				want = `[{"type":"text","text":` + want + `}]`
			}
			t.Run(name, func(t *testing.T) {
				var message AgentMessage
				if err := json.Unmarshal([]byte(`{"role":"user","content":`+input+`,"timestamp":123}`), &message); err != nil {
					t.Fatal(err)
				}
				var text string
				switch content := message.User.Content.(type) {
				case ai.UserText:
					text = string(content)
				case ai.UserContentBlocks:
					text = content[0].(ai.TextContent).Text
				default:
					t.Fatalf("unexpected content %T", content)
				}
				if got := jsstring.ToUTF16(text); !reflect.DeepEqual(got, tc.units) {
					t.Errorf("units=%04x, want %04x", got, tc.units)
				}
				encoded, err := json.Marshal(message)
				if err != nil {
					t.Fatal(err)
				}
				var fields struct{ Content json.RawMessage }
				if err := json.Unmarshal(encoded, &fields); err != nil {
					t.Fatal(err)
				}
				if string(fields.Content) != want {
					t.Errorf("content=%s, want %s", fields.Content, want)
				}
			})
		}
	}
}
