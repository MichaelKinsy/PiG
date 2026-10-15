package ai

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

// chunkedBody returns one scripted chunk per Read, so a CRLF can arrive as two reads.
type chunkedBody struct{ chunks []string }

func (body *chunkedBody) Read(p []byte) (int, error) {
	if len(body.chunks) == 0 {
		return 0, io.EOF
	}
	n := copy(p, body.chunks[0])
	body.chunks[0] = body.chunks[0][n:]
	if body.chunks[0] == "" {
		body.chunks = body.chunks[1:]
	}
	return n, nil
}

func (*chunkedBody) Close() error { return nil }

// pi-mono #10390: Pi's consumeLine ends a line at a "\r" that closes a network chunk; the "\n" of the CRLF
// then arrives as an empty line, flushes the record without its data, and the turn aborts with
// "Could not parse Anthropic SSE event message_start: Unexpected end of JSON input; data=".
// pig divergence (D121): a trailing "\r" waits for the next chunk, so a CRLF split across chunks is one line break.
func TestAnthropicStreamSurvivesCRLFSplitAcrossChunks(t *testing.T) {
	crlf := strings.ReplaceAll(anthropicMinimalFixture(), "\n", "\r\n")
	var chunks []string
	for _, line := range strings.SplitAfter(crlf, "\r\n") {
		if before, ok := strings.CutSuffix(line, "\n"); ok {
			chunks = append(chunks, before, "\n")
		} else if line != "" {
			chunks = append(chunks, line)
		}
	}
	fetch := &http.Client{Transport: responsesTestRoundTripperFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: &chunkedBody{chunks: chunks}}, nil
	})}
	provider := NewAnthropicProvider(AnthropicConfig{Model: "claude-haiku-4-5", APIKey: "fake-key", BaseURL: "http://127.0.0.1:9"})
	defer func() { _ = provider.Close() }()
	stream, err := provider.Stream(t.Context(), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("Hello")}}}), StreamOptions{Fetch: fetch})
	if err != nil {
		t.Fatal(err)
	}
	result := stream.Result()
	if result.StopReason != StopReasonStop || result.ErrorMessage != "" || result.Usage.TotalTokens != 17 {
		t.Fatalf("result = %+v", result)
	}
	if text, _ := result.Content[0].(TextContent); text.Text != "Hello" {
		t.Fatalf("content = %#v", result.Content)
	}
}
