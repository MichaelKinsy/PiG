package host

import (
	"testing"

	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

// BenchmarkIsFrame is the envelope check every received frame passes (wire.ts isFrame): the cost per message of the
// frames a busy link carries (a call, its result, a state snapshot, ops, an ack, the hold that opens a reference).
func BenchmarkIsFrame(b *testing.B) {
	hold := `{"t":"hold","id":3,"at":"session:S1","token":{"name":"t.counter","scope":"remote","version":1,"input":{"type":"object","properties":{"name":{"type":"string"}},"required":["name"]},"host":{"name":"session:S1"},"variants":{"main":{"count":{"kind":"state","schema":{"type":"object","properties":{"n":{"type":"number"}},"required":["n"]}},"add":{"kind":"method","args":{"type":"number"},"result":{"type":"number"}}}}},"input":{"name":"a"}}`
	for _, frame := range []struct{ name, text string }{
		{"call", `{"t":"call","id":7,"hold":3,"member":"add","args":1,"root":{"rootId":"r#1","codeVersion":1}}`},
		{"result", `{"t":"result","id":7,"value":{"n":4}}`},
		{"snapshot", `{"t":"state","hold":3,"m":"count","as":"snap","value":{"n":4,"items":["a","b","c"]}}`},
		{"ops", `{"t":"state","hold":3,"m":"count","as":"ops","ops":[["s",["n"],5],["a",["items"],"d"]]}`},
		{"ack", `{"t":"ack","n":12}`},
		{"hold", hold},
	} {
		value, err := chordjson.Decode([]byte(frame.text))
		if err != nil {
			b.Fatal(err)
		}
		b.Run(frame.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if !IsFrame(value) {
					b.Fatal("not a frame")
				}
			}
		})
	}
}
