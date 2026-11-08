package mcp

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// Ports packages/mcp/test/lines.test.ts (Pi c158fa2ee, f6278a472 #11791). The "joins the message once" test counts Buffer.concat calls, which
// Go does not use; the linear scan is pinned by TestStdioSplitterLargeMessageScalesLinearly.

func startLineReader(maxMessageBytes int) (*lineReader, *recordedEvents) {
	events := &TransportEvents{}
	recorded := record(events)
	return &lineReader{events: events, maxMessageBytes: maxMessageBytes}, recorded
}

func feedLines(reader *lineReader, data []byte, chunkSize int) {
	for i := 0; i < len(data); i += chunkSize {
		reader.readStdout(data[i:min(i+chunkSize, len(data))])
	}
}

func messageJSON(recorded *recordedEvents) []string {
	var out []string
	for _, message := range recorded.messages {
		data, _ := json.Marshal(message)
		out = append(out, string(data))
	}
	return out
}

func TestLineReaderDeliversAMessageSplitAcrossManyChunks(t *testing.T) {
	reader, recorded := startLineReader(0)
	text := strings.Repeat("x", 1024*1024)
	line := `{"jsonrpc":"2.0","id":1,"result":{"text":"` + text + `"}}`
	feedLines(reader, []byte(line+"\n"), 4096)
	if len(recorded.errors) != 0 || !reflect.DeepEqual(messageJSON(recorded), []string{line}) {
		t.Fatalf("errors = %q, messages = %d", recorded.errors, len(recorded.messages))
	}
}

func TestLineReaderHandlesMultiByteCharactersCRLFBlankLinesAndSeveralMessagesPerChunkAtAnySplit(t *testing.T) {
	input := []byte(strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"result":{"text":"héllo 🌍"}}`,
		"",
		`{"jsonrpc":"2.0","method":"notifications/x"}` + "\r",
		"not json",
		`{"jsonrpc":"2.0","id":2,"result":{}}`,
		"",
	}, "\n"))
	want := []string{
		`{"jsonrpc":"2.0","id":1,"result":{"text":"héllo 🌍"}}`,
		`{"jsonrpc":"2.0","method":"notifications/x"}`,
		`{"jsonrpc":"2.0","id":2,"result":{}}`,
	}
	for chunkSize := 1; chunkSize <= len(input); chunkSize++ {
		reader, recorded := startLineReader(0)
		feedLines(reader, input, chunkSize)
		reader.end()
		if got := messageJSON(recorded); !reflect.DeepEqual(got, want) {
			t.Fatalf("chunk size %d: messages = %q, want %q", chunkSize, got, want)
		}
		if len(recorded.errors) != 1 {
			t.Fatalf("chunk size %d: errors = %q, want 1", chunkSize, recorded.errors)
		}
	}
}

func TestLineReaderDropsAnOversizedMessageAndKeepsReadingTheNextOne(t *testing.T) {
	reader, recorded := startLineReader(64)
	next := `{"jsonrpc":"2.0","id":2,"result":{}}`
	feedLines(reader, []byte(strings.Repeat("a", 50)), 10)
	feedLines(reader, []byte(strings.Repeat("a", 50)+"\n"+next+"\n"), 10)
	if len(recorded.errors) == 0 || recorded.errors[0] != "MCP stdio message exceeds 64 bytes" {
		t.Fatalf("errors = %q", recorded.errors)
	}
	if got := messageJSON(recorded); !reflect.DeepEqual(got, []string{next}) {
		t.Fatalf("messages = %q", got)
	}
}

func TestLineReaderReportsAMessageWithoutItsNewlineAtTheEnd(t *testing.T) {
	reader, recorded := startLineReader(0)
	feedLines(reader, []byte(`{"jsonrpc":"2.0",`), 4)
	reader.end()
	if !reflect.DeepEqual(recorded.errors, []string{"MCP stdio server closed with an incomplete JSON-RPC message"}) {
		t.Fatalf("errors = %q", recorded.errors)
	}
}

// A message that has not ended is dropped as soon as the pending bytes pass the cap, not when its newline arrives, and the reader keeps going.
func TestLineReaderDropsAnUnfinishedMessageOncePendingBytesPassTheCap(t *testing.T) {
	reader, recorded := startLineReader(64)
	feedLines(reader, []byte(strings.Repeat("a", 70)), 10)
	if !reflect.DeepEqual(recorded.errors, []string{"MCP stdio message exceeds 64 bytes"}) {
		t.Fatalf("errors after the unfinished message = %q", recorded.errors)
	}
	next := `{"jsonrpc":"2.0","id":2,"result":{}}`
	feedLines(reader, []byte("\n"+next+"\n"), 10)
	if got := messageJSON(recorded); !reflect.DeepEqual(got, []string{next}) {
		t.Fatalf("messages = %q", got)
	}
}

// A complete line over the cap is dropped whole, and the line after it in the same chunk is delivered.
func TestLineReaderDropsAnOversizedLineThatArrivesInOneChunk(t *testing.T) {
	reader, recorded := startLineReader(64)
	next := `{"jsonrpc":"2.0","id":2,"result":{}}`
	reader.readStdout([]byte(strings.Repeat("a", 100) + "\n" + next + "\n"))
	if len(recorded.errors) != 1 || recorded.errors[0] != "MCP stdio message exceeds 64 bytes" {
		t.Fatalf("errors = %q", recorded.errors)
	}
	if got := messageJSON(recorded); !reflect.DeepEqual(got, []string{next}) {
		t.Fatalf("messages = %q", got)
	}
}

// An oversized message does not pin its buffer: once it is consumed the splitter keeps at most splitterKeepBytes while idle.
func TestLineReaderReleasesTheBufferOfAnOversizedMessage(t *testing.T) {
	reader, recorded := startLineReader(1 << 20)
	reader.readStdout([]byte(strings.Repeat("a", 4*splitterKeepBytes) + "\n"))
	if cap(reader.splitter.buf) <= splitterKeepBytes {
		t.Fatalf("the test message did not outgrow the keep limit: cap %d", cap(reader.splitter.buf))
	}
	next := `{"jsonrpc":"2.0","id":2,"result":{}}`
	reader.readStdout([]byte(next + "\n"))
	if got := cap(reader.splitter.buf); got > splitterKeepBytes {
		t.Fatalf("the splitter keeps %d bytes while idle, limit %d", got, splitterKeepBytes)
	}
	if got := messageJSON(recorded); !reflect.DeepEqual(got, []string{next}) {
		t.Fatalf("messages = %q", got)
	}
}
