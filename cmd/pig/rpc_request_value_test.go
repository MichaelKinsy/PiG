package main

import (
	"io"
	"strings"
	"testing"
)

// rpc-mode.ts:716-717,792-798 catches ToPrimitive failures and echoes the unmodified type value. JSON values cannot contain a callable own toString, so every own toString shadows the inherited method with a non-callable value.
func TestRPCUnknownCommandCoercion(t *testing.T) {
	for _, tc := range []struct{ value, message string }{
		{`{"toString":null}`, "Cannot convert object to primitive value"},
		{`{"toString":"custom"}`, "Cannot convert object to primitive value"},
		{`[{"toString":false}]`, "Cannot convert object to primitive value"},
		{`{"valueOf":null}`, "Unknown command: [object Object]"},
		{`[null,[],[1,2]]`, "Unknown command: ,,1,2"},
	} {
		envelope, err := parseRPCCommand([]byte(`{"id":null,"type":` + tc.value + `}`))
		if err != nil {
			t.Fatal(err)
		}
		got := rpcUnknownCommand(envelope)
		if string(got.ID) != "null" || string(got.Command) != tc.value || got.Error != tc.message {
			t.Fatalf("%s: %#v", tc.value, got)
		}
	}
}

// The command envelope retains no state after dispatch; the request owns its canonicalized identifier until its response and any bash updates complete.
func BenchmarkRPCRequestEnvelope(b *testing.B) {
	for _, tc := range []struct{ name, id string }{
		{"string", `"request-1"`},
		{"object", `{"2":0,"name":"request","values":[null,true,1e400,1e21]}`},
		{"large", `"` + strings.Repeat("x", 64*1024) + `"`},
	} {
		b.Run(tc.name, func(b *testing.B) {
			line := []byte(`{"id":` + tc.id + `,"type":"abort_retry"}`)
			b.ReportAllocs()
			for b.Loop() {
				envelope, err := parseRPCCommand(line)
				if err != nil {
					b.Fatal(err)
				}
				writeJSONLine(io.Discard, rpcSuccess(envelope.ID, envelope.Type, nil))
			}
		})
	}
}

// The envelope reads only "id" and "type"; other members, such as a prompt's image data, are scanned in place rather than copied. parseRPCCommand keeps one copy of the line in Raw, so a second copy of the payload would double prompt ingress memory.
func TestRPCRequestEnvelopeDoesNotCopyPayloadMembers(t *testing.T) {
	const payload = 1 << 20
	line := []byte(`{"id":{"k":"v"},"type":"prompt","message":"hi","images":[{"type":"image","mimeType":"image/png","data":"` + strings.Repeat("A", payload) + `"}]}`)
	result := testing.Benchmark(func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			envelope, err := parseRPCCommand(line)
			if err != nil || envelope.Type != "prompt" || string(envelope.ID) != `{"k":"v"}` {
				b.Fatalf("envelope=%+v err=%v", envelope, err)
			}
		}
	})
	if perOp := result.AllocedBytesPerOp(); perOp > int64(len(line))*3/2 {
		t.Fatalf("parseRPCCommand allocated %d bytes for a %d-byte line, want one Raw copy", perOp, len(line))
	}
	// The retained members own their bytes: the reader may reuse its line buffer after dispatch.
	reused := []byte(`{"id":"abc","type":"xyz"}`)
	envelope, err := parseRPCCommand(reused)
	if err != nil {
		t.Fatal(err)
	}
	for i := range reused {
		reused[i] = ' '
	}
	if string(envelope.ID) != `"abc"` || string(envelope.TypeValue) != `"xyz"` || envelope.Type != "xyz" {
		t.Fatalf("envelope aliases its input: id=%s type=%s", envelope.ID, envelope.TypeValue)
	}
}
