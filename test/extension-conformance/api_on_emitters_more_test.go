package extensionconformance

import (
	"context"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/extensiontest"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// TestConformanceAPIOnBeforeAgentStartChainsSystemPrompt is the Go reference for pi.on("before_agent_start") (extensions/types.ts:1603-1606, runner.ts emitBeforeAgentStart): a handler registered through extension.API.OnBeforeAgentStart sees the system prompt the previous handler returned, and the combined result carries the last one.
//
// mutation-checked: the mutant "Runner.EmitBeforeAgentStart hands every handler the original system prompt" fails it.
func TestConformanceAPIOnBeforeAgentStartChainsSystemPrompt(t *testing.T) {
	t.Parallel()
	fake := &extensiontest.Fake{}
	var api extension.API = fake
	one, two := "one", "two"
	var seenBySecond string
	api.OnBeforeAgentStart(func(context.Context, extension.BeforeAgentStartEvent) (extension.BeforeAgentStartEventResult, error) {
		return extension.BeforeAgentStartEventResult{SystemPrompt: &one}, nil
	})
	api.OnBeforeAgentStart(func(_ context.Context, evt extension.BeforeAgentStartEvent) (extension.BeforeAgentStartEventResult, error) {
		var err error
		seenBySecond, err = evt.SystemPrompt.Get()
		return extension.BeforeAgentStartEventResult{SystemPrompt: &two}, err
	})
	runner := apiReferenceRunner(t, map[string][]extension.HandlerFn{"before_agent_start": bridgePtr(fake.OnBeforeAgentStartHandlers)})
	result, err := runner.EmitBeforeAgentStart(t.Context(), extension.UserPromptTrigger("hi", nil), extension.BuildSystemPromptOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if seenBySecond != "one" || result == nil || result.SystemPrompt == nil || *result.SystemPrompt != "two" {
		t.Fatalf("second handler saw %q, result %+v: want it to see \"one\" and the result to carry \"two\"", seenBySecond, result)
	}
}

// TestConformanceAPIOnResourcesDiscoverAggregatesPaths is the Go reference for pi.on("resources_discover") (extensions/types.ts:1558-1561, runner.ts emitResourcesDiscover): the paths a handler registered through extension.API.OnResourcesDiscover returns are collected in handler order and attributed to the extension that returned them.
//
// mutation-checked: the mutant "Runner.EmitResourcesDiscover drops the attribution of a path" fails it.
func TestConformanceAPIOnResourcesDiscoverAggregatesPaths(t *testing.T) {
	t.Parallel()
	fake := &extensiontest.Fake{}
	var api extension.API = fake
	api.OnResourcesDiscover(func(context.Context, extension.ResourcesDiscoverEvent) (extension.ResourcesDiscoverResult, error) {
		return extension.ResourcesDiscoverResult{SkillPaths: []string{"a"}, PromptPaths: []string{"p"}}, nil
	})
	api.OnResourcesDiscover(func(context.Context, extension.ResourcesDiscoverEvent) (extension.ResourcesDiscoverResult, error) {
		return extension.ResourcesDiscoverResult{SkillPaths: []string{"b"}, ThemePaths: []string{"t"}}, nil
	})
	runner := apiReferenceRunner(t, map[string][]extension.HandlerFn{"resources_discover": bridgePtr(fake.OnResourcesDiscoverHandlers)})
	result, err := runner.EmitResourcesDiscover(t.Context(), ".", "startup")
	if err != nil {
		t.Fatal(err)
	}
	attributed := func(paths ...string) []extension.AttributedResourcePath {
		var out []extension.AttributedResourcePath
		for _, p := range paths {
			out = append(out, extension.AttributedResourcePath{Path: p, ExtensionPath: "api-reference"})
		}
		return out
	}
	if !reflect.DeepEqual(result.SkillPaths, attributed("a", "b")) || !reflect.DeepEqual(result.PromptPaths, attributed("p")) || !reflect.DeepEqual(result.ThemePaths, attributed("t")) {
		t.Fatalf("aggregate %+v: want skills a,b, prompts p and themes t attributed to api-reference", result)
	}
}

// TestConformanceAPIOnCacheWarmingDecisionOverridesTheAction is the Go reference for the cache warming decision handler registered through extension.API.OnCacheWarmingDecision: the last handler that returns an action decides, and a handler that returns none leaves the earlier decision (extensions/types.ts:1589 on("cache_warming_decision"), runner.ts:1121 emitCacheWarmingDecision).
//
// mutation-checked: the mutant "Runner.EmitCacheWarmingDecision ignores the returned action" fails it.
func TestConformanceAPIOnCacheWarmingDecisionOverridesTheAction(t *testing.T) {
	t.Parallel()
	fake := &extensiontest.Fake{}
	var api extension.API = fake
	stop := extension.CacheWarmingActionStop
	api.OnCacheWarmingDecision(func(context.Context, extension.CacheWarmingDecisionEvent) (extension.CacheWarmingDecisionEventResult, error) {
		return extension.CacheWarmingDecisionEventResult{Action: &stop}, nil
	})
	api.OnCacheWarmingDecision(func(context.Context, extension.CacheWarmingDecisionEvent) (extension.CacheWarmingDecisionEventResult, error) {
		return extension.CacheWarmingDecisionEventResult{}, nil
	})
	runner := apiReferenceRunner(t, map[string][]extension.HandlerFn{"cache_warming_decision": bridgePtr(fake.OnCacheWarmingDecisionHandlers)})
	action, err := runner.EmitCacheWarmingDecision(t.Context(), extension.CacheWarmingDecisionEvent{Type: "cache_warming_decision", Action: extension.CacheWarmingActionWarm})
	if err != nil || action != extension.CacheWarmingActionStop {
		t.Fatalf("action %q (%v): want the first handler's stop to survive the second handler's empty result", action, err)
	}
}

// TestConformanceAPIOnUserBashFirstResultWins is the Go reference for pi.on("user_bash") (extensions/types.ts:1636, runner.ts emitUserBash): the first result a handler registered through extension.API.OnUserBash returns is the emit result, so a later handler never runs.
//
// mutation-checked: the mutant "Runner.EmitUserBash keeps running handlers after a result" fails it.
func TestConformanceAPIOnUserBashFirstResultWins(t *testing.T) {
	t.Parallel()
	fake := &extensiontest.Fake{}
	var api extension.API = fake
	var ran []string
	api.OnUserBash(func(context.Context, extension.UserBashEvent) (extension.UserBashEventResult, error) {
		ran = append(ran, "first")
		return extension.UserBashEventResult{Result: map[string]any{"output": "x", "exitCode": float64(0), "cancelled": false, "truncated": false}}, nil
	})
	api.OnUserBash(func(context.Context, extension.UserBashEvent) (extension.UserBashEventResult, error) {
		ran = append(ran, "second")
		return extension.UserBashEventResult{Result: map[string]any{"output": "y", "exitCode": float64(0), "cancelled": false, "truncated": false}}, nil
	})
	runner := apiReferenceRunner(t, map[string][]extension.HandlerFn{"user_bash": bridgePtr(fake.OnUserBashHandlers)})
	result, err := runner.EmitUserBash(t.Context(), extension.UserBashEvent{Type: "user_bash", Command: "ls"})
	if err != nil || result == nil || !reflect.DeepEqual(ran, []string{"first"}) || result.Result.(map[string]any)["output"] != "x" {
		t.Fatalf("result %+v (%v), handlers that ran %v: want the first handler's result alone", result, err, ran)
	}
}

// TestConformanceAPIOnBeforeProviderRequestChainsThePayload is the Go reference for pi.on("before_provider_request") (extensions/types.ts:1593-1595, runner.ts emitBeforeProviderRequest): a handler registered through extension.API.OnBeforeProviderRequest sees the payload the previous handler returned, and the emit result is the last returned payload.
//
// mutation-checked: the mutant "Runner.EmitBeforeProviderRequest hands every handler the original payload" fails it.
func TestConformanceAPIOnBeforeProviderRequestChainsThePayload(t *testing.T) {
	t.Parallel()
	fake := &extensiontest.Fake{}
	var api extension.API = fake
	var seenBySecond any
	api.OnBeforeProviderRequest(func(context.Context, extension.BeforeProviderRequestEvent) (extension.BeforeProviderRequestEventResult, error) {
		return map[string]any{"model": "one"}, nil
	})
	api.OnBeforeProviderRequest(func(_ context.Context, evt extension.BeforeProviderRequestEvent) (extension.BeforeProviderRequestEventResult, error) {
		seenBySecond = evt.Payload
		return map[string]any{"model": "two"}, nil
	})
	runner := apiReferenceRunner(t, map[string][]extension.HandlerFn{"before_provider_request": bridgeResult(fake.OnBeforeProviderRequestHandlers)})
	got, err := runner.EmitBeforeProviderRequest(t.Context(), map[string]any{"model": "original"})
	if err != nil || !reflect.DeepEqual(seenBySecond, map[string]any{"model": "one"}) || !reflect.DeepEqual(got, map[string]any{"model": "two"}) {
		t.Fatalf("second handler saw %v, emit returned %v (%v)", seenBySecond, got, err)
	}
}

// TestConformanceAPIOnProjectTrustFirstDecisionWins is the Go reference for pi.on("project_trust") (extensions/types.ts:698, runner.ts emitProjectTrustEvent): a handler registered through extension.API.OnProjectTrust that returns undecided falls through to the next handler, and the first yes or no ends the dispatch with the project folder's trust context passed to every handler.
//
// mutation-checked: the mutant "EmitProjectTrust treats undecided as a decision" fails it.
func TestConformanceAPIOnProjectTrustFirstDecisionWins(t *testing.T) {
	t.Parallel()
	fake := &extensiontest.Fake{}
	var api extension.API = fake
	var ran []string
	var trustCwd string
	api.OnProjectTrust(func(_ context.Context, _ extension.ProjectTrustEvent, trust extension.ProjectTrustContext) (extension.ProjectTrustEventResult, error) {
		ran = append(ran, "undecided")
		return extension.ProjectTrustEventResult{Trusted: extension.ProjectTrustUndecided}, nil
	})
	api.OnProjectTrust(func(_ context.Context, _ extension.ProjectTrustEvent, trust extension.ProjectTrustContext) (extension.ProjectTrustEventResult, error) {
		ran = append(ran, "yes")
		trustCwd = trust.Cwd
		return extension.ProjectTrustEventResult{Trusted: extension.ProjectTrustYes}, nil
	})
	api.OnProjectTrust(func(context.Context, extension.ProjectTrustEvent, extension.ProjectTrustContext) (extension.ProjectTrustEventResult, error) {
		ran = append(ran, "late")
		return extension.ProjectTrustEventResult{Trusted: extension.ProjectTrustNo}, nil
	})
	handlers := make([]extension.HandlerFn, 0, len(fake.OnProjectTrustHandlers))
	for _, handler := range fake.OnProjectTrustHandlers {
		handlers = append(handlers, func(args ...any) (any, error) {
			return handler(args[1].(context.Context), args[0].(extension.ProjectTrustEvent), args[2].(extension.ProjectTrustContext))
		})
	}
	runner := apiReferenceRunner(t, map[string][]extension.HandlerFn{"project_trust": handlers})
	result, _, err := inproc.EmitProjectTrust(runner, t.Context(), extension.ProjectTrustEvent{Type: "project_trust", Cwd: "/work"}, extension.ProjectTrustContext{Cwd: "/work"})
	if err != nil || result == nil || result.Trusted != extension.ProjectTrustYes || trustCwd != "/work" || !reflect.DeepEqual(ran, []string{"undecided", "yes"}) {
		t.Fatalf("result %+v (%v), handlers that ran %v, trust cwd %q: want undecided to fall through, yes to win, no third handler", result, err, ran, trustCwd)
	}
}

// TestConformanceAPIOnAgentBeforeSettleChainsContinuation is the Go reference for pi.on("agent_before_settle") (extensions/types.ts:1606 on("agent_before_settle"), runner.ts:1029 emitBoundary): a handler registered through extension.API.OnAgentBeforeSettle that returns continue is visible to the next handler as the proposal so far, and the dispatch result carries the final continuation.
//
// mutation-checked: the mutant "Runner.EmitBoundary hands every handler the initial proposal" fails it.
func TestConformanceAPIOnAgentBeforeSettleChainsContinuation(t *testing.T) {
	t.Parallel()
	fake := &extensiontest.Fake{}
	var api extension.API = fake
	yes := true
	var seenBySecond bool
	api.OnAgentBeforeSettle(func(context.Context, *extension.AgentBeforeSettleEvent) (extension.AgentBeforeSettleEventResult, error) {
		return extension.AgentBeforeSettleEventResult{Continue: &yes}, nil
	})
	api.OnAgentBeforeSettle(func(_ context.Context, evt *extension.AgentBeforeSettleEvent) (extension.AgentBeforeSettleEventResult, error) {
		seenBySecond = evt.Continue
		return extension.AgentBeforeSettleEventResult{}, nil
	})
	runner := apiReferenceRunner(t, map[string][]extension.HandlerFn{"agent_before_settle": bridgeResult(fake.OnAgentBeforeSettleHandlers)})
	result, err := runner.EmitBoundary(t.Context(), &extension.AgentBeforeSettleEvent{Type: "agent_before_settle", BoundaryState: extension.BoundaryState{Outcome: "completed"}},
		func([]extension.SessionBoundaryDraft) (extension.BoundaryContextPreview, error) {
			return extension.BoundaryContextPreview{}, nil
		})
	if err != nil || !seenBySecond || !result.Continue || !result.Valid {
		t.Fatalf("second handler saw continue=%v, result %+v (%v): want the first handler's continuation to chain", seenBySecond, result, err)
	}
}
