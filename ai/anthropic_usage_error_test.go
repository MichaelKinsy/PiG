package ai

import (
	"context"
	"io"
	"strings"
	"testing"
)

// cancelAtUsageEOF makes cancellation occur after the complete initial usage
// event has been consumed, without relying on a timer or a live provider.
type cancelAtUsageEOF struct {
	io.Reader
	cancel context.CancelFunc
}

func (r cancelAtUsageEOF) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	if err == io.EOF {
		r.cancel()
	}
	return n, err
}

func TestAnthropicFailureRetainsBilledUsage(t *testing.T) {
	const initial = `event: message_start
data: {"message":{"id":"m","model":"model","usage":{"input_tokens":100,"output_tokens":0}}}

`
	for _, tc := range []struct {
		name, tail string
		abort      bool
		reason     StopReason
	}{
		{"provider_error", `event: error
data: {"type":"error","error":{"type":"overloaded_error","message":"overloaded"}}

`, false, StopReasonError},
		{"truncated", "", false, StopReasonError},
		{"aborted", "", true, StopReasonAborted},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			b := newAssistantStreamBuilder(ctx, APIAnthropicMessages, "anthropic", "model")
			b.modelCost = ModelCost{Input: 3}
			var reader io.Reader = strings.NewReader(initial + tc.tail)
			if tc.abort {
				reader = cancelAtUsageEOF{reader, cancel}
			}
			p := &anthropicProvider{}
			go p.parseAnthropicSSE(ctx, reader, b, anthropicStreamNames{})
			got := b.stream.Result()
			if got.StopReason != tc.reason || got.Usage.Input != 100 || got.Usage.Cost.Total != 0.00030000000000000003 {
				t.Fatalf("billed usage lost on %s: %+v", tc.name, got)
			}
		})
	}
}
