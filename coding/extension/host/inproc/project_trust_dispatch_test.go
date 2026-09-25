package inproc_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

func TestEmitProjectTrustFirstDecisiveResultWinsAfterErrorsAndUndecided(t *testing.T) {
	var order []string
	exts := []extension.Extension{
		projectTrustExtension("/ext/a", func(extension.ProjectTrustEvent, context.Context) (any, error) {
			order = append(order, "error")
			return nil, errors.New("broken")
		}, func(extension.ProjectTrustEvent, context.Context) (any, error) {
			order = append(order, "undecided")
			return extension.ProjectTrustEventResult{Trusted: extension.ProjectTrustUndecided, Remember: new(true)}, nil
		}),
		projectTrustExtension("/ext/b", func(extension.ProjectTrustEvent, context.Context) (any, error) {
			order = append(order, "yes")
			return extension.ProjectTrustEventResult{Trusted: extension.ProjectTrustYes, Remember: new(true)}, nil
		}, func(extension.ProjectTrustEvent, context.Context) (any, error) {
			order = append(order, "must-not-run")
			return extension.ProjectTrustEventResult{Trusted: extension.ProjectTrustNo}, nil
		}),
	}

	result, gotErrors, err := inproc.EmitProjectTrust(inproc.NewRunner(exts, "/project"),
		context.Background(), extension.ProjectTrustEvent{Type: "project_trust", Cwd: "/project"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || result.Trusted != extension.ProjectTrustYes || result.Remember == nil || !*result.Remember {
		t.Fatalf("result = %+v", result)
	}
	if !reflect.DeepEqual(order, []string{"error", "undecided", "yes"}) {
		t.Fatalf("order = %v", order)
	}
	if len(gotErrors) != 1 || gotErrors[0].ExtensionPath != "/ext/a" || gotErrors[0].Event != "project_trust" || gotErrors[0].Error != "broken" {
		t.Fatalf("errors = %+v", gotErrors)
	}
}

func TestEmitProjectTrustAwaitsHandlerCompletion(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{})
	runner := inproc.NewRunner([]extension.Extension{projectTrustExtension("/ext", func(extension.ProjectTrustEvent, context.Context) (any, error) {
		close(started)
		<-release
		return extension.ProjectTrustEventResult{Trusted: extension.ProjectTrustNo}, nil
	})}, "/project")

	done := make(chan struct{})
	go func() {
		defer close(done)
		result, _, err := inproc.EmitProjectTrust(runner, context.Background(), extension.ProjectTrustEvent{Type: "project_trust", Cwd: "/project"})
		if err != nil || result == nil || result.Trusted != extension.ProjectTrustNo {
			t.Errorf("result=%+v err=%v", result, err)
		}
	}()
	<-started
	select {
	case <-done:
		t.Fatal("EmitProjectTrust returned before handler completion")
	default:
	}
	close(release)
	<-done
}

func TestEmitProjectTrustCancellationReachesHandler(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	runner := inproc.NewRunner([]extension.Extension{projectTrustExtension("/ext", func(_ extension.ProjectTrustEvent, dispatch context.Context) (any, error) {
		return nil, dispatch.Err()
	})}, "/project")
	result, gotErrors, err := inproc.EmitProjectTrust(runner, ctx, extension.ProjectTrustEvent{Type: "project_trust", Cwd: "/project"})
	if err != nil || result != nil {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if len(gotErrors) != 1 || gotErrors[0].Error != context.Canceled.Error() {
		t.Fatalf("errors = %+v", gotErrors)
	}
}

func BenchmarkEmitProjectTrustFirstDecisive(b *testing.B) {
	exts := make([]extension.Extension, 0, 24)
	for i := range 24 {
		decision := extension.ProjectTrustUndecided
		if i == 23 {
			decision = extension.ProjectTrustYes
		}
		exts = append(exts, projectTrustExtension("/ext", func(extension.ProjectTrustEvent, context.Context) (any, error) {
			return extension.ProjectTrustEventResult{Trusted: decision}, nil
		}))
	}
	runner := inproc.NewRunner(exts, "/project")
	event := extension.ProjectTrustEvent{Type: "project_trust", Cwd: "/project"}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		result, handlerErrors, err := inproc.EmitProjectTrust(runner, context.Background(), event)
		if err != nil || len(handlerErrors) != 0 || result == nil || result.Trusted != extension.ProjectTrustYes {
			b.Fatalf("result=%+v errors=%+v err=%v", result, handlerErrors, err)
		}
	}
}

func projectTrustExtension(path string, handlers ...func(extension.ProjectTrustEvent, context.Context) (any, error)) extension.Extension {
	wrapped := make([]extension.HandlerFn, 0, len(handlers))
	for _, handler := range handlers {
		h := handler
		wrapped = append(wrapped, func(args ...any) (any, error) {
			event, _ := args[0].(extension.ProjectTrustEvent)
			ctx, _ := args[1].(context.Context)
			return h(event, ctx)
		})
	}
	return extension.Extension{Path: path, ResolvedPath: path, Handlers: map[string][]extension.HandlerFn{"project_trust": wrapped}}
}
