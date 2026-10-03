package experimental

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/chord"
	"github.com/MichaelKinsy/PiG/internal/experimental/durabletest"
	"github.com/MichaelKinsy/PiG/internal/experimental/services"
	"github.com/MichaelKinsy/PiG/tui"
)

func TestExperimentalClientTuiUpstream(t *testing.T) {
	isolateExperimentalTest(t)
	previousTheme := tui.ActiveTheme().Name
	previousKeybindings := tui.GetKeybindings()
	tui.SetTheme("dark")
	t.Cleanup(func() {
		tui.SetThemeByName(previousTheme)
		tui.SetKeybindings(previousKeybindings)
	})

	// upstream: packages/coding-agent/test/experimental-client-tui.test.ts:100-105.
	cases := []struct {
		kind      string
		command   ClientCommand
		sessionId string
		creates   int64
	}{
		{"new", ClientCommand{Command: "client"}, "two", 1},
		{"continued", ClientCommand{Command: "client", Continue: new(true)}, "one", 0},
		{"plugin-selected", ClientCommand{Command: "client", PluginPackages: []string{"./example-plugin"}}, "two", 1},
	}
	for _, tt := range cases {
		t.Run(fmt.Sprintf("opens a %s Session directly and exercises the full lifecycle only for a new Session", tt.kind), func(t *testing.T) {
			const serverId = "00000000-0000-4000-8000-000000000001"
			background := context.Background()
			directoryState, err := chord.NewReplicatedState(&services.SessionDirectoryState{Revision: 1, Sessions: []services.SessionSummary{{SessionAddress: services.SessionAddress{ServerId: serverId, SessionId: "one"}, CreatedAt: 1}}})
			if err != nil {
				t.Fatal(err)
			}
			attachment, err := chord.NewReplicatedState(&services.SessionAttachmentState{Status: "detached"})
			if err != nil {
				t.Fatal(err)
			}
			connectionState, err := chord.NewReplicatedState(&services.ServerConnectionState{Status: "connected", Since: "now"})
			if err != nil {
				t.Fatal(err)
			}
			modelsState, err := chord.NewReplicatedState(&services.ModelsState{
				Catalog: services.ModelsCatalog{Revision: 1, AvailableModels: []services.ModelSummary{
					{ModelRef: services.ModelRef{Provider: "test", ModelId: "one"}, Name: "Model One", Reasoning: false},
					{ModelRef: services.ModelRef{Provider: "test", ModelId: "two"}, Name: "Model Two", Reasoning: true},
				}},
				Configuration: services.ModelsConfiguration{Model: &services.ModelRef{Provider: "test", ModelId: "one"}, ThinkingLevel: "off"},
				Refresh:       services.ModelsRefresh{Status: "idle"},
			})
			if err != nil {
				t.Fatal(err)
			}
			var changedMu sync.Mutex
			changed := make(chan struct{})
			notify := func() {
				changedMu.Lock()
				close(changed)
				changed = make(chan struct{})
				changedMu.Unlock()
			}
			waitFor := func(description string, satisfied func() bool) {
				t.Helper()
				for {
					changedMu.Lock()
					next := changed
					changedMu.Unlock()
					if satisfied() {
						return
					}
					select {
					case <-next:
					case <-t.Context().Done():
						t.Fatalf("waiting for %s: %v", description, t.Context().Err())
					}
				}
			}

			var creates atomic.Int64
			management := &clientTuiManagementSpy{
				create: func(ctx context.Context, _ services.SessionCreateOptions) (services.SessionSummary, error) {
					creates.Add(1)
					created := services.SessionSummary{SessionAddress: services.SessionAddress{ServerId: serverId, SessionId: "two"}, CreatedAt: 2}
					err := directoryState.Change(ctx, func(draft *services.SessionDirectoryState) error {
						draft.Revision = 2
						draft.Sessions = append(draft.Sessions, created)
						return nil
					})
					return created, err
				},
				attach: func(_ context.Context, id string) error {
					return attachment.Replace(background, &services.SessionAttachmentState{Status: "attaching", SessionID: id})
				},
				detach: func(context.Context) error {
					return attachment.Replace(background, &services.SessionAttachmentState{Status: "detached"})
				},
			}
			var callsMu sync.Mutex
			var selectedModels []services.ModelRef
			var selectedThinking []ai.ThinkingLevel
			models := &clientTuiModelsSpy{
				clientTuiStateService: clientTuiStateService[*services.ModelsState]{modelsState},
				selectModel: func(ctx context.Context, model services.ModelRef) error {
					if ctx == nil {
						return errors.New("select requires a context")
					}
					if err := modelsState.Change(ctx, func(draft *services.ModelsState) error { draft.Configuration.Model = new(model); return nil }); err != nil {
						return err
					}
					callsMu.Lock()
					selectedModels = append(selectedModels, model)
					callsMu.Unlock()
					notify()
					return nil
				},
				selectThinking: func(ctx context.Context, level ai.ThinkingLevel) error {
					if ctx == nil {
						return errors.New("selectThinking requires a context")
					}
					if err := modelsState.Change(ctx, func(draft *services.ModelsState) error { draft.Configuration.ThinkingLevel = level; return nil }); err != nil {
						return err
					}
					callsMu.Lock()
					selectedThinking = append(selectedThinking, level)
					callsMu.Unlock()
					notify()
					return nil
				},
			}
			finishPrompt := make(chan struct{})
			var finishOnce sync.Once
			finish := func() { finishOnce.Do(func() { close(finishPrompt) }) }
			t.Cleanup(finish)
			var prompts []string
			// experimental-client-tui.test.ts:107-118: the faux response records the prompt it answers, waits for promptFinished, then answers.
			durable := durabletest.OpenFauxConversation(func(ctx context.Context, input string) (string, error) {
				callsMu.Lock()
				prompts = append(prompts, input)
				callsMu.Unlock()
				notify()
				select {
				case <-finishPrompt:
				case <-ctx.Done():
					return "", context.Cause(ctx)
				}
				return "remote answer", nil
			})
			t.Cleanup(func() { _ = durable.Harness.Close(background) })
			transcriptView, err := durable.Conversation.ViewState(background)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(transcriptView.Dispose)

			reloadSource := "\"use strict\";\nconst { defineFacet, defineService } = require(\"@earendil-works/chord\");\nconst Models = defineService(\"pi.models\");\nmodule.exports = { __esModule: true, default: defineFacet({ id: \"test-tui-facet\", setup(env) { env.use(Models); } }) };\n"
			hash := sha256.Sum256([]byte(reloadSource))
			reloadArtifact := FacetBundleArtifact{
				Format: FacetBundleArtifactFormat, FormatVersion: FacetBundleArtifactFormatVersion,
				Plugin: FacetBundlePlugin{Id: "test-tui-plugin"}, EntryName: "tui",
				Entry:  FacetBundleEntry{File: "tui.cjs", Integrity: "sha256-" + base64.StdEncoding.EncodeToString(hash[:]), ExternalImports: []string{"@earendil-works/chord"}},
				Source: reloadSource,
			}
			reloadData := CreatePresentationFacetData([]FacetBundleArtifact{reloadArtifact})
			var prepares []services.PrepareSessionPluginsRequest
			var presentationReloads, sessionReloads atomic.Int64
			plugins := &clientTuiPresentationPluginsSpy{
				prepare: func(ctx context.Context, request services.PrepareSessionPluginsRequest) (chord.JsonValue, error) {
					if ctx == nil {
						return nil, errors.New("prepareSession requires a context")
					}
					callsMu.Lock()
					prepares = append(prepares, services.PrepareSessionPluginsRequest{SessionId: request.SessionId, PackagePaths: slices.Clone(request.PackagePaths)})
					callsMu.Unlock()
					return reloadData, nil
				},
				reload: func(context.Context) (chord.JsonValue, error) {
					presentationReloads.Add(1)
					notify()
					return reloadData, nil
				},
			}
			sessionPlugins := &clientTuiSessionPluginsSpy{reload: func(context.Context) error { sessionReloads.Add(1); notify(); return nil }}
			serverProvider, err := chord.NewRemoteServiceProvider(chord.SingletonService(services.SessionDirectoryDefinition), chord.SingletonService(services.SessionManagementDefinition), chord.SingletonService(services.PresentationPluginsDefinition))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := serverProvider.Dispose(); err != nil {
					t.Error(err)
				}
			})
			if err := chord.Provide[services.SessionDirectory](serverProvider, services.SessionDirectoryDefinition, &clientTuiStateService[*services.SessionDirectoryState]{directoryState}); err != nil {
				t.Fatal(err)
			}
			if err := chord.Provide[services.SessionManagement](serverProvider, services.SessionManagementDefinition, management); err != nil {
				t.Fatal(err)
			}
			if err := chord.Provide[services.PresentationPlugins](serverProvider, services.PresentationPluginsDefinition, plugins); err != nil {
				t.Fatal(err)
			}
			sessionProvider, err := chord.NewRemoteServiceProvider(chord.SingletonService(services.ModelsDefinition), chord.SingletonService(services.AgentControllerDefinition), chord.SingletonService(services.SessionPluginsDefinition), chord.SingletonService(services.TranscriptDefinition))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := sessionProvider.Dispose(); err != nil {
					t.Error(err)
				}
			})
			if err := chord.Provide[services.Models](sessionProvider, services.ModelsDefinition, models); err != nil {
				t.Fatal(err)
			}
			if err := chord.Provide[services.AgentController](sessionProvider, services.AgentControllerDefinition, services.CreateAgentController(durable.Harness, durable.Conversation)); err != nil {
				t.Fatal(err)
			}
			if err := chord.Provide[services.SessionPlugins](sessionProvider, services.SessionPluginsDefinition, sessionPlugins); err != nil {
				t.Fatal(err)
			}
			if err := chord.Provide[services.Transcript](sessionProvider, services.TranscriptDefinition, clientTuiViewTranscript{transcriptView}); err != nil {
				t.Fatal(err)
			}
			server, disposeSources := newClientTuiLoopbackServer(t, serverId, serverProvider, sessionProvider, connectionState, attachment)
			server.Radius = true
			t.Cleanup(func() {
				if err := disposeSources(background); err != nil {
					t.Error(err)
				}
			})
			var finished atomic.Bool
			observation := newClientTuiObservation(t)
			component, err := CreateExperimentalClientTui(background, ExperimentalClientTuiOptions{
				Command: tt.command, UI: tui.New(), Servers: []ClientTuiServer{server},
				RequestRender:  observation.RequestRender,
				Finish:         func() { finished.Store(true); notify() },
				RunOnMain:      observation.Executor.RunOnMain,
				QueueMicrotask: observation.Executor.QueueMicrotask,
			})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				finish()
				if err := component.Close(); err != nil {
					t.Error(err)
				}
			})
			render := func() string {
				t.Helper()
				lines, err := observation.Render(background, component, 80)
				if err != nil {
					t.Fatal(err)
				}
				return strings.Join(lines, "\n")
			}
			handleInput := func(data ...string) {
				t.Helper()
				if err := observation.HandleInputBatch(background, component, data...); err != nil {
					t.Fatal(err)
				}
			}
			waitRendered := func(text string, present bool) {
				t.Helper()
				if _, err := observation.Wait(t.Context(), component, 80, func(lines []string) bool {
					return strings.Contains(strings.Join(lines, "\n"), text) == present
				}); err != nil {
					t.Fatalf("wait for rendered %q present=%t: %v", text, present, err)
				}
			}
			if got := creates.Load(); got != tt.creates {
				t.Fatalf("create calls = %d, want %d", got, tt.creates)
			}
			var packagePaths []string
			for _, path := range tt.command.PluginPackages {
				absolute, err := filepath.Abs(path)
				if err != nil {
					t.Fatal(err)
				}
				packagePaths = append(packagePaths, absolute)
			}
			wantPrepare := services.PrepareSessionPluginsRequest{SessionId: tt.sessionId, PackagePaths: packagePaths}
			callsMu.Lock()
			prepareCalls := slices.Clone(prepares)
			prepared := slices.ContainsFunc(prepareCalls, func(got services.PrepareSessionPluginsRequest) bool { return reflect.DeepEqual(got, wantPrepare) })
			modelSelectCount := len(selectedModels)
			callsMu.Unlock()
			if !prepared {
				t.Fatalf("prepareSession calls = %#v, want %#v", prepareCalls, wantPrepare)
			}
			if got, want := attachment.Value(), (&services.SessionAttachmentState{Status: "attached", SessionID: tt.sessionId}); !reflect.DeepEqual(got, want) {
				t.Fatalf("attachment = %#v, want %#v", got, want)
			}
			if modelSelectCount != 0 {
				t.Fatalf("startup selected a model %d times", modelSelectCount)
			}
			for _, text := range []string{"Server: " + serverId, "Session: " + tt.sessionId, "faux/faux-1"} {
				if got := render(); !strings.Contains(got, text) {
					t.Fatalf("render lacks %q: %q", text, got)
				}
			}
			for _, text := range []string{"Experimental Sessions", "Experimental Models"} {
				if got := render(); strings.Contains(got, text) {
					t.Fatalf("render contains %q: %q", text, got)
				}
			}
			if tt.kind != "new" {
				return
			}

			handleInput("hello", "\r")
			waitFor("prompt call", func() bool {
				callsMu.Lock()
				defer callsMu.Unlock()
				return len(prompts) > 0
			})
			callsMu.Lock()
			observedPrompts := slices.Clone(prompts)
			callsMu.Unlock()
			if !reflect.DeepEqual(observedPrompts, []string{"hello"}) {
				t.Fatalf("faux provider prompts = %#v, want [hello]", observedPrompts)
			}
			waitRendered("Working...", true)
			finish()
			waitRendered("remote answer", true)
			if got := render(); !strings.Contains(got, "hello") || strings.Contains(got, "Working...") {
				t.Fatalf("completed prompt render = %q", got)
			}

			handleInput("/reload", "\x1b", "\r")
			waitFor("both plugin reload calls", func() bool { return presentationReloads.Load() >= 1 && sessionReloads.Load() >= 1 })
			if presentationReloads.Load() != 1 || sessionReloads.Load() != 1 {
				t.Fatalf("reload calls: presentation=%d session=%d, want one each", presentationReloads.Load(), sessionReloads.Load())
			}
			waitRendered("Reloaded plugins.", true)
			disconnectedFrame, err := observation.PublishAndRender(background, component, 80, func() error {
				if err := attachment.Replace(background, &services.SessionAttachmentState{Status: "detached"}); err != nil {
					return err
				}
				return connectionState.Replace(background, &services.ServerConnectionState{Status: "disconnected", Since: "later", Reason: "network lost", RetryAt: nil})
			})
			if err != nil {
				t.Fatal(err)
			}
			// Pi's first waitFor callback runs in the publication turn before queued closeLane work.
			if got := strings.Join(disconnectedFrame, "\n"); !strings.Contains(got, "retrying") {
				t.Fatalf("disconnect publication frame lacks retrying: %q", got)
			}
			handleInput("\x03")
			if !finished.Load() {
				t.Fatal("Ctrl-C did not finish while disconnected")
			}
			finished.Store(false)
			handleInput("\x04")
			if !finished.Load() {
				t.Fatal("Ctrl-D did not finish while disconnected")
			}
			finished.Store(false)
			reconnectedFrame, err := observation.PublishAndRender(background, component, 80, func() error {
				if err := connectionState.Replace(background, &services.ServerConnectionState{Status: "connecting", Attempt: 1}); err != nil {
					return err
				}
				if err := connectionState.Replace(background, &services.ServerConnectionState{Status: "connected", Since: "reconnected"}); err != nil {
					return err
				}
				return attachment.Replace(background, &services.SessionAttachmentState{Status: "attached", SessionID: tt.sessionId})
			})
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(strings.Join(reconnectedFrame, "\n"), "Reattaching") {
				waitRendered("Reattaching", false)
			}

			handleInput("/model", "\x1b", "\r")
			waitRendered("Select model:", true)
			handleInput("\x1b[B", "\r")
			wantModel := services.ModelRef{Provider: "test", ModelId: "two"}
			waitFor("model selection call", func() bool { callsMu.Lock(); defer callsMu.Unlock(); return len(selectedModels) > 0 })
			callsMu.Lock()
			modelCalls := slices.Clone(selectedModels)
			callsMu.Unlock()
			if !slices.Contains(modelCalls, wantModel) {
				t.Fatalf("select calls = %#v, want %#v", modelCalls, wantModel)
			}
			if got := modelsState.Value().Configuration.Model; !reflect.DeepEqual(got, &wantModel) {
				t.Fatalf("selected model = %#v, want %#v", got, wantModel)
			}
			waitRendered("Select model:", false)
			handleInput("/thinking", "\x1b", "\r")
			waitRendered("Select thinking level:", true)
			handleInput("\x1b[B", "\r")
			waitFor("thinking selection call", func() bool {
				callsMu.Lock()
				defer callsMu.Unlock()
				return len(selectedThinking) > 0
			})
			callsMu.Lock()
			thinkingCalls := slices.Clone(selectedThinking)
			callsMu.Unlock()
			if !slices.Contains(thinkingCalls, ai.ThinkingLevel("high")) {
				t.Fatalf("selectThinking calls = %#v, want high", thinkingCalls)
			}
			if got := modelsState.Value().Configuration.ThinkingLevel; got != "high" {
				t.Fatalf("thinking level = %q, want high", got)
			}
			handleInput("\x03")
			waitFor("finish", finished.Load)

			if err := component.Close(); err != nil {
				t.Fatal(err)
			}
			rendersAfterClose := observation.Requests()
			if err := directoryState.Change(background, func(draft *services.SessionDirectoryState) error {
				draft.Revision = 3
				draft.Sessions = []services.SessionSummary{}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if err := attachment.Replace(background, &services.SessionAttachmentState{Status: "detached"}); err != nil {
				t.Fatal(err)
			}
			if err := modelsState.Change(background, func(draft *services.ModelsState) error {
				draft.Refresh = services.ModelsRefresh{Status: "refreshing"}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if got := observation.Requests(); got != rendersAfterClose {
				t.Fatalf("renders after close = %d, want unchanged %d", got, rendersAfterClose)
			}
		})
	}
}

// upstream: packages/coding-agent/test/experimental-client-tui.test.ts:106-341 supplies state-backed service spies to real Chord providers.
type clientTuiStateService[T any] struct {
	state *chord.MutableReplicatedState[T]
}

func (service *clientTuiStateService[T]) State() chord.ReplicatedStateOf[T] { return service.state }

type clientTuiManagementSpy struct {
	create func(context.Context, services.SessionCreateOptions) (services.SessionSummary, error)
	attach func(context.Context, string) error
	detach func(context.Context) error
}

func (spy *clientTuiManagementSpy) Create(ctx context.Context, options services.SessionCreateOptions) (services.SessionSummary, error) {
	return spy.create(ctx, options)
}
func (*clientTuiManagementSpy) Remove(context.Context, string) error { return nil }
func (spy *clientTuiManagementSpy) Attach(ctx context.Context, id string) error {
	return spy.attach(ctx, id)
}
func (spy *clientTuiManagementSpy) Detach(ctx context.Context) error { return spy.detach(ctx) }

type clientTuiModelsSpy struct {
	clientTuiStateService[*services.ModelsState]
	selectModel    func(context.Context, services.ModelRef) error
	selectThinking func(context.Context, ai.ThinkingLevel) error
}

func (*clientTuiModelsSpy) CycleThinking(context.Context) error { return nil }
func (*clientTuiModelsSpy) GetThinkingLevels(context.Context) ([]ai.ThinkingLevel, error) {
	return []ai.ThinkingLevel{"off", "high"}, nil
}
func (*clientTuiModelsSpy) Refresh(context.Context) error { return nil }
func (spy *clientTuiModelsSpy) Select(ctx context.Context, model services.ModelRef) error {
	return spy.selectModel(ctx, model)
}
func (spy *clientTuiModelsSpy) SelectThinking(ctx context.Context, level ai.ThinkingLevel) error {
	return spy.selectThinking(ctx, level)
}

type clientTuiPresentationPluginsSpy struct {
	prepare func(context.Context, services.PrepareSessionPluginsRequest) (chord.JsonValue, error)
	reload  func(context.Context) (chord.JsonValue, error)
}

func (spy *clientTuiPresentationPluginsSpy) PrepareSession(ctx context.Context, request services.PrepareSessionPluginsRequest) (chord.JsonValue, error) {
	return spy.prepare(ctx, request)
}
func (spy *clientTuiPresentationPluginsSpy) Reload(ctx context.Context) (chord.JsonValue, error) {
	return spy.reload(ctx)
}

type clientTuiSessionPluginsSpy struct{ reload func(context.Context) error }

func (spy *clientTuiSessionPluginsSpy) Reload(ctx context.Context) error { return spy.reload(ctx) }

// clientTuiViewTranscript serves a durable conversation's attached view state as the Transcript service.
type clientTuiViewTranscript struct{ state services.TranscriptViewState }

func (transcript clientTuiViewTranscript) State() chord.ReplicatedStateOf[services.ConversationView] {
	return transcript.state
}
