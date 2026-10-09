package tui

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
)

// fakeModelRuntime is Pi's ModelRuntime as the selector reads it. refresh decides what Refresh returns; it runs off the owner loop.
type fakeModelRuntime struct {
	models  []*ai.Model
	err     string
	refresh func(ctx context.Context) ai.ModelsRefreshResult
}

func (r *fakeModelRuntime) GetAvailableSnapshot() []*ai.Model { return r.models }
func (r *fakeModelRuntime) GetModel(provider, id string) *ai.Model {
	for _, model := range r.models {
		if model.ProviderID() == provider && model.ModelID() == id {
			return model
		}
	}
	return nil
}
func (r *fakeModelRuntime) GetError() string { return r.err }
func (r *fakeModelRuntime) Refresh(ctx context.Context, _ ...ai.ModelsRefreshOptions) ai.ModelsRefreshResult {
	if r.refresh == nil {
		return ai.ModelsRefreshResult{}
	}
	return r.refresh(ctx)
}

// refreshFailing is a refresh whose catalogs failed, in the order given (the order Pi's error Map iterates).
func refreshFailing(providers ...string) ai.ModelsRefreshResult {
	result := ai.ModelsRefreshResult{Errors: map[string]error{}, ErrorOrder: providers}
	for _, id := range providers {
		result.Errors[id] = errors.New(id + " catalog failed")
	}
	return result
}

func selectorTestModel(id, name string) *ai.Model {
	return &ai.Model{ID: id, DisplayName: name, ProviderMeta: ai.ProviderMetadata{ProviderID: "faux"}}
}

// ownerQueue is the application's owner loop: PostToOwner work queues here and the test runs them.
type ownerQueue struct{ tasks chan func() }

func newModelSelectorTUI(t *testing.T) (*TuiMainScreen, *ownerQueue) {
	t.Helper()
	ui := newManualRenderTUI(&bytes.Buffer{}, 120, 30)
	queue := &ownerQueue{tasks: make(chan func(), 16)}
	ui.SetOwnerDispatcher(func(_ context.Context, fn func()) error { queue.tasks <- fn; return nil })
	return ui, queue
}

func (q *ownerQueue) run(t *testing.T) {
	t.Helper()
	select {
	case fn := <-q.tasks:
		fn()
	case <-time.After(5 * time.Second):
		t.Fatal("the selector's catalog refresh never reached the owner loop")
	}
}

func selectorRows(selector *ModelSelectorComponent) []string {
	return plain(selector.Render(120))
}

func rowStartingWith(rows []string, prefix string) string {
	for _, row := range rows {
		if strings.Contains(row, prefix) {
			return row
		}
	}
	return ""
}

// model-selector.test.ts:29 "keeps the current model marked while browsing": the current model leads the list with the cursor and the check
// mark, and moving the cursor down moves only the cursor.
func TestModelSelectorConstructorKeepsTheCurrentModelMarkedWhileBrowsing(t *testing.T) {
	ui, queue := newModelSelectorTUI(t)
	current, browsed := selectorTestModel("current-model", "Current Model"), selectorTestModel("browsed-model", "Browsed Model")
	selector := NewModelSelectorComponent(ui, current, &fakeModelRuntime{models: []*ai.Model{browsed, current}}, nil, func(*ai.Model) {}, func() {}, "", nil, nil)
	t.Cleanup(selector.Dispose)
	_ = queue
	provider := current.ProviderID()
	row := func(id string) string {
		return strings.TrimRight(rowStartingWith(selectorRows(selector), id+" ["), " ")
	}
	if got, want := row("current-model"), "→ ✓ current-model ["+provider+"]"; got != want {
		t.Fatalf("current row = %q, want %q", got, want)
	}
	selector.HandleInput("\x1b[B")
	if got, want := row("current-model"), "  ✓ current-model ["+provider+"]"; got != want {
		t.Fatalf("current row after moving down = %q, want %q", got, want)
	}
	if got, want := row("browsed-model"), "→   browsed-model ["+provider+"]"; got != want {
		t.Fatalf("browsed row = %q, want %q", got, want)
	}
}

// model-selector.test.ts:59 "uses the configured save binding": the hint names the configured key, the default key does nothing, and the
// configured key calls onSelectAsDefault with the highlighted model. Without onSelectAsDefault there is no action and no hint (model-selector.ts:139).
func TestModelSelectorConstructorUsesTheConfiguredSaveBinding(t *testing.T) {
	previous := GetTUIKeybindings()
	t.Cleanup(func() { SetTUIKeybindings(previous) })
	SetTUIKeybindings(NewTUIKeybindingsManager(map[string][]string{"app.models.save": {"ctrl+r"}}))
	ui, _ := newModelSelectorTUI(t)
	current := selectorTestModel("current-model", "Current Model")
	var saved []*ai.Model
	selector := NewModelSelectorComponent(ui, current, &fakeModelRuntime{models: []*ai.Model{current}}, nil, func(*ai.Model) {}, func() {}, "", func(m *ai.Model) { saved = append(saved, m) }, nil)
	t.Cleanup(selector.Dispose)
	if rendered := strings.Join(selectorRows(selector), "\n"); !strings.Contains(rendered, "Ctrl+R to set as default") {
		t.Fatalf("hint ignores the configured save binding: %q", rendered)
	}
	selector.HandleInput("\x13")
	if len(saved) != 0 {
		t.Fatal("the default save key ran after the rebinding")
	}
	selector.HandleInput("\x12")
	if len(saved) != 1 || saved[0] != current {
		t.Fatalf("saved %v, want the current model", saved)
	}

	plainSelector := NewModelSelectorComponent(ui, current, &fakeModelRuntime{models: []*ai.Model{current}}, nil, func(*ai.Model) {}, func() {}, "", nil, nil)
	t.Cleanup(plainSelector.Dispose)
	if rendered := strings.Join(selectorRows(plainSelector), "\n"); strings.Contains(rendered, "to set as default") {
		t.Fatalf("a selector without onSelectAsDefault shows the save hint: %q", rendered)
	}
	plainSelector.HandleInput("\x12")
	if plainSelector.Done() {
		t.Fatal("the save key selected without onSelectAsDefault")
	}
}

// model-selector.test.ts:82 "lists every catalog that failed to refresh", and the other refreshModels outcomes (model-selector.ts:184-221): the
// outcome reaches the picker on the owner loop; a timeout, one failure, several failures, the runtime's error, and success each show their message.
func TestModelSelectorConstructorShowsTheCatalogRefreshOutcome(t *testing.T) {
	current := selectorTestModel("current-model", "Current Model")
	for _, c := range []struct {
		name    string
		timeout bool
		aborted bool
		failed  []string
		runtime string
		want    string
	}{
		{"several failed", false, false, []string{"openai", "anthropic"}, "", "Could not refresh 2 model catalogs (openai, anthropic); showing cached models."},
		{"one failed", false, false, []string{"openai"}, "", "Could not refresh openai; showing cached models."},
		{"timed out", true, true, nil, "", "Model refresh timed out; showing cached models."},
		// result.aborted without the timer is not a timeout (model-selector.ts:195 `result.aborted && timedOut`): the outcome falls through to the success path.
		{"aborted without the timeout", false, true, nil, "", "Model catalogs refreshed."},
		{"runtime error", false, false, nil, "models.json is invalid", "models.json is invalid"},
		{"refreshed", false, false, nil, "", "Model catalogs refreshed."},
	} {
		t.Run(c.name, func(t *testing.T) {
			if c.timeout {
				previous := modelSelectorRefreshTimeout
				modelSelectorRefreshTimeout = 20 * time.Millisecond
				t.Cleanup(func() { modelSelectorRefreshTimeout = previous })
			}
			ui, queue := newModelSelectorTUI(t)
			runtime := &fakeModelRuntime{models: []*ai.Model{current}, err: c.runtime, refresh: func(ctx context.Context) ai.ModelsRefreshResult {
				if c.timeout {
					<-ctx.Done()
					return ai.ModelsRefreshResult{Aborted: true}
				}
				result := refreshFailing(c.failed...)
				result.Aborted = c.aborted
				return result
			}}
			selector := NewModelSelectorComponent(ui, current, runtime, nil, func(*ai.Model) {}, func() {}, "", nil, nil)
			t.Cleanup(selector.Dispose)
			if rendered := strings.Join(selectorRows(selector), "\n"); !strings.Contains(rendered, "Refreshing model catalogs…") {
				t.Fatalf("before the refresh: %q", rendered)
			}
			queue.run(t)
			if rendered := strings.Join(selectorRows(selector), "\n"); !strings.Contains(rendered, c.want) || strings.Contains(rendered, "Refreshing model catalogs…") {
				t.Fatalf("after the refresh: %q, want %q", rendered, c.want)
			}
		})
	}
}

// model-selector.ts:151-152, 163-168: the refresh reloads the runtime's snapshot, keeping the query; dispose aborts the refresh and a result that
// arrives after dispose changes nothing (model-selector.ts:168 `if (this.closed) return`).
func TestModelSelectorConstructorReloadsTheSnapshotAfterTheRefreshAndIgnoresALateResultAfterDispose(t *testing.T) {
	ui, queue := newModelSelectorTUI(t)
	current, added := selectorTestModel("current-model", "Current Model"), selectorTestModel("added-model", "Added Model")
	runtime := &fakeModelRuntime{models: []*ai.Model{current}}
	runtime.refresh = func(context.Context) ai.ModelsRefreshResult {
		runtime.models = []*ai.Model{current, added}
		return ai.ModelsRefreshResult{}
	}
	selector := NewModelSelectorComponent(ui, current, runtime, nil, func(*ai.Model) {}, func() {}, "model", nil, nil)
	if got := selector.GetSearchInput().Text(); got != "model" {
		t.Fatalf("initial search = %q", got)
	}
	queue.run(t)
	rows := strings.Join(selectorRows(selector), "\n")
	if !strings.Contains(rows, "added-model") || !strings.Contains(rows, "current-model") {
		t.Fatalf("snapshot not reloaded: %q", rows)
	}
	selector.Dispose()

	started := make(chan struct{})
	aborted := make(chan struct{})
	slow := &fakeModelRuntime{models: []*ai.Model{current}, refresh: func(ctx context.Context) ai.ModelsRefreshResult {
		close(started)
		<-ctx.Done()
		close(aborted)
		result := refreshFailing("openai")
		result.Aborted = true
		return result
	}}
	late := NewModelSelectorComponent(ui, current, slow, nil, func(*ai.Model) {}, func() {}, "", nil, nil)
	<-started
	late.Dispose()
	<-aborted
	queue.run(t)
	if rendered := strings.Join(selectorRows(late), "\n"); strings.Contains(rendered, "Could not refresh") || strings.Contains(rendered, "timed out") {
		t.Fatalf("a result after dispose changed the picker: %q", rendered)
	}
}

// model-selector.ts:56-60, 187-194: onSelect, onSelectAsDefault and onCancel receive the model or run on the owner loop's input; a scoped model is
// refreshed from the runtime (model-selector.ts:103-107) and the scoped scope is the starting one when there are scoped models.
func TestModelSelectorConstructorCallbacksAndScopedModels(t *testing.T) {
	ui, _ := newModelSelectorTUI(t)
	a, b := selectorTestModel("a-model", "A"), selectorTestModel("b-model", "B")
	stale := *b
	stale.DisplayName = "stale"
	var chosen []string
	cancelled := 0
	runtime := &fakeModelRuntime{models: []*ai.Model{a, b}}
	selector := NewModelSelectorComponent(ui, a, runtime, []ScopedModelItem{{Model: &stale}}, func(m *ai.Model) { chosen = append(chosen, m.ID) }, func() { cancelled++ }, "", func(m *ai.Model) { chosen = append(chosen, "default:"+m.ID) }, nil)
	t.Cleanup(selector.Dispose)
	if selector.Scope() != ModelScopeScoped || selector.VisibleCount() != 1 {
		t.Fatalf("scope %v visible %d, want the scoped view with the one scoped model", selector.Scope(), selector.VisibleCount())
	}
	if rows := strings.Join(selectorRows(selector), "\n"); !strings.Contains(rows, "Model Name: B") || strings.Contains(rows, "stale") {
		t.Fatalf("the scoped model's metadata was not refreshed from the runtime: %q", rows)
	}
	selector.HandleInput("\r")
	if !slices.Equal(chosen, []string{"b-model"}) {
		t.Fatalf("onSelect got %v", chosen)
	}
	second := NewModelSelectorComponent(ui, a, runtime, nil, func(*ai.Model) {}, func() { cancelled++ }, "", nil, nil)
	t.Cleanup(second.Dispose)
	second.HandleInput("\x1b")
	if cancelled != 1 {
		t.Fatalf("onCancel ran %d times", cancelled)
	}
}

// model-selector.ts:185-196: a refresh that outlives its timeout is aborted and its outcome ("timed out") is still shown. The owner loop's post
// waits on the selector's lifetime, not on the expired refresh context, so a dispatcher that refuses an ended context (as the interactive mode's
// postToMain does) still receives it.
func TestModelSelectorConstructorDeliversATimedOutRefreshOutcomeToAnOwnerLoopThatRefusesAnEndedContext(t *testing.T) {
	previous := modelSelectorRefreshTimeout
	modelSelectorRefreshTimeout = 20 * time.Millisecond
	t.Cleanup(func() { modelSelectorRefreshTimeout = previous })
	ui := newManualRenderTUI(&bytes.Buffer{}, 120, 30)
	queue := &ownerQueue{tasks: make(chan func(), 4)}
	ui.SetOwnerDispatcher(func(ctx context.Context, fn func()) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		queue.tasks <- fn
		return nil
	})
	current := selectorTestModel("current-model", "Current Model")
	runtime := &fakeModelRuntime{models: []*ai.Model{current}, refresh: func(ctx context.Context) ai.ModelsRefreshResult {
		<-ctx.Done()
		return ai.ModelsRefreshResult{Aborted: true}
	}}
	selector := NewModelSelectorComponent(ui, current, runtime, nil, func(*ai.Model) {}, func() {}, "", nil, nil)
	t.Cleanup(selector.Dispose)
	queue.run(t)
	if rendered := strings.Join(selectorRows(selector), "\n"); !strings.Contains(rendered, "Model refresh timed out; showing cached models.") {
		t.Fatalf("after the timeout: %q", rendered)
	}
}

// model-selector.ts:191-205: refreshModelCatalogs returns { aborted: true } for a timed-out refresh instead of rejecting, so the `result.aborted && timedOut` branch
// sets the timeout message and then still runs loadModelsFromSnapshot; the catch path that keeps the list is only for a rejection. A model the aborted refresh
// added before it stopped is therefore listed.
func TestModelSelectorConstructorReloadsTheSnapshotAfterATimedOutRefreshResult(t *testing.T) {
	previous := modelSelectorRefreshTimeout
	modelSelectorRefreshTimeout = 20 * time.Millisecond
	t.Cleanup(func() { modelSelectorRefreshTimeout = previous })
	ui, queue := newModelSelectorTUI(t)
	current, added := selectorTestModel("current-model", "Current Model"), selectorTestModel("added-model", "Added Model")
	runtime := &fakeModelRuntime{models: []*ai.Model{current}}
	runtime.refresh = func(ctx context.Context) ai.ModelsRefreshResult {
		<-ctx.Done()
		runtime.models = []*ai.Model{current, added}
		return ai.ModelsRefreshResult{Aborted: true}
	}
	selector := NewModelSelectorComponent(ui, current, runtime, nil, func(*ai.Model) {}, func() {}, "", nil, nil)
	t.Cleanup(selector.Dispose)
	queue.run(t)
	if rendered := strings.Join(selectorRows(selector), "\n"); !strings.Contains(rendered, "Model refresh timed out; showing cached models.") || !strings.Contains(rendered, "added-model") {
		t.Fatalf("after the timeout: %q, want the timeout message and the reloaded snapshot", rendered)
	}
}
