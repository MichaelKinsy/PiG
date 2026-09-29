package inproc_test

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

func TestUpstreamInputEvent(t *testing.T) {
	makeRunner := func(handlers ...extension.HandlerFn) *inproc.Runner {
		exts := make([]extension.Extension, len(handlers))
		for i, handler := range handlers {
			exts[i] = newFakeExtension("input-test")
			exts[i].Handlers["input"] = []extension.HandlerFn{handler}
		}
		return inproc.NewRunner(exts, ".")
	}
	emit := func(t *testing.T, r *inproc.Runner, text string, images []extension.ImageContent, source string, streaming string, want extension.InputEventResult) {
		t.Helper()
		got, err := r.EmitInput(t.Context(), text, images, source, streaming)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("EmitInput = %#v, %v; want %#v", got, err, want)
		}
	}
	// .upstream/v0.87.1/packages/coding-agent/test/extensions-input-event.test.ts:37
	t.Run("returns continue when no handlers, undefined return, or explicit continue", func(t *testing.T) {
		for _, r := range []*inproc.Runner{makeRunner(), makeRunner(func(...any) (any, error) { return nil, nil }), makeRunner(func(...any) (any, error) { return extension.InputEventResultContinue{}, nil })} {
			emit(t, r, "x", nil, "interactive", "", extension.InputEventResultContinue{})
		}
	})
	original := []extension.ImageContent{map[string]any{"type": "image", "data": "orig", "mimeType": "image/png"}}
	// .upstream/v0.87.1/packages/coding-agent/test/extensions-input-event.test.ts:48
	t.Run("transforms text and preserves images when omitted", func(t *testing.T) {
		r := makeRunner(func(args ...any) (any, error) {
			e := args[0].(extension.InputEvent)
			return extension.InputEventResultTransform{Text: "T:" + e.Text}, nil
		})
		emit(t, r, "hi", original, "interactive", "", extension.InputEventResultTransform{Text: "T:hi", Images: original})
	})
	// .upstream/v0.87.1/packages/coding-agent/test/extensions-input-event.test.ts:57
	t.Run("transforms and replaces images when provided", func(t *testing.T) {
		images := []extension.ImageContent{map[string]any{"type": "image", "data": "new", "mimeType": "image/jpeg"}}
		r := makeRunner(func(...any) (any, error) { return extension.InputEventResultTransform{Text: "X", Images: images}, nil })
		emit(t, r, "hi", original, "interactive", "", extension.InputEventResultTransform{Text: "X", Images: images})
	})
	// .upstream/v0.87.1/packages/coding-agent/test/extensions-input-event.test.ts:69
	t.Run("chains transforms across multiple handlers", func(t *testing.T) {
		r := makeRunner(func(args ...any) (any, error) {
			return extension.InputEventResultTransform{Text: args[0].(extension.InputEvent).Text + "[1]"}, nil
		}, func(args ...any) (any, error) {
			return extension.InputEventResultTransform{Text: args[0].(extension.InputEvent).Text + "[2]"}, nil
		})
		emit(t, r, "X", nil, "interactive", "", extension.InputEventResultTransform{Text: "X[1][2]"})
	})
	// .upstream/v0.87.1/packages/coding-agent/test/extensions-input-event.test.ts:78
	t.Run("short-circuits on handled and skips subsequent handlers", func(t *testing.T) {
		called := false
		r := makeRunner(func(...any) (any, error) { return extension.InputEventResultHandled{}, nil }, func(...any) (any, error) { called = true; return nil, nil })
		emit(t, r, "X", nil, "interactive", "", extension.InputEventResultHandled{})
		if called {
			t.Fatal("subsequent handler ran")
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/extensions-input-event.test.ts:88
	t.Run("passes source correctly for all source types", func(t *testing.T) {
		var seen string
		r := makeRunner(func(args ...any) (any, error) {
			seen = args[0].(extension.InputEvent).Source
			return extension.InputEventResultContinue{}, nil
		})
		for _, source := range []string{"interactive", "rpc", "extension"} {
			emit(t, r, "x", nil, source, "", extension.InputEventResultContinue{})
			if seen != source {
				t.Fatalf("source = %q; want %q", seen, source)
			}
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/extensions-input-event.test.ts:98
	t.Run("passes streamingBehavior correctly", func(t *testing.T) {
		var seen string
		r := makeRunner(func(args ...any) (any, error) {
			seen = args[0].(extension.InputEvent).StreamingBehavior
			return extension.InputEventResultContinue{}, nil
		})
		for _, behavior := range []string{"steer", "followUp", ""} {
			emit(t, r, "x", nil, "interactive", behavior, extension.InputEventResultContinue{})
			if seen != behavior {
				t.Fatalf("streamingBehavior = %q; want %q", seen, behavior)
			}
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/extensions-input-event.test.ts:110
	t.Run("catches handler errors and continues", func(t *testing.T) {
		r := makeRunner(func(...any) (any, error) { return nil, errors.New("boom") })
		var errs []string
		r.AddErrorListener(func(e *extension.ExtensionError) { errs = append(errs, e.Error) })
		emit(t, r, "x", nil, "interactive", "", extension.InputEventResultContinue{})
		if !slices.Contains(errs, "boom") {
			t.Fatalf("errors = %#v; want boom", errs)
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/extensions-input-event.test.ts:119
	t.Run("hasHandlers returns correct value", func(t *testing.T) {
		if makeRunner().HasHandlers("input") {
			t.Fatal("no handlers reported present")
		}
		if !makeRunner(func(...any) (any, error) { return nil, nil }).HasHandlers("input") {
			t.Fatal("input handler missing")
		}
	})
}

// Awaited upstream handlers finish before the next handler observes transformed input.
func TestUpstreamInputEventAwaitsHandler(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	ext := extWithInputHandler("wait", func(e extension.InputEvent, ctx context.Context) extension.InputEventResult {
		close(started)
		select {
		case <-release:
		case <-ctx.Done():
		}
		return extension.InputEventResultTransform{Text: e.Text + " done"}
	})
	r := inproc.NewRunner([]extension.Extension{ext}, ".")
	done := make(chan struct{})
	var result extension.InputEventResult
	var err error
	go func() { defer close(done); result, err = r.EmitInput(t.Context(), "work", nil, "interactive", "") }()
	<-started
	select {
	case <-done:
		t.Error("dispatch returned before handler completed")
	default:
	}
	close(release)
	<-done
	if err != nil || !reflect.DeepEqual(result, extension.InputEventResultTransform{Text: "work done"}) {
		t.Fatalf("result = %#v, %v", result, err)
	}
}
