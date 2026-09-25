package inproc_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/coding/extension/host/invocation"
)

// promptUI is a real (non-noop) UI surface whose dialogs return fixed
// answers. Select can run a nested prompt or block until released.
type promptUI struct {
	extension.UIContext
	nested  func(ctx context.Context)
	release chan struct{}
}

func newPromptUI() *promptUI { return &promptUI{UIContext: extension.NoopUIContext} }

func (u *promptUI) Select(ctx context.Context, _ string, _ []string, _ extension.ExtensionUIDialogOptions) (string, error) {
	if u.nested != nil {
		u.nested(ctx)
	}
	if u.release != nil {
		select {
		case <-u.release:
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	return "picked", nil
}
func (u *promptUI) Confirm(context.Context, string, string, extension.ExtensionUIDialogOptions) (bool, error) {
	return true, nil
}
func (u *promptUI) Input(context.Context, string, string, extension.ExtensionUIDialogOptions) (string, error) {
	return "typed", nil
}
func (u *promptUI) Editor(context.Context, string, string) (string, error) { return "edited", nil }
func (u *promptUI) Custom(context.Context, any, any) (any, error) {
	return nil, errors.New("custom failed")
}

// promptRecorder collects ui_prompt_start/ui_prompt_end deliveries.
type promptRecorder struct {
	mu     sync.Mutex
	events []string
	seen   chan struct{}
}

func newPromptRecorder() *promptRecorder { return &promptRecorder{seen: make(chan struct{}, 64)} }

func (p *promptRecorder) record(line string) {
	p.mu.Lock()
	p.events = append(p.events, line)
	p.mu.Unlock()
	p.seen <- struct{}{}
}

func (p *promptRecorder) wait(t *testing.T, n int) []string {
	t.Helper()
	for range n {
		select {
		case <-p.seen:
		case <-time.After(5 * time.Second):
			t.Fatalf("waited for %d prompt events, have %v", n, p.snapshot())
		}
	}
	return p.snapshot()
}

func (p *promptRecorder) snapshot() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.events...)
}

func promptExtension(path string, rec *promptRecorder) extension.Extension {
	ext := newFakeExtension(path)
	ext.Handlers["ui_prompt_start"] = []extension.HandlerFn{func(args ...any) (any, error) {
		event := args[0].(extension.UIPromptStartEvent)
		rec.record(fmt.Sprintf("%s:%s:%s:%s:%s", path, event.Type, event.Reason, event.Kind, event.Title))
		return nil, nil
	}}
	ext.Handlers["ui_prompt_end"] = []extension.HandlerFn{func(args ...any) (any, error) {
		event := args[0].(extension.UIPromptEndEvent)
		rec.record(fmt.Sprintf("%s:%s:%s:%s:%s", path, event.Type, event.Reason, event.Kind, event.Title))
		return nil, nil
	}}
	return ext
}

func runnerUI(t *testing.T, r *inproc.Runner) extension.UIContext {
	t.Helper()
	ui, err := extension.FromContext(r.DispatchContext(context.Background())).UI()
	if err != nil {
		t.Fatal(err)
	}
	return ui
}

// Every blocking dialog reports one start/end pair with its kind; custom
// carries no title, matching upstream wrapUIPromptContext.
func TestUIPrompt_EachDialogReportsStartAndEnd(t *testing.T) {
	rec := newPromptRecorder()
	r := inproc.NewRunner([]extension.Extension{promptExtension("a", rec)}, ".")
	r.SetUIContext(newPromptUI())
	ui := runnerUI(t, r)
	ctx := context.Background()

	if got, _ := ui.Select(ctx, "Pick", []string{"x"}, nil); got != "picked" {
		t.Fatalf("select = %q", got)
	}
	_, _ = ui.Confirm(ctx, "Sure", "message", nil)
	_, _ = ui.Input(ctx, "Name", "placeholder", nil)
	_, _ = ui.Editor(ctx, "Edit", "prefill")
	if _, err := ui.Custom(ctx, nil, nil); err == nil {
		t.Fatal("custom error was swallowed by the prompt scope")
	}
	got := rec.wait(t, 10)
	want := []string{
		"a:ui_prompt_start:ui_prompt:select:Pick", "a:ui_prompt_end:ui_prompt:select:Pick",
		"a:ui_prompt_start:ui_prompt:confirm:Sure", "a:ui_prompt_end:ui_prompt:confirm:Sure",
		"a:ui_prompt_start:ui_prompt:input:Name", "a:ui_prompt_end:ui_prompt:input:Name",
		"a:ui_prompt_start:ui_prompt:editor:Edit", "a:ui_prompt_end:ui_prompt:editor:Edit",
		"a:ui_prompt_start:ui_prompt:custom:", "a:ui_prompt_end:ui_prompt:custom:",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("events =\n%v\nwant\n%v", got, want)
	}
}

// A prompt opened while another is pending raises the depth only. The end
// event repeats the outer prompt, as upstream's activeUIPrompt does.
func TestUIPrompt_NestedPromptReportsOuterOnly(t *testing.T) {
	rec := newPromptRecorder()
	r := inproc.NewRunner([]extension.Extension{promptExtension("a", rec)}, ".")
	ui := newPromptUI()
	r.SetUIContext(ui)
	wrapped := runnerUI(t, r)
	ui.nested = func(ctx context.Context) { _, _ = wrapped.Input(ctx, "Inner", "", nil) }

	_, _ = wrapped.Select(context.Background(), "Outer", nil, nil)
	got := rec.wait(t, 2)
	want := []string{"a:ui_prompt_start:ui_prompt:select:Outer", "a:ui_prompt_end:ui_prompt:select:Outer"}
	if !slices.Equal(got, want) {
		t.Fatalf("events = %v, want %v", got, want)
	}
	select {
	case <-rec.seen:
		t.Fatalf("nested prompt emitted extra events: %v", rec.snapshot())
	case <-time.After(50 * time.Millisecond):
	}
}

// Concurrent prompts share one depth: the second begins while the first is
// open, so only one pair is reported, ending with the first prompt's kind.
func TestUIPrompt_OverlappingPromptsShareOneScope(t *testing.T) {
	rec := newPromptRecorder()
	r := inproc.NewRunner([]extension.Extension{promptExtension("a", rec)}, ".")
	endFirst := r.BeginUIPrompt(extension.UIPromptKindSelect, "First")
	endSecond := r.BeginUIPrompt(extension.UIPromptKindConfirm, "Second")
	endFirst()
	endFirst() // ignored: end runs once
	endSecond()
	got := rec.wait(t, 2)
	want := []string{"a:ui_prompt_start:ui_prompt:select:First", "a:ui_prompt_end:ui_prompt:select:First"}
	if !slices.Equal(got, want) {
		t.Fatalf("events = %v, want %v", got, want)
	}
}

// Upstream queues prompt emissions as FIFO microtasks. For Go in-process
// handlers, completion is the only observable suspension boundary; the next
// event is admitted after the prior handler body returns.
func TestUIPrompt_SynchronousHandlerInvocationIsFIFO(t *testing.T) {
	events := make(chan string, 3)
	ext := newFakeExtension("ordered")
	ext.Handlers["ui_prompt_start"] = []extension.HandlerFn{
		func(...any) (any, error) {
			events <- "start-1"
			return nil, nil
		},
		func(...any) (any, error) {
			events <- "start-2"
			return nil, nil
		},
	}
	ext.Handlers["ui_prompt_end"] = []extension.HandlerFn{func(...any) (any, error) {
		events <- "end"
		return nil, nil
	}}
	r := inproc.NewRunner([]extension.Extension{ext}, ".")
	defer r.Invalidate("test done")

	r.BeginUIPrompt(extension.UIPromptKindInput, "ordered")()
	got := []string{<-events, <-events, <-events}
	want := []string{"start-1", "start-2", "end"}
	if !slices.Equal(got, want) {
		t.Fatalf("handler body order = %v, want %v", got, want)
	}
}

// A subprocess handler acknowledges when its synchronous body returns or
// reaches an awaited operation, so suspended start work does not delay end.
func TestUIPrompt_DialogDoesNotAwaitHandlers(t *testing.T) {
	rec := newPromptRecorder()
	release := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	started := make(chan struct{})
	ext := promptExtension("a", rec)
	record := ext.Handlers["ui_prompt_start"][0]
	ext.Handlers["ui_prompt_start"] = []extension.HandlerFn{func(args ...any) (any, error) {
		dispatchCtx, _ := args[1].(context.Context)
		invocation.Acknowledge(dispatchCtx)
		close(started)
		<-release
		return record(args...)
	}}
	r := inproc.NewRunner([]extension.Extension{ext}, ".")
	r.SetUIContext(newPromptUI())
	ui := runnerUI(t, r)

	answered := make(chan string, 1)
	go func() {
		got, _ := ui.Select(context.Background(), "Pick", nil, nil)
		answered <- got
	}()
	<-started
	select {
	case got := <-answered:
		if got != "picked" {
			t.Fatalf("select = %q", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("dialog waited for the ui_prompt_start handler")
	}
	events := rec.wait(t, 1)
	if len(events) != 1 || events[0] != "a:ui_prompt_end:ui_prompt:select:Pick" {
		t.Fatalf("end did not complete while start suspended: %v", events)
	}
	unblock()
	got := rec.wait(t, 1)
	want := []string{"a:ui_prompt_end:ui_prompt:select:Pick", "a:ui_prompt_start:ui_prompt:select:Pick"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("events = %v, want %v", got, want)
	}
}

// A cancelled prompt still ends. A failing handler is reported through the
// error listeners and does not stop later extensions.
func TestUIPrompt_CancellationEndsAndHandlerErrorsAreReported(t *testing.T) {
	rec := newPromptRecorder()
	failing := newFakeExtension("failing")
	failing.Handlers["ui_prompt_start"] = []extension.HandlerFn{func(...any) (any, error) {
		return nil, errors.New("start-boom")
	}}
	r := inproc.NewRunner([]extension.Extension{failing, promptExtension("b", rec)}, ".")
	errs := make(chan *extension.ExtensionError, 4)
	r.AddErrorListener(func(err *extension.ExtensionError) { errs <- err })
	ui := newPromptUI()
	ui.release = make(chan struct{})
	r.SetUIContext(ui)
	wrapped := runnerUI(t, r)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := wrapped.Select(ctx, "Pick", nil, nil)
		done <- err
	}()
	rec.wait(t, 1)
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("select err = %v, want context.Canceled", err)
	}
	got := rec.wait(t, 1)
	want := []string{"b:ui_prompt_start:ui_prompt:select:Pick", "b:ui_prompt_end:ui_prompt:select:Pick"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("events = %v, want %v", got, want)
	}
	select {
	case err := <-errs:
		if err.ExtensionPath != "failing" || err.Event != "ui_prompt_start" || err.Error != "start-boom" {
			t.Fatalf("error = %+v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("handler error was not reported")
	}
}

// Print mode binds no UI surface, so its no-op dialogs report nothing, and
// a runner invalidated by reload dispatches nothing further.
func TestUIPrompt_NoUIAndStaleRunnerReportNothing(t *testing.T) {
	rec := newPromptRecorder()
	r := inproc.NewRunner([]extension.Extension{promptExtension("a", rec)}, ".")
	r.SetUIContext(nil)
	if r.HasUI() {
		t.Fatal("nil UI context reported HasUI")
	}
	_, _ = runnerUI(t, r).Select(context.Background(), "Pick", nil, nil)

	stale := inproc.NewRunner([]extension.Extension{promptExtension("stale", rec)}, ".")
	stale.SetUIContext(newPromptUI())
	stale.Invalidate("")
	stale.BeginUIPrompt(extension.UIPromptKindInput, "late")()
	select {
	case <-rec.seen:
		t.Fatalf("events without UI or after invalidation: %v", rec.snapshot())
	case <-time.After(100 * time.Millisecond):
	}
}

// The wire payload omits an empty title, as upstream spreads title only when
// it is truthy.
func TestUIPromptEvents_JSONShape(t *testing.T) {
	for _, tc := range []struct {
		event any
		want  string
	}{
		{extension.UIPromptStartEvent{Type: "ui_prompt_start", Reason: "ui_prompt", Kind: extension.UIPromptKindSelect, Title: "Pick"},
			`{"type":"ui_prompt_start","reason":"ui_prompt","kind":"select","title":"Pick"}`},
		{extension.UIPromptEndEvent{Type: "ui_prompt_end", Reason: "ui_prompt", Kind: extension.UIPromptKindCustom},
			`{"type":"ui_prompt_end","reason":"ui_prompt","kind":"custom"}`},
	} {
		got, err := json.Marshal(tc.event)
		if err != nil || string(got) != tc.want {
			t.Fatalf("marshal = %s, %v; want %s", got, err, tc.want)
		}
	}
}

func TestUIPrompt_InvalidationCancelsActiveHandler(t *testing.T) {
	started, stopped := make(chan struct{}), make(chan struct{})
	ext := newFakeExtension("owned-cancellation")
	ext.Handlers["ui_prompt_start"] = []extension.HandlerFn{func(args ...any) (any, error) {
		close(started)
		<-args[1].(context.Context).Done()
		close(stopped)
		return nil, nil
	}}
	r := inproc.NewRunner([]extension.Extension{ext}, ".")
	end := r.BeginUIPrompt(extension.UIPromptKindInput, "Owned")
	defer end()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("handler did not start")
	}
	r.Invalidate("replacement")
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("invalidation did not cancel handler")
	}
}
