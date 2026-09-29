package coding

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"testing/synctest"

	"github.com/MichaelKinsy/PiG/ai"
)

func TestServicesOwnBackgroundModelTasksNotBorrowingRuntime(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		services := newTestServices(t)
		defer services.Close()
		runtime, err := NewRuntime(RuntimeOptions{Services: services})
		if err != nil {
			t.Fatal(err)
		}
		started, stopped := make(chan struct{}), make(chan struct{})
		var taskContext context.Context
		if !services.ModelRuntime().startBackground(func(ctx context.Context) {
			taskContext = ctx
			close(started)
			<-ctx.Done()
			close(stopped)
		}) {
			t.Fatal("live Services rejected background work")
		}
		<-started
		if err := runtime.Close(); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if taskContext.Err() != nil {
			t.Fatal("Runtime.Close cancelled caller-owned Services")
		}
		services.Close()
		select {
		case <-stopped:
		default:
			t.Fatal("Services.Close did not drain background work")
		}
		if !errors.Is(taskContext.Err(), context.Canceled) {
			t.Fatalf("background context=%v", taskContext.Err())
		}
		if services.ModelRuntime().startBackground(func(context.Context) { t.Error("closed Services ran work") }) {
			t.Fatal("closed Services accepted work")
		}
	})
}

func TestStandaloneModelRuntimeHasTheSameCloseOwner(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		modelRuntime, err := CreateModelRuntime(t.Context(), CreateModelRuntimeOptions{
			AuthPath: filepath.Join(t.TempDir(), "auth.json"), ModelsPath: new((*string)(nil)), RefreshOnCreate: new(false),
		})
		if err != nil {
			t.Fatal(err)
		}
		defer modelRuntime.Close()
		started, stopped := make(chan struct{}), make(chan struct{})
		modelRuntime.startBackground(func(ctx context.Context) { close(started); <-ctx.Done(); close(stopped) })
		<-started
		modelRuntime.Close()
		select {
		case <-stopped:
		default:
			t.Fatal("ModelRuntime.Close did not use its Services owner")
		}
		modelRuntime.services.Close()
	})
}

// Services.Close drains Services-owned model tasks only. A caller-owned stream on a Models-collection provider belongs to its Session, and cmd/pig's exitProcess calls Services.Close before it stops extension transports (cmd/pig/profiling.go), so waiting for that stream could hold process exit behind an extension response. Pi has no equivalent wait: its process exits.
func TestServicesCloseDoesNotAwaitCallerOwnedStream(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		services, _ := nativeCompatServices(t, "", nil)
		model := nativeCompatModel("native", "close-stream", "https://fixture.invalid")
		provider := nativeCompatProvider(model)
		started, release := make(chan struct{}), make(chan struct{})
		stream := func(context.Context, *ai.Model, ai.TranscriptContext, ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
			result := ai.NewAssistantMessageEventStream()
			go func() {
				close(started)
				<-release
				result.End(&ai.AssistantMessage{Model: "native", Provider: "close-stream", StopReason: ai.StopReasonStop})
			}()
			return result, nil
		}
		provider.Stream, provider.StreamSimple = stream, stream
		runtime := services.ModelRuntime()
		if err := runtime.RegisterNativeProvider(provider); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		out := runtime.Stream(context.Background(), runtime.GetModel("close-stream", "native"), ai.Context{}, ai.StreamOptions{})
		<-started
		closed := make(chan struct{})
		go func() { services.Close(); close(closed) }()
		synctest.Wait()
		select {
		case <-closed:
		default:
			t.Error("Services.Close waited for a caller-owned in-flight stream")
		}
		close(release)
		<-closed
		if result := out.Result(); result.StopReason != ai.StopReasonStop {
			t.Fatalf("caller stream result=%+v", result)
		}
	})
}
