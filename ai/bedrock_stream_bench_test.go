//go:build !pig_strip_bedrock_converse_stream

package ai

import (
	"context"
	"encoding/binary"
	"hash/crc32"
	"net"
	"testing"
)

// bedrockBenchFrame builds one event-stream frame without the oracle's helpers.
func bedrockBenchFrame(eventType string, payload string) []byte {
	header := func(name, value string) []byte {
		out := []byte{byte(len(name))}
		out = append(out, name...)
		out = append(out, 7, byte(len(value)>>8), byte(len(value)))
		return append(out, value...)
	}
	headers := append(append(header(":event-type", eventType), header(":content-type", "application/json")...), header(":message-type", "event")...)
	total := 12 + len(headers) + len(payload) + 4
	out := make([]byte, 0, total)
	out = binary.BigEndian.AppendUint32(out, uint32(total))
	out = binary.BigEndian.AppendUint32(out, uint32(len(headers)))
	out = binary.BigEndian.AppendUint32(out, crc32.ChecksumIEEE(out))
	out = append(out, headers...)
	out = append(out, payload...)
	return binary.BigEndian.AppendUint32(out, crc32.ChecksumIEEE(out))
}

// BenchmarkBedrockStream10kDeltas streams one buffered 10,000-delta body through the provider and a plain consumer: the microtask model costs the promise machinery of about a dozen reactions per event on top of the decode.
func BenchmarkBedrockStream10kDeltas(b *testing.B) {
	var body []byte
	body = append(body, bedrockBenchFrame("messageStart", `{"role":"assistant"}`)...)
	for range 10_000 {
		body = append(body, bedrockBenchFrame("contentBlockDelta", `{"contentBlockIndex":0,"delta":{"text":"word "}}`)...)
	}
	body = append(body, bedrockBenchFrame("contentBlockStop", `{"contentBlockIndex":0}`)...)
	body = append(body, bedrockBenchFrame("messageStop", `{"stopReason":"end_turn"}`)...)
	body = append(body, bedrockBenchFrame("metadata", `{"usage":{"inputTokens":1,"outputTokens":2,"totalTokens":3}}`)...)
	b.ReportAllocs()
	for range b.N {
		func() {
			server := newBedrockFixtureServer(b, nil, body, true)
			server.openOnce()
			_, port, _ := net.SplitHostPort(server.listener.Addr().String())
			ctx, cancel := context.WithCancel(b.Context())
			defer cancel()
			provider := bedrockObservationProvider(port)
			stream, err := provider.Stream(ctx, NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("probe")}}}), StreamOptions{
				APIKey: "test", Env: ProviderEnv{"AWS_BEDROCK_SKIP_AUTH": "1", "AWS_REGION": "us-east-1"},
			})
			if err != nil {
				b.Fatal(err)
			}
			events := 0
			for range stream.Events(ctx) {
				events++
			}
			if result := stream.Result(); result.StopReason != StopReasonStop || events != 10_004 {
				b.Fatalf("stop=%s events=%d error=%q", result.StopReason, events, result.ErrorMessage)
			}
		}()
	}
}
