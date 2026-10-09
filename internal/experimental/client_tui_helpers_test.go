package experimental

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
	"github.com/MichaelKinsy/PiG/internal/chord"
	"github.com/MichaelKinsy/PiG/internal/experimental/services"
	"github.com/MichaelKinsy/PiG/tui"
)

// newClientTuiLoopbackServer ports only the shared fixture at experimental-client-tui.test.ts:274-335. The original three test rows are owned by client_tui_upstream_test.go. The caller owns providers; closeBindings owns the two real Chord namespaces and joins their disposal.
func newClientTuiLoopbackServer(t *testing.T, serverId string, serverProvider, sessionProvider *chord.RemoteServiceProvider, connection *chord.MutableReplicatedState[*services.ServerConnectionState], attachment *chord.MutableReplicatedState[*services.SessionAttachmentState]) (server ClientTuiServer, closeBindings func(context.Context) error) {
	t.Helper()
	createBinding := func(provider *chord.RemoteServiceProvider) *chord.RemoteServiceBinding {
		t.Helper()
		catalogue := provider.Catalogue()
		ids := make([]string, len(catalogue))
		for i, entry := range catalogue {
			ids[i] = entry.ServiceId
		}
		binding, err := chord.CreateRemoteServiceBinding(chord.RemoteServiceBindingOptions{
			Services: chord.ServiceIDs(ids...), Transport: chord.NewLoopbackTransport(provider), Bound: new(false),
			OnError: func(err error) { t.Errorf("loopback service binding: %v", err) },
		})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := binding.Dispose(context.Background()); err != nil {
				t.Errorf("dispose loopback binding: %v", err)
			}
		})
		return binding
	}
	serverBinding := createBinding(serverProvider)
	sessionBinding := createBinding(sessionProvider)
	server = ClientTuiServer{
		ServerId: serverId, Radius: true,
		Server:  &clientTuiLoopbackServerSource{binding: serverBinding, provider: serverProvider, connection: connection},
		Session: &clientTuiLoopbackSessionSource{binding: sessionBinding, attachment: attachment},
	}
	var once sync.Once
	var closeError error
	closeBindings = func(ctx context.Context) error {
		once.Do(func() {
			failures := make([]error, 2)
			var group sync.WaitGroup
			group.Go(func() { failures[0] = serverBinding.Dispose(ctx) })
			group.Go(func() { failures[1] = sessionBinding.Dispose(ctx) })
			group.Wait()
			closeError = errors.Join(failures...)
		})
		return closeError
	}
	return server, closeBindings
}

type clientTuiLoopbackServerSource struct {
	binding    *chord.RemoteServiceBinding
	provider   *chord.RemoteServiceProvider
	connection *chord.MutableReplicatedState[*services.ServerConnectionState]
}

func (*clientTuiLoopbackServerSource) AcceptsUnavailableServices() bool { return false }
func (source *clientTuiLoopbackServerSource) Catalogue(context.Context) ([]chord.ServiceCatalogueEntry, error) {
	return source.provider.Catalogue(), nil
}
func (source *clientTuiLoopbackServerSource) Open(chord.RemoteServiceSourceOpenOptions) (chord.RemoteServices, error) {
	return &clientTuiLoopbackScope{RemoteServiceBinding: source.binding, rebind: true}, nil
}
func (source *clientTuiLoopbackServerSource) Connection() chord.ReplicatedStateOf[*services.ServerConnectionState] {
	return source.connection
}
func (source *clientTuiLoopbackServerSource) Dispose(ctx context.Context) error {
	return source.binding.Dispose(ctx)
}

type clientTuiLoopbackSessionSource struct {
	binding    *chord.RemoteServiceBinding
	attachment *chord.MutableReplicatedState[*services.SessionAttachmentState]
}

func (*clientTuiLoopbackSessionSource) AcceptsUnavailableServices() bool { return true }
func (*clientTuiLoopbackSessionSource) Catalogue(context.Context) ([]chord.ServiceCatalogueEntry, error) {
	return []chord.ServiceCatalogueEntry{}, nil
}
func (source *clientTuiLoopbackSessionSource) Open(chord.RemoteServiceSourceOpenOptions) (chord.RemoteServices, error) {
	return &clientTuiLoopbackScope{RemoteServiceBinding: source.binding}, nil
}
func (source *clientTuiLoopbackSessionSource) Attachment() chord.ReplicatedStateOf[*services.SessionAttachmentState] {
	return source.attachment
}
func (source *clientTuiLoopbackSessionSource) Dispose(ctx context.Context) error {
	return source.binding.Dispose(ctx)
}
func (source *clientTuiLoopbackSessionSource) WhenAttached(ctx context.Context, sessionId string) error {
	if err := source.binding.Rebind(ctx, true); err != nil {
		return err
	}
	if err := source.binding.Ready(ctx); err != nil {
		return err
	}
	return source.attachment.Replace(ctx, &services.SessionAttachmentState{Status: "attached", SessionID: sessionId})
}
func (source *clientTuiLoopbackSessionSource) WhenDetached(ctx context.Context) error {
	if err := source.binding.Rebind(ctx, false); err != nil {
		return err
	}
	return source.attachment.Replace(ctx, &services.SessionAttachmentState{Status: "detached"})
}

// Scopes borrow their namespace; the fixture's closeBindings owns actual disposal, as in the upstream open() fixture.
type clientTuiLoopbackScope struct {
	*chord.RemoteServiceBinding
	rebind bool
}

func (scope *clientTuiLoopbackScope) Ready(ctx context.Context) error {
	if scope.rebind {
		if err := scope.Rebind(ctx, true); err != nil {
			return err
		}
	}
	return scope.RemoteServiceBinding.Ready(ctx)
}
func (*clientTuiLoopbackScope) Dispose(context.Context) error { return nil }

// clientTuiObservation uses the same executor as RunClientTui. Notifications only wake an observation; snapshots and input always cross the real owner-loop completion barrier.
type clientTuiObservation struct {
	Executor *ClientTuiExecutor
	changed  chan struct{}
	requests atomic.Uint64
}

func newClientTuiObservation(t *testing.T) *clientTuiObservation {
	t.Helper()
	observation := &clientTuiObservation{Executor: NewClientTuiExecutor(), changed: make(chan struct{}, 1)}
	t.Cleanup(observation.Executor.Close)
	return observation
}

func (observation *clientTuiObservation) RequestRender() {
	observation.requests.Add(1)
	// Coalesce wakeups, not render counts. A retained wakeup observes the latest owner state.
	select {
	case observation.changed <- struct{}{}:
	default:
	}
}

func (observation *clientTuiObservation) Requests() uint64 { return observation.requests.Load() }

func (observation *clientTuiObservation) Render(ctx context.Context, component tui.Component, width int) ([]string, error) {
	var lines []string
	err := observation.Executor.RunOnMain(ctx, func() { lines = component.Render(width) })
	return lines, err
}

func (observation *clientTuiObservation) HandleInput(ctx context.Context, component tui.InputHandler, data string) error {
	return observation.Executor.RunOnMain(ctx, func() { component.HandleInput(data) })
}

func (observation *clientTuiObservation) HandleInputBatch(ctx context.Context, component tui.InputHandler, data ...string) error {
	return observation.Executor.RunOnMain(ctx, func() {
		for _, chunk := range data {
			component.HandleInput(chunk)
		}
	})
}

// PublishAndRender captures the original test's immediate observation inside the same owner turn as its state publication. RunOnMain returns only after the real microtask checkpoint; the returned lines retain the pre-checkpoint observation.
func (observation *clientTuiObservation) PublishAndRender(ctx context.Context, component tui.Component, width int, publish func() error) ([]string, error) {
	var lines []string
	var failure error
	err := observation.Executor.RunOnMain(ctx, func() {
		failure = publish()
		if failure == nil {
			lines = component.Render(width)
		}
	})
	return lines, errors.Join(failure, err)
}

func (observation *clientTuiObservation) Wait(ctx context.Context, component tui.Component, width int, predicate func([]string) bool) ([]string, error) {
	for {
		lines, err := observation.Render(ctx, component, width)
		if err != nil || predicate(lines) {
			return lines, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-observation.changed:
		}
	}
}

// userEntry, assistantEntry and conversationView build the durable ConversationView the chat view renders (packages/durable/src/harness/view.ts).
func userEntry(id durable.EntryId, text string) durable.EntryRecord {
	return durable.EntryRecord{Id: id, ConversationId: durable.ROOT_CONVERSATION_ID, Kind: "pi.user", Model: []ai.Message{ai.UserMessage{Content: ai.UserText(text)}}}
}

func assistantEntry(id durable.EntryId, message ai.AssistantMessage) durable.EntryRecord {
	return durable.EntryRecord{Id: id, ConversationId: durable.ROOT_CONVERSATION_ID, Kind: "pi.assistant", Model: []ai.Message{message}}
}

func resetEntry(id durable.EntryId) durable.EntryRecord {
	return durable.EntryRecord{Id: id, ConversationId: durable.ROOT_CONVERSATION_ID, Kind: "pi.reset"}
}

// conversationView is a view with the given entries and documents; a nil document is absent.
func conversationView(entries []durable.EntryRecord, live *harness.LiveState, inbox *harness.InboxState) services.ConversationView {
	docs := harness.ViewDocs{}
	for _, mounted := range []struct {
		kind     string
		document any
	}{{services.LiveDocKind, live}, {services.InboxDocKind, inbox}} {
		if reflect.ValueOf(mounted.document).IsNil() {
			continue
		}
		docs = harness.ViewDocsOf(docs, mounted.kind, jsonObjectOf(mounted.document))
	}
	return services.ConversationView{Conversation: durable.ConversationRecord{Id: durable.ROOT_CONVERSATION_ID}, Entries: entries, Docs: docs}
}

func jsonObjectOf(value any) durable.JsonObject {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	var object durable.JsonObject
	if err := json.Unmarshal(encoded, &object); err != nil {
		panic(err)
	}
	return object
}

func generationOf(message ai.AssistantMessage) *harness.LiveGeneration {
	return &harness.LiveGeneration{Message: &message}
}
