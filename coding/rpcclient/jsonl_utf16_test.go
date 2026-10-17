package rpcclient

// pi: packages/coding-agent/src/modes/rpc/jsonl.ts

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// JSON.stringify preserves unmatched UTF-16 units in ordinary response fields, not only values with custom marshalers.
func TestSerializeJsonLinePreservesUTF16Strings(t *testing.T) {
	for _, tc := range []struct {
		name  string
		units []uint16
		want  string
	}{
		{"empty", nil, `""`},
		{"ordinary", []uint16{'a', 'b'}, `"ab"`},
		{"high", []uint16{'a', 0xd800, 'b'}, `"a\ud800b"`},
		{"low", []uint16{'a', 0xdfff, 'b'}, `"a\udfffb"`},
		{"pair", []uint16{0xd83d, 0xde00}, `"😀"`},
		{"replacement", []uint16{'a', 0xfffd, 'b'}, `"a�b"`},
		{"literal escape", []uint16{'\\', 'u', 'd', '8', '0', '0'}, `"\\ud800"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			value := struct {
				ID   string   `json:"id"`
				Text []string `json:"text"`
			}{ID: jsstring.FromUTF16(tc.units), Text: []string{jsstring.FromUTF16(tc.units)}}
			got, err := SerializeJsonLine(value)
			want := `{"id":` + tc.want + `,"text":[` + tc.want + "]}\n"
			if err != nil || string(got) != want {
				t.Fatalf("frame=%s err=%v, want %s", got, err, want)
			}
		})
	}
}

func BenchmarkSerializeJsonLineUTF16(b *testing.B) {
	for _, tc := range []struct{ name, chunk string }{
		{"ordinary", "hello é😀"},
		{"unmatched units", "hello " + jsstring.FromUTF16([]uint16{0xd800, 'x', 0xdfff})},
	} {
		b.Run(tc.name, func(b *testing.B) {
			value := struct {
				Type    string `json:"type"`
				Message string `json:"message"`
			}{Type: "steer", Message: strings.Repeat(tc.chunk, 128)}
			b.SetBytes(int64(len(value.Message)))
			b.ReportAllocs()
			for b.Loop() {
				encoded, err := SerializeJsonLine(value)
				if err != nil || len(encoded) == 0 || encoded[len(encoded)-1] != '\n' {
					b.Fatalf("frame=%q err=%v", encoded, err)
				}
			}
		})
	}
}
