package subprocess

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// providerPayload is a before_provider_request-shaped JSON value of about size bytes: a conversation of messages with 4 KB text blocks
// (escapes, quotes, multi-byte characters) and an image block of base64 data per 128 messages.
func providerPayload(size int) []byte {
	text := strings.Repeat("The quick brown fox \"jumps\"\n\tover the lazy dög — 日本語, c:\\dir\\file; ", 64)[:4096]
	quoted := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\t", `\t`).Replace(text)
	image := strings.Repeat("iVBORw0KGgoAAAANSUhEUgAAALQAAAC0CAIAAACyr5FlAAAAA3NCSVQICAjb4U/gAAAgAElEQVR4nOy9", 1300)[:97<<10]
	var b strings.Builder
	b.WriteString(`{"type":"before_provider_request","payload":{"model":"pf-1","stream":true,"messages":[`)
	for i := 0; b.Len() < size; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		fmt.Fprintf(&b, `{"role":%q,"content":[{"type":"text","text":"%s"}],"n":%d,"ok":true,"x":null}`, role, quoted, i)
		if i%128 == 127 {
			fmt.Fprintf(&b, `,{"role":"user","content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,%s"}}]}`, image)
		}
	}
	b.WriteString(`]},"extra":[1,2.5,-3e2]}`)
	return []byte(b.String())
}

// BenchmarkReadFrameLargePayload is the host's read of one frame an extension sent with a large payload (a call whose arguments, or a
// response whose result, are a provider request): the frame read and the envelope decoded, from 64 KiB to 43 MiB.
func BenchmarkReadFrameLargePayload(b *testing.B) {
	for _, size := range []int{64 << 10, 1 << 20, 16 << 20, 43 << 20} {
		name := fmt.Sprintf("%dKiB", size>>10)
		if size >= 1<<20 {
			name = fmt.Sprintf("%dMiB", size>>20)
		}
		payload := providerPayload(size)
		body, err := json.Marshal(Envelope{Type: MsgResponse, ID: "id", Response: &ResponsePayload{Result: payload}})
		if err != nil {
			b.Fatal(err)
		}
		wire := binary.BigEndian.AppendUint32(nil, uint32(len(body)))
		wire = append(wire, body...)
		b.Run(name, func(b *testing.B) {
			b.SetBytes(int64(len(body)))
			b.ReportAllocs()
			for b.Loop() {
				frame, err := readFrameBuffer(bytes.NewReader(wire[4:]), len(body))
				if err != nil {
					b.Fatal(err)
				}
				var env Envelope
				err = json.Unmarshal(frame.data, &env)
				frame.release()
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
