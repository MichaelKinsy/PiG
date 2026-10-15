package jsonvalid

import (
	"fmt"
	"strings"
	"testing"

	sdkjson "github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// sessionLines builds lines of the shape a long coding session holds: a tool result whose text is source code with escaped newlines and quotes, an assistant turn with thinking, text and a tool call, and a small custom entry.
func sessionLines() [][]byte {
	code := strings.Repeat("func main() {\n\tfmt.Println(\"hello, \\\"world\\\"\")\n}\n// héllo 日本語\n", 700)
	tool := fmt.Sprintf(`{"type":"message","id":"a1b2c3d4","parentId":"e5f6a7b8","timestamp":"2026-01-01T00:00:00.000Z","message":{"role":"toolResult","toolCallId":"c1","toolName":"read","content":[{"type":"text","text":%q}],"isError":false,"timestamp":1767225600000}}`, code)
	assistant := fmt.Sprintf(`{"type":"message","id":"a1b2c3d5","parentId":"a1b2c3d4","timestamp":"2026-01-01T00:00:01.000Z","message":{"role":"assistant","content":[{"type":"thinking","thinking":%q,"thinkingSignature":"%s"},{"type":"text","text":"Done."},{"type":"toolCall","id":"c2","name":"edit","arguments":{"path":"/a/b.go","edits":[{"oldText":%q,"newText":%q}]}}],"api":"anthropic-messages","provider":"anthropic","model":"m","usage":{"input":1200,"output":340,"cacheRead":90000,"cacheWrite":0,"totalTokens":91540,"cost":{"input":0.0036,"output":0.0051,"cacheRead":0.027,"cacheWrite":0,"total":0.0357}},"stopReason":"toolUse","timestamp":1767225601000}}`, strings.Repeat("reasoning about the change ", 200), strings.Repeat("A", 4000), code[:2000], code[:2100])
	small := `{"type":"custom","id":"a1b2c3d6","parentId":"a1b2c3d5","timestamp":"2026-01-01T00:00:02.000Z","customType":"x","data":{"n":1,"ok":true,"list":[1,2,3]}}`
	return [][]byte{[]byte(tool), []byte(assistant), []byte(small)}
}

func BenchmarkValid(b *testing.B) {
	lines := sessionLines()
	total := 0
	for _, l := range lines {
		total += len(l)
	}
	for _, c := range []struct {
		name string
		f    func([]byte) bool
	}{{"scanner", sdkjson.Valid}, {"jsonvalid", Valid}} {
		b.Run(c.name, func(b *testing.B) {
			b.SetBytes(int64(total))
			b.ReportAllocs()
			for b.Loop() {
				for _, l := range lines {
					if !c.f(l) {
						b.Fatal("invalid")
					}
				}
			}
		})
	}
}
