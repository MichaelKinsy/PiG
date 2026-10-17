//go:build !pig_strip_mistral_conversations

package ai

import (
	"encoding/json"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"
)

// mistralStringBufferEvents is readMistralEvents as the reader ran it before its buffer was appended to: the whole buffer is concatenated with each chunk and searched from its start (mistral-conversations.ts:457-473).
func mistralStringBufferEvents(chunks [][]byte) (events []string, err error) {
	var decoder mistralTextDecoder
	buffer := ""
	boundary := func(buffer string) (index, length int) {
		for i := range len(buffer) {
			for _, alternative := range [...]string{"\r\n\r\n", "\r\n\r", "\r\n\n", "\r\r\n", "\n\r\n", "\r\r", "\n\r", "\n\n"} {
				if strings.HasPrefix(buffer[i:], alternative) {
					return i, len(alternative)
				}
			}
		}
		return -1, 0
	}
	for read := 0; ; read++ {
		done := read == len(chunks)
		if done {
			buffer += decoder.flush()
		} else {
			buffer += decoder.decode(chunks[read])
		}
		for {
			index, length := boundary(buffer)
			if index < 0 {
				break
			}
			event, isDone, err := parseMistralEvent(buffer[:index])
			if err != nil {
				return events, err
			}
			buffer = buffer[index+length:]
			if isDone {
				return events, nil
			}
			if event != nil {
				events = append(events, string(event))
			}
		}
		if done {
			break
		}
	}
	if trimJSWhitespace(buffer) != "" {
		event, isDone, err := parseMistralEvent(buffer)
		if err != nil {
			return events, err
		}
		if !isDone && event != nil {
			events = append(events, string(event))
		}
	}
	return events, nil
}

func mistralLoopEvents(chunks [][]byte) (events []string, err error) {
	loop := &mistralEventLoop{reader: &chunkListReader{chunks: chunks}}
	err = loop.run(func(raw json.RawMessage) error {
		events = append(events, string(raw))
		return nil
	})
	return events, err
}

// TestMistralReaderMatchesStringBuffer compares the reader with the concatenating reader it replaced on random bodies split at random points: the same events in the same order and the same error.
func TestMistralReaderMatchesStringBuffer(t *testing.T) {
	pieces := []string{
		"data: {\"choices\":[1]}", "data: {\"choices\":[\"é\"]}", "data:[DONE]", "data: {}", "data: x", ": note", "event: e", "data:  ",
		"\r", "\n", "\r\n", "\n\n", "\r\r", "\xef\xbb\xbf", "\xc3", "\xa9", " ",
	}
	random := rand.New(rand.NewPCG(16, 17))
	for range 20000 {
		body := randomSSEBody(random, pieces)
		chunks := randomSplit(random, body, 6)
		wantEvents, wantErr := mistralStringBufferEvents(slices.Clone(chunks))
		gotEvents, gotErr := mistralLoopEvents(slices.Clone(chunks))
		if !slices.Equal(gotEvents, wantEvents) || errorText(gotErr) != errorText(wantErr) {
			t.Fatalf("chunks %q:\n got %q, %v\nwant %q, %v", chunks, gotEvents, gotErr, wantEvents, wantErr)
		}
	}
}
