package mcp

import (
	"strings"
	"testing"
)

// String.prototype.trim removes U+FEFF and every Unicode space separator but not U+0085, the opposite of Go's strings.TrimSpace on those two. Pi trims
// with it in describeHttpFailure (streamable-http.ts:176), the SSE data check (:347) and the stdio line checks (stdio.ts:119,200).
func TestDescribeHTTPFailureTrimsTheBodyLikeJavaScript(t *testing.T) {
	for _, tc := range []struct{ body, want string }{
		{"\ufeff \u00a0boom\u2003\u3000", "MCP HTTP request failed with status 500: boom"},
		{"\ufeff\u00a0", "MCP HTTP request failed with status 500"},
		{"\u0085boom", "MCP HTTP request failed with status 500: \u0085boom"},
		{"\u0085", "MCP HTTP request failed with status 500: \u0085"},
	} {
		if got := describeHTTPFailure(500, tc.body); got != tc.want {
			t.Errorf("describeHTTPFailure(500, %q) = %q, want %q", tc.body, got, tc.want)
		}
	}
}

// The stdio and Streamable HTTP transports decide what is blank with String.prototype.trim, whose whitespace includes
// U+FEFF and excludes U+0085 (NEL), unlike unicode.IsSpace: stdio.ts handleStdout `if (!text.trim()) continue;`, the
// close check `this.stdoutBuffer.toString("utf8").trim()`, streamable-http.ts consumeSse `!event.data.trim()` and
// describeHttpFailure `body.trim()`.

type recordedEvents struct {
	messages []JSONRPCMessage
	errors   []string
}

func record(events *TransportEvents) *recordedEvents {
	recorded := &recordedEvents{}
	events.OnMessage(func(message JSONRPCMessage) { recorded.messages = append(recorded.messages, message) })
	events.OnError(func(err error) { recorded.errors = append(recorded.errors, err.Error()) })
	return recorded
}

// JSON.parse("\u0085") throws `Unexpected token '\u0085', "\u0085" is not valid JSON` (Node 24).
const nelSyntaxError = "Unexpected token '\u0085', \"\u0085\" is not valid JSON"

func TestStdioTransportSkipsOnlyLinesThatAreBlankToJavaScriptTrim(t *testing.T) {
	transport := &StdioTransport{}
	recorded := record(&transport.TransportEvents)
	rest := transport.drainLines([]byte("\uFEFF\n \t\uFEFF\r\n\u0085\n{\"jsonrpc\":\"2.0\",\"method\":\"ping\"}\n"), DefaultMaxMessageBytes)
	if len(rest) != 0 {
		t.Fatalf("rest = %q", rest)
	}
	if len(recorded.messages) != 1 || recorded.messages[0].Method != "ping" {
		t.Fatalf("messages = %+v", recorded.messages)
	}
	if len(recorded.errors) != 1 || recorded.errors[0] != nelSyntaxError {
		t.Fatalf("errors = %q, want the JSON.parse error of the NEL line only", recorded.errors)
	}
}

func TestStdioTransportReportsAnIncompleteMessageOnlyWhenTheRemainderIsNotBlankToJavaScriptTrim(t *testing.T) {
	for _, test := range []struct {
		rest     string
		reported bool
	}{
		{"\uFEFF \t", false},
		{"\u0085", true},
		{"{\"jsonrpc\"", true},
	} {
		transport := &StdioTransport{stdoutRemainder: []byte(test.rest)}
		recorded := record(&transport.TransportEvents)
		transport.finishStdout()
		want := []string(nil)
		if test.reported {
			want = []string{"MCP stdio server closed with an incomplete JSON-RPC message"}
		}
		if strings.Join(recorded.errors, "|") != strings.Join(want, "|") {
			t.Fatalf("remainder %q: errors = %q, want %q", test.rest, recorded.errors, want)
		}
	}
}

func TestStreamableHTTPTransportSkipsOnlySSEDataThatIsBlankToJavaScriptTrim(t *testing.T) {
	transport := &StreamableHTTPTransport{}
	recorded := record(&transport.TransportEvents)
	stream := ": primer\n\ndata: \uFEFF\n\ndata: \u0085\n\ndata: {\"jsonrpc\":\"2.0\",\"method\":\"ping\"}\n\n"
	if err := transport.consumeSSE(strings.NewReader(stream), &streamCursor{}, nil); err != nil {
		t.Fatal(err)
	}
	if len(recorded.messages) != 1 || recorded.messages[0].Method != "ping" {
		t.Fatalf("messages = %+v", recorded.messages)
	}
	if len(recorded.errors) != 1 || recorded.errors[0] != nelSyntaxError {
		t.Fatalf("errors = %q, want the JSON.parse error of the NEL event only", recorded.errors)
	}
}
