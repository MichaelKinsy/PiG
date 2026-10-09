package chord

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sync"
	"testing"
)

// observeRemoteClientService is a typed client view over a remote keyed service instance.
type observeRemoteClientService struct{ remote *RemoteService }

func (client observeRemoteClientService) M(ctx context.Context) (string, error) {
	raw, err := client.remote.Call(ctx, "m")
	if err != nil {
		return "", err
	}
	var answer string
	err = json.Unmarshal(raw, &answer)
	return answer, err
}

// observeRemoteClientImplementation is one provider-side instance.
type observeRemoteClientImplementation struct{ tag string }

func (implementation *observeRemoteClientImplementation) M(context.Context) (string, error) {
	return implementation.tag, nil
}

var observeRemoteClientServices = sync.OnceValues(func() (ServiceDefinition[observeRemoteClientService], ServiceDefinition[*observeRemoteClientImplementation]) {
	client := DefineService[observeRemoteClientService]("orc.k")
	RegisterRemoteClient(client, func(remote *RemoteService) observeRemoteClientService { return observeRemoteClientService{remote} })
	return client, DefineService[*observeRemoteClientImplementation]("orc.k")
})

// Pi packages/chord/src/services/consumer.ts:496-518 RemoteServices.observe<T>(service, handler) and :640-641 #assertRemotable,
// measured on the installed chord 1.1.0 through a loopback binding: a process-local service is rejected with service_not_allowed
// "Service X is process-local" before the allowlist check, an unallowlisted one with "Remote service X is not allowlisted"; the
// handler receives each live instance's typed view, a handler failure goes to onError, and after stop (idempotent) a new instance is
// not delivered. Pi answers ["a0","b0"] with errors ["handler failed"].
func TestObserveRemoteClientMatchesPi(t *testing.T) {
	client, implementation := observeRemoteClientServices()
	provider, err := NewRemoteServiceProvider(KeyedService(implementation))
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var reported []string
	binding, err := CreateRemoteServiceBinding(RemoteServiceBindingOptions{
		Services:  ServiceIDs(client.Id(), "orc.o"),
		Transport: NewLoopbackTransport(provider),
		OnError: func(err error) {
			mu.Lock()
			reported = append(reported, err.Error())
			mu.Unlock()
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ignore := func(context.Context, observeRemoteClientService) error { return nil }
	for _, check := range []struct {
		service ServiceDefinition[observeRemoteClientService]
		want    string
	}{
		{DefineService[observeRemoteClientService]("orc.l", ServiceOptions{Local: true}), "Service orc.l is process-local"},
		{DefineService[observeRemoteClientService]("orc.z", ServiceOptions{Local: true}), "Service orc.z is process-local"},
		{DefineService[observeRemoteClientService]("orc.z"), "Remote service orc.z is not allowlisted"},
	} {
		if _, err := ObserveRemoteClient(binding, check.service, ignore); !hasRemoteServiceErrorCode(err, ErrServiceNotAllowed) || err.Error() != check.want {
			t.Errorf("ObserveRemoteClient(%s) = %v, want service_not_allowed %s", check.service.Id(), err, check.want)
		}
	}

	ctx := t.Context()
	if _, err := Spawn(provider, implementation, "a", &observeRemoteClientImplementation{"a0"}); err != nil {
		t.Fatal(err)
	}
	var seen []string
	stop, err := ObserveRemoteClient(binding, client, func(ctx context.Context, view observeRemoteClientService) error {
		answer, err := view.M(ctx)
		if err != nil {
			return err
		}
		mu.Lock()
		seen = append(seen, answer)
		count := len(seen)
		mu.Unlock()
		if count == 2 {
			return errors.New("handler failed")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := binding.Ready(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := Spawn(provider, implementation, "b", &observeRemoteClientImplementation{"b0"}); err != nil {
		t.Fatal(err)
	}
	stop()
	stop()
	if _, err := Spawn(provider, implementation, "c", &observeRemoteClientImplementation{"c0"}); err != nil {
		t.Fatal(err)
	}
	quiesceBinding(binding)
	mu.Lock()
	defer mu.Unlock()
	if !slices.Equal(seen, []string{"a0", "b0"}) || !slices.Equal(reported, []string{"handler failed"}) {
		t.Fatalf("seen %q, reported %q; Pi sees [a0 b0] and reports [handler failed]", seen, reported)
	}
}
