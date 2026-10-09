package services

// pi: packages/coding-agent/src/experimental/services/sessions.ts
// pi: packages/coding-agent/src/experimental/services/plugins.ts

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/chord"
)

// upstream: packages/coding-agent/src/experimental/services/{agent-controller,plugins,sessions}.ts declare each service's members. The typed remote clients and the guarded views must reach exactly the provider member of the same name with the caller's arguments, and return the provider's value or error unchanged.

type recordedCall struct {
	member string
	args   []any
}

type recorder struct {
	calls []recordedCall
	fail  error
}

func (r *recorder) record(member string, args ...any) error {
	r.calls = append(r.calls, recordedCall{member, args})
	return r.fail
}

type recordingController struct{ *recorder }

func (c recordingController) Prompt(_ context.Context, request AgentPromptRequest) (AgentOperationResponse, error) {
	return AgentOperationResponse{Accepted: true, OperationID: new("prompt-op")}, c.record("prompt", request)
}
func (c recordingController) Steer(_ context.Context, request AgentPromptRequest) (AgentQueueResponse, error) {
	return AgentQueueResponse{Accepted: true, EntryID: new("steer-entry")}, c.record("steer", request)
}
func (c recordingController) FollowUp(_ context.Context, request AgentPromptRequest) (AgentQueueResponse, error) {
	return AgentQueueResponse{Accepted: true, EntryID: new("follow-entry")}, c.record("followUp", request)
}
func (c recordingController) CancelQueued(_ context.Context, id string) (AgentCancelQueuedResponse, error) {
	return AgentCancelQueuedResponse{Outcome: "not_found"}, c.record("cancelQueued", id)
}
func (c recordingController) Abort(context.Context) error { return c.record("abort") }
func (c recordingController) Compact(_ context.Context, request AgentCompactionRequest) (AgentOperationResponse, error) {
	return AgentOperationResponse{Accepted: true, OperationID: new("compact-op")}, c.record("compact", request)
}
func (c recordingController) WaitForPrompt(_ context.Context, id string) (AgentPromptResult, error) {
	return AgentPromptResult{Status: "done", Text: new("answer")}, c.record("waitForPrompt", id)
}

type recordingPresentationPlugins struct{ *recorder }

func (p recordingPresentationPlugins) PrepareSession(_ context.Context, request PrepareSessionPluginsRequest) (chord.JsonValue, error) {
	return map[string]any{"prepared": request.SessionId}, p.record("prepareSession", request)
}
func (p recordingPresentationPlugins) Reload(context.Context) (chord.JsonValue, error) {
	return []any{"reloaded"}, p.record("reload")
}

type recordingSessionPlugins struct{ *recorder }

func (p recordingSessionPlugins) Reload(context.Context) error { return p.record("reload") }

type recordingSessionManagement struct{ *recorder }

func (m recordingSessionManagement) Create(_ context.Context, options SessionCreateOptions) (SessionSummary, error) {
	return SessionSummary{SessionAddress{"server", "created"}, 7}, m.record("create", options)
}
func (m recordingSessionManagement) Remove(_ context.Context, id string) error {
	return m.record("remove", id)
}
func (m recordingSessionManagement) Attach(_ context.Context, id string) error {
	return m.record("attach", id)
}
func (m recordingSessionManagement) Detach(context.Context) error { return m.record("detach") }

func remoteClientFor[T any](t *testing.T, definition chord.ServiceDefinition[T], implementation T) T {
	t.Helper()
	provider, err := chord.NewRemoteServiceProvider(chord.SingletonService(definition))
	requireModelsOK(t, err)
	t.Cleanup(func() { requireModelsOK(t, provider.Dispose()) })
	requireModelsOK(t, chord.Provide[T](provider, definition, implementation))
	binding, err := chord.CreateRemoteServiceBinding(chord.RemoteServiceBindingOptions{Services: chord.ServiceIDs(definition.Id()), Transport: chord.NewLoopbackTransport(provider), OnError: func(err error) { t.Error(err) }})
	requireModelsOK(t, err)
	t.Cleanup(func() { requireModelsOK(t, binding.Dispose(context.Background())) })
	client, err := chord.UseRemoteClient(binding, definition)
	requireModelsOK(t, err)
	requireModelsOK(t, binding.Ready(t.Context()))
	return client
}

type clientCase struct {
	member string
	want   []any
	call   func(context.Context) (any, error)
	result any
}

func runClientCases(t *testing.T, rec *recorder, cases []clientCase) {
	t.Helper()
	for _, test := range cases {
		t.Run(test.member, func(t *testing.T) {
			rec.calls, rec.fail = nil, nil
			got, err := test.call(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, test.result) {
				t.Errorf("result = %#v, want %#v", got, test.result)
			}
			if len(rec.calls) != 1 || rec.calls[0].member != test.member || !reflect.DeepEqual(rec.calls[0].args, test.want) {
				t.Fatalf("provider calls = %#v, want one %s%v", rec.calls, test.member, test.want)
			}
			rec.calls, rec.fail = nil, errors.New("provider failed")
			if _, err := test.call(t.Context()); err == nil || err.Error() != "provider failed" {
				t.Fatalf("provider failure = %v", err)
			}
		})
	}
}

func runResolveFailure(t *testing.T, rec *recorder, resolveFailure error, cases []clientCase) {
	t.Helper()
	for _, test := range cases {
		rec.calls = nil
		if _, err := test.call(t.Context()); !errors.Is(err, resolveFailure) || len(rec.calls) != 0 {
			t.Errorf("%s: err=%v calls=%v", test.member, err, rec.calls)
		}
	}
}

func agentControllerCases(controller AgentController) []clientCase {
	prompt := AgentPromptRequest{Message: "hello", Images: []AgentPromptImage{{Type: "image", Data: "AA==", MimeType: "image/png"}}}
	compaction := AgentCompactionRequest{CustomInstructions: new("shorter")}
	return []clientCase{
		{"prompt", []any{prompt}, func(ctx context.Context) (any, error) { return controller.Prompt(ctx, prompt) }, AgentOperationResponse{Accepted: true, OperationID: new("prompt-op")}},
		{"steer", []any{prompt}, func(ctx context.Context) (any, error) { return controller.Steer(ctx, prompt) }, AgentQueueResponse{Accepted: true, EntryID: new("steer-entry")}},
		{"followUp", []any{prompt}, func(ctx context.Context) (any, error) { return controller.FollowUp(ctx, prompt) }, AgentQueueResponse{Accepted: true, EntryID: new("follow-entry")}},
		{"cancelQueued", []any{"entry"}, func(ctx context.Context) (any, error) { return controller.CancelQueued(ctx, "entry") }, AgentCancelQueuedResponse{Outcome: "not_found"}},
		{"abort", nil, func(ctx context.Context) (any, error) { return nil, controller.Abort(ctx) }, nil},
		{"compact", []any{compaction}, func(ctx context.Context) (any, error) { return controller.Compact(ctx, compaction) }, AgentOperationResponse{Accepted: true, OperationID: new("compact-op")}},
		{"waitForPrompt", []any{"prompt-op"}, func(ctx context.Context) (any, error) { return controller.WaitForPrompt(ctx, "prompt-op") }, AgentPromptResult{Status: "done", Text: new("answer")}},
	}
}

func TestAgentControllerRemoteClientAndViewReachProviderMembers(t *testing.T) {
	rec := &recorder{}
	implementation := recordingController{rec}
	t.Run("remote", func(t *testing.T) {
		runClientCases(t, rec, agentControllerCases(remoteClientFor(t, AgentControllerDefinition, AgentController(implementation))))
	})
	t.Run("view", func(t *testing.T) {
		runClientCases(t, rec, agentControllerCases(agentControllerView{resolve: func() (AgentController, error) { return implementation, nil }}))
	})
	t.Run("view resolve failure", func(t *testing.T) {
		resolveFailure := errors.New("not selected")
		runResolveFailure(t, rec, resolveFailure, agentControllerCases(agentControllerView{resolve: func() (AgentController, error) { return nil, resolveFailure }}))
	})
}

func presentationPluginsCases(plugins PresentationPlugins) []clientCase {
	request := PrepareSessionPluginsRequest{SessionId: "session", PackagePaths: []string{"a", "b"}}
	return []clientCase{
		{"prepareSession", []any{request}, func(ctx context.Context) (any, error) { return plugins.PrepareSession(ctx, request) }, map[string]any{"prepared": "session"}},
		{"reload", nil, func(ctx context.Context) (any, error) { return plugins.Reload(ctx) }, []any{"reloaded"}},
	}
}

func sessionPluginsCases(plugins SessionPlugins) []clientCase {
	return []clientCase{{"reload", nil, func(ctx context.Context) (any, error) { return nil, plugins.Reload(ctx) }, nil}}
}

func TestPluginRemoteClientsAndViewsReachProviderMembers(t *testing.T) {
	rec := &recorder{}
	presentation, session := recordingPresentationPlugins{rec}, recordingSessionPlugins{rec}
	resolveFailure := errors.New("not selected")
	t.Run("presentation remote", func(t *testing.T) {
		runClientCases(t, rec, presentationPluginsCases(remoteClientFor(t, PresentationPluginsDefinition, PresentationPlugins(presentation))))
	})
	t.Run("presentation view", func(t *testing.T) {
		runClientCases(t, rec, presentationPluginsCases(presentationPluginsView{resolve: func() (PresentationPlugins, error) { return presentation, nil }}))
	})
	t.Run("session remote", func(t *testing.T) {
		runClientCases(t, rec, sessionPluginsCases(remoteClientFor(t, SessionPluginsDefinition, SessionPlugins(session))))
	})
	t.Run("session view", func(t *testing.T) {
		runClientCases(t, rec, sessionPluginsCases(sessionPluginsView{resolve: func() (SessionPlugins, error) { return session, nil }}))
	})
	t.Run("view resolve failure", func(t *testing.T) {
		runResolveFailure(t, rec, resolveFailure, presentationPluginsCases(presentationPluginsView{resolve: func() (PresentationPlugins, error) { return nil, resolveFailure }}))
		runResolveFailure(t, rec, resolveFailure, sessionPluginsCases(sessionPluginsView{resolve: func() (SessionPlugins, error) { return nil, resolveFailure }}))
	})
}

func sessionManagementCases(management SessionManagement) []clientCase {
	options := SessionCreateOptions{Id: new("chosen")}
	return []clientCase{
		{"create", []any{options}, func(ctx context.Context) (any, error) { return management.Create(ctx, options) }, SessionSummary{SessionAddress{"server", "created"}, 7}},
		{"remove", []any{"gone"}, func(ctx context.Context) (any, error) { return nil, management.Remove(ctx, "gone") }, nil},
		{"attach", []any{"next"}, func(ctx context.Context) (any, error) { return nil, management.Attach(ctx, "next") }, nil},
		{"detach", nil, func(ctx context.Context) (any, error) { return nil, management.Detach(ctx) }, nil},
	}
}

func TestSessionManagementRemoteClientAndViewReachProviderMembers(t *testing.T) {
	rec := &recorder{}
	implementation := recordingSessionManagement{rec}
	t.Run("remote", func(t *testing.T) {
		runClientCases(t, rec, sessionManagementCases(remoteClientFor(t, SessionManagementDefinition, SessionManagement(implementation))))
	})
	t.Run("view", func(t *testing.T) {
		runClientCases(t, rec, sessionManagementCases(sessionManagementView{resolve: func() (SessionManagement, error) { return implementation, nil }}))
	})
	t.Run("view resolve failure", func(t *testing.T) {
		resolveFailure := errors.New("not selected")
		runResolveFailure(t, rec, resolveFailure, sessionManagementCases(sessionManagementView{resolve: func() (SessionManagement, error) { return nil, resolveFailure }}))
	})
}

// The directory exposes only replicated state: the remote client hydrates the provider's value and follows its replacements; the view reads through to the selected provider.
func TestSessionDirectoryRemoteClientAndViewFollowProviderState(t *testing.T) {
	state, err := chord.NewReplicatedState(&SessionDirectoryState{Revision: 1, Sessions: []SessionSummary{{SessionAddress{"server", "a"}, 1}}})
	requireModelsOK(t, err)
	directory := serverSessionDirectory{state: state}
	remote := remoteClientFor(t, SessionDirectoryDefinition, SessionDirectory(directory))
	view := sessionDirectoryView{resolve: func() (SessionDirectory, error) { return directory, nil }}
	for name, source := range map[string]SessionDirectory{"remote": remote, "view": view} {
		t.Run(name, func(t *testing.T) {
			var seen []*SessionDirectoryState
			stop, err := source.State().Subscribe(func(value *SessionDirectoryState, _ context.Context, _ chord.ReplicatedStateDelivery) {
				seen = append(seen, value)
			})
			requireModelsOK(t, err)
			defer stop()
			if got := source.State().Value(); !reflect.DeepEqual(got, state.Value()) {
				t.Fatalf("value = %#v, want %#v", got, state.Value())
			}
			revision := state.Value().Revision + 1
			requireModelsOK(t, state.Change(t.Context(), func(draft *SessionDirectoryState) error {
				draft.Revision = revision
				draft.Sessions = append(draft.Sessions, SessionSummary{SessionAddress{"server", name}, 1})
				return nil
			}))
			if got := source.State().Value(); !reflect.DeepEqual(got, state.Value()) {
				t.Fatalf("value after change = %#v, want %#v", got, state.Value())
			}
			if len(seen) < 2 || !reflect.DeepEqual(seen[len(seen)-1], state.Value()) {
				t.Fatalf("deliveries = %#v, want hydration then %#v", seen, state.Value())
			}
		})
	}
	t.Run("view resolve failure", func(t *testing.T) {
		resolveFailure := errors.New("not selected")
		failing := sessionDirectoryView{resolve: func() (SessionDirectory, error) { return nil, resolveFailure }}
		if _, err := failing.State().Subscribe(func(*SessionDirectoryState, context.Context, chord.ReplicatedStateDelivery) {}); !errors.Is(err, resolveFailure) {
			t.Fatalf("subscribe = %v", err)
		}
	})
}
