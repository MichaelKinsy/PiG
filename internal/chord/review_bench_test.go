package chord

import (
	"context"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent/harness/pico3"
)

type reviewBenchState struct {
	Count   int    `json:"count"`
	Payload string `json:"payload"`
}

type reviewBenchService interface {
	Increment(context.Context) (int, error)
	State() pico3.ReplicatedStateOf[*reviewBenchState]
}

type reviewBenchCounter struct {
	state *MutableReplicatedState[*reviewBenchState]
}

func (counter *reviewBenchCounter) State() pico3.ReplicatedStateOf[*reviewBenchState] {
	return counter.state
}
func (counter *reviewBenchCounter) Increment(ctx context.Context) (int, error) {
	var count int
	err := counter.state.Change(ctx, func(draft *reviewBenchState) error {
		draft.Count++
		count = draft.Count
		return nil
	})
	return count, err
}

func BenchmarkReviewJSONCopyIncrement(b *testing.B) {
	ctx := context.Background()
	def := pico3.DefineService[reviewBenchService]("review.benchmark")
	state, err := NewReplicatedState(&reviewBenchState{Payload: strings.Repeat("x", 64*1024)})
	if err != nil {
		b.Fatal(err)
	}
	provider, err := NewRemoteServiceProvider(SingletonService(def))
	if err != nil {
		b.Fatal(err)
	}
	if err := Provide[reviewBenchService](provider, def, &reviewBenchCounter{state: state}); err != nil {
		b.Fatal(err)
	}
	endpoint := CreateRemoteServiceEndpoint(provider)
	binding, err := CreateRemoteServiceBinding(RemoteServiceBindingOptions{Services: []string{def.Id()}, Transport: NewJSONCopyTransport(endpoint)})
	if err != nil {
		b.Fatal(err)
	}
	service, err := UseRemote(binding, def)
	if err != nil {
		b.Fatal(err)
	}
	if err := binding.Ready(ctx); err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		if err := binding.Dispose(ctx); err != nil {
			b.Error(err)
		}
		endpoint.Dispose()
		if err := provider.Dispose(); err != nil {
			b.Error(err)
		}
	})
	b.ReportAllocs()
	for b.Loop() {
		if _, err := CallResult[int](ctx, service, "increment"); err != nil {
			b.Fatal(err)
		}
	}
}
