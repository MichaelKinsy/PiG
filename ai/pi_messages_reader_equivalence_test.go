package ai

import (
	"encoding/json"
	"errors"
	"io"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"
)

// chunkListReader delivers chunks, then io.EOF.
type chunkListReader struct {
	chunks [][]byte
}

func (reader *chunkListReader) ReadChunk() ([]byte, error) {
	if len(reader.chunks) == 0 {
		return nil, io.EOF
	}
	chunk := reader.chunks[0]
	reader.chunks = reader.chunks[1:]
	return chunk, nil
}

func (reader *chunkListReader) streamSettled() bool { return len(reader.chunks) == 0 }

// randomSplit cuts body into chunks of up to limit bytes, some of them empty.
func randomSplit(random *rand.Rand, body []byte, limit int) [][]byte {
	var chunks [][]byte
	for len(body) > 0 {
		n := min(len(body), random.IntN(limit+1))
		chunks = append(chunks, body[:n:n])
		body = body[n:]
	}
	return chunks
}

// randomSSEBody joins up to 23 random pieces of SSE text.
func randomSSEBody(random *rand.Rand, pieces []string) []byte {
	var body []byte
	for range random.IntN(24) {
		body = append(body, pieces[random.IntN(len(pieces))]...)
	}
	return body
}

// piMessagesStringBufferEvents is readPiMessagesEvents as the reader ran it before its buffer was appended to: each chunk is concatenated with the whole buffer, the CRLF replace runs over all of it and the search starts at its beginning (pi-messages.ts:286-308).
func piMessagesStringBufferEvents(chunks [][]byte) (events []string, err error) {
	var decoder piMessagesTextDecoder
	buffer := ""
	emit := func(raw string) error {
		event, ok, err := parsePiMessagesEvent(raw)
		if err == nil && ok {
			events = append(events, string(event))
		}
		return err
	}
	for read := 0; ; read++ {
		done := read == len(chunks)
		if done {
			buffer += decoder.flush()
		} else {
			buffer += decoder.decode(chunks[read])
		}
		buffer = strings.ReplaceAll(buffer, "\r\n", "\n")
		for {
			split := strings.Index(buffer, "\n\n")
			if split == -1 {
				break
			}
			if err := emit(buffer[:split]); err != nil {
				return events, err
			}
			buffer = buffer[split+2:]
		}
		if done {
			break
		}
	}
	if trimJSWhitespace(buffer) != "" {
		if err := emit(buffer); err != nil {
			return events, err
		}
	}
	return events, nil
}

func piMessagesLoopEvents(chunks [][]byte) (events []string, err error) {
	executor := &continuationExecutor{}
	executor.run(func(turn *continuationTurn) {
		loop := &piMessagesEventLoop{reader: &chunkListReader{chunks: chunks}, turn: turn}
		err = loop.run(func(raw json.RawMessage) (bool, error) {
			events = append(events, string(raw))
			return false, nil
		})
	})
	if errors.Is(err, errPiMessagesEventsEnded) {
		err = nil
	}
	return events, err
}

// TestPiMessagesReaderMatchesStringBuffer compares the reader with the concatenating reader it replaced on random bodies split at random points: the same events in the same order and the same error. Runs of carriage returns before a line feed are in the alphabet because Pi's replace folds one of them per read.
func TestPiMessagesReaderMatchesStringBuffer(t *testing.T) {
	pieces := []string{
		`data: {"type":"x"}`, `data: "é"`, "data: [DONE]", "data: 0", "data: x", ": note", "event: e", "data:  ",
		"\r", "\n", "\r\n", "\n\n", "\r\r", "\r\r\n", "\xef\xbb\xbf", "\xc3", "\xa9", " ",
	}
	random := rand.New(rand.NewPCG(18, 19))
	for range 20000 {
		body := randomSSEBody(random, pieces)
		chunks := randomSplit(random, body, 6)
		wantEvents, wantErr := piMessagesStringBufferEvents(slices.Clone(chunks))
		gotEvents, gotErr := piMessagesLoopEvents(slices.Clone(chunks))
		if !slices.Equal(gotEvents, wantEvents) || errorText(gotErr) != errorText(wantErr) {
			t.Fatalf("chunks %q:\n got %q, %v\nwant %q, %v", chunks, gotEvents, gotErr, wantEvents, wantErr)
		}
	}
}
