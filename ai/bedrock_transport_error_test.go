//go:build !pig_strip_bedrock_converse_stream

package ai

import (
	"context"
	"net"
	"syscall"
	"testing"

	btypes "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

func TestBedrockRequestTransportFailureMatchesFetchFailure(t *testing.T) {
	transportErr := &smithyhttp.RequestSendError{Err: &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}}
	mapped := mapBedrockTransportError(context.Background(), transportErr, "fetch failed")
	result := &AssistantMessage{StopReason: StopReasonError, ErrorMessage: formatBedrockError(mapped)}
	assertRetryableTransportResult(t, result, "fetch failed")
}

func TestBedrockMidStreamTransportFailureMatchesTerminated(t *testing.T) {
	events := make(chan btypes.ConverseStreamOutput)
	close(events)
	streamErr := &net.OpError{Op: "read", Net: "tcp", Err: syscall.ECONNRESET}
	builder := newAssistantStreamBuilder(context.Background(), APIBedrockConverseStream, "amazon-bedrock", "test-model")
	provider := &BedrockProvider{}
	go provider.parseBedrockEvents(context.Background(), &fakeBedrockEventStream{events: events, err: streamErr}, builder, "")

	result := builder.stream.Result()
	assertRetryableTransportResult(t, result, "terminated")
}
