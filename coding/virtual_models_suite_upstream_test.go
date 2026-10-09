package coding

// pi: packages/coding-agent/src/core/bug-report.ts

// Ports .upstream/v0.99.1/packages/coding-agent/test/suite/virtual-models.test.ts (13 cases) with the same inputs and expectations.
// "projects the session once per request under a virtual selection" (suite/virtual-models.test.ts:291-303) spies on buildSessionProjection; the Go port counts calls through Session.BuildSessionProjectionCalls.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

type virtualSuite struct {
	session  *Session
	runtime  *ModelRuntime
	faux     virtualFaux
	requests *[]ModelRouteRequest
	mu       sync.Mutex
	events   []agent.AgentEvent
}

const (
	virtualRetrySettings      = `"retry":{"enabled":true,"maxRetries":3,"baseDelayMs":1}`
	virtualCompactionSettings = `"compaction":{"keepRecentTokens":1,"reserveTokens":0}`
)

// virtualRoute is upstream's Route: (request) => ModelRoute, over the physical models of the faux provider.
type virtualRoute func(request ModelRouteRequest, find func(id string) *ai.Model) (ModelRoute, error)

// defaultVirtualRoute is upstream's `defaultRoute` (suite/virtual-models.test.ts:20-30): new user turns pick by thinking level, everything else stays put.
func defaultVirtualRoute(request ModelRouteRequest, find func(id string) *ai.Model) (ModelRoute, error) {
	if request.Reason == ModelRouteReasonDirect {
		return ModelRoute{Model: find("large"), ThinkingLevel: ai.ThinkingLow}, nil
	}
	var sticky *ModelRoutePreviousLike
	if request.Failed != nil {
		sticky = &ModelRoutePreviousLike{request.Failed.Model, request.Failed.ThinkingLevel}
	} else if request.Previous != nil {
		sticky = &ModelRoutePreviousLike{request.Previous.Model, request.Previous.ThinkingLevel}
	}
	if request.Reason != ModelRouteReasonUser && sticky != nil {
		level := sticky.level
		if level == "" {
			level = ai.ThinkingHigh
		}
		return ModelRoute{Model: sticky.model, ThinkingLevel: level}, nil
	}
	if request.ThinkingLevel == ai.ThinkingHigh {
		return ModelRoute{Model: find("large"), ThinkingLevel: ai.ThinkingHigh}, nil
	}
	return ModelRoute{Model: find("small"), ThinkingLevel: ai.ThinkingOff}, nil
}

// ModelRoutePreviousLike is the shared shape of a request's previous and failed responses.
type ModelRoutePreviousLike struct {
	model *ai.Model
	level ai.ModelThinkingLevel
}

// newVirtualSuite is upstream's `createRoutedHarness` (suite/virtual-models.test.ts:39-78).
func newVirtualSuite(t *testing.T, route virtualRoute, settings string, handlers map[string][]extension.HandlerFn) *virtualSuite {
	t.Helper()
	home := t.TempDir()
	t.Setenv("PIG_HOME", home)
	agentDir := filepath.Join(home, "agent")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte("{"+settings+"}"), 0o644); err != nil {
		t.Fatal(err)
	}
	services, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{CWD: t.TempDir(), AgentDir: agentDir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(services.Close)
	if err := services.Auth().Set("faux", ai.Credential{Type: ai.CredentialAPIKey, Key: "faux-key"}); err != nil {
		t.Fatal(err)
	}
	faux := ai.NewFauxProvider(ai.FauxConfig{ProviderID: "faux", Models: []ai.FauxModelDefinition{
		{ID: "small", ContextWindow: 1000},
		{ID: "large", ContextWindow: 50_000, MaxTokens: 4000, Reasoning: true},
	}})
	runtime := services.ModelRuntime()
	if err := runtime.RegisterNativeProvider(faux.Provider()); err != nil {
		t.Fatal(err)
	}
	suite := &virtualSuite{runtime: runtime, faux: faux, requests: &[]ModelRouteRequest{}}
	find := func(id string) *ai.Model { return runtime.GetModel("faux", id) }
	extRuntime := extension.CreateExtensionRuntime()
	if err := extRuntime.RegisterVirtualModel(VirtualModelDefinition{
		Provider: "router", ID: "auto", Name: "Auto", ThinkingLevels: []ai.ModelThinkingLevel{ai.ThinkingLow, ai.ThinkingHigh}, ContextWindow: 1000,
		Route: func(_ context.Context, request ModelRouteRequest) (ModelRoute, error) {
			suite.mu.Lock()
			*suite.requests = append(*suite.requests, request)
			suite.mu.Unlock()
			return route(request, find)
		},
	}, "/ext/router"); err != nil {
		t.Fatal(err)
	}
	runner := inproc.NewRunner([]extension.Extension{{Handlers: handlers}}, t.TempDir(), extRuntime)
	echo := &retryEchoTool{}
	session, err := NewSession(services, SessionOptions{Model: find("small"), Runner: runner, Tools: []agent.AgentTool{echo}, SkipBuiltinTools: true, NoSession: false})
	if err != nil {
		t.Fatal(err)
	}
	suite.session = session
	// upstream's test harness builds its Agent without a sessionId (suite/harness.ts:191), so the faux provider adds no prompt-cache usage. NewSession always sets one, and the cached prefix would double the reported context tokens.
	session.agent.SetSessionID("")
	session.Subscribe(func(event agent.AgentEvent) {
		suite.mu.Lock()
		suite.events = append(suite.events, event)
		suite.mu.Unlock()
	})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for event := range session.Events() {
			AcknowledgeEvent(event)
		}
	}()
	t.Cleanup(func() { _ = session.Close(); <-done })
	if err := session.SetModel(runtime.GetModel("router", "auto")); err != nil {
		t.Fatal(err)
	}
	if err := session.SetThinkingLevel(ai.ThinkingHigh); err != nil {
		t.Fatal(err)
	}
	return suite
}

func (s *virtualSuite) reasons() []ModelRouteReason {
	s.mu.Lock()
	defer s.mu.Unlock()
	reasons := []ModelRouteReason{}
	for _, request := range *s.requests {
		reasons = append(reasons, request.Reason)
	}
	return reasons
}

// dispatched is the physical model and thinking level recorded on each response.
func (s *virtualSuite) dispatched() []string {
	out := []string{}
	for _, message := range s.session.Messages() {
		if a := message.Assistant; a != nil {
			out = append(out, a.Provider+"/"+a.ModelID+":"+string(a.ThinkingLevel))
		}
	}
	return out
}

func (s *virtualSuite) compactionStartReasons() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	reasons := []string{}
	for _, event := range s.events {
		if start, ok := event.(agent.CompactionStartEvent); ok {
			reasons = append(reasons, start.Reason)
		}
	}
	return reasons
}

func (s *virtualSuite) compactionEnds() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, event := range s.events {
		if _, ok := event.(agent.CompactionEndEvent); ok {
			n++
		}
	}
	return n
}

func (s *virtualSuite) prompt(t *testing.T, text string) {
	t.Helper()
	if err := s.session.Prompt(t.Context(), text); err != nil {
		t.Fatal(err)
	}
}

func fauxText(text string) ai.FauxResponseStep {
	return ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText(text)}, StopReason: "stop"})
}

func fauxErrorStep(message string) ai.FauxResponseStep {
	return ai.FauxStaticStep(ai.FauxResponse{StopReason: "error", ErrorMessage: message})
}

func fauxEchoCall() ai.FauxResponseStep {
	return ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxToolCall("echo", map[string]any{"text": "hi"}, &ai.FauxToolCallOptions{ID: ""})}, StopReason: "toolUse"})
}

// fauxFactory scripts a response computed from the request, as upstream's `(context, options, state, model) => message`.
func fauxFactory(f func(options ai.StreamOptions, model *ai.Model) ai.FauxResponse) ai.FauxResponseStep {
	return ai.FauxFactoryStep(func(_ ai.TranscriptContext, options ai.StreamOptions, _ *ai.FauxProviderState, model *ai.Model) (ai.AssistantMessage, error) {
		return f(options, model).AssistantMessage(), nil
	})
}

// compactWith is upstream's `pi.on("session_before_compact", ...)` returning a summary for the prepared range.
func compactWith(summary string) map[string][]extension.HandlerFn {
	return map[string][]extension.HandlerFn{"session_before_compact": {func(args ...any) (any, error) {
		raw, err := json.Marshal(args[0].(extension.SessionBeforeCompactEvent).Preparation)
		if err != nil {
			return nil, err
		}
		var preparation struct {
			FirstKeptEntryID string
			TokensBefore     int
		}
		if err := json.Unmarshal(raw, &preparation); err != nil {
			return nil, err
		}
		return extension.SessionBeforeCompactResult{Compaction: &extension.CompactionResult{Summary: summary, FirstKeptEntryID: preparation.FirstKeptEntryID, TokensBefore: preparation.TokensBefore}}, nil
	}}}
}

func assertEqual[T any](t *testing.T, name string, got, want T) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s = %v, want %v", name, got, want)
	}
}

// upstream suite/virtual-models.test.ts:80-100
func TestVirtualSuiteRoutesEachRequestIncludingRetriesWhileTheSelectionStaysVirtual(t *testing.T) {
	s := newVirtualSuite(t, defaultVirtualRoute, virtualRetrySettings, nil)
	s.faux.SetResponses([]ai.FauxResponseStep{fauxErrorStep("overloaded_error"), fauxEchoCall(), fauxText("done")})

	s.prompt(t, "hello")

	assertEqual(t, "reasons", s.reasons(), []ModelRouteReason{"user", "retry", "continuation"})
	requests := *s.requests
	if requests[1].Failed == nil || requests[1].Failed.Message.ErrorMessage != "overloaded_error" {
		t.Fatalf("retry failed = %+v", requests[1].Failed)
	}
	if requests[2].Previous == nil || requests[2].Previous.Model.ID != "large" {
		t.Fatalf("continuation previous = %+v", requests[2].Previous)
	}
	assertEqual(t, "dispatched", s.dispatched(), []string{"faux/large:high", "faux/large:high"})
	if model := s.session.Model(); model.ProviderMeta.ProviderID != "router" || model.ID != "auto" {
		t.Fatalf("selection = %s/%s", model.ProviderMeta.ProviderID, model.ID)
	}
	if s.session.ThinkingLevel() != ai.ThinkingHigh {
		t.Fatalf("thinking level = %q", s.session.ThinkingLevel())
	}
	// Limits come from the physical model that produced the latest response, not the virtual model.
	if usage := s.session.ContextUsage(); usage == nil || usage.ContextWindow != 50_000 {
		t.Fatalf("context usage = %+v", usage)
	}
}

// upstream suite/virtual-models.test.ts:102-122
func TestVirtualSuiteRetriesTheFirstRequestOfATurnOnTheModelRoutedForThatTurn(t *testing.T) {
	s := newVirtualSuite(t, defaultVirtualRoute, virtualRetrySettings, nil)
	if err := s.session.SetThinkingLevel(ai.ThinkingLow); err != nil {
		t.Fatal(err)
	}
	s.faux.SetResponses([]ai.FauxResponseStep{fauxText("easy answer"), fauxErrorStep("overloaded_error"), fauxText("hard answer")})
	s.prompt(t, "easy")
	if err := s.session.SetThinkingLevel(ai.ThinkingHigh); err != nil {
		t.Fatal(err)
	}

	s.prompt(t, "hard")

	assertEqual(t, "reasons", s.reasons(), []ModelRouteReason{"user", "user", "retry"})
	// The retry reports the failed request on large next to the small response of the previous turn.
	requests := *s.requests
	if requests[2].Failed == nil || requests[2].Failed.Model.ID != "large" || requests[2].Previous == nil || requests[2].Previous.Model.ID != "small" {
		t.Fatalf("retry request = failed %+v previous %+v", requests[2].Failed, requests[2].Previous)
	}
	assertEqual(t, "dispatched", s.dispatched(), []string{"faux/small:off", "faux/large:high"})
}

// upstream suite/virtual-models.test.ts:124-147
func TestVirtualSuiteRoutesTheCompactAndRetryAfterATruncatedResponseAsARetry(t *testing.T) {
	s := newVirtualSuite(t, defaultVirtualRoute, virtualCompactionSettings, compactWith("overflow compacted"))
	future := time.Now().UnixMilli() + 10_000
	s.faux.SetResponses([]ai.FauxResponseStep{
		ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText(strings.Repeat("x", 64))}, StopReason: "length", Timestamp: &future}),
		fauxText("done"),
	})

	s.prompt(t, strings.Repeat("x", 5000))

	assertEqual(t, "compaction reasons", s.compactionStartReasons(), []string{"overflow"})
	// Compaction may fold the prompt into the summary, so the retry is not a new user turn.
	assertEqual(t, "reasons", s.reasons(), []ModelRouteReason{"user", "retry"})
	requests := *s.requests
	if requests[1].Failed == nil || requests[1].Failed.Model.ID != "large" || requests[1].Failed.Message.StopReason != ai.StopReasonLength {
		t.Fatalf("retry failed = %+v", requests[1].Failed)
	}
}

// upstream suite/virtual-models.test.ts:149-171
func TestVirtualSuiteRoutesRequestsAfterExtensionMessagesAsContinuations(t *testing.T) {
	continued := false
	s := newVirtualSuite(t, defaultVirtualRoute, "", map[string][]extension.HandlerFn{"agent_before_settle": {func(...any) (any, error) {
		if continued {
			return nil, nil
		}
		continued = true
		return extension.BoundaryResult{Entries: &[]extension.SessionBoundaryDraft{{Type: "custom_message", CustomType: "nudge", Content: "Keep going.", Display: false}}, Continue: new(true)}, nil
	}}})
	s.faux.SetResponses([]ai.FauxResponseStep{fauxText("first"), fauxText("second")})

	s.prompt(t, "hello")

	// The hidden custom message becomes a user message for the model, but the user did not write it.
	assertEqual(t, "reasons", s.reasons(), []ModelRouteReason{"user", "continuation"})
}

// upstream suite/virtual-models.test.ts:173-191
func TestVirtualSuiteRoutesTheFirstRequestOfAPromptAsAUserTurnWhenExtensionMessagesFollowThePrompt(t *testing.T) {
	s := newVirtualSuite(t, defaultVirtualRoute, "", map[string][]extension.HandlerFn{"before_agent_start": {func(...any) (any, error) {
		return &extension.BeforeAgentStartEventResult{Message: &extension.CustomMessageRef{CustomType: "context", Content: "Extra context.", Display: false}}, nil
	}}})
	s.faux.SetResponses([]ai.FauxResponseStep{fauxText("first"), fauxText("second")})

	s.prompt(t, "hello")
	s.prompt(t, "again")

	// The context ends with the extension message, but the request answers the user's prompt.
	messages := s.session.Messages()
	if role := messages[len(messages)-2].Role(); role != "custom" {
		t.Fatalf("second to last role = %q, want custom", role)
	}
	assertEqual(t, "reasons", s.reasons(), []ModelRouteReason{"user", "user"})
}

// upstream suite/virtual-models.test.ts:193-215
func TestVirtualSuiteEndsTheRunWithAnErrorResponseWhenRoutingFailsAndKeepsTheLastPhysicalLimits(t *testing.T) {
	var fail atomic.Bool
	s := newVirtualSuite(t, func(request ModelRouteRequest, find func(string) *ai.Model) (ModelRoute, error) {
		if fail.Load() {
			return ModelRoute{}, errRouterUnavailable
		}
		return defaultVirtualRoute(request, find)
	}, "", nil)
	s.faux.SetResponses([]ai.FauxResponseStep{fauxText("answer")})
	s.prompt(t, "hello")

	fail.Store(true)
	s.prompt(t, "again")

	messages := s.session.Messages()
	last := messages[len(messages)-1].Assistant
	if last == nil || last.Provider != "router" || last.ModelID != "auto" || last.StopReason != ai.StopReasonError || !strings.Contains(last.ErrorMessage, "router unavailable") {
		t.Fatalf("last message = %+v", last)
	}
	if n := s.faux.CallCount(); n != 1 {
		t.Fatalf("provider calls = %d, want 1", n)
	}
	// The failed attempt names the virtual model, whose declared window is 1k; the large model's 50k applies.
	if usage := s.session.ContextUsage(); usage == nil || usage.ContextWindow != 50_000 {
		t.Fatalf("context usage = %+v", usage)
	}
}

var errRouterUnavailable = &routerError{"router unavailable"}

type routerError struct{ text string }

func (e *routerError) Error() string { return e.text }

// upstream suite/virtual-models.test.ts:217-226
func TestVirtualSuiteChecksCompactionAgainstThePhysicalModelThatProducedTheResponse(t *testing.T) {
	s := newVirtualSuite(t, defaultVirtualRoute, "", nil)
	s.faux.SetResponses([]ai.FauxResponseStep{fauxText("short answer"), fauxText("long answer")})
	s.prompt(t, "hello")

	// About 20k tokens exceed the virtual model's declared 1k window but fit the large model's 50k.
	s.prompt(t, strings.Repeat("x", 80_000))

	if reasons := s.compactionStartReasons(); len(reasons) != 0 {
		t.Fatalf("compaction started: %v", reasons)
	}
}

// upstream suite/virtual-models.test.ts:228-257
func TestVirtualSuiteCompactsBeforeARequestRoutedToAModelWithASmallerWindow(t *testing.T) {
	s := newVirtualSuite(t, defaultVirtualRoute, virtualCompactionSettings, compactWith("compacted"))
	compactedBeforeSmall := false
	s.faux.SetResponses([]ai.FauxResponseStep{
		fauxText(strings.Repeat("y", 8000)),
		fauxFactory(func(ai.StreamOptions, *ai.Model) ai.FauxResponse {
			compactedBeforeSmall = s.compactionEnds() == 1
			return ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("small answer")}, StopReason: "stop"}
		}),
	})
	s.prompt(t, "hello")
	if reasons := s.compactionStartReasons(); len(reasons) != 0 {
		t.Fatalf("compaction started: %v", reasons)
	}

	// About 2k tokens fit the large model that answered last, but not the small model's 1k window.
	if err := s.session.SetThinkingLevel(ai.ThinkingLow); err != nil {
		t.Fatal(err)
	}
	s.prompt(t, "next")

	assertEqual(t, "compaction reasons", s.compactionStartReasons(), []string{"threshold"})
	if !compactedBeforeSmall {
		t.Fatal("the small model answered before compaction ended")
	}
	dispatched := s.dispatched()
	if last := dispatched[len(dispatched)-1]; last != "faux/small:off" {
		t.Fatalf("last dispatched = %q", last)
	}
}

// upstream suite/virtual-models.test.ts:259-289
func TestVirtualSuiteCompactsBetweenTurnsOfARunWhenTheNextRequestIsRoutedToASmallerWindow(t *testing.T) {
	route := func(request ModelRouteRequest, find func(string) *ai.Model) (ModelRoute, error) {
		if request.Reason == ModelRouteReasonContinuation {
			return ModelRoute{Model: find("small"), ThinkingLevel: ai.ThinkingOff}, nil
		}
		return defaultVirtualRoute(request, find)
	}
	s := newVirtualSuite(t, route, virtualCompactionSettings, compactWith("compacted"))
	compactedBeforeSmall := false
	s.faux.SetResponses([]ai.FauxResponseStep{
		fauxEchoCall(),
		fauxFactory(func(ai.StreamOptions, *ai.Model) ai.FauxResponse {
			compactedBeforeSmall = s.compactionEnds() == 1
			return ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("small answer")}, StopReason: "stop"}
		}),
	})

	// About 2k tokens fit the large model of the first turn, but not the small model of the second.
	s.prompt(t, strings.Repeat("x", 8000))

	assertEqual(t, "compaction reasons", s.compactionStartReasons(), []string{"threshold"})
	if !compactedBeforeSmall {
		t.Fatal("the small model answered before compaction ended")
	}
	assertEqual(t, "dispatched", s.dispatched(), []string{"faux/large:high", "faux/small:off"})
}

// upstream suite/virtual-models.test.ts:291-303
func TestVirtualSuiteProjectsTheSessionOncePerRequestUnderAVirtualSelection(t *testing.T) {
	s := newVirtualSuite(t, defaultVirtualRoute, "", nil)
	s.faux.SetResponses([]ai.FauxResponseStep{fauxEchoCall(), fauxText("done")})
	before := s.session.inner.BuildSessionProjectionCalls()

	s.prompt(t, "hello")

	// One per request, one between the turns, and one for the compaction check after the run.
	if got := s.session.inner.BuildSessionProjectionCalls() - before; got != 4 {
		t.Fatalf("projections = %d, want 4", got)
	}
}

// upstream suite/virtual-models.test.ts:305-344
func TestVirtualSuiteStoresRouterStateOnTheBranchAndPassesItToLaterRequests(t *testing.T) {
	var states []string
	route := func(request ModelRouteRequest, find func(string) *ai.Model) (ModelRoute, error) {
		states = append(states, string(request.State))
		turns := 0
		if request.State != nil {
			var state struct{ Turns int }
			if err := json.Unmarshal(request.State, &state); err != nil {
				return ModelRoute{}, err
			}
			turns = state.Turns
		}
		route, err := defaultVirtualRoute(request, find)
		// Returning request.state keeps it without storing it again. Direct requests neither get nor store state.
		if request.Reason == ModelRouteReasonContinuation {
			route.State = request.State
			return route, err
		}
		route.State = json.RawMessage(`{"turns":` + strconv.Itoa(turns+1) + `}`)
		return route, err
	}
	s := newVirtualSuite(t, route, `"compaction":{"keepRecentTokens":1}`, nil)
	s.faux.SetResponses([]ai.FauxResponseStep{fauxEchoCall(), fauxText("first"), fauxText("second"), fauxText("summary"), fauxText("summary")})

	s.prompt(t, "one")
	s.prompt(t, "two")
	stored := func() []string {
		var out []string
		for _, entry := range s.session.Inner().GetBranch() {
			if entry.Base().Type != "custom" {
				continue
			}
			var custom struct {
				CustomType string          `json:"customType"`
				Data       json.RawMessage `json:"data"`
			}
			if err := json.Unmarshal(entry.Raw(), &custom); err == nil && custom.CustomType == VirtualModelStateEntry {
				out = append(out, string(custom.Data))
			}
		}
		return out
	}
	assertEqual(t, "stored", stored(), []string{`{"provider":"router","modelId":"auto","state":{"turns":1}}`, `{"provider":"router","modelId":"auto","state":{"turns":2}}`})

	if _, err := s.session.Compact(t.Context(), ""); err != nil {
		t.Fatal(err)
	}

	assertEqual(t, "reasons", s.reasons(), []ModelRouteReason{"user", "continuation", "user", "direct"})
	assertEqual(t, "states", states, []string{"", `{"turns":1}`, `{"turns":1}`, ""})
	if n := len(stored()); n != 2 {
		t.Fatalf("stored states = %d, want 2", n)
	}
}

// upstream suite/virtual-models.test.ts:346-369
func TestVirtualSuiteDoesNotRouteCompactionsThatAnExtensionSupplies(t *testing.T) {
	route := func(request ModelRouteRequest, find func(string) *ai.Model) (ModelRoute, error) {
		if request.Reason == ModelRouteReasonDirect {
			return ModelRoute{}, errRouterUnavailable
		}
		return defaultVirtualRoute(request, find)
	}
	s := newVirtualSuite(t, route, `"compaction":{"keepRecentTokens":1}`, compactWith("extension summary"))
	s.faux.SetResponses([]ai.FauxResponseStep{fauxText("first answer"), fauxText("second answer")})
	s.prompt(t, "first")
	s.prompt(t, "second")

	result, err := s.session.Compact(t.Context(), "")

	if err != nil || result.Summary != "extension summary" {
		t.Fatalf("result = %+v, %v", result, err)
	}
	assertEqual(t, "reasons", s.reasons(), []ModelRouteReason{"user", "user"})
}

// agent-session.ts:4307-4313 summarizeForBugReport takes _getSummarizationRequestAuth, which routes a virtual model first (agent-session.ts:570-577):
// the bug report summary goes to the routed model with the router's thinking level, and bug-report.ts:346 sizes it from that model.
func TestVirtualSuiteRoutesBugReportSummaries(t *testing.T) {
	var summaries []string
	s := newVirtualSuite(t, defaultVirtualRoute, "", nil)
	summary := fauxFactory(func(options ai.StreamOptions, model *ai.Model) ai.FauxResponse {
		summaries = append(summaries, model.ID+":"+string(options.Thinking)+":"+strconv.Itoa(options.MaxTokens))
		return ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("bug summary")}, StopReason: "stop"}
	})
	s.faux.SetResponses([]ai.FauxResponseStep{fauxText("first answer"), summary})
	s.prompt(t, "first")

	text, err := s.session.SummarizeForBugReport(t.Context(), "")

	if err != nil || text != "bug summary" {
		t.Fatalf("summary = %q, %v", text, err)
	}
	assertEqual(t, "reasons", s.reasons(), []ModelRouteReason{"user", "direct"})
	assertEqual(t, "summaries", summaries, []string{"large:low:4000"})
}

// upstream suite/virtual-models.test.ts:371-396
func TestVirtualSuiteRoutesCompactionSummariesBeforeSizingThem(t *testing.T) {
	var summaries []string
	s := newVirtualSuite(t, defaultVirtualRoute, `"compaction":{"keepRecentTokens":1}`, nil)
	summary := fauxFactory(func(options ai.StreamOptions, model *ai.Model) ai.FauxResponse {
		level := string(options.Thinking)
		if level == "" {
			level = "off"
		}
		summaries = append(summaries, model.ID+":"+level+":"+strconv.Itoa(options.MaxTokens))
		return ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("summary")}, StopReason: "stop"}
	})
	// Compaction summarizes the history and the split turn prefix with one routed model.
	s.faux.SetResponses([]ai.FauxResponseStep{fauxText("first answer"), fauxText("second answer"), summary, summary})
	s.prompt(t, "first")
	s.prompt(t, "second")

	result, err := s.session.Compact(t.Context(), "")

	if err != nil || !strings.Contains(result.Summary, "summary") {
		t.Fatalf("result = %+v, %v", result, err)
	}
	assertEqual(t, "reasons", s.reasons(), []ModelRouteReason{"user", "user", "direct"})
	// The router's thinking level applies, and the output budget respects the large model's 4000 tokens.
	assertEqual(t, "summaries", summaries, []string{"large:low:4000", "large:low:4000"})
}
