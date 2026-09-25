package rpcclient

import (
	"io"
	"slices"
	"strings"
	"testing"
	"testing/iotest"
)

// TestReadJSONLLinesFramesSplitRecords ports upstream jsonl.ts framing with
// every UTF-8 byte boundary forced into a separate read.
func TestReadJSONLLinesFramesSplitRecords(t *testing.T) {
	input := "{\"a\":1}\r\n\nnot json\n{\"b\":\"x\u2028y\u2029z\"}\n{\"c\":3}\n\r"
	var got []string
	err := ReadJSONLLines(iotest.OneByteReader(strings.NewReader(input)), func(line []byte) bool {
		got = append(got, string(line))
		return true
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{`{"a":1}`, "", "not json", "{\"b\":\"x\u2028y\u2029z\"}", `{"c":3}`, ""}
	if !slices.Equal(got, want) {
		t.Fatalf("lines = %q, want %q", got, want)
	}
}

func TestRpcClientReadStdoutDrainsAfterCallbacksStop(t *testing.T) {
	// Upstream's detach removes listeners without closing or pausing stdout.
	input := strings.NewReader("{\"type\":\"ready\"}\n{\"type\":\"tick\"}\n{\"type\":\"tick\"}\nunterminated")
	client := NewRpcClient(RpcClientOptions{})
	proc := &agentProcess{}
	var got []string
	client.OnEvent(func(event JsonAgentSessionEvent) {
		got = append(got, event.Type)
		proc.stopReading.Store(true)
	})
	client.readStdout(proc, io.NopCloser(iotest.OneByteReader(input)))
	if !slices.Equal(got, []string{"ready"}) {
		t.Fatalf("callbacks after detach: %v", got)
	}
	if input.Len() != 0 {
		t.Fatalf("stdout stopped draining with %d unread bytes", input.Len())
	}
}

// TestRpcClientReadStdoutParsesSplitInvalidBlankAndUnterminatedRecords drives
// the client's production JSON parser after one-byte JSONL framing.
func TestRpcClientReadStdoutParsesSplitInvalidBlankAndUnterminatedRecords(t *testing.T) {
	client := NewRpcClient(RpcClientOptions{})
	var got []string
	client.OnEvent(func(event JsonAgentSessionEvent) {
		got = append(got, event.Type+" "+string(event.Raw))
	})
	input := "not json\n\n{\"type\":\"custom\",\"n\":1}\r\nnull\n{\"type\":\"custom\",\"n\":2}"
	stdout := io.NopCloser(iotest.OneByteReader(strings.NewReader(input)))
	client.readStdout(&agentProcess{}, stdout)

	want := []string{
		`custom {"type":"custom","n":1}`,
		`custom {"type":"custom","n":2}`,
	}
	if !slices.Equal(got, want) {
		t.Fatalf("events = %q, want %q", got, want)
	}
}
