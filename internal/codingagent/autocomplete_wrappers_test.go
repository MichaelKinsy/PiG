package codingagent

import (
	"bytes"
	"context"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/tui"
)

type autocompleteStackError struct{}

func (autocompleteStackError) Error() string { return "callback failed" }
func (autocompleteStackError) ErrorStack() string {
	return "Error: callback failed\n    at extension.ts:7:2"
}

func TestAutocompleteFailureReachesUncaughtCrash(t *testing.T) {
	m := &InteractiveMode{opts: InteractiveModeOptions{AgentDir: t.TempDir()}}
	func() {
		defer func() {
			value := recover()
			if value == nil {
				t.Fatal("provider rejection did not reach the crash owner")
			}
			var output bytes.Buffer
			m.uncaughtCrash(value, []byte("host worker stack"), &output)
			if !strings.Contains(output.String(), "Error: callback failed\n    at extension.ts:7:2") || strings.Contains(output.String(), "host worker stack") {
				t.Fatalf("crash output=%q", output.String())
			}
		}()
		m.reportAutocompleteError(autocompleteStackError{})
	}()
	m.reportAutocompleteError(context.Canceled)
	records := ReadCrashLog(CrashLogPath(m.opts.AgentDir))
	if len(records) != 1 || records[0].Kind != "uncaught_exception" {
		t.Fatalf("crash records=%+v", records)
	}
}

type autocompleteBridgeProbe struct {
	*captureUIBridge
	mu        sync.Mutex
	providers []*extension.AutocompleteProvider
}

func (p *autocompleteBridgeProbe) SyncAutocomplete(_ context.Context, provider *extension.AutocompleteProvider) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.providers = append(p.providers, provider)
	return nil
}

func autocompleteModeProbe(t *testing.T) *InteractiveMode {
	t.Helper()
	m, _ := newExtensionDialogProbe(t)
	ctx, cancel := context.WithCancel(t.Context())
	m.runCtx, m.backgroundCtx = ctx, ctx
	m.installRenderDispatcher()
	m.keybindings = DefaultKeybindingsManager()
	m.editor.SetAsyncApply(func(fn func()) { _ = m.postToMain(ctx, fn) })
	m.editor.SetAutocompleteTaskOwner(ctx, m.backgroundTasks.Go, func(err error) { t.Error(err) })
	m.editor.SetAutocomplete(tui.NewCombinedProvider([]tui.SlashCommand{{Name: "keep"}, {Name: "drop"}}, t.TempDir(), ""))
	t.Cleanup(func() { m.tuiStopped.Store(true); cancel(); m.backgroundTasks.Wait(); m.tuiInst.Stop() })
	return m
}
func autocompleteOwnerCall(t *testing.T, m *InteractiveMode, call func() error) {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- call() }()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
			return
		case fn := <-m.uiTaskCh:
			fn()
		case <-timer.C:
			t.Fatal("autocomplete owner call did not complete")
		}
	}
}
func drainAutocompleteWork(t *testing.T, m *InteractiveMode) {
	t.Helper()
	done := m.autocompletePending
	if done == nil {
		return
	}
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for {
		select {
		case <-done:
			return
		case fn := <-m.uiTaskCh:
			fn()
		case <-timer.C:
			t.Fatal("autocomplete work did not complete")
		}
	}
}

// Ports the three factory cases in Pi interactive-mode-status.test.ts:304-398 with real owner-loop installation and custom-editor publication.
func TestAutocompleteFactoriesRebuildOnOwnerLoop(t *testing.T) {
	m := autocompleteModeProbe(t)
	bridge := &autocompleteBridgeProbe{captureUIBridge: &captureUIBridge{}}
	m.opts.SubprocessUIBridge = bridge
	ui := &ExtUIContext{m: m}
	var factories []string
	var should []string
	wrapper := func(tag string, trigger []string) extension.AutocompleteProviderFactory {
		return func(_ context.Context, current *extension.AutocompleteProvider) (*extension.AutocompleteProvider, error) {
			factories = append(factories, tag)
			return &extension.AutocompleteProvider{TriggerCharacters: trigger, GetSuggestions: current.GetSuggestions, ApplyCompletion: current.ApplyCompletion, ShouldTriggerFileCompletion: func(ctx context.Context, lines []string, line, col int) (bool, error) {
				should = append(should, tag)
				return current.ShouldTriggerFileCompletion(ctx, lines, line, col)
			}}, nil
		}
	}
	autocompleteOwnerCall(t, m, func() error { return ui.AddAutocompleteProvider(wrapper("wrap1", []string{"$"})) })
	if !slices.Equal(factories, []string{"wrap1"}) || len(m.autocompleteFactories) != 1 {
		t.Fatalf("registration=%v", factories)
	}
	autocompleteOwnerCall(t, m, func() error { return ui.AddAutocompleteProvider(wrapper("wrap2", []string{"!", "$"})) })
	if !slices.Equal(factories, []string{"wrap1", "wrap1", "wrap2"}) {
		t.Fatalf("factories=%v", factories)
	}
	if got := m.autocompleteProvider.TriggerCharacters; !slices.Equal(got, []string{"$", "!"}) {
		t.Fatalf("triggers=%v", got)
	}
	allowed, err := m.autocompleteProvider.ShouldTriggerFileCompletion(t.Context(), []string{"foo"}, 0, 3)
	if err != nil || !allowed || !slices.Equal(should, []string{"wrap2", "wrap1"}) {
		t.Fatalf("trigger=%t %v calls=%v", allowed, err, should)
	}
	bridge.mu.Lock()
	if bridge.providers[len(bridge.providers)-1] != m.autocompleteProvider {
		t.Error("custom editor received a different provider")
	}
	bridge.mu.Unlock()
	prior := m.autocompleteProvider
	m.editor.SetAutocomplete(tui.NewCombinedProvider([]tui.SlashCommand{{Name: "new"}}, t.TempDir(), ""))
	drainAutocompleteWork(t, m)
	if prior == m.autocompleteProvider {
		t.Fatal("base replacement did not rebuild factories")
	}
	var suggestions *extension.AutocompleteSuggestions
	autocompleteOwnerCall(t, m, func() error {
		var err error
		suggestions, err = m.autocompleteProvider.GetSuggestions(t.Context(), []string{"/new"}, 0, 4, false)
		return err
	})
	if suggestions == nil || len(suggestions.Items) != 1 || suggestions.Items[0].Value != "new" {
		t.Fatalf("new base=%+v", suggestions)
	}
	count := len(factories)
	m.resetAutocompleteWrappers()
	m.editor.SetAutocomplete(tui.NewSlashOnlyProvider(nil))
	if m.autocompleteProvider != nil || len(m.autocompleteFactories) != 0 || len(factories) != count {
		t.Fatal("reload retained old factories")
	}
}

func TestAutocompleteOwnerDisconnectRemovesFactory(t *testing.T) {
	m := autocompleteModeProbe(t)
	owner, cancel := context.WithCancel(t.Context())
	ui := &ExtUIContext{m: m}
	autocompleteOwnerCall(t, m, func() error {
		return ui.AddAutocompleteProviderWithLifetime(owner, func(_ context.Context, current *extension.AutocompleteProvider) (*extension.AutocompleteProvider, error) {
			return current, nil
		})
	})
	cancel()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for len(m.autocompleteFactories) > 0 {
		select {
		case fn := <-m.uiTaskCh:
			fn()
		case <-timer.C:
			t.Fatal("disconnected owner retained its factory")
		}
	}
	if m.autocompleteProvider != nil {
		t.Fatal("disconnected owner retained its installed provider")
	}
}

func TestAutocompleteCompletionHoldsInputUntilOwnerApply(t *testing.T) {
	m := autocompleteModeProbe(t)
	entered, release := make(chan struct{}), make(chan struct{})
	ui := &ExtUIContext{m: m}
	autocompleteOwnerCall(t, m, func() error {
		return ui.AddAutocompleteProvider(func(_ context.Context, current *extension.AutocompleteProvider) (*extension.AutocompleteProvider, error) {
			return &extension.AutocompleteProvider{GetSuggestions: func(context.Context, []string, int, int, bool) (*extension.AutocompleteSuggestions, error) {
				return &extension.AutocompleteSuggestions{Items: []extension.AutocompleteItem{{Value: "completed", Label: "completed"}}, Prefix: "/"}, nil
			}, ApplyCompletion: func(context.Context, []string, int, int, extension.AutocompleteItem, string) (extension.AutocompleteCompletion, error) {
				close(entered)
				<-release
				return extension.AutocompleteCompletion{Lines: []string{"completed"}, CursorCol: 9}, nil
			}}, nil
		})
	})
	m.editor.HandleInput("/")
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for !m.editor.AutocompleteOpen() {
		select {
		case fn := <-m.uiTaskCh:
			fn()
		case <-timer.C:
			t.Fatal("popup did not arrive")
		}
	}
	m.editor.HandleInput("\t")
	<-entered
	if m.editor.Text() != "/" {
		t.Fatal("worker mutated the editor before owner apply")
	}
	ticket := newInputTicket()
	if err := m.dispatchInputChunk(m.runCtx, "x", ticket); err != nil {
		t.Fatal(err)
	}
	ticket.settle()
	if ticket.settled() {
		t.Fatal("input following remote completion was not held")
	}
	close(release)
	for !ticket.settled() {
		select {
		case fn := <-m.uiTaskCh:
			fn()
		case <-timer.C:
			t.Fatal("completion did not release input")
		}
	}
	if m.editor.Text() != "completedx" {
		t.Fatalf("ordered text=%q", m.editor.Text())
	}
}

// Ports Pi CombinedAutocompleteProvider.shouldTriggerFileCompletion's line.slice(0, col): a negative column counts back from the end of the line in UTF-16 units. The expected values were produced by running that JS (node 24) on each row.
func TestShouldTriggerFileCompletionSlicesTheLineLikeJavaScript(t *testing.T) {
	provider := (&InteractiveMode{}).baseAutocompleteProvider(nil)
	for _, row := range []struct {
		line string
		col  int
		want bool
	}{
		{"/ab", -9, true},
		{"/ab", -5, true},
		{"/ab", -3, true},
		{"/ab", -2, false},
		{"/ab", -1, false},
		{"/ab", 0, true},
		{"/ab", 1, false},
		{"/ab", 2, false},
		{"/ab", 3, false},
		{"/ab", 4, false},
		{"/ab", 5, false},
		{"/ab", 9, false},
		{"/ab cd", -9, true},
		{"/ab cd", -5, false},
		{"/ab cd", -3, false},
		{"/ab cd", -2, false},
		{"/ab cd", -1, true},
		{"/ab cd", 0, true},
		{"/ab cd", 1, false},
		{"/ab cd", 2, false},
		{"/ab cd", 3, false},
		{"/ab cd", 4, false},
		{"/ab cd", 5, true},
		{"/ab cd", 9, true},
		{"  /x", -9, true},
		{"  /x", -5, true},
		{"  /x", -3, true},
		{"  /x", -2, true},
		{"  /x", -1, false},
		{"  /x", 0, true},
		{"  /x", 1, true},
		{"  /x", 2, true},
		{"  /x", 3, false},
		{"  /x", 4, false},
		{"  /x", 5, false},
		{"  /x", 9, false},
		{"foo", -9, true},
		{"foo", -5, true},
		{"foo", -3, true},
		{"foo", -2, true},
		{"foo", -1, true},
		{"foo", 0, true},
		{"foo", 1, true},
		{"foo", 2, true},
		{"foo", 3, true},
		{"foo", 4, true},
		{"foo", 5, true},
		{"foo", 9, true},
		{"😀/a b", -9, true},
		{"😀/a b", -5, true},
		{"😀/a b", -3, true},
		{"😀/a b", -2, true},
		{"😀/a b", -1, true},
		{"😀/a b", 0, true},
		{"😀/a b", 1, true},
		{"😀/a b", 2, true},
		{"😀/a b", 3, true},
		{"😀/a b", 4, true},
		{"😀/a b", 5, true},
		{"😀/a b", 9, true},
		{"/😀 x", -9, true},
		{"/😀 x", -5, true},
		{"/😀 x", -3, false},
		{"/😀 x", -2, false},
		{"/😀 x", -1, false},
		{"/😀 x", 0, true},
		{"/😀 x", 1, false},
		{"/😀 x", 2, false},
		{"/😀 x", 3, false},
		{"/😀 x", 4, false},
		{"/😀 x", 5, true},
		{"/😀 x", 9, true},
		{"a /b", -9, true},
		{"a /b", -5, true},
		{"a /b", -3, true},
		{"a /b", -2, true},
		{"a /b", -1, true},
		{"a /b", 0, true},
		{"a /b", 1, true},
		{"a /b", 2, true},
		{"a /b", 3, true},
		{"a /b", 4, true},
		{"a /b", 5, true},
		{"a /b", 9, true},
		{"", -9, true},
		{"", -5, true},
		{"", -3, true},
		{"", -2, true},
		{"", -1, true},
		{"", 0, true},
		{"", 1, true},
		{"", 2, true},
		{"", 3, true},
		{"", 4, true},
		{"", 5, true},
		{"", 9, true},
	} {
		got, err := provider.ShouldTriggerFileCompletion(t.Context(), []string{row.line}, 0, row.col)
		if err != nil || got != row.want {
			t.Errorf("line %q col %d = %v, %v; want %v", row.line, row.col, got, err, row.want)
		}
	}
}
