//go:build !race

package ai

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// openAILargeTextRecords is a completions stream of large text-delta records, in which the record bytes, not the per-record bookkeeping, set the cost.
func openAILargeTextRecords() (stream string, records []string) {
	text := strings.Repeat("lorem ipsum dolor sit amet, consectetur adipiscing elit ", 150)
	var body strings.Builder
	for range 64 {
		record := `{"id":"chatcmpl-1","object":"chat.completion.chunk","created":1,"model":"gpt-x","choices":[{"index":0,"delta":{"content":"` + text + `"},"finish_reason":null}]}`
		records = append(records, record)
		body.WriteString("data: " + record + "\n\n")
	}
	body.WriteString("data: {\"id\":\"chatcmpl-1\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	return body.String(), records
}

// A completions record is validated by the decode that reads it. The floor is json.Valid of each record followed by the chunk decode. A stream
// that the decoder validates with json.Valid before the chunk decode, and that an unwanted provider event observer (a session's) scans a third
// time, costs about 3.6 times that floor; with the decode standing in for both scans, about 1.5 times.
func TestOpenAICompletionsRecordsAreScannedOnce(t *testing.T) {
	if testing.Short() {
		t.Skip("CPU ratio")
	}
	stream, records := openAILargeTextRecords()
	recordBytes := make([][]byte, len(records))
	for i, record := range records {
		recordBytes[i] = []byte(record)
	}
	provider := &openAIProvider{cfg: OpenAIConfig{Model: "gpt-x", ProviderID: "openai"}}
	options := StreamOptions{
		OnProviderStreamEvent:     func(context.Context, any, *Model) error { t.Error("an unwanted observer was called"); return nil },
		ProviderStreamEventWanted: func() bool { return false },
	}
	decode := func() {
		builder := newAssistantStreamBuilder(context.Background(), APIOpenAICompletions, "openai", "gpt-x")
		builder.setProviderEventObserver(options, &Model{ID: "gpt-x"})
		provider.parseSSE(context.Background(), strings.NewReader(stream), builder, nil)
		if result := builder.stream.Result(); result.StopReason != StopReasonStop || len(result.Content) != 1 {
			t.Fatalf("unexpected result %+v", result)
		}
	}
	decode()
	parsed := fastest(11, decode)
	valid := 0
	floor := fastest(11, func() {
		for i, record := range recordBytes {
			if json.Valid(record) {
				valid++
			}
			parseOpenAIChunkFast(records[i])
		}
	})
	ratio := float64(parsed) / float64(floor)
	t.Logf("%d records of %d bytes: stream %v, json.Valid and chunk decode of each record %v (%d valid): %.2fx", len(records), len(records[0]), parsed, floor, valid, ratio)
	testenv.TimingGateFatal(t, ratio > 2.4, "decoding the stream took %v, %.2fx validating each record with json.Valid and decoding it (%v); want at most 2.4x", parsed, ratio, floor)
}
