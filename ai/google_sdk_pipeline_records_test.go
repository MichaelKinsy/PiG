package ai

import (
	"errors"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"
)

// googleStringBufferRecords is processStreamResponse's record split as the generator ran it before its buffer was appended to: each chunk is concatenated with the whole buffer and every delimiter is searched for from its start (index.mjs:13793-13856).
func googleStringBufferRecords(chunks [][]byte) (records []string, err error) {
	decoder := webTextDecoder{}
	buffer := ""
	for _, chunk := range chunks {
		chunkString := decoder.decode(chunk, true)
		if err := googleRawChunkError([]byte(chunkString)); err != nil {
			return records, err
		}
		buffer += chunkString
		for {
			index, length := -1, 0
			for _, delimiter := range [...]string{"\n\n", "\r\r", "\r\n\r\n"} {
				if found := strings.Index(buffer, delimiter); found != -1 && (index == -1 || found < index) {
					index, length = found, len(delimiter)
				}
			}
			if index < 0 {
				break
			}
			event := trimJSWhitespace(buffer[:index])
			buffer = buffer[index+length:]
			if data, ok := strings.CutPrefix(event, "data:"); ok {
				records = append(records, trimJSWhitespace(data))
			}
		}
	}
	if trimJSWhitespace(buffer) != "" {
		return records, errors.New("Incomplete JSON segment at the end")
	}
	return records, nil
}

// googleGeneratorRecords runs processStreamResponse over a body that holds chunks and collects the text of each HttpResponse it yields.
func googleGeneratorRecords(chunks [][]byte) (records []string, err error) {
	executor := &continuationExecutor{}
	executor.run(func(turn *continuationTurn) {
		stream := newWebReadableStream(executor, true)
		for _, chunk := range chunks {
			if len(chunk) > 0 {
				stream.enqueue(chunk)
			}
		}
		stream.closeController()
		generator := googleProcessStreamResponse(executor, &googleFetchResponse{body: stream}, 0)
		defer generator.close()
		for {
			next := awaitContinuation(turn, generator.next(nil).settled)
			if next.err != nil {
				err = next.err
				return
			}
			result := next.value.(tslibIterResult)
			if result.done {
				return
			}
			records = append(records, result.value.(*googleHTTPResponse).text)
		}
	})
	return records, err
}

// TestGoogleSDKRecordsMatchStringBuffer compares processStreamResponse with the concatenating generator it replaced on random bodies split at random points: the same records in the same order and the same error.
func TestGoogleSDKRecordsMatchStringBuffer(t *testing.T) {
	pieces := []string{
		`data: {"a":1}`, `data:"é"`, "data: ", ": note", "event: e", `{"error":{"code":400,"status":"x"}}`,
		"\r", "\n", "\r\n", "\n\n", "\r\r", "\r\n\r\n", "\xef\xbb\xbf", "\xc3", "\xa9", " ",
	}
	random := rand.New(rand.NewPCG(20, 21))
	for range 20000 {
		body := randomSSEBody(random, pieces)
		chunks := slices.DeleteFunc(randomSplit(random, body, 6), func(chunk []byte) bool { return len(chunk) == 0 })
		want, wantErr := googleStringBufferRecords(chunks)
		got, gotErr := googleGeneratorRecords(chunks)
		if !slices.Equal(got, want) || errorText(gotErr) != errorText(wantErr) {
			t.Fatalf("chunks %q:\n got %q, %v\nwant %q, %v", chunks, got, gotErr, want, wantErr)
		}
	}
}
