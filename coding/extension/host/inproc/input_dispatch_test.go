package inproc_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// extWithInputHandler wraps a typed input handler.
func extWithInputHandler(path string, fn func(extension.InputEvent, context.Context) extension.InputEventResult) extension.Extension {
	ext := newFakeExtension(path)
	ext.Handlers["input"] = []extension.HandlerFn{
		func(args ...any) (any, error) {
			ev, _ := args[0].(extension.InputEvent)
			ctx, _ := args[1].(context.Context)
			r := fn(ev, ctx)
			if r == nil {
				return nil, nil
			}
			return r, nil
		},
	}
	return ext
}

func TestEmitInput_NoHandlersReturnsContinue(t *testing.T) {
	r := inproc.NewRunner(nil, ".")
	got, err := r.EmitInput(context.Background(), "hello", nil, "interactive", "")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if _, ok := got.(extension.InputEventResultContinue); !ok {
		t.Errorf("got = %T, want InputEventResultContinue", got)
	}
}

func TestEmitInput_StaleRunnerReturnsErrStaleContext(t *testing.T) {
	r := inproc.NewRunner(nil, ".")
	r.Invalidate("")
	_, err := r.EmitInput(context.Background(), "x", nil, "interactive", "")
	if !errors.Is(err, extension.ErrStaleContext) {
		t.Errorf("err = %v, want ErrStaleContext", err)
	}
}

// TestEmitInput_PureContinueChainReturnsContinue: every handler returns
// Continue ⇒ runner returns Continue with original text/images.
func TestEmitInput_PureContinueChainReturnsContinue(t *testing.T) {
	exts := []extension.Extension{
		extWithInputHandler("/ext/a", func(extension.InputEvent, context.Context) extension.InputEventResult {
			return extension.InputEventResultContinue{}
		}),
		extWithInputHandler("/ext/b", func(extension.InputEvent, context.Context) extension.InputEventResult {
			return extension.InputEventResultContinue{}
		}),
	}
	r := inproc.NewRunner(exts, ".")
	got, err := r.EmitInput(context.Background(), "hi", nil, "interactive", "")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if _, ok := got.(extension.InputEventResultContinue); !ok {
		t.Errorf("got = %T, want Continue", got)
	}
}

// TestEmitInput_TransformChainCompounds: handler A transforms "hi" →
// "HI", handler B sees "HI" and transforms to "HI!"; final result
// reflects "HI!". Locks the chain-transform contract.
func TestEmitInput_TransformChainCompounds(t *testing.T) {
	exts := []extension.Extension{
		extWithInputHandler("/ext/a", func(ev extension.InputEvent, _ context.Context) extension.InputEventResult {
			return extension.InputEventResultTransform{Text: ev.Text + "-A"}
		}),
		extWithInputHandler("/ext/b", func(ev extension.InputEvent, _ context.Context) extension.InputEventResult {
			return extension.InputEventResultTransform{Text: ev.Text + "-B"}
		}),
	}
	r := inproc.NewRunner(exts, ".")

	got, err := r.EmitInput(context.Background(), "hi", nil, "interactive", "")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	xform, ok := got.(extension.InputEventResultTransform)
	if !ok {
		t.Fatalf("got = %T, want Transform", got)
	}
	if xform.Text != "hi-A-B" {
		t.Errorf("Text = %q, want hi-A-B (chain must compound)", xform.Text)
	}
}

// TestEmitInput_HandledShortCircuitsRemainingHandlers: first Handled
// halts dispatch; subsequent handlers do not run.
func TestEmitInput_HandledShortCircuitsRemainingHandlers(t *testing.T) {
	var firstFired, thirdFired atomic.Int32
	exts := []extension.Extension{
		extWithInputHandler("/ext/a", func(extension.InputEvent, context.Context) extension.InputEventResult {
			firstFired.Add(1)
			return extension.InputEventResultContinue{}
		}),
		extWithInputHandler("/ext/b", func(extension.InputEvent, context.Context) extension.InputEventResult {
			return extension.InputEventResultHandled{}
		}),
		extWithInputHandler("/ext/c", func(extension.InputEvent, context.Context) extension.InputEventResult {
			thirdFired.Add(1)
			return extension.InputEventResultContinue{}
		}),
	}
	r := inproc.NewRunner(exts, ".")
	got, err := r.EmitInput(context.Background(), "x", nil, "interactive", "")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if _, ok := got.(extension.InputEventResultHandled); !ok {
		t.Errorf("got = %T, want Handled", got)
	}
	if firstFired.Load() != 1 {
		t.Errorf("first fired %d, want 1", firstFired.Load())
	}
	if thirdFired.Load() != 0 {
		t.Errorf("third fired %d, want 0 (Handled short-circuits)", thirdFired.Load())
	}
}

// TestEmitInput_TransformThenHandled: handler A transforms, handler B
// returns Handled. The Handled result is returned (NOT a Transform with
// A's mutations). This locks upstream behavior: Handled wins over
// previously-accumulated Transform mutations.
//
// upstream: runner.ts:996 (`if (result?.action === "handled") return result`)
func TestEmitInput_TransformThenHandled(t *testing.T) {
	exts := []extension.Extension{
		extWithInputHandler("/ext/a", func(ev extension.InputEvent, _ context.Context) extension.InputEventResult {
			return extension.InputEventResultTransform{Text: "transformed"}
		}),
		extWithInputHandler("/ext/b", func(extension.InputEvent, context.Context) extension.InputEventResult {
			return extension.InputEventResultHandled{}
		}),
	}
	r := inproc.NewRunner(exts, ".")
	got, err := r.EmitInput(context.Background(), "original", nil, "interactive", "")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if _, ok := got.(extension.InputEventResultHandled); !ok {
		t.Errorf("got = %T, want Handled (overrides accumulated Transform)", got)
	}
}

// TestEmitInput_TransformWithNilImagesKeepsCurrentImages: per upstream
// `result.images ?? currentImages`, a Transform with nil Images means
// "keep current".
func TestEmitInput_TransformWithNilImagesKeepsCurrentImages(t *testing.T) {
	exts := []extension.Extension{
		extWithInputHandler("/ext/a", func(extension.InputEvent, context.Context) extension.InputEventResult {
			return extension.InputEventResultTransform{Text: "modified", Images: nil}
		}),
	}
	r := inproc.NewRunner(exts, ".")

	originalImgs := []extension.ImageContent{"img1", "img2"}
	got, err := r.EmitInput(context.Background(), "x", originalImgs, "interactive", "")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	xform, ok := got.(extension.InputEventResultTransform)
	if !ok {
		t.Fatalf("got = %T, want Transform", got)
	}
	if xform.Text != "modified" {
		t.Errorf("Text = %q, want modified", xform.Text)
	}
	// Note: upstream's reference-equality check `currentImages !== images`
	// treats unchanged-but-passed-through images as "not changed". pig's
	// imagesChanged flag is set ONLY when a handler explicitly assigns
	// non-nil Images. Here Images was nil, so flag stays false ⇒ Transform
	// is returned because Text changed, but Images carries the original.
	if len(xform.Images) != 2 || xform.Images[0] != "img1" {
		t.Errorf("Images = %v, want original [img1, img2]", xform.Images)
	}
}

// TestEmitInput_TransformImagesUpdates: handler explicitly sets new
// Images; runner threads them through.
func TestEmitInput_TransformImagesUpdates(t *testing.T) {
	exts := []extension.Extension{
		extWithInputHandler("/ext/a", func(extension.InputEvent, context.Context) extension.InputEventResult {
			return extension.InputEventResultTransform{Text: "x", Images: []extension.ImageContent{"new-img"}}
		}),
	}
	r := inproc.NewRunner(exts, ".")
	got, err := r.EmitInput(context.Background(), "x", nil, "interactive", "")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	xform, ok := got.(extension.InputEventResultTransform)
	if !ok {
		t.Fatalf("got = %T, want Transform", got)
	}
	if len(xform.Images) != 1 || xform.Images[0] != "new-img" {
		t.Errorf("Images = %v, want [new-img]", xform.Images)
	}
}

// TestEmitInput_HandlerErrorRoutesViaEmitErrorAndContinues: upstream
// EmitInput has explicit try/catch (runner.ts:1218-1225); pig mirrors.
func TestEmitInput_HandlerErrorRoutesViaEmitErrorAndContinues(t *testing.T) {
	var subsequentRan atomic.Int32
	exts := []extension.Extension{
		{
			Path: "/ext/bad",
			Handlers: map[string][]extension.HandlerFn{
				"input": {func(args ...any) (any, error) { return nil, errors.New("boom") }},
			},
			Tools:     map[string]extension.RegisteredTool{},
			Commands:  map[string]extension.RegisteredCommand{},
			Flags:     map[string]extension.ExtensionFlag{},
			Shortcuts: map[extension.KeyID]extension.ExtensionShortcut{},
		},
		extWithInputHandler("/ext/good", func(extension.InputEvent, context.Context) extension.InputEventResult {
			subsequentRan.Add(1)
			return extension.InputEventResultContinue{}
		}),
	}
	r := inproc.NewRunner(exts, ".")
	var captured []*extension.ExtensionError
	var mu sync.Mutex
	r.AddErrorListener(func(e *extension.ExtensionError) {
		mu.Lock()
		captured = append(captured, e)
		mu.Unlock()
	})

	if _, err := r.EmitInput(context.Background(), "x", nil, "interactive", ""); err != nil {
		t.Fatalf("err = %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(captured) != 1 {
		t.Fatalf("captured %d errors, want 1", len(captured))
	}
	if captured[0].Event != "input" {
		t.Errorf("Event = %q, want input", captured[0].Event)
	}
	if subsequentRan.Load() != 1 {
		t.Errorf("subsequent fired %d, want 1 (chain must continue)", subsequentRan.Load())
	}
}

// TestEmitInput_HandlerSeesPreviousHandlersTransformedText: transform-
// chain guarantee: handler B's event.Text is what handler A returned.
func TestEmitInput_HandlerSeesPreviousHandlersTransformedText(t *testing.T) {
	var bSawText string
	exts := []extension.Extension{
		extWithInputHandler("/ext/a", func(extension.InputEvent, context.Context) extension.InputEventResult {
			return extension.InputEventResultTransform{Text: "transformed-by-a"}
		}),
		extWithInputHandler("/ext/b", func(ev extension.InputEvent, _ context.Context) extension.InputEventResult {
			bSawText = ev.Text
			return extension.InputEventResultContinue{}
		}),
	}
	r := inproc.NewRunner(exts, ".")
	if _, err := r.EmitInput(context.Background(), "original", nil, "interactive", ""); err != nil {
		t.Fatalf("err = %v", err)
	}
	if bSawText != "transformed-by-a" {
		t.Errorf("handler B saw Text = %q, want transformed-by-a", bSawText)
	}
}

// TestEmitInput_SourcePropagates: the `source` parameter reaches the
// handler's event.Source.
func TestEmitInput_SourcePropagates(t *testing.T) {
	var seen string
	var streamingBehavior string
	exts := []extension.Extension{
		extWithInputHandler("/ext/a", func(ev extension.InputEvent, _ context.Context) extension.InputEventResult {
			seen = ev.Source
			streamingBehavior = ev.StreamingBehavior
			return extension.InputEventResultContinue{}
		}),
	}
	r := inproc.NewRunner(exts, ".")
	_, _ = r.EmitInput(context.Background(), "x", nil, "rpc", "followUp")
	if seen != "rpc" {
		t.Errorf("Source = %q, want rpc", seen)
	}
	if streamingBehavior != "followUp" {
		t.Errorf("StreamingBehavior = %q, want followUp", streamingBehavior)
	}
}
