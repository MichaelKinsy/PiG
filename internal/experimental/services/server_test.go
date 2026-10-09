package services

// pi: packages/coding-agent/src/experimental/services/server.ts

import (
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/chord"
)

type testServerPresentation struct {
	attach  func(context.Context, string) error
	detach  func(context.Context) error
	prepare func(context.Context, string) error
}

func (p testServerPresentation) AttachSession(ctx context.Context, id string) error {
	if p.attach != nil {
		return p.attach(ctx, id)
	}
	return nil
}
func (p testServerPresentation) DetachSession(ctx context.Context) error {
	if p.detach != nil {
		return p.detach(ctx)
	}
	return nil
}
func (p testServerPresentation) PrepareSessionRemoval(ctx context.Context, id string) error {
	if p.prepare != nil {
		return p.prepare(ctx, id)
	}
	return nil
}

type testServerEndpoint struct {
	attachment *RoutedServerServiceAttachment
}

func (e testServerEndpoint) Invoke(ctx context.Context, call chord.ServiceCall, publish chord.ServiceUpdatePublisher) (json.RawMessage, error) {
	return e.attachment.InvokeService(ctx, call, publish)
}
func (e testServerEndpoint) Dispose() {
	if err := e.attachment.Release(context.Background()); err != nil {
		panic(err)
	}
}

func serverCall(ctx context.Context, attachment *RoutedServerServiceAttachment, id, member string, args ...any) (json.RawMessage, error) {
	encoded := make([]json.RawMessage, len(args))
	for i, arg := range args {
		value, err := json.Marshal(arg)
		if err != nil {
			return nil, err
		}
		encoded[i] = value
	}
	return attachment.InvokeService(ctx, chord.ServiceCall{ServiceId: id, Member: member, Args: encoded}, func(context.Context, string, chord.ServiceProviderUpdate) error { return nil })
}

// The unchanged pinned server.ts and Chord provider/endpoint/replica produce the independent expected trace.
func TestServerServicesMatchPinnedUpstream(t *testing.T) {
	root, err := filepath.Abs("../../..")
	requireModelsOK(t, err)
	output, err := exec.CommandContext(t.Context(), "node", "--experimental-transform-types", "testdata/server-oracle.mjs", root).Output()
	if err != nil {
		t.Fatalf("oracle: %v (%s)", err, output)
	}
	var expected any
	requireModelsOK(t, json.Unmarshal(output, &expected))
	events, values, failures := []string{}, []any{}, []string{}
	selections := []PrepareSessionPluginsRequest{}
	sessions := []SessionSummary{{SessionAddress{"server", "b"}, 12}, {SessionAddress{"server", "a"}, 12}}
	failDetach := false
	services, err := CreateExperimentalServerServices(ExperimentalServerServicesOptions{
		List: func(context.Context) ([]SessionSummary, error) {
			events = append(events, "list")
			return slices.Clone(sessions), nil
		},
		Create: func(_ context.Context, options SessionCreateOptions) (SessionSummary, error) {
			events = append(events, "create")
			id := "generated"
			if options.Id != nil {
				id = *options.Id
			}
			created := SessionSummary{SessionAddress{"server", id}, 23}
			sessions = append(sessions, created)
			return created, nil
		},
		Remove: func(_ context.Context, id string) error {
			events = append(events, "remove:"+id)
			sessions = slices.DeleteFunc(sessions, func(s SessionSummary) bool { return s.SessionId == id })
			return nil
		},
		PrepareSessionPlugins: func(_ context.Context, id string, paths []string) (PreparedSessionPlugins, error) {
			selections = append(selections, PrepareSessionPluginsRequest{id, slices.Clone(paths)})
			if id == "bad" {
				return PreparedSessionPlugins{}, errors.New("prepare failed")
			}
			if paths == nil {
				paths = []string{"default"}
			}
			return PreparedSessionPlugins{PackagePaths: paths, PresentationPlugins: map[string]any{"prepared": id}}, nil
		},
		ReloadPresentationPlugins: func(_ context.Context, paths []string) (chord.JsonValue, error) { return slices.Clone(paths), nil },
	})
	requireModelsOK(t, err)
	t.Cleanup(func() { requireModelsOK(t, services.Dispose()) })
	presentation := testServerPresentation{
		attach: func(_ context.Context, id string) error { events = append(events, "attach:"+id); return nil },
		detach: func(context.Context) error {
			events = append(events, "detach")
			if failDetach {
				return errors.New("detach failed")
			}
			return nil
		},
		prepare: func(_ context.Context, id string) error { events = append(events, "prepareRemoval:"+id); return nil },
	}
	a, err := services.Host.AttachClient(t.Context(), presentation)
	requireModelsOK(t, err)
	b, err := services.Host.AttachClient(t.Context(), presentation)
	requireModelsOK(t, err)
	transport := chord.NewJSONCopyTransport(testServerEndpoint{a})
	catalogue, err := transport.Invoke(t.Context(), chord.CreateServiceCatalogueCall())
	requireModelsOK(t, err)
	binding, err := chord.CreateRemoteServiceBinding(chord.RemoteServiceBindingOptions{Services: chord.ServiceIDs(SessionDirectoryDefinition.Id()), Transport: transport, OnError: func(err error) { t.Error(err) }})
	requireModelsOK(t, err)
	directory, err := chord.UseRemote(binding, SessionDirectoryDefinition)
	requireModelsOK(t, err)
	requireModelsOK(t, binding.Ready(t.Context()))
	replica, err := directory.State("state")
	requireModelsOK(t, err)
	snapshots := []*SessionDirectoryState{}
	unsubscribe, err := chord.TypedReplica[*SessionDirectoryState](replica).Subscribe(func(value *SessionDirectoryState, _ context.Context, _ chord.ReplicatedStateDelivery) {
		snapshots = append(snapshots, value)
	})
	requireModelsOK(t, err)
	bUpdates := 0
	_, err = b.InvokeService(t.Context(), chord.CreateServiceSubscribeCall("same", SessionDirectoryDefinition.Id(), chord.ServiceSingleton), func(context.Context, string, chord.ServiceProviderUpdate) error { bUpdates++; return nil })
	requireModelsOK(t, err)
	record := func(attachment *RoutedServerServiceAttachment, id, member string, args ...any) {
		result, err := serverCall(t.Context(), attachment, id, member, args...)
		if err != nil {
			failures = append(failures, err.Error())
			return
		}
		var value any
		if result != nil {
			requireModelsOK(t, json.Unmarshal(result, &value))
		}
		values = append(values, value)
	}
	plugins, management := PresentationPluginsDefinition.Id(), SessionManagementDefinition.Id()
	record(b, plugins, "reload")
	record(a, plugins, "prepareSession", PrepareSessionPluginsRequest{"a", nil})
	record(a, plugins, "reload")
	record(b, plugins, "prepareSession", PrepareSessionPluginsRequest{"b", []string{}})
	record(b, plugins, "reload")
	record(a, plugins, "prepareSession", PrepareSessionPluginsRequest{"a", []string{"x", "x", "é"}})
	record(a, plugins, "prepareSession", PrepareSessionPluginsRequest{"bad", []string{"bad"}})
	record(a, plugins, "reload")
	failDetach = true
	record(a, management, "detach")
	record(a, plugins, "reload")
	failDetach = false
	record(a, management, "detach")
	record(a, plugins, "reload")
	record(b, plugins, "reload")
	empty := ""
	record(a, management, "create", SessionCreateOptions{Id: &empty})
	record(a, management, "attach", "b")
	record(a, management, "remove", "a")
	requireModelsOK(t, b.Release(t.Context()))
	requireModelsOK(t, services.Refresh(context.Background()))
	record(b, plugins, "reload")
	unsubscribe()
	requireModelsOK(t, binding.Dispose(t.Context()))
	requireModelsOK(t, services.Dispose())
	record(a, plugins, "reload")
	actual := map[string]any{"catalogue": json.RawMessage(catalogue), "values": values, "failures": failures, "selections": selections, "snapshots": snapshots, "bUpdates": bUpdates, "events": events}
	encoded, err := json.Marshal(actual)
	requireModelsOK(t, err)
	var decoded any
	requireModelsOK(t, json.Unmarshal(encoded, &decoded))
	checkModelsEqual(t, decoded, expected)
}

// A TypeScript service member applies its synchronous prefix during invoke, so mutations from one connection join
// serviceMutationTail in call order (packages/chord/src/services/provider.ts:234; experimental/server-services.ts mutation tail).
// The attachment exposes that admission boundary: begun in sequence, mutations run in sequence, whatever the scheduler does.
func TestServerServicesAdmitMutationsInCallOrder(t *testing.T) {
	const calls = 400
	var mu sync.Mutex
	var order []string
	services, err := CreateExperimentalServerServices(ExperimentalServerServicesOptions{
		List: func(context.Context) ([]SessionSummary, error) { return nil, nil },
		Create: func(_ context.Context, options SessionCreateOptions) (SessionSummary, error) {
			mu.Lock()
			order = append(order, *options.Id)
			mu.Unlock()
			return SessionSummary{SessionAddress{"server", *options.Id}, 1}, nil
		},
		Remove: func(context.Context, string) error { return nil },
		PrepareSessionPlugins: func(context.Context, string, []string) (PreparedSessionPlugins, error) {
			return PreparedSessionPlugins{}, nil
		},
		ReloadPresentationPlugins: func(context.Context, []string) (chord.JsonValue, error) { return nil, nil },
	})
	requireModelsOK(t, err)
	t.Cleanup(func() { requireModelsOK(t, services.Dispose()) })
	attachment, err := services.Host.AttachClient(t.Context(), testServerPresentation{})
	requireModelsOK(t, err)
	initiator, ok := any(attachment).(interface {
		BeginInvokeService(context.Context, chord.ServiceCall, chord.ServiceUpdatePublisher) (*chord.ServiceInvocation, error)
	})
	if !ok {
		t.Fatal("RoutedServerServiceAttachment has no admission boundary (BeginInvokeService)")
	}
	invocations := make([]*chord.ServiceInvocation, calls)
	for i := range invocations {
		argument, _ := json.Marshal(map[string]any{"id": strconv.Itoa(i)})
		invocations[i], err = initiator.BeginInvokeService(t.Context(), chord.ServiceCall{ServiceId: "pi.session-management", Member: "create", Args: []json.RawMessage{argument}}, nil)
		requireModelsOK(t, err)
	}
	for _, invocation := range invocations {
		_, err := invocation.Wait(t.Context())
		requireModelsOK(t, err)
	}
	for i, id := range order {
		if id != strconv.Itoa(i) {
			t.Fatalf("position %d ran mutation %s; order %v", i, id, order[:min(len(order), i+6)])
		}
	}
	if len(order) != calls {
		t.Fatalf("%d mutations ran, want %d", len(order), calls)
	}
}
