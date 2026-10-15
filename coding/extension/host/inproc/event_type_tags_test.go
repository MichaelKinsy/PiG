package inproc_test

import (
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// packages/coding-agent/src/core/extensions/types.ts: each event interface carries its literal `type` tag
// (InputEvent "input", ContextEvent "context", ContextWithSystemEvent "context_with_system",
// BeforeProviderRequestEvent "before_provider_request", BeforeProviderHeadersEvent "before_provider_headers"),
// and the runner hands every handler an event stamped with that tag together with its payload fields.
// Pi: packages/coding-agent/src/core/extensions/types.ts:1133 (InputEvent.type); packages/coding-agent/src/core/extensions/types.ts:865 (ContextEvent.type); packages/coding-agent/src/core/extensions/types.ts:875 (ContextWithSystemEvent.type); packages/coding-agent/src/core/extensions/types.ts:881 (BeforeProviderRequestEvent.type); packages/coding-agent/src/core/extensions/types.ts:891 (BeforeProviderHeadersEvent.type).
func TestRunnerStampsEachEventWithItsTypeTag(t *testing.T) {
	seen := map[string]any{}
	handler := func(name string) extension.HandlerFn {
		return func(args ...any) (any, error) {
			seen[name] = args[0]
			return nil, nil
		}
	}
	ext := extension.Extension{Handlers: map[string][]extension.HandlerFn{
		"input":                   {handler("input")},
		"context":                 {handler("context")},
		"context_with_system":     {handler("context_with_system")},
		"before_provider_request": {handler("before_provider_request")},
		"before_provider_headers": {handler("before_provider_headers")},
	}}
	r := inproc.NewRunner([]extension.Extension{ext}, t.TempDir())
	r.BindCore(extension.ExtensionActions{}, extension.ContextActions{}, nil)
	ctx := t.Context()
	if _, err := r.EmitInput(ctx, "hello", nil, extension.InputSource("interactive"), "steer"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.EmitContext(ctx, []extension.AgentMessage{}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.EmitContextWithSystem(ctx, []extension.AgentMessage{}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.EmitBeforeProviderRequest(ctx, map[string]any{"model": "m"}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.EmitBeforeProviderHeaders(ctx, extension.ProviderHeaders{"x-a": new("1")}); err != nil {
		t.Fatal(err)
	}
	if e, ok := seen["input"].(extension.InputEvent); !ok || e.Type != "input" || e.Text != "hello" || e.StreamingBehavior != "steer" {
		t.Errorf("input event = %#v", seen["input"])
	}
	if e, ok := seen["context"].(extension.ContextEvent); !ok || e.Type != "context" {
		t.Errorf("context event = %#v", seen["context"])
	}
	if e, ok := seen["context_with_system"].(extension.ContextWithSystemEvent); !ok || e.Type != "context_with_system" {
		t.Errorf("context_with_system event = %#v", seen["context_with_system"])
	}
	if e, ok := seen["before_provider_request"].(extension.BeforeProviderRequestEvent); !ok || e.Type != "before_provider_request" {
		t.Errorf("before_provider_request event = %#v", seen["before_provider_request"])
	}
	if e, ok := seen["before_provider_headers"].(extension.BeforeProviderHeadersEvent); !ok || e.Type != "before_provider_headers" || e.Headers["x-a"] == nil || *e.Headers["x-a"] != "1" {
		t.Errorf("before_provider_headers event = %#v", seen["before_provider_headers"])
	}
}
