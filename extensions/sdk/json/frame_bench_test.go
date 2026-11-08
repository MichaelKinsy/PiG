package json

import (
	"fmt"
	"strings"
	"testing"
)

// providerPayloadFrame is a host request frame shaped like the LangSmith workload's before_provider_request event: an envelope whose
// args member holds a provider payload of about size bytes, a growing conversation of messages with 4 KB text blocks (escapes,
// quotes, multi-byte characters) and an image block of base64 data per 128 messages.
func providerPayloadFrame(size int) []byte {
	text := strings.Repeat("The quick brown fox \"jumps\"\n\tover the lazy dög — 日本語, c:\\dir\\file; ", 64)[:4096]
	image := strings.Repeat("iVBORw0KGgoAAAANSUhEUgAAALQAAAC0CAIAAACyr5FlAAAAA3NCSVQICAjb4U/gAAAgAElEQVR4nOy9", 1300)[:97<<10]
	var b strings.Builder
	b.WriteString(`{"type":"request","id":"req-1","request":{"method":"event","event":"before_provider_request","handler_id":3,"args":`)
	b.WriteString(`{"type":"before_provider_request","payload":{"model":"pf-1","stream":true,"messages":[`)
	quoted := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\t", `\t`).Replace(text)
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
	b.WriteString(`]},"extra":[1,2.5,-3e2]}}}`)
	return []byte(b.String())
}

var frameBenchSizes = []int{4 << 10, 64 << 10, 1 << 20, 16 << 20, 43 << 20}

func sizeName(n int) string {
	if n >= 1<<20 {
		return fmt.Sprintf("%dMiB", n>>20)
	}
	return fmt.Sprintf("%dKiB", n>>10)
}

type benchEnvelope struct {
	Type    string      `json:"type"`
	ID      string      `json:"id,omitempty"`
	Request *benchReqst `json:"request,omitempty"`
}

type benchReqst struct {
	Method    string     `json:"method"`
	Event     string     `json:"event,omitempty"`
	HandlerID int        `json:"handler_id,omitempty"`
	Args      RawMessage `json:"args,omitempty"`
}

// BenchmarkFrameEnvelope is the read loop's decode of one frame: the envelope with the payload kept raw.
func BenchmarkFrameEnvelope(b *testing.B) {
	for _, size := range frameBenchSizes {
		frame := providerPayloadFrame(size)
		b.Run(sizeName(size), func(b *testing.B) {
			b.SetBytes(int64(len(frame)))
			b.ReportAllocs()
			for b.Loop() {
				var env benchEnvelope
				if err := UnmarshalFrame(frame, &env); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkFramePayloadAny is the event dispatch's decode of the payload kept raw by the envelope into the map a handler receives.
func BenchmarkFramePayloadAny(b *testing.B) {
	for _, size := range frameBenchSizes {
		var env benchEnvelope
		frame := providerPayloadFrame(size)
		if err := UnmarshalFrame(frame, &env); err != nil {
			b.Fatal(err)
		}
		args := env.Request.Args
		b.Run(sizeName(size), func(b *testing.B) {
			b.SetBytes(int64(len(args)))
			b.ReportAllocs()
			for b.Loop() {
				var data map[string]any
				if err := Unmarshal(args, &data); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkFramePayloadAnyTrusted is BenchmarkFramePayloadAny without the validity pass, which the read loop already made over the frame.
func BenchmarkFramePayloadAnyTrusted(b *testing.B) {
	saved := verifyTrusted
	verifyTrusted = false // the package's tests validate every trusted input with the scanner
	defer func() { verifyTrusted = saved }()
	for _, size := range frameBenchSizes {
		var env benchEnvelope
		frame := providerPayloadFrame(size)
		if err := UnmarshalFrame(frame, &env); err != nil {
			b.Fatal(err)
		}
		args := env.Request.Args
		b.Run(sizeName(size), func(b *testing.B) {
			b.SetBytes(int64(len(args)))
			b.ReportAllocs()
			for b.Loop() {
				var data map[string]any
				if err := UnmarshalTrusted(args, &data); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// toolsPayloadFrame is a request frame whose payload is many small objects: a provider request with tool definitions (nested schemas of
// short names and descriptions) and short chat messages with tool calls, about size bytes.
func toolsPayloadFrame(size int) []byte {
	var b strings.Builder
	b.WriteString(`{"type":"request","id":"req-1","request":{"method":"event","event":"before_provider_request","handler_id":3,"args":`)
	b.WriteString(`{"type":"before_provider_request","payload":{"model":"pf-1","stream":true,"tools":[`)
	for i := 0; b.Len() < size/4; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `{"type":"function","function":{"name":"tool_%d","description":"Does thing %d with the given path and options.","parameters":{"type":"object","properties":{"path":{"type":"string","description":"File path"},"limit":{"type":"integer","description":"Max lines"},"mode":{"type":"string","enum":["a","b","c"]}},"required":["path"],"additionalProperties":false}}}`, i, i)
	}
	b.WriteString(`],"messages":[`)
	for i := 0; b.Len() < size; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `{"role":"assistant","content":null,"tool_calls":[{"id":"call_%d","type":"function","function":{"name":"read","arguments":"{\"path\":\"src/file_%d.go\",\"limit\":200}"}}],"usage":{"input":%d,"output":%d,"cacheRead":0,"cost":{"total":0.0125}}},{"role":"tool","tool_call_id":"call_%d","content":"ok"}`, i, i, i*3, i, i)
	}
	b.WriteString(`]}}}}`)
	return []byte(b.String())
}

// BenchmarkFramePayloadAnySmall is BenchmarkFramePayloadAnyTrusted over a payload of many short strings and keys.
func BenchmarkFramePayloadAnySmall(b *testing.B) {
	saved := verifyTrusted
	verifyTrusted = false
	defer func() { verifyTrusted = saved }()
	for _, size := range []int{64 << 10, 1 << 20, 16 << 20} {
		var env benchEnvelope
		frame := toolsPayloadFrame(size)
		if err := UnmarshalFrame(frame, &env); err != nil {
			b.Fatal(err)
		}
		args := env.Request.Args
		b.Run(sizeName(size), func(b *testing.B) {
			b.SetBytes(int64(len(args)))
			b.ReportAllocs()
			for b.Loop() {
				var data map[string]any
				if err := UnmarshalTrusted(args, &data); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
