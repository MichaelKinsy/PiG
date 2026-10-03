package sdk

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

func routeRequestArgs(virtualID string, request string) json.RawMessage {
	return json.RawMessage(`{"provider":"router","id":"` + virtualID + `","request":` + request + `}`)
}

// Upstream loader.ts:480-497 and virtual-models.ts:87-101: a virtual model registered while the factory ran is declared without its route, in call order; registering the same provider and id again replaces it, and unregistering drops it.
func TestRegisterVirtualModelBeforeRunQueuesTheDeclarationWithoutTheRoute(t *testing.T) {
	ext := New("router")
	route := func(Context, ModelRouteRequest) (ModelRoute, error) { return ModelRoute{}, nil }
	for _, model := range []VirtualModel{
		{Provider: "router", ID: "auto", Name: "Auto", ThinkingLevels: []string{"off", "high"}, ContextWindow: 200000, MaxTokens: 8192, Input: []string{"text", "image"}, Route: route},
		{Provider: "router", ID: "cheap", Name: "Cheap", Route: route},
		{Provider: "router", ID: "gone", Name: "Gone", Route: route},
		{Provider: "router", ID: "auto", Name: "Auto v2", ThinkingLevels: []string{"off"}, Route: route},
	} {
		if err := ext.RegisterVirtualModel(model); err != nil {
			t.Fatal(err)
		}
	}
	ext.UnregisterVirtualModel("router", "gone")
	host, reg, done := surfaceHost(t, ext, nil)
	defer surfaceShutdown(t, host, done)
	want := []virtualModelDecl{
		{Provider: "router", ID: "auto", Name: "Auto v2", ThinkingLevels: []string{"off"}},
		{Provider: "router", ID: "cheap", Name: "Cheap"},
	}
	if !reflect.DeepEqual(reg.VirtualModels, want) {
		t.Fatalf("virtual_models = %+v, want %+v", reg.VirtualModels, want)
	}
	if strings.Contains(mustJSON(t, reg.VirtualModels), `"route"`) {
		t.Fatalf("the declaration carries the route: %s", mustJSON(t, reg.VirtualModels))
	}
}

// Upstream loader.ts:480-497 after load: the declaration goes to the host at once, and a refusal is the caller's error.
func TestRegisterVirtualModelAtRuntimeCallsTheHost(t *testing.T) {
	ext := New("router")
	route := func(Context, ModelRouteRequest) (ModelRoute, error) { return ModelRoute{}, nil }
	got := make(chan error, 2)
	ext.Command("go", "", func(ctx Context, _ string) error {
		got <- ctx.RegisterVirtualModel(VirtualModel{Provider: "router", ID: "late", Name: "Late", ContextWindow: 1000, Route: route})
		got <- ctx.RegisterVirtualModel(VirtualModel{Provider: "router", ID: "claimed", Name: "Claimed", Route: route})
		ctx.UnregisterVirtualModel("router", "late")
		return nil
	})
	host, _, done := surfaceHost(t, ext, nil)
	defer surfaceShutdown(t, host, done)
	calls, resp := runSurfaceCommand(t, host, "go", func(call *callMsg) *callResultMsg {
		if strings.Contains(string(call.Args), "claimed") {
			return &callResultMsg{Error: &errorInfo{Message: "virtual model router/claimed is the id of a physical model"}}
		}
		return &callResultMsg{}
	})
	if resp.Error != nil || len(calls) != 3 {
		t.Fatalf("calls = %+v, response = %+v", calls, resp)
	}
	if calls[0].Method != "registerVirtualModel" || string(calls[0].Args) != `{"provider":"router","id":"late","name":"Late","contextWindow":1000}` {
		t.Fatalf("register call = %s %s", calls[0].Method, calls[0].Args)
	}
	if calls[2].Method != "unregisterVirtualModel" || string(calls[2].Args) != `{"provider":"router","id":"late"}` {
		t.Fatalf("unregister call = %s %s", calls[2].Method, calls[2].Args)
	}
	if err := recv(t, got); err != nil {
		t.Fatalf("first registration: %v", err)
	}
	if err := recv(t, got); err == nil || !strings.Contains(err.Error(), "is the id of a physical model") {
		t.Fatalf("refused registration error = %v", err)
	}
}

// Upstream virtual-models.ts:55-85 (ModelRouteRequest, ModelRoute) and loader.ts:485-487: the host's route request reaches the extension's route with the request fields, and the route's model, thinking level and state go back.
func TestVirtualModelRouteRunsInTheExtension(t *testing.T) {
	ext := New("router")
	seen := make(chan ModelRouteRequest, 4)
	ctxSeen := make(chan Context, 4)
	registerVirtualModel(t, ext, VirtualModel{Provider: "router", ID: "auto", Name: "Auto", Route: func(ctx Context, request ModelRouteRequest) (ModelRoute, error) {
		seen <- request
		ctxSeen <- ctx
		switch request.Reason {
		case ModelRouteReasonRetry:
			return ModelRoute{}, context.DeadlineExceeded
		case ModelRouteReasonDirect:
			return ModelRoute{Model: map[string]any{"provider": "anthropic", "id": "haiku"}, ThinkingLevel: "off"}, nil
		}
		return ModelRoute{Model: map[string]any{"provider": "anthropic", "id": "opus"}, ThinkingLevel: "high", State: map[string]any{"turns": 2}}, nil
	}})
	host, _, done := surfaceHost(t, ext, nil)
	defer surfaceShutdown(t, host, done)
	request := `{"model":{"provider":"router","id":"auto","name":"Auto"},"thinkingLevel":"medium","reason":"user","previous":{"model":{"provider":"anthropic","id":"sonnet"},"thinkingLevel":"low"},"state":{"turns":1},"messages":[{"role":"system","content":"s"},{"role":"user","content":"hello"}]}`
	_, resp := runSurfaceRequest(t, host, "route-1", &requestMsg{Method: "virtual_model_route", Args: routeRequestArgs("auto", request)}, nil)
	if resp.Error != nil {
		t.Fatalf("route failed: %+v", resp.Error)
	}
	got := recv(t, seen)
	if got.Model["id"] != "auto" || got.ThinkingLevel != "medium" || got.Reason != ModelRouteReasonUser ||
		got.Previous == nil || got.Previous.Model["id"] != "sonnet" || got.Previous.ThinkingLevel != "low" ||
		got.Failed != nil || string(got.State) != `{"turns":1}` || len(got.Messages) != 2 || got.Messages[1]["content"] != "hello" {
		t.Fatalf("route request = %+v", got)
	}
	if want := `{"model":{"id":"opus","provider":"anthropic"},"thinkingLevel":"high","state":{"turns":2}}`; string(resp.Result) != want {
		t.Fatalf("route result = %s, want %s", resp.Result, want)
	}
	if routerCtx := recv(t, ctxSeen); routerCtx.ext != ext {
		t.Fatal("the route ran without the extension's context")
	}

	// virtual-models.ts:75-85: an absent state keeps the current state, so it is not on the wire; `direct` requests have none.
	_, resp = runSurfaceRequest(t, host, "route-2", &requestMsg{Method: "virtual_model_route", Args: routeRequestArgs("auto", `{"model":{"provider":"router","id":"auto"},"thinkingLevel":"off","reason":"direct","messages":[]}`)}, nil)
	if resp.Error != nil || string(resp.Result) != `{"model":{"id":"haiku","provider":"anthropic"},"thinkingLevel":"off"}` {
		t.Fatalf("direct result = %s, error = %+v", resp.Result, resp.Error)
	}
	if got := recv(t, seen); got.State != nil || got.Previous != nil {
		t.Fatalf("direct request = %+v", got)
	}

	// virtual-models.ts:66-69: a retry carries the failed request's model, level and message.
	failed := `{"model":{"provider":"router","id":"auto"},"thinkingLevel":"high","reason":"retry","failed":{"model":{"provider":"anthropic","id":"opus"},"thinkingLevel":"high","message":{"role":"assistant","stopReason":"error","errorMessage":"overloaded"}},"messages":[]}`
	_, resp = runSurfaceRequest(t, host, "route-3", &requestMsg{Method: "virtual_model_route", Args: routeRequestArgs("auto", failed)}, nil)
	if resp.Error == nil || !strings.Contains(resp.Error.Message, "deadline exceeded") {
		t.Fatalf("a route error must fail the request: %+v", resp)
	}
	if got := recv(t, seen); got.Failed == nil || got.Failed.Model["id"] != "opus" || got.Failed.ThinkingLevel != "high" || got.Failed.Message["errorMessage"] != "overloaded" {
		t.Fatalf("failed request = %+v", got.Failed)
	}
}

// A router replaced by a later registration of the same provider and id runs, and a request for a virtual model that is not registered fails with a message that names it.
func TestVirtualModelRouteFollowsReplacementAndUnregistration(t *testing.T) {
	ext := New("router")
	answer := func(id string) ModelRouteFunc {
		return func(Context, ModelRouteRequest) (ModelRoute, error) {
			return ModelRoute{Model: map[string]any{"provider": "p", "id": id}, ThinkingLevel: "off"}, nil
		}
	}
	registerVirtualModel(t, ext, VirtualModel{Provider: "router", ID: "auto", Name: "Auto", Route: answer("first")})
	registerVirtualModel(t, ext, VirtualModel{Provider: "router", ID: "auto", Name: "Auto", Route: answer("second")})
	host, _, done := surfaceHost(t, ext, nil)
	defer surfaceShutdown(t, host, done)
	request := `{"model":{},"thinkingLevel":"off","reason":"user","messages":[]}`
	_, resp := runSurfaceRequest(t, host, "r1", &requestMsg{Method: "virtual_model_route", Args: routeRequestArgs("auto", request)}, nil)
	if resp.Error != nil || !strings.Contains(string(resp.Result), `"id":"second"`) {
		t.Fatalf("replaced route answered %s (%+v)", resp.Result, resp.Error)
	}
	_, resp = runSurfaceRequest(t, host, "r2", &requestMsg{Method: "virtual_model_route", Args: routeRequestArgs("nope", request)}, nil)
	if resp.Error == nil || !strings.Contains(resp.Error.Message, "router/nope") {
		t.Fatalf("unknown virtual model response = %+v", resp)
	}
}

// Upstream virtual-models.ts:100 route(request) receives request.signal: the host's cancel of the request cancels the router's context.
func TestVirtualModelRouteObservesCancellation(t *testing.T) {
	ext := New("router")
	started := make(chan struct{})
	cancelled := make(chan error, 1)
	registerVirtualModel(t, ext, VirtualModel{Provider: "router", ID: "auto", Name: "Auto", Route: func(ctx Context, _ ModelRouteRequest) (ModelRoute, error) {
		close(started)
		<-ctx.Done()
		cancelled <- ctx.Err()
		return ModelRoute{}, ctx.Err()
	}})
	host, _, done := surfaceHost(t, ext, nil)
	defer surfaceShutdown(t, host, done)
	host.writeEnvelope(t, envelope{Type: msgRequest, ID: "route-c", Request: &requestMsg{Method: "virtual_model_route", Args: routeRequestArgs("auto", `{"model":{},"thinkingLevel":"off","reason":"user","messages":[]}`)}})
	recv(t, started)
	host.writeEnvelope(t, envelope{Type: msgCancel, ID: "route-c"})
	if err := recv(t, cancelled); err == nil {
		t.Fatal("the router's context was not cancelled")
	}
	for {
		env := host.readEnvelope(t)
		if env.Type == msgResponse {
			if env.Response.Error == nil {
				t.Fatalf("cancelled route answered %s", env.Response.Result)
			}
			return
		}
	}
}

// Upstream virtual-models.ts:75-85: returning `request.state` keeps the current state. Before the first state the request has none, so a router that returns it sends no state, as JSON.stringify drops undefined; a stored state that router returns goes back unchanged.
func TestVirtualModelRouteReturningTheRequestStateKeepsIt(t *testing.T) {
	ext := New("router")
	registerVirtualModel(t, ext, VirtualModel{Provider: "router", ID: "auto", Name: "Auto", Route: func(_ Context, request ModelRouteRequest) (ModelRoute, error) {
		return ModelRoute{Model: map[string]any{"provider": "p", "id": "m"}, ThinkingLevel: "off", State: request.State}, nil
	}})
	host, _, done := surfaceHost(t, ext, nil)
	defer surfaceShutdown(t, host, done)
	_, resp := runSurfaceRequest(t, host, "r1", &requestMsg{Method: "virtual_model_route", Args: routeRequestArgs("auto", `{"model":{},"thinkingLevel":"off","reason":"user","messages":[]}`)}, nil)
	if want := `{"model":{"id":"m","provider":"p"},"thinkingLevel":"off"}`; resp.Error != nil || string(resp.Result) != want {
		t.Fatalf("route without a state = %s (%+v), want %s", resp.Result, resp.Error, want)
	}
	_, resp = runSurfaceRequest(t, host, "r2", &requestMsg{Method: "virtual_model_route", Args: routeRequestArgs("auto", `{"model":{},"thinkingLevel":"off","reason":"user","state":{"turns":1},"messages":[]}`)}, nil)
	if want := `{"model":{"id":"m","provider":"p"},"thinkingLevel":"off","state":{"turns":1}}`; resp.Error != nil || string(resp.Result) != want {
		t.Fatalf("route with a state = %s (%+v), want %s", resp.Result, resp.Error, want)
	}
}
