//go:build !pig_strip_bedrock_converse_stream

package bedrock

import (
	"github.com/MichaelKinsy/PiG/ai"

	"context"
	"net"
	"syscall"
	"testing"

	smithyhttp "github.com/aws/smithy-go/transport/http"

	btypes "github.com/MichaelKinsy/PiG/third_party/awssdk/service/bedrockruntime/types"
)

func TestBedrockRequestTransportFailureMatchesFetchFailure(t *testing.T) {
	transportErr := &smithyhttp.RequestSendError{Err: &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}}
	mapped := mapBedrockTransportError(context.Background(), transportErr, "fetch failed")
	result := &ai.AssistantMessage{StopReason: ai.StopReasonError, ErrorMessage: formatBedrockError(mapped)}
	assertRetryableTransportResult(t, result, "fetch failed")
}

func TestBedrockMidStreamTransportFailureMatchesTerminated(t *testing.T) {
	events := make(chan btypes.ConverseStreamOutput)
	close(events)
	streamErr := &net.OpError{Op: "read", Net: "tcp", Err: syscall.ECONNRESET}
	builder := ai.NewAssistantStreamBuilder(context.Background(), ai.APIBedrockConverseStream, "amazon-bedrock", "test-model")
	provider := &BedrockProvider{}
	go provider.parseBedrockEvents(context.Background(), &fakeBedrockEventStream{events: events, err: streamErr}, builder, "")

	result := builder.Stream().Result()
	assertRetryableTransportResult(t, result, "terminated")
}
