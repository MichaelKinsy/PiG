package cli

// pi: packages/coding-agent/src/core/project-trust.ts

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

func trustExtension(handlers ...extension.HandlerFn) extension.Extension {
	return extension.Extension{Name: "trust", ResolvedPath: "/trust", Handlers: map[string][]extension.HandlerFn{"project_trust": handlers}}
}

func trustYes(...any) (any, error) {
	return extension.ProjectTrustEventResult{Trusted: extension.ProjectTrustYes}, nil
}

// project-trust.ts:47-95: the extension decision is consulted before the saved decision, so it beats it.
func TestResolveProjectTrustedExtensionDecisionBeatsASavedDecision(t *testing.T) {
	project := trustProjectFixture(t)
	store := codingagent.NewProjectTrustStore(t.TempDir())
	if err := store.Set(project, new(false)); err != nil {
		t.Fatal(err)
	}
	trusted, err := resolveProjectTrusted(t.Context(), projectTrustResolutionOptions{
		CWD: project, Store: store, Default: "never", Runner: inproc.NewRunner([]extension.Extension{trustExtension(trustYes)}, project), UI: extension.NoopUIContext,
	})
	if err != nil || !trusted {
		t.Fatalf("trusted = %v err = %v, want the extension's yes to beat the saved no", trusted, err)
	}
	if saved, _ := store.Get(project); saved == nil || *saved {
		t.Fatalf("saved decision = %v: a decision without remember was written", saved)
	}
}

// project-trust.ts:61-70: a handler that throws is reported through onExtensionError and the later handler still decides.
func TestResolveProjectTrustedReportsAThrowingHandlerAndContinues(t *testing.T) {
	project := trustProjectFixture(t)
	var reports []string
	trusted, err := resolveProjectTrusted(t.Context(), projectTrustResolutionOptions{
		CWD: project, Store: codingagent.NewProjectTrustStore(t.TempDir()), Default: "never", UI: extension.NoopUIContext,
		Runner: inproc.NewRunner([]extension.Extension{trustExtension(
			func(...any) (any, error) { return nil, errors.New("boom") },
			trustYes,
		)}, project),
		OnExtensionError: func(message string) { reports = append(reports, message) },
	})
	if err != nil || !trusted {
		t.Fatalf("trusted = %v err = %v, want the second handler to decide", trusted, err)
	}
	if want := []string{`Extension "/trust" project_trust error: boom`}; !slices.Equal(reports, want) {
		t.Fatalf("reports = %q, want %q", reports, want)
	}
}

// project-trust.ts:47-95: the prompt waits for the awaited extension event; it is shown only after every handler abstained, and the dialog's answer is what resolves.
func TestResolveProjectTrustedPromptsOnlyAfterTheHandlersFinish(t *testing.T) {
	project := trustProjectFixture(t)
	entered, release := make(chan struct{}), make(chan struct{})
	var mu sync.Mutex
	var order []string
	note := func(s string) { mu.Lock(); order = append(order, s); mu.Unlock() }
	ui := &orderedTrustUI{UIContext: extension.NoopUIContext, onSelect: func() { note("select") }}
	handler := func(...any) (any, error) {
		close(entered)
		<-release
		note("handler")
		return extension.ProjectTrustEventResult{Trusted: extension.ProjectTrustUndecided}, nil
	}
	type result struct {
		trusted bool
		err     error
	}
	done := make(chan result, 1)
	go func() {
		trusted, err := resolveProjectTrusted(t.Context(), projectTrustResolutionOptions{
			CWD: project, Store: codingagent.NewProjectTrustStore(t.TempDir()), Default: "ask", UI: ui,
			Runner: inproc.NewRunner([]extension.Extension{trustExtension(handler)}, project),
		})
		done <- result{trusted, err}
	}()
	select {
	case <-entered:
	case r := <-done:
		t.Fatalf("resolution ended (%v, %v) before the project_trust handler ran", r.trusted, r.err)
	}
	close(release)
	r := <-done
	mu.Lock()
	defer mu.Unlock()
	if r.err != nil || !r.trusted || !slices.Equal(order, []string{"handler", "select"}) {
		t.Fatalf("result = %+v, order = %q, want the handler before the dialog and the Trust answer", r, order)
	}
}

type orderedTrustUI struct {
	extension.UIContext
	onSelect func()
}

func (ui *orderedTrustUI) Select(_ context.Context, _ string, options []string, _ extension.ExtensionUIDialogOptions) (string, error) {
	ui.onSelect()
	return "Trust", nil
}

// A dialog that ends because the caller's context was cancelled answers not trusted (Go ends the selection with the context; Pi's select resolves undefined on abort).
func TestResolveProjectTrustedCancelledDialogAnswersNotTrusted(t *testing.T) {
	project := trustProjectFixture(t)
	store := codingagent.NewProjectTrustStore(t.TempDir())
	ctx, cancel := context.WithCancel(t.Context())
	ui := &cancellingTrustUI{UIContext: extension.NoopUIContext, cancel: cancel}
	trusted, err := resolveProjectTrusted(ctx, projectTrustResolutionOptions{CWD: project, Store: store, Default: "ask", UI: ui})
	if err != nil || trusted {
		t.Fatalf("trusted = %v err = %v, want false and no error", trusted, err)
	}
	if saved, _ := store.Get(project); saved != nil {
		t.Fatalf("a cancelled dialog stored %v", *saved)
	}
}

type cancellingTrustUI struct {
	extension.UIContext
	cancel context.CancelFunc
}

func (ui *cancellingTrustUI) Select(ctx context.Context, _ string, _ []string, _ extension.ExtensionUIDialogOptions) (string, error) {
	ui.cancel()
	return "", ctx.Err()
}
