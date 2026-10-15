package ai

import (
	"context"
	"testing"
)

// responsesRecordsForPublication is a Responses stream with every event processResponsesStream handles: items of each kind opened and
// closed, deltas, a function call's and a custom tool call's input, a final-answer message, usage and the terminal response.
var responsesRecordsForPublication = []string{
	`{"type":"response.created","response":{"id":"resp_1"}}`,
	`{"type":"response.output_item.added","output_index":0,"item":{"type":"reasoning","id":"rs_1","summary":[]}}`,
	`{"type":"response.reasoning_summary_text.delta","output_index":0,"delta":"think"}`,
	`{"type":"response.reasoning_summary_part.done","output_index":0}`,
	`{"type":"response.reasoning_text.delta","output_index":0,"delta":"more"}`,
	`{"type":"response.output_item.done","output_index":0,"item":{"type":"reasoning","id":"rs_1","summary":[{"type":"summary_text","text":"think"}],"encrypted_content":"e"}}`,
	`{"type":"response.output_item.added","output_index":1,"item":{"type":"message","id":"msg_1","role":"assistant","content":[],"phase":"final_answer"}}`,
	`{"type":"response.output_text.delta","output_index":1,"delta":"hello"}`,
	`{"type":"response.refusal.delta","output_index":1,"delta":" no"}`,
	`{"type":"response.output_item.done","output_index":1,"item":{"type":"message","id":"msg_1","role":"assistant","content":[{"type":"output_text","text":"hello no"}]}}`,
	`{"type":"response.output_item.added","output_index":2,"item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"read","arguments":""}}`,
	`{"type":"response.function_call_arguments.delta","output_index":2,"delta":"{\"path\":"}`,
	`{"type":"response.function_call_arguments.done","output_index":2,"arguments":"{\"path\":\"a\"}"}`,
	`{"type":"response.output_item.done","output_index":2,"item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"read","arguments":"{\"path\":\"a\"}"}}`,
	`{"type":"response.output_item.added","output_index":3,"item":{"type":"custom_tool_call","id":"ct_1","call_id":"call_2","name":"g","input":""}}`,
	`{"type":"response.custom_tool_call_input.delta","output_index":3,"delta":"x"}`,
	`{"type":"response.custom_tool_call_input.delta","output_index":3,"delta":"y"}`,
	`{"type":"response.custom_tool_call_input.done","output_index":3,"input":"xyz"}`,
	`{"type":"response.output_item.done","output_index":3,"item":{"type":"custom_tool_call","id":"ct_1","call_id":"call_2","name":"g","input":"xyz"}}`,
	`[1]`,
	`{"type":"response.unknown"}`,
	`{"type":"response.completed","response":{"id":"resp_1","status":"completed","usage":{"input_tokens":3,"output_tokens":4,"total_tokens":7}}}`,
}

// processResponsesStream publishes before each record only what changed outside a push. Every other handler marks the message, so before
// each record is read the published message equals the producer's, as publishing on every record did.
func TestResponsesRecordsPublishEveryDirectChange(t *testing.T) {
	builder := newAssistantStreamBuilder(context.Background(), APIOpenAIResponses, "openai", "gpt-5")
	builder.start()
	reader := &publicationCheckingReader{t: t, builder: builder}
	for _, record := range responsesRecordsForPublication {
		reader.records = append(reader.records, "data: "+record+"\n\n")
	}
	(&openAIResponsesProvider{cfg: OpenAIResponsesConfig{ProviderID: "openai", Model: "gpt-5"}}).parseResponsesSSE(context.Background(), reader, builder, nil)
	if reader.reads != len(responsesRecordsForPublication) {
		t.Fatalf("read %d of %d records", reader.reads, len(responsesRecordsForPublication))
	}
	if result := builder.stream.Result(); result.StopReason != StopReasonToolUse || len(result.Content) != 4 {
		t.Fatalf("unexpected result: stop %q, %d blocks, error %q", result.StopReason, len(result.Content), result.ErrorMessage)
	}
}
