//go:build !race

package ai

import (
	"context"
	"strings"
	"testing"
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

// scanPasses is the wire-record scans that recordScanHook reports while the provider decodes stream: the passes and bytes of json.Valid in the
// stream decoder and of the validity scan an event observer's skip decision makes, each beyond the one decode that reads the record.
func scanPasses(t *testing.T, stream string, wantObserver bool) (valid, observer, scannedBytes int) {
	t.Helper()
	recordScanHook = func(pass recordScanPass, bytes int) {
		scannedBytes += bytes
		switch pass {
		case recordScanValid:
			valid++
		case recordScanObserver:
			observer++
		}
	}
	defer func() { recordScanHook = nil }()
	provider := &openAIProvider{cfg: OpenAIConfig{Model: "gpt-x", ProviderID: "openai"}}
	options := StreamOptions{
		OnProviderStreamEvent:     func(context.Context, any, *Model) error { t.Error("an unwanted observer was called"); return nil },
		ProviderStreamEventWanted: func() bool { return wantObserver },
	}
	builder := newAssistantStreamBuilder(context.Background(), APIOpenAICompletions, "openai", "gpt-x")
	builder.setProviderEventObserver(options, &Model{ID: "gpt-x"})
	provider.parseSSE(context.Background(), strings.NewReader(stream), builder, nil)
	if result := builder.stream.Result(); result.StopReason != StopReasonStop || len(result.Content) != 1 {
		t.Fatalf("unexpected result %+v", result)
	}
	return valid, observer, scannedBytes
}

// A completions record is validated by the decode that reads it: the chunk decode stands in for the json.Valid that the stream decoder runs on a
// record it cannot decode, and for the scan an unwanted provider event observer makes to decide it may skip the record. The test counts the scans
// beyond the decode (recordScanHook) instead of timing them against a floor, so a second scan (json.Valid before the decode) or a third (the
// observer's) fails it on any machine at any load. The control stream, whose records carry a member the chunk decode does not read and so fall back
// to the decoder's json.Valid and the observer's scan, shows that the count sees both.
func TestOpenAICompletionsRecordsAreScannedOnce(t *testing.T) {
	stream, records := openAILargeTextRecords()
	if valid, observer, scanned := scanPasses(t, stream, false); valid != 0 || observer != 0 {
		t.Errorf("%d records of %d bytes: json.Valid ran %d times and the observer's scan %d times (%d bytes) beyond the decode; want none", len(records), len(records[0]), valid, observer, scanned)
	}

	const controlRecords = 3
	control := strings.Repeat(`data: {"id":"chatcmpl-1","extra":{"a":1},"choices":[{"index":0,"delta":{"content":"a"},"finish_reason":null}]}`+"\n\n", controlRecords) +
		"data: {\"id\":\"chatcmpl-1\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"
	valid, observer, scanned := scanPasses(t, control, false)
	if valid != controlRecords || observer != controlRecords || scanned == 0 {
		t.Errorf("control: json.Valid ran %d times and the observer's scan %d times (%d bytes) over %d records the chunk decode does not read; want once each per record", valid, observer, scanned, controlRecords)
	}
}
