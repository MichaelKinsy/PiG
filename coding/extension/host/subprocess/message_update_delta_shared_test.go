package subprocess

import (
	"encoding/json"
	"math/rand/v2"
	"reflect"
	"strings"
	"testing"
	"unsafe"

	"github.com/MichaelKinsy/PiG/ai"
)

// unsafeView is the string the bytes of buffer hold now, sharing them as a streamed text shares its accumulator's.
func unsafeView(buffer []byte) string {
	return unsafe.String(unsafe.SliceData(buffer), len(buffer)) //nolint:gosec // G103: the test shares a streamed text's accumulator bytes the way the production sender does
}

// The UTF-8 check and the prefix comparison of a message_update delta skip the bytes a streamed text shares with the previous payload's (validUTF8, extends). A runtime that applies the deltas of texts grown in place, in a buffer that also reallocates, cut inside code points, rewritten from a fresh buffer or cut back to a prefix of the same bytes, holds after every event the object the whole payload decodes to, and an event that carries an ill-formed string still travels whole.
func TestMessageUpdateDeltaOfSharedBytesEqualsTheWholePayload(t *testing.T) {
	base := ai.AssistantMessage{API: "openai-completions", Provider: "p", Model: "m", Timestamp: 1700000000000, StopReason: ai.StopReasonStop}
	pieces := []string{"word ", "é", "ö✓", "漢字", "🙂", "\xed\xa0\x80", "x"}
	whole, appends := 0, 0
	for seed := range uint64(12) {
		random := rand.New(rand.NewPCG(seed, 99))
		stream := &messageUpdateStream{}
		conn := &Conn{}
		var held any
		var heldSeq uint64
		var text, args []byte
		// rest holds the bytes of a code point a delta cut, which the buffer's next delta completes.
		rests := map[*[]byte]string{}
		grow := func(buffer *[]byte, surrogates bool) {
			if rest := rests[buffer]; rest != "" {
				*buffer = append(*buffer, rest...)
				delete(rests, buffer)
				return
			}
			piece := pieces[random.IntN(len(pieces))]
			if piece == "\xed\xa0\x80" && (!surrogates || random.IntN(10) != 0) {
				piece = "y"
			}
			// Now and then a delta ends inside a code point, and the text is ill-formed until the next one.
			if cut := random.IntN(len(piece) + 1); random.IntN(4) == 0 {
				piece, rests[buffer] = piece[:cut], piece[cut:]
			}
			*buffer = append(*buffer, piece...)
		}
		for step := range 300 {
			switch roll := random.IntN(40); {
			case roll == 0:
				// A text rewritten from a fresh buffer shares no bytes with the previous one.
				text = append([]byte(nil), strings.Repeat("fresh ", 20+random.IntN(10))...)
				delete(rests, &text)
			case roll == 1 && len(text) > 80:
				// Cut back to a prefix of the same bytes. Its capacity ends there, so the next growth moves to a new buffer and the bytes of the longer text stay as they were.
				cut := len(text) - 1 - random.IntN(20)
				text = text[:cut:cut]
				delete(rests, &text)
			default:
				for range 1 + random.IntN(3) {
					grow(&text, true)
				}
			}
			if random.IntN(2) == 0 {
				grow(&args, false)
			}
			// The strings are views of the buffers: a later append in place extends the bytes they cover without changing them.
			message := base
			message.Content = []ai.AssistantContentBlock{
				ai.TextContent{Text: unsafeView(text)},
				toolCallWithArguments("call_1", "write", `{"path": "a.txt", "content": "`+unsafeView(args)),
			}
			event := assistantEvent(message, func(p *ai.AssistantMessage) any { return ai.TextDeltaEvent{ContentIndex: 0, Delta: "d", Partial: p} })
			encoded, err := json.Marshal(event)
			if err != nil {
				t.Fatal(err)
			}
			prepared, inStream, err := stream.prepare(conn, event)
			if err != nil {
				t.Fatal(err)
			}
			if !inStream {
				whole++
				continue
			}
			if prepared.args != nil {
				held = decodeOrdered(t, prepared.args)
			} else {
				if prepared.stream.Base != heldSeq {
					t.Fatalf("seed %d step %d: patch applies to %d, runtime holds %d", seed, step, prepared.stream.Base, heldSeq)
				}
				if strings.Contains(string(prepared.stream.Ops), `"op":"append"`) {
					appends++
				}
				held = applyOps(t, held, prepared.stream.Ops)
			}
			heldSeq = prepared.stream.Seq
			stream.sent(conn, prepared, nil)
			if want := decodeOrdered(t, encoded); !reflect.DeepEqual(held, want) {
				t.Fatalf("seed %d step %d: the runtime's object differs from the whole payload\nops: %s\nheld: %s\nwant: %s", seed, step, prepared.stream.Ops, dumpOrdered(held), dumpOrdered(want))
			}
		}
	}
	if whole == 0 || appends < 1000 {
		t.Fatalf("the streams must exercise appends and payloads sent whole: %d appends, %d whole", appends, whole)
	}
}
