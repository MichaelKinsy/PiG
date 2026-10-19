package ai

import (
	"context"
	"io"
	"reflect"
	"testing"
)

// publicationCheckingReader delivers one record per read. Each read after the first comes from the decoder's next record, after the
// message was published or found unchanged: the published message must then equal the producer's.
type publicationCheckingReader struct {
	t       *testing.T
	builder *assistantStreamBuilder
	records []string
	reads   int
}

func (reader *publicationCheckingReader) Read(p []byte) (int, error) {
	if reader.reads > 0 {
		reader.builder.cell.mu.RLock()
		published := reader.builder.cell.current
		reader.builder.cell.mu.RUnlock()
		if want := cloneAssistantMessageFrom(*reader.builder.partial, nil, nil); !reflect.DeepEqual(published, want) {
			reader.t.Fatalf("before record %d the published message lags the producer's:\npublished %+v\n  partial %+v", reader.reads, published, want)
		}
	}
	if len(reader.records) == 0 {
		return 0, io.EOF
	}
	reader.reads++
	n := copy(p, reader.records[0])
	if n < len(reader.records[0]) {
		reader.t.Fatalf("record %d does not fit one read", reader.reads)
	}
	reader.records = reader.records[1:]
	return n, nil
}

// The completions loop publishes before each record only what changed outside a push. Every change it makes directly (response id and
// model, usage from the chunk or the choice, the stop reason and error message, a reasoning signature) is published before the next record
// is read, as publishing on every record did.
func TestCompletionsRecordsPublishEveryDirectChange(t *testing.T) {
	head := []string{
		`{"id":"c1","model":"routed","choices":[{"index":0,"delta":{"role":"assistant"}}]}`,
		`{"id":"c1","choices":[{"index":0,"delta":{"content":"hi"}}]}`,
		`{"id":"c1","choices":[{"index":0,"delta":{"reasoning_details":[{"type":"reasoning.encrypted","id":"r1","data":"secret","format":"f","index":0}]}}]}`,
		`{"id":"c1","choices":[{"index":0,"delta":{"reasoning_content":"think"}}]}`,
		`{"id":"c1","choices":[{"index":0,"delta":{"reasoning_details":[{"type":"reasoning.text","text":"more","index":1}]}}]}`,
		`{"id":"c1","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"read","arguments":"{\"pa"}}]}}]}`,
		`{"id":"c1","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"th\":1}"}}]}}]}`,
		`{"id":"c1","choices":[],"usage":{"prompt_tokens":3,"completion_tokens":4}}`,
	}
	for _, tail := range [][]string{
		{`{"id":"c1","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`, `{"id":"c1","choices":[{"index":0,"delta":{},"usage":{"prompt_tokens":5,"completion_tokens":6}}]}`},
		{`{"id":"c1","choices":[{"index":0,"delta":{},"finish_reason":"content_filter"}]}`, `{"id":"c1","choices":[{"index":0,"delta":{"content":"after"}}]}`},
		{`{"id":"c1","choices":[{"index":0,"delta":{},"finish_reason":"stop","usage":{"prompt_tokens":7,"completion_tokens":8}}]}`},
	} {
		builder := newAssistantStreamBuilder(context.Background(), APIOpenAICompletions, "openrouter", "openrouter/auto")
		builder.start()
		reader := &publicationCheckingReader{t: t, builder: builder}
		for _, record := range append(append([]string(nil), head...), tail...) {
			reader.records = append(reader.records, "data: "+record+"\n\n")
		}
		reader.records = append(reader.records, "data: [DONE]\n\n")
		provider := &openAIProvider{cfg: OpenAIConfig{Model: "openrouter/auto", ProviderID: "openrouter"}}
		provider.parseSSE(context.Background(), reader, builder, nil)
		if reader.reads != len(head)+len(tail)+1 {
			t.Fatalf("read %d records", reader.reads)
		}
	}
}
