package sdk

import (
	"fmt"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// contextArgs is a context event with count messages: a user message, an assistant message with a tool call and a text block, and the tool's result, in turn.
func contextArgs(count int) string {
	var b strings.Builder
	b.WriteString(`{"type":"context","messages":[`)
	for i := range count {
		if i > 0 {
			b.WriteByte(',')
		}
		switch i % 3 {
		case 0:
			fmt.Fprintf(&b, `{"role":"user","content":"question %d about the files in the repository and what they hold","timestamp":%d}`, i, i)
		case 1:
			fmt.Fprintf(&b, `{"role":"assistant","content":[{"type":"text","text":"I will read it"},{"type":"toolCall","id":"c%d","name":"read","arguments":{"path":"src/file%d.go","offset":%d,"limit":200}}],"api":"openai-completions","provider":"faux","model":"m","usage":{"input":1,"output":2,"cacheRead":0,"cacheWrite":0,"totalTokens":3,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"stopReason":"toolUse","timestamp":%d}`, i, i, i, i)
		default:
			fmt.Fprintf(&b, `{"role":"toolResult","toolCallId":"c%d","toolName":"read","content":[{"type":"text","text":"package main\n\nfunc main() {}\n"}],"isError":false,"timestamp":%d}`, i-1, i)
		}
	}
	b.WriteString(`]}`)
	return b.String()
}

// BenchmarkHandedBackReply compares the reply a handler that drops a context message, or hands back a provider payload, gets: written from the decoded maps in
// byte order (what the SDK did) and written in the member order the host sent.
func BenchmarkHandedBackReply(b *testing.B) {
	for _, count := range []int{30, 300, 3000} {
		args := []byte(contextArgs(count))
		decode := func() map[string]any {
			var data map[string]any
			if err := json.Unmarshal(args, &data); err != nil {
				b.Fatal(err)
			}
			return data
		}
		data := decode()
		snapshot := append([]any{}, data["messages"].([]any)...)
		reply := map[string]any{"messages": data["messages"].([]any)[1:], "_pigContextUnchanged": false}
		roots := captureEchoRoots("context", data, snapshot)
		b.Run(fmt.Sprintf("context-%d/byte-order", count), func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(args)))
			for b.Loop() {
				if _, err := json.MarshalJS(reply); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run(fmt.Sprintf("context-%d/member-order", count), func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(args)))
			for b.Loop() {
				out := roots.reply(reply, args).(orderedReply)
				if _, err := json.MarshalJSOrdered(out.value, out.order); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
	// The payload of a provider request holds the same messages in a request.
	for _, count := range []int{300} {
		args := []byte(`{"type":"before_provider_request","payload":{"model":"m","messages":` + strings.TrimSuffix(strings.TrimPrefix(contextArgs(count), `{"type":"context","messages":`), `}`) + `,"stream":true}}`)
		var data map[string]any
		if err := json.Unmarshal(args, &data); err != nil {
			b.Fatal(err)
		}
		roots := captureEchoRoots("before_provider_request", data, nil)
		b.Run(fmt.Sprintf("payload-%d/byte-order", count), func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(args)))
			for b.Loop() {
				if _, err := json.MarshalJS(data["payload"]); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run(fmt.Sprintf("payload-%d/member-order", count), func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(args)))
			for b.Loop() {
				out := roots.reply(data["payload"], args).(orderedReply)
				if _, err := json.MarshalJSOrdered(out.value, out.order); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
