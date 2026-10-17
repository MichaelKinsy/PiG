package coding

// pi: packages/coding-agent/src/core/virtual-models.ts

// Ports .upstream/v0.99.1/packages/coding-agent/test/virtual-models.test.ts: the seven "ModelRuntime virtual models" cases and the eight "createAgentSession with virtual models" cases, with the same inputs and expectations. The Go form of `createAgentSession` is NewSession with a caller-supplied session manager.

import (
	"context"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// virtualFaux is what these tests use of the faux provider (upstream `fauxProvider`).
type virtualFaux interface {
	Provider() *ai.ModelsProvider
	SetResponses([]ai.FauxResponseStep)
	CallCount() int
}

type virtualRuntimeFixture struct {
	runtime    *ModelRuntime
	faux       virtualFaux
	definition VirtualModelDefinition
	virtual    *ai.Model
	requests   *[]ModelRouteRequest
}

// createVirtualRuntime is upstream's `createRuntime` (virtual-models.test.ts:20-48): an in-memory runtime with a faux provider of `small` and `large` and the `router/auto` virtual model.
func createVirtualRuntime(t *testing.T) virtualRuntimeFixture {
	t.Helper()
	agentDir := t.TempDir()
	t.Setenv(icodingagent.ENV_AGENT_DIR, agentDir)
	runtime, err := CreateModelRuntime(t.Context(), CreateModelRuntimeOptions{Credentials: ai.NewInMemoryCredentialStore(), ModelsStore: ai.NewInMemoryModelsStore(), ModelsPath: new((*string)(nil)), AllowModelNetwork: false})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(runtime.Close)
	faux := ai.NewFauxProvider(ai.FauxConfig{ProviderID: "faux", Models: []ai.FauxModelDefinition{
		{ID: "small", ContextWindow: 1000, MaxTokens: 100, Input: []string{"text"}},
		{ID: "large", ContextWindow: 50_000, MaxTokens: 5000, Input: []string{"text", "image"}, Reasoning: true},
	}})
	if err := runtime.RegisterNativeProvider(faux.Provider()); err != nil {
		t.Fatal(err)
	}
	requests := &[]ModelRouteRequest{}
	definition := VirtualModelDefinition{
		Provider: "router", ID: "auto", Name: "Auto", ThinkingLevels: []ai.ModelThinkingLevel{ai.ThinkingLow, ai.ThinkingHigh},
		Route: func(_ context.Context, request ModelRouteRequest) (ModelRoute, error) {
			*requests = append(*requests, request)
			id := "small"
			if request.ThinkingLevel == ai.ThinkingHigh {
				id = "large"
			}
			return ModelRoute{Model: runtime.GetModel("faux", id), ThinkingLevel: ai.ThinkingHigh}, nil
		},
	}
	if err := runtime.RegisterVirtualModel(definition); err != nil {
		t.Fatal(err)
	}
	if result := runtime.Refresh(t.Context(), ai.ModelsRefreshOptions{AllowNetwork: new(false)}); result.Aborted || len(result.Errors) != 0 {
		t.Fatalf("refresh = %+v", result)
	}
	return virtualRuntimeFixture{runtime: runtime, faux: faux, definition: definition, virtual: runtime.GetModel("router", "auto"), requests: requests}
}

func assistantFromModel(model *ai.Model, text string) ai.AssistantMessage {
	return ai.AssistantMessage{Content: []ai.AssistantContentBlock{ai.TextContent{Text: text}}, API: model.ProviderMeta.API, Provider: model.ProviderMeta.ProviderID, Model: model.ID, StopReason: ai.StopReasonStop, Timestamp: time.Now().UnixMilli()}
}

func userText(text string, timestamp int64) ai.UserMessage {
	return ai.UserMessage{Content: ai.UserText(text), Timestamp: timestamp}
}

func modelIDsOf(models []*ai.Model, provider string) []string {
	ids := []string{}
	for _, model := range models {
		if model.ProviderMeta.ProviderID == provider {
			ids = append(ids, model.ID)
		}
	}
	return ids
}

// upstream virtual-models.test.ts:55-76
func TestVirtualModelListsAndRoutesToAPhysicalModelWithAClampedThinkingLevel(t *testing.T) {
	f := createVirtualRuntime(t)
	virtual := f.virtual
	if virtual == nil || virtual.ProviderMeta.ProviderID != "router" || virtual.ID != "auto" || virtual.Capabilities.ContextWindow != 0 || virtual.Capabilities.MaxOutputTokens != 0 {
		t.Fatalf("virtual model = %+v", virtual)
	}
	if !reflect.DeepEqual(virtual.Input, []string{"text", "image"}) {
		t.Fatalf("input = %v", virtual.Input)
	}
	if got, want := ai.GetSupportedThinkingLevels(virtual), []ai.ModelThinkingLevel{ai.ThinkingLow, ai.ThinkingHigh}; !reflect.DeepEqual(got, want) {
		t.Fatalf("supported thinking levels = %v, want %v", got, want)
	}
	large := f.runtime.GetModel("faux", "large")
	answer := assistantFromModel(large, "answer")
	answer.ThinkingLevel = ai.ThinkingMedium
	messages := []ai.Message{userText("first", 1), answer, userText("second", 2)}

	low, err := f.runtime.ResolveModel(t.Context(), virtual, messages, ResolveModelOptions{Reason: ModelRouteReasonUser, ThinkingLevel: ai.ThinkingLow})
	if err != nil {
		t.Fatal(err)
	}
	if low.Model.ID != "small" {
		t.Fatalf("low model = %q", low.Model.ID)
	}
	// The router asked for "high", but the small model does not reason.
	if low.ThinkingLevel != ai.ThinkingOff {
		t.Fatalf("low thinking level = %q, want off", low.ThinkingLevel)
	}
	previous := (*f.requests)[0].Previous
	if previous == nil || previous.Model.ID != "large" || previous.ThinkingLevel != ai.ThinkingMedium {
		t.Fatalf("previous = %+v", previous)
	}

	high, err := f.runtime.ResolveModel(t.Context(), virtual, messages, ResolveModelOptions{Reason: ModelRouteReasonUser, ThinkingLevel: ai.ThinkingHigh})
	if err != nil {
		t.Fatal(err)
	}
	if high.Model.ID != "large" || high.ThinkingLevel != ai.ThinkingHigh {
		t.Fatalf("high = %q %q", high.Model.ID, high.ThinkingLevel)
	}
}

// upstream virtual-models.test.ts:78-103
func TestVirtualModelReportsTheFailedRequestOfARetrySeparatelyFromTheLatestSuccessfulResponse(t *testing.T) {
	f := createVirtualRuntime(t)
	small, large := f.runtime.GetModel("faux", "small"), f.runtime.GetModel("faux", "large")
	failed := assistantFromModel(large, "")
	failed.ThinkingLevel, failed.StopReason, failed.ErrorMessage = ai.ThinkingHigh, ai.StopReasonError, "overloaded_error"
	messages := []ai.Message{userText("first", 1), assistantFromModel(small, "answer"), userText("second", 2)}

	if _, err := f.runtime.ResolveModel(t.Context(), f.virtual, messages, ResolveModelOptions{Reason: ModelRouteReasonRetry, ThinkingLevel: ai.ThinkingLow, Failed: &failed}); err != nil {
		t.Fatal(err)
	}
	// A routing failure names the virtual model, so there is no failed physical request to report.
	failedRoute := assistantFromModel(f.virtual, "")
	failedRoute.StopReason = ai.StopReasonError
	if _, err := f.runtime.ResolveModel(t.Context(), f.virtual, messages, ResolveModelOptions{Reason: ModelRouteReasonRetry, ThinkingLevel: ai.ThinkingLow, Failed: &failedRoute}); err != nil {
		t.Fatal(err)
	}

	requests := *f.requests
	if requests[0].Previous == nil || requests[0].Previous.Model.ID != "small" {
		t.Fatalf("previous = %+v", requests[0].Previous)
	}
	got := requests[0].Failed
	if got == nil || got.Model.ID != "large" || got.ThinkingLevel != ai.ThinkingHigh || !reflect.DeepEqual(got.Message, failed) {
		t.Fatalf("failed = %+v", got)
	}
	if requests[1].Failed != nil {
		t.Fatalf("failed of a routing failure = %+v, want none", requests[1].Failed)
	}
}

// upstream virtual-models.test.ts:105-134
func TestVirtualModelsListSeveralUnderAProviderWithPhysicalModels(t *testing.T) {
	f := createVirtualRuntime(t)
	route := func(context.Context, ModelRouteRequest) (ModelRoute, error) {
		return ModelRoute{Model: f.runtime.GetModel("faux", "small"), ThinkingLevel: ai.ThinkingOff}, nil
	}
	with := func(provider, id, name string, route func(context.Context, ModelRouteRequest) (ModelRoute, error)) VirtualModelDefinition {
		definition := f.definition
		definition.Provider, definition.ID, definition.Name = provider, id, name
		if route != nil {
			definition.Route = route
		}
		return definition
	}
	for _, definition := range []VirtualModelDefinition{with("faux", "auto", "Auto", route), with("faux", "fast", "Fast", route), with("router", "second", "Second", nil)} {
		if err := f.runtime.RegisterVirtualModel(definition); err != nil {
			t.Fatal(err)
		}
	}

	if got, want := modelIDsOf(f.runtime.GetModels(), "faux"), []string{"small", "large", "auto", "fast"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("faux models = %v, want %v", got, want)
	}
	if got, want := modelIDsOf(f.runtime.GetModels(), "router"), []string{"auto", "second"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("router models = %v, want %v", got, want)
	}
	// Virtual models on a physical provider are available when the provider is.
	available, err := f.runtime.GetAvailable(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if got, want := modelIDsOf(available, "faux"), []string{"small", "large", "auto", "fast"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("available faux models = %v, want %v", got, want)
	}
	routed, err := f.runtime.ResolveModel(t.Context(), f.runtime.GetModel("faux", "fast"), nil, ResolveModelOptions{Reason: ModelRouteReasonUser, ThinkingLevel: ai.ThinkingOff})
	if err != nil || routed.Model.ProviderMeta.ProviderID != "faux" || routed.Model.ID != "small" {
		t.Fatalf("fast routed to %+v, %v", routed.Model, err)
	}

	if err := f.runtime.RegisterVirtualModel(with("faux", "large", "Large", nil)); err == nil || !strings.Contains(err.Error(), "conflicts with a physical model") {
		t.Fatalf("registering over a physical model = %v", err)
	}
	f.runtime.UnregisterVirtualModel("faux", "fast")
	f.runtime.UnregisterVirtualModel("router", "auto")
	if got, want := modelIDsOf(f.runtime.GetModels(), "faux"), []string{"small", "large", "auto"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("faux models after unregister = %v, want %v", got, want)
	}
	if got, want := modelIDsOf(f.runtime.GetModels(), "router"), []string{"second"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("router models after unregister = %v, want %v", got, want)
	}
}

// upstream virtual-models.test.ts:136-146
func TestVirtualModelRejectsRoutesToVirtualOrUnknownModels(t *testing.T) {
	f := createVirtualRuntime(t)
	unknown := *f.virtual
	unknown.ProviderMeta.ProviderID, unknown.ID = "faux", "missing"
	for _, target := range []*ai.Model{f.virtual, &unknown} {
		definition := f.definition
		definition.Route = func(context.Context, ModelRouteRequest) (ModelRoute, error) {
			return ModelRoute{Model: target, ThinkingLevel: ai.ThinkingOff}, nil
		}
		if err := f.runtime.RegisterVirtualModel(definition); err != nil {
			t.Fatal(err)
		}
		if _, err := f.runtime.ResolveModel(t.Context(), f.virtual, nil, ResolveModelOptions{Reason: ModelRouteReasonUser, ThinkingLevel: ai.ThinkingLow}); err == nil || !strings.Contains(err.Error(), "which is not a physical model") {
			t.Fatalf("route to %s/%s: error = %v", target.ProviderMeta.ProviderID, target.ID, err)
		}
	}
}

// upstream virtual-models.test.ts:148-169
func TestVirtualModelRoutesDirectStreamSimpleCallsWithinTheRoutedModelsLimits(t *testing.T) {
	f := createVirtualRuntime(t)
	maxTokens := -1
	f.faux.SetResponses([]ai.FauxResponseStep{ai.FauxFactoryStep(func(_ ai.TranscriptContext, options ai.StreamOptions, _ *ai.FauxProviderState, _ *ai.Model) (ai.AssistantMessage, error) {
		maxTokens = options.MaxTokens
		return ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("hello")}, StopReason: "stop"}.AssistantMessage(), nil
	})})

	// The caller sized the request without knowing the routed model.
	message := f.runtime.CompleteSimple(t.Context(), f.virtual, ai.Context{Messages: []ai.Message{userText("hi", 1)}}, ai.StreamOptions{Thinking: ai.ThinkingLevelHigh, MaxTokens: 20_000})

	var reasons []ModelRouteReason
	for _, request := range *f.requests {
		reasons = append(reasons, request.Reason)
	}
	if want := []ModelRouteReason{ModelRouteReasonDirect}; !reflect.DeepEqual(reasons, want) {
		t.Fatalf("reasons = %v, want %v", reasons, want)
	}
	if message.Provider != "faux" || message.Model != "large" || message.StopReason != ai.StopReasonStop {
		t.Fatalf("message = %s/%s %s (%s)", message.Provider, message.Model, message.StopReason, message.ErrorMessage)
	}
	if maxTokens != 5000 {
		t.Fatalf("maxTokens = %d, want 5000", maxTokens)
	}
}

// upstream virtual-models.test.ts:171-190
func TestVirtualModelDoesNotForwardCallerCredentialsToARoutedModelOfAnotherProvider(t *testing.T) {
	f := createVirtualRuntime(t)
	type seen struct {
		apiKey string
		header string
	}
	var got []seen
	respond := ai.FauxFactoryStep(func(_ ai.TranscriptContext, options ai.StreamOptions, _ *ai.FauxProviderState, _ *ai.Model) (ai.AssistantMessage, error) {
		header := ""
		if value := options.Headers["x-caller"]; value != nil {
			header = *value
		}
		got = append(got, seen{options.APIKey, header})
		return ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("hello")}, StopReason: "stop"}.AssistantMessage(), nil
	})
	f.faux.SetResponses([]ai.FauxResponseStep{respond, respond})
	request := ai.Context{Messages: []ai.Message{userText("hi", 1)}}
	options := ai.StreamOptions{APIKey: "caller-key", Headers: ai.ProviderHeaders{"x-caller": new("1")}}
	definition := f.definition
	definition.Provider, definition.ID = "faux", "auto"
	if err := f.runtime.RegisterVirtualModel(definition); err != nil {
		t.Fatal(err)
	}

	f.runtime.CompleteSimple(t.Context(), f.virtual, request, options)
	f.runtime.CompleteSimple(t.Context(), f.runtime.GetModel("faux", "auto"), request, options)

	if len(got) != 2 {
		t.Fatalf("provider calls = %d, want 2", len(got))
	}
	// The router provider's credentials stay with it; the faux provider's own virtual model keeps them.
	if got[0].apiKey == "caller-key" || got[0].header != "" {
		t.Fatalf("caller credentials reached another provider: %+v", got[0])
	}
	if got[1].apiKey != "caller-key" || got[1].header != "1" {
		t.Fatalf("the provider's own virtual model lost the caller credentials: %+v", got[1])
	}
}

// upstream virtual-models.test.ts:192-199
func TestVirtualModelFailsUnroutedStreamCallsOnVirtualModels(t *testing.T) {
	f := createVirtualRuntime(t)

	message := f.runtime.Complete(t.Context(), f.virtual, ai.Context{Messages: []ai.Message{userText("hi", 1)}}, ai.StreamOptions{})

	if message.StopReason != ai.StopReasonError || !strings.Contains(message.ErrorMessage, "must be routed before streaming") {
		t.Fatalf("message = %s: %q", message.StopReason, message.ErrorMessage)
	}
}

// Session cases: virtual-models.test.ts:202-381 ("createAgentSession with virtual models").

// virtualSessionFixture builds a Session over the runtime of a fixture and a caller-supplied session manager, as `createAgentSession({modelRuntime, sessionManager})`.
func openVirtualSession(t *testing.T, f virtualRuntimeFixture, manager *icodingagent.Session, model *ai.Model) (*Session, error) {
	t.Helper()
	session, err := NewSession(f.runtime.services, SessionOptions{Model: model, SessionManager: manager, SkipBuiltinTools: true})
	if err != nil {
		return nil, err
	}
	t.Cleanup(func() { _ = session.Close() })
	go func() {
		for range session.Events() {
		}
	}()
	return session, nil
}

// resumeVirtual is upstream's `resume`: a transcript where the virtual model was selected and the large model answered.
func resumeVirtual(t *testing.T, f virtualRuntimeFixture, model *ai.Model) (*Session, *icodingagent.Session) {
	t.Helper()
	manager := icodingagent.NewSession("virtual-resume", t.TempDir())
	if _, err := manager.AppendModelChange("router", "auto"); err != nil {
		t.Fatal(err)
	}
	appendVirtualTranscript(t, manager, userText("hi", 1), assistantFromModel(f.runtime.GetModel("faux", "large"), "hello"))
	session, err := openVirtualSession(t, f, manager, model)
	if err != nil {
		t.Fatal(err)
	}
	return session, manager
}

func appendVirtualTranscript(t *testing.T, manager *icodingagent.Session, messages ...ai.Message) {
	t.Helper()
	for _, message := range messages {
		var agentMessage agent.AgentMessage
		switch message := message.(type) {
		case ai.UserMessage:
			agentMessage = agent.AgentMessage{User: &agent.UserMessage{Role: agent.RoleUser, Content: message.Content, Timestamp: message.Timestamp}}
		case ai.AssistantMessage:
			agentMessage = agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: agent.RoleAssistant, Content: message.Content, API: message.API, Provider: message.Provider, ModelID: message.Model, StopReason: message.StopReason, ErrorMessage: message.ErrorMessage, Timestamp: message.Timestamp}}
		}
		if _, err := manager.AppendMessage(agentMessage); err != nil {
			t.Fatal(err)
		}
	}
}

// upstream virtual-models.test.ts:226-233
func TestVirtualSessionRestoresTheVirtualSelectionInsteadOfThePhysicalModelThatAnswered(t *testing.T) {
	f := createVirtualRuntime(t)

	session, _ := resumeVirtual(t, f, nil)

	if model := session.Model(); model == nil || model.ProviderMeta.ProviderID != "router" || model.ID != "auto" {
		t.Fatalf("session model = %+v", model)
	}
	if routed := session.RoutedModel(); routed == nil || routed.Model.ProviderMeta.ProviderID != "faux" || routed.Model.ID != "large" {
		t.Fatalf("routed model = %+v", routed)
	}
}

// upstream virtual-models.test.ts:235-255: extensions register while the session is created, without waiting for the availability refresh.
func TestVirtualSessionRestoresAVirtualSelectionRegisteredRightBeforeTheSessionOpens(t *testing.T) {
	f := createVirtualRuntime(t)
	definition := f.definition
	definition.Provider = "late"
	if err := f.runtime.RegisterVirtualModel(definition); err != nil {
		t.Fatal(err)
	}
	manager := icodingagent.NewSession("virtual-late", t.TempDir())
	if _, err := manager.AppendModelChange("late", "auto"); err != nil {
		t.Fatal(err)
	}
	appendVirtualTranscript(t, manager, userText("hi", 1), assistantFromModel(f.runtime.GetModel("faux", "large"), "hello"))

	session, err := openVirtualSession(t, f, manager, nil)
	if err != nil {
		t.Fatal(err)
	}

	if model := session.Model(); model == nil || model.ProviderMeta.ProviderID != "late" || model.ID != "auto" {
		t.Fatalf("session model = %+v (modelFallbackMessage must be empty)", model)
	}
}

// upstream virtual-models.test.ts:257-265
func TestVirtualSessionFallsBackToThePhysicalModelWhenTheVirtualModelIsNotRegistered(t *testing.T) {
	f := createVirtualRuntime(t)
	f.runtime.UnregisterVirtualModel("router", "auto")

	session, _ := resumeVirtual(t, f, nil)

	if model := session.Model(); model == nil || model.ProviderMeta.ProviderID != "faux" || model.ID != "large" {
		t.Fatalf("session model = %+v", model)
	}
	if routed := session.RoutedModel(); routed != nil {
		t.Fatalf("routed model = %+v, want none", routed)
	}
}

// upstream virtual-models.test.ts:267-292
func TestVirtualSessionFallsBackToTheLastPhysicalResponseWhenTheTranscriptEndsWithARoutingFailure(t *testing.T) {
	f := createVirtualRuntime(t)
	manager := icodingagent.NewSession("virtual-routing-failure", t.TempDir())
	if _, err := manager.AppendModelChange("router", "auto"); err != nil {
		t.Fatal(err)
	}
	failed := assistantFromModel(f.virtual, "")
	failed.StopReason, failed.ErrorMessage = ai.StopReasonError, "router failed"
	appendVirtualTranscript(t, manager, userText("hi", 1), assistantFromModel(f.runtime.GetModel("faux", "large"), "hello"), userText("again", 2), failed)
	f.runtime.UnregisterVirtualModel("router", "auto")

	session, err := openVirtualSession(t, f, manager, nil)
	if err != nil {
		t.Fatal(err)
	}

	if model := session.Model(); model == nil || model.ProviderMeta.ProviderID != "faux" || model.ID != "large" {
		t.Fatalf("session model = %+v", model)
	}
}

func fauxTextSteps(count int, text string) []ai.FauxResponseStep {
	steps := make([]ai.FauxResponseStep, count)
	for i := range steps {
		steps[i] = ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText(text)}, StopReason: "stop"})
	}
	return steps
}

func lastModelChange(manager *icodingagent.Session) (icodingagent.SessionEntry, bool) {
	var last icodingagent.SessionEntry
	found := false
	for _, entry := range manager.GetBranch() {
		if entry.Base().Type == "model_change" {
			last, found = entry, true
		}
	}
	return last, found
}

func countModelChanges(manager *icodingagent.Session) int {
	n := 0
	for _, entry := range manager.GetBranch() {
		if entry.Base().Type == "model_change" {
			n++
		}
	}
	return n
}

// upstream virtual-models.test.ts:294-330: tree navigation can leave the latest model_change on another branch.
func TestVirtualSessionResumesTheSelectionMadeBeforeTreeNavigationLeftItsModelChangeOnAnotherBranch(t *testing.T) {
	for _, order := range []string{"virtual then physical", "physical then virtual"} {
		t.Run(order, func(t *testing.T) {
			f := createVirtualRuntime(t)
			f.faux.SetResponses(fauxTextSteps(6, "ok"))
			large := f.runtime.GetModel("faux", "large")
			before, after := f.virtual, large
			if order == "physical then virtual" {
				before, after = large, f.virtual
			}
			manager := icodingagent.NewSession("virtual-tree-"+strings.ReplaceAll(order, " ", "-"), t.TempDir())
			session, err := openVirtualSession(t, f, manager, before)
			if err != nil {
				t.Fatal(err)
			}
			if err := session.Prompt(t.Context(), "one"); err != nil {
				t.Fatal(err)
			}
			firstAnswer := manager.GetLeafID()
			if err := session.SetModel(after); err != nil {
				t.Fatal(err)
			}
			if err := session.Prompt(t.Context(), "two"); err != nil {
				t.Fatal(err)
			}
			// Navigating back to before the switch keeps `after` selected, but its model_change is on the old branch.
			if _, err := session.NavigateTree(t.Context(), *firstAnswer, NavigateTreeOptions{}); err != nil {
				t.Fatal(err)
			}
			if err := session.Prompt(t.Context(), "three"); err != nil {
				t.Fatal(err)
			}
			_ = session.Close()

			resumed, err := openVirtualSession(t, f, manager, nil)
			if err != nil {
				t.Fatal(err)
			}
			if model := resumed.Model(); model == nil || model.ProviderMeta.ProviderID != after.ProviderMeta.ProviderID || model.ID != after.ID {
				t.Fatalf("resumed model = %+v, want %s/%s", model, after.ProviderMeta.ProviderID, after.ID)
			}
		})
	}
}

// upstream virtual-models.test.ts:332-363: responses record a physical selection only when the branch holds no virtual one, so a request redirected by prepareRequest does not record a model_change on every prompt.
func TestVirtualSessionDoesNotRecordAPhysicalSelectionOnEveryPromptWhileRequestsAreRedirected(t *testing.T) {
	f := createVirtualRuntime(t)
	f.faux.SetResponses(fauxTextSteps(2, "ok"))
	small := f.runtime.GetModel("faux", "small")
	manager := icodingagent.NewSession("virtual-redirect", t.TempDir())
	session, err := openVirtualSession(t, f, manager, f.runtime.GetModel("faux", "large"))
	if err != nil {
		t.Fatal(err)
	}
	// upstream wraps `session.agent.prepareRequest` and returns `model: small`.
	prepareRequest := session.Agent().PrepareRequestHook()
	session.Agent().SetPrepareRequest(func(ctx context.Context, request agent.PrepareRequestContext) (*agent.AgentRequestUpdate, error) {
		update, err := prepareRequest(ctx, request)
		if update != nil {
			update.Model = small
		}
		return update, err
	})
	initial := countModelChanges(manager)

	for _, text := range []string{"one", "two"} {
		if err := session.Prompt(t.Context(), text); err != nil {
			t.Fatal(err)
		}
	}

	var models []string
	for _, message := range session.Messages() {
		if message.Assistant != nil {
			models = append(models, message.Assistant.ModelID)
		}
	}
	if want := []string{"small", "small"}; !slices.Equal(models, want) {
		t.Fatalf("assistant models = %v, want %v", models, want)
	}
	if n := countModelChanges(manager); n != initial {
		t.Fatalf("model_change entries = %d, want %d", n, initial)
	}
}

// upstream virtual-models.test.ts:365-380
func TestVirtualSessionRecordsAnExplicitModelOverrideOnResumeWithTheNextPrompt(t *testing.T) {
	f := createVirtualRuntime(t)
	f.faux.SetResponses(fauxTextSteps(1, "ok"))

	session, manager := resumeVirtual(t, f, f.runtime.GetModel("faux", "small"))
	// Opening the session does not write to it.
	if entry, ok := lastModelChange(manager); !ok || entry.Raw() == nil || !strings.Contains(string(entry.Raw()), `"router"`) {
		t.Fatalf("model_change after opening = %s", entry.Raw())
	}
	if err := session.Prompt(t.Context(), "again"); err != nil {
		t.Fatal(err)
	}
	if entry, ok := lastModelChange(manager); !ok || !strings.Contains(string(entry.Raw()), `"small"`) {
		t.Fatalf("model_change after the prompt = %s", entry.Raw())
	}
}

// upstream .upstream/v0.99.2/packages/coding-agent/test/virtual-models.test.ts:54-113 (#10198): the selection of a branch costs one
// catalog lookup at most, for the last model_change, instead of one per assistant message.
func TestGetBranchSelectionLooksUpOnlyTheLastModelChange(t *testing.T) {
	selectBranch := func(t *testing.T, f virtualRuntimeFixture, build func(manager *icodingagent.Session)) (*ModelSelection, []string) {
		t.Helper()
		manager := icodingagent.NewSession("branch-selection", t.TempDir())
		build(manager)
		var lookups []string
		selection := GetBranchSelection(manager.GetBranch(), func(provider, modelID string) *ai.Model {
			lookups = append(lookups, provider+"/"+modelID)
			return f.runtime.GetModel(provider, modelID)
		})
		return selection, lookups
	}
	// virtual-models.test.ts:66-85
	t.Run("looks up only the last model_change", func(t *testing.T) {
		f := createVirtualRuntime(t)
		small, large := f.runtime.GetModel("faux", "small"), f.runtime.GetModel("faux", "large")

		physical, lookups := selectBranch(t, f, func(manager *icodingagent.Session) {
			if _, err := manager.AppendModelChange(small.ProviderMeta.ProviderID, small.ID); err != nil {
				t.Fatal(err)
			}
			for range 100 {
				appendVirtualTranscript(t, manager, assistantFromModel(large, "ok"))
			}
		})
		if physical == nil || *physical != (ModelSelection{Provider: "faux", ModelID: "large"}) {
			t.Fatalf("physical selection = %+v", physical)
		}
		if !slices.Equal(lookups, []string{"faux/small"}) {
			t.Fatalf("physical lookups = %v, want [faux/small]", lookups)
		}

		routed, lookups := selectBranch(t, f, func(manager *icodingagent.Session) {
			if _, err := manager.AppendModelChange(small.ProviderMeta.ProviderID, small.ID); err != nil {
				t.Fatal(err)
			}
			appendVirtualTranscript(t, manager, assistantFromModel(small, "ok"))
			if _, err := manager.AppendModelChange(f.virtual.ProviderMeta.ProviderID, f.virtual.ID); err != nil {
				t.Fatal(err)
			}
			for range 100 {
				appendVirtualTranscript(t, manager, assistantFromModel(large, "ok"))
			}
		})
		if routed == nil || *routed != (ModelSelection{Provider: "router", ModelID: "auto"}) {
			t.Fatalf("routed selection = %+v", routed)
		}
		if !slices.Equal(lookups, []string{"router/auto"}) {
			t.Fatalf("routed lookups = %v, want [router/auto]", lookups)
		}
	})
	// virtual-models.test.ts:87-98
	t.Run("uses the last model_change without responses after it", func(t *testing.T) {
		f := createVirtualRuntime(t)
		small := f.runtime.GetModel("faux", "small")
		selection, lookups := selectBranch(t, f, func(manager *icodingagent.Session) {
			if _, err := manager.AppendModelChange(f.virtual.ProviderMeta.ProviderID, f.virtual.ID); err != nil {
				t.Fatal(err)
			}
			appendVirtualTranscript(t, manager, assistantFromModel(small, "ok"))
			if _, err := manager.AppendModelChange(small.ProviderMeta.ProviderID, small.ID); err != nil {
				t.Fatal(err)
			}
		})
		if selection == nil || *selection != (ModelSelection{Provider: "faux", ModelID: "small"}) || len(lookups) != 0 {
			t.Fatalf("selection = %+v, lookups = %v", selection, lookups)
		}
	})
	// virtual-models.test.ts:100-112
	t.Run("uses the latest physical response without a model_change", func(t *testing.T) {
		f := createVirtualRuntime(t)
		small, large := f.runtime.GetModel("faux", "small"), f.runtime.GetModel("faux", "large")
		selection, lookups := selectBranch(t, f, func(manager *icodingagent.Session) {
			failed := assistantFromModel(f.virtual, "")
			failed.StopReason = ai.StopReasonError
			appendVirtualTranscript(t, manager, assistantFromModel(small, "ok"), assistantFromModel(large, "ok"),
				// Failed routing leaves the virtual model on its message.
				failed)
		})
		if selection == nil || *selection != (ModelSelection{Provider: "faux", ModelID: "large"}) || len(lookups) != 0 {
			t.Fatalf("selection = %+v, lookups = %v", selection, lookups)
		}
	})
}

// BenchmarkGetBranchSelection measures the catalog lookups of a long branch (#10198): the cost must not grow with assistant messages.
func BenchmarkGetBranchSelection(b *testing.B) {
	manager := icodingagent.NewSession("branch-selection-bench", b.TempDir())
	model := ai.Model{ID: "large", ProviderMeta: ai.ProviderMetadata{ProviderID: "faux", API: ai.APIOpenAICompletions}}
	if _, err := manager.AppendModelChange("faux", "small"); err != nil {
		b.Fatal(err)
	}
	for range 2000 {
		message := assistantFromModel(&model, "ok")
		if _, err := manager.AppendMessage(agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: agent.RoleAssistant, Content: message.Content, API: message.API, Provider: message.Provider, ModelID: message.Model, StopReason: message.StopReason, Timestamp: message.Timestamp}}); err != nil {
			b.Fatal(err)
		}
	}
	branch := manager.GetBranch()
	var lookups int
	getModel := func(provider, modelID string) *ai.Model { lookups++; return &model }
	b.ReportAllocs()
	for b.Loop() {
		GetBranchSelection(branch, getModel)
	}
	b.ReportMetric(float64(lookups)/float64(b.N), "lookups/op")
}
