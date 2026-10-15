//go:build !race

package ai

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

// kilobyteReader returns at most one kilobyte per read, as a body that arrives in small network reads does.
type kilobyteReader struct{ data []byte }

func (reader *kilobyteReader) Read(buffer []byte) (int, error) {
	if len(reader.data) == 0 {
		return 0, io.EOF
	}
	n := copy(buffer[:min(len(buffer), 1024)], reader.data)
	reader.data = reader.data[n:]
	return n, nil
}

// codexLongRecordBytes is what a Codex SSE turn allocates when one record of about size bytes arrives in 1 KB reads.
func codexLongRecordBytes(t *testing.T, size int) uint64 {
	body := "data: {\"type\":\"response.created\",\"response\":{\"id\":\"r1\"}}\n\n" +
		"data: {\"type\":\"response.queued_padding\",\"pad\":\"" + strings.Repeat("x", size) + "\"}\n\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"r1\",\"status\":\"completed\"}}\n\n"
	provider := NewOpenAICodexResponsesProvider(OpenAICodexResponsesConfig{Model: "strict", APIKey: codexTestToken(t, "acct")})
	defer func() { _ = provider.Close() }()
	fetch := codexRoundTripper(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(&kilobyteReader{data: []byte(body)})}, nil
	})
	return allocatedBytes(func() {
		stream, err := provider.Stream(t.Context(), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("x")}}}), StreamOptions{Transport: TransportSSE, Fetch: &http.Client{Transport: fetch}})
		if err != nil {
			t.Fatal(err)
		}
		if result := stream.Result(); result.StopReason != StopReasonStop {
			t.Fatalf("stop=%q error=%q", result.StopReason, result.ErrorMessage)
		}
	})
}

// A record that spans many reads costs memory in proportion to its size: the parser appends each read's text to its buffer and searches only the new bytes for the end of the record, instead of concatenating the whole buffer again and searching it from the start on every read.
func TestCodexSSELongRecordIsLinear(t *testing.T) {
	small, large := codexLongRecordBytes(t, 64<<10), codexLongRecordBytes(t, 640<<10)
	ratio := float64(large) / float64(small)
	t.Logf("64 KB record: %d B allocated; 640 KB record: %d B (%.1fx)", small, large, ratio)
	if ratio > 20 {
		t.Fatalf("a 10x longer record allocated %.1fx the bytes (%d vs %d); want at most 20x", ratio, large, small)
	}
}
