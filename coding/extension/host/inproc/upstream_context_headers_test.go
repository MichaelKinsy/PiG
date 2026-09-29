package inproc_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

func TestUpstreamRunnerContextAndHeaders(t *testing.T) {
	// .upstream/v0.87.1/packages/coding-agent/test/extensions-runner.test.ts:130
	t.Run("continues past undecided handlers and returns the first yes/no decision", func(t *testing.T) {
		cwd := t.TempDir()
		var exts []extension.Extension
		for _, decision := range []extension.ProjectTrustEventDecision{extension.ProjectTrustUndecided, extension.ProjectTrustNo} {
			exts = append(exts, extension.Extension{Handlers: map[string][]extension.HandlerFn{"project_trust": {func(...any) (any, error) {
				return extension.ProjectTrustEventResult{Trusted: decision, Remember: new(true)}, nil
			}}}})
		}
		r := inproc.NewRunner(exts, cwd)
		r.SetUIContext(nil, extension.ModeTUI)
		r.BindCore(extension.ExtensionActions{}, extension.ContextActions{}, nil)
		result, reported, err := inproc.EmitProjectTrust(r, t.Context(), extension.ProjectTrustEvent{Type: "project_trust", Cwd: cwd})
		if err != nil {
			t.Fatal(err)
		}
		want := &extension.ProjectTrustEventResult{Trusted: extension.ProjectTrustNo, Remember: new(true)}
		if !reflect.DeepEqual(result, want) || len(reported) != 0 {
			t.Fatalf("result=%+v errors=%v", result, reported)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/extensions-runner.test.ts:556
	t.Run("exposes print mode and hasUI false by default", func(t *testing.T) {
		r := inproc.NewRunner(nil, t.TempDir())
		r.BindCore(extension.ExtensionActions{}, extension.ContextActions{}, nil)
		ctx := extension.FromContext(r.DispatchContext(t.Context()))
		mode, err := ctx.Mode()
		if err != nil || mode != extension.ModePrint {
			t.Fatalf("mode=%q error=%v", mode, err)
		}
		hasUI, err := ctx.HasUI()
		if err != nil || hasUI {
			t.Fatalf("hasUI=%v error=%v", hasUI, err)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/extensions-runner.test.ts:566
	t.Run("exposes project trust state on ExtensionContext", func(t *testing.T) {
		r := inproc.NewRunner(nil, t.TempDir())
		r.BindCore(extension.ExtensionActions{}, extension.ContextActions{IsProjectTrusted: func() bool { return false }}, nil)
		trusted, err := extension.FromContext(r.DispatchContext(t.Context())).IsProjectTrusted()
		if err != nil || trusted {
			t.Fatalf("trusted=%v error=%v", trusted, err)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/extensions-runner.test.ts:602
	t.Run("calls error listeners when handler throws", func(t *testing.T) {
		ext := extension.Extension{Path: "throws.ts", Handlers: map[string][]extension.HandlerFn{"context": {func(...any) (any, error) { return nil, errors.New("Handler error!") }}}}
		r := inproc.NewRunner([]extension.Extension{ext}, t.TempDir())
		var reported []*extension.ExtensionError
		r.AddErrorListener(func(err *extension.ExtensionError) { reported = append(reported, err) })
		if _, err := r.EmitContext(t.Context(), []extension.AgentMessage{}); err != nil {
			t.Fatal(err)
		}
		if len(reported) != 1 || reported[0].Event != "context" || !strings.Contains(reported[0].Error, "Handler error!") {
			t.Fatalf("errors=%+v", reported)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/extensions-runner.test.ts:1360
	t.Run("lets a handler mutate headers in place and preserves existing headers", func(t *testing.T) {
		ext := extension.Extension{Path: "headers.ts", Handlers: map[string][]extension.HandlerFn{"before_provider_headers": {func(args ...any) (any, error) {
			args[0].(extension.BeforeProviderHeadersEvent).Headers["X-Turn-Index"] = new("3")
			return nil, nil
		}}}}
		r := inproc.NewRunner([]extension.Extension{ext}, t.TempDir())
		if !r.HasHandlers("before_provider_headers") {
			t.Fatal("handler missing")
		}
		headers, err := r.EmitBeforeProviderHeaders(t.Context(), extension.ProviderHeaders{"User-Agent": new("kimchi/1.0")})
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(headers, extension.ProviderHeaders{"X-Turn-Index": new("3"), "User-Agent": new("kimchi/1.0")}) {
			t.Fatalf("headers=%v", headers)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/extensions-runner.test.ts:1380
	t.Run("isolates a throwing handler and still applies the others", func(t *testing.T) {
		a := extension.Extension{Path: "a-throwing.ts", Handlers: map[string][]extension.HandlerFn{"before_provider_headers": {func(...any) (any, error) { return nil, errors.New("header handler boom") }}}}
		b := extension.Extension{Path: "b-good.ts", Handlers: map[string][]extension.HandlerFn{"before_provider_headers": {func(args ...any) (any, error) {
			args[0].(extension.BeforeProviderHeadersEvent).Headers["X-Good"] = new("yes")
			return nil, nil
		}}}}
		r := inproc.NewRunner([]extension.Extension{a, b}, t.TempDir())
		var reported []*extension.ExtensionError
		r.AddErrorListener(func(err *extension.ExtensionError) { reported = append(reported, err) })
		headers, err := r.EmitBeforeProviderHeaders(t.Context(), extension.ProviderHeaders{"User-Agent": new("x")})
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(headers, extension.ProviderHeaders{"X-Good": new("yes"), "User-Agent": new("x")}) {
			t.Fatalf("headers=%v", headers)
		}
		if len(reported) != 1 || reported[0].Event != "before_provider_headers" || !strings.Contains(reported[0].Error, "header handler boom") {
			t.Fatalf("errors=%+v", reported)
		}
	})
}
