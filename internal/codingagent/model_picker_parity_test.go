package codingagent

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/tui"
)

func modelPickerTestMode(t *testing.T) *InteractiveMode {
	t.Helper()
	clearAllAuthEnv(t)
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	current := &ai.Model{ID: "current", Provider: captureStreamOptionsProvider{}}
	sm := NewSettingsManager(t.TempDir(), dir)
	if err := sm.SetDefaultModelAndProvider("capture", "original"); err != nil {
		t.Fatal(err)
	}
	registry := NewModelRegistry(dir)
	// Catalog refresh can outlive picker cancellation. Keep its unrelated cache in memory; settings remain file-backed to verify default persistence.
	registry.SetModelsStore(ai.NewInMemoryModelsStore())
	m := NewInteractiveMode(nil, InteractiveModeOptions{AgentDir: dir, CWD: t.TempDir(), Model: current, SettingsManager: sm, ModelRegistry: registry, ModelBuilder: func(spec string) (*ai.Model, error) {
		return &ai.Model{ID: spec, Provider: captureStreamOptionsProvider{}}, nil
	}})
	m.chatContainer = tui.NewContainer()
	m.tuiInst = tui.NewWithOutput(io.Discard, 100, 35)
	m.installRenderDispatcher()
	m.editor = tui.NewEditor()
	m.statusLine = NewFooterComponent(current, "", nil)
	m.agent = mustNewAgent(agent.AgentOptions{Model: current})
	m.setModalInputChannel(make(chan []byte, 2))
	return m
}

// Pi's selector uses the runtime's available snapshot, not a second provider allowlist.
func TestModelPickerGeminiAuthAndCatalog(t *testing.T) {
	for _, tc := range []struct {
		name, gemini, google string
		available            bool
	}{
		{"Gemini only", "gemini-test", "", true},
		{"both keys", "gemini-test", "ignored-google-test", true},
		{"Google only", "", "ignored-google-test", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := modelPickerTestMode(t)
			t.Setenv("GEMINI_API_KEY", tc.gemini)
			t.Setenv("GOOGLE_API_KEY", tc.google)
			var want []string
			if tc.available {
				for _, model := range ai.ListModels("google") {
					want = append(want, "google/"+model.ID)
				}
			}
			var got []string
			for _, item := range m.modelArgCompletions("") {
				if item.Description == "google" {
					got = append(got, item.Value)
				}
			}
			if !slices.Equal(got, want) {
				t.Errorf("Google completion catalog = %v, want generated catalog %v", got, want)
			}
			query := ai.ListModels("google")[0].ID
			input, release := m.acquireModalInputChannel()
			defer release()
			input <- []byte("\r")
			input <- []byte("\x1b") // terminates the empty picker on the broken path
			spec, ok := m.buildSlashContext(context.Background()).PickModel(query)
			if ok != tc.available || (ok && spec != "google/"+query) {
				t.Fatalf("PickModel(%q) = %q, %v; available=%v", query, spec, ok, tc.available)
			}
		})
	}
}

func TestCycleModelDoesNotPersistDefault(t *testing.T) {
	m := modelPickerTestMode(t)
	next := &ai.Model{ID: "next", ProviderMeta: ai.ProviderMetadata{ProviderID: "capture"}}
	handle := &recordingCompactHandle{agent: m.agent, cycleResults: []*ModelCycleResult{{Model: next}}}
	m.opts.SessionHandle = handle
	m.cycleModel(true)
	if got := m.opts.SettingsManager.GetDefaultModel(); got != "original" {
		t.Fatalf("cycling persisted %q as default", got)
	}
	if len(handle.cycleOptions) != 1 || len(handle.cycleOptions[0]) != 0 {
		t.Fatalf("cycle options = %v, want no persisted default", handle.cycleOptions)
	}
}

// Pi appends a saved default to the session scope and maps that scope directly on reopening; only the all-models list is sorted.
func TestModelPickerReopensInScopeOrderAfterSavingDefault(t *testing.T) {
	m := modelPickerTestMode(t)
	models := `{"providers":{"capture":{"baseUrl":"http://127.0.0.1:1/v1","api":"openai-completions","apiKey":"fixture-key","models":[{"id":"model-two","name":"Model Two"},{"id":"model-one","name":"Model One"}]}}}`
	if err := os.WriteFile(filepath.Join(m.opts.AgentDir, "models.json"), []byte(models), 0o600); err != nil {
		t.Fatal(err)
	}
	// The picker renders its existing snapshot before refreshing asynchronously.
	m.opts.ModelRegistry.Refresh()
	m.opts.Model = &ai.Model{ID: "model-one", Provider: captureStreamOptionsProvider{}}
	m.opts.ModelBuilder = func(spec string) (*ai.Model, error) {
		_, id, _ := strings.Cut(spec, "/")
		return &ai.Model{ID: id, Provider: captureStreamOptionsProvider{}}, nil
	}
	if err := m.opts.SettingsManager.SetDefaultModelAndProvider("capture", "model-one"); err != nil {
		t.Fatal(err)
	}
	handle := &recordingCompactHandle{
		agent:        m.agent,
		scopedModels: []extension.ScopedModel{{Model: m.opts.Model}},
	}
	m.opts.SessionHandle = handle
	m.scopedModelIDs = []string{"capture/model-one"}
	m.setModalInputChannel(make(chan []byte, 3))
	input, release := m.acquireModalInputChannel()
	defer release()
	input <- []byte("\t")
	input <- []byte("model-two")
	input <- []byte("\x13")
	sc := m.buildSlashContext(context.Background())
	spec, ok := sc.PickModel("")
	if !ok || spec != "capture/model-two" {
		t.Fatalf("save selection = %q, %v; want capture/model-two", spec, ok)
	}
	if err := sc.SwitchModel(spec); err != nil {
		t.Fatal(err)
	}
	if want := []string{"capture/model-one", "capture/model-two"}; !slices.Equal(m.scopedModelIDs, want) {
		t.Fatalf("scope = %v, want %v", m.scopedModelIDs, want)
	}
	if len(handle.scopedModels) != 2 || modelSpec(handle.scopedModels[1].Model) != "capture/model-two" {
		t.Fatalf("Session scope = %+v, want appended capture/model-two", handle.scopedModels)
	}

	var output bytes.Buffer
	m.tuiInst = tui.NewWithOutput(&output, 100, 35)
	// The picker's frame requests run on the owner loop, as in production; a timer render would race the read of output.
	m.installRenderDispatcher()
	input <- []byte("\x1b")
	if _, accepted := m.buildSlashContext(context.Background()).PickModel(""); accepted {
		t.Fatal("reopened picker accepted Escape")
	}
	one, two := strings.Index(output.String(), "model-one"), strings.Index(output.String(), "model-two")
	if one < 0 || two < 0 || one >= two {
		t.Fatalf("reopened rows must retain model-one before appended model-two, got:\n%s", output.String())
	}
}

// showModelSelector passes persist=false for Enter and persist=true only for Ctrl+S.
func TestModelPickerPersistsOnlyExplicitDefault(t *testing.T) {
	for _, tc := range []struct {
		name, key       string
		persist, cancel bool
	}{
		{"Enter", "\r", false, false}, {"CtrlS", "\x13", true, false}, {"Escape", "\x1b", false, true}, {"CtrlC", "\x03", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := modelPickerTestMode(t)
			t.Setenv("OPENAI_API_KEY", "fake-openai")
			target := ai.ListModels("openai")[0].ID
			m.opts.ModelBuilder = func(string) (*ai.Model, error) {
				return &ai.Model{ID: target, Provider: captureStreamOptionsProvider{}}, nil
			}
			m.scopedModelIDs = []string{"capture/original"}
			if err := m.opts.SettingsManager.UpdateGlobal(func(settings *Settings) { settings.EnabledModels = []string{"capture/original"} }); err != nil {
				t.Fatal(err)
			}
			input, release := m.acquireModalInputChannel()
			defer release()
			input <- []byte(tc.key)
			input <- []byte("\x1b")
			sc := m.buildSlashContext(context.Background())
			spec, ok := sc.PickModel(target)
			if ok == tc.cancel {
				t.Fatalf("selection accepted=%v, want %v", ok, !tc.cancel)
			}
			if ok {
				if err := sc.SwitchModel(spec); err != nil {
					t.Fatal(err)
				}
			}
			want := "original"
			if tc.persist {
				want = target
			}
			m.opts.SettingsManager.Reload()
			if got := m.opts.SettingsManager.GetDefaultModel(); got != want {
				t.Fatalf("default model=%q, want %q", got, want)
			}
			wantScope := []string{"capture/original"}
			if tc.persist {
				wantScope = append(wantScope, "capture/"+target)
			}
			if !slices.Equal(m.scopedModelIDs, wantScope) || !slices.Equal(m.opts.SettingsManager.GetEnabledModels(), wantScope) {
				t.Fatalf("scope=%v, saved scope=%v, want %v", m.scopedModelIDs, m.opts.SettingsManager.GetEnabledModels(), wantScope)
			}
		})
	}
}

// model-selector.ts:184-221: the catalog refresh outcome is applied to the picker on the loop that owns it. The selector's refresh goroutine
// reaches the picker only through TUI.PostToOwner, and the modal loop that reads the picker's keys runs the posted task, so the picker's state has
// one writer at a time (-race: the refresh's updateList and HandleInput share m.filtered).
func TestModelPickerAppliesTheCatalogRefreshOutcomeOnTheModalLoop(t *testing.T) {
	m := modelPickerTestMode(t)
	models := `{"providers":{"capture":{"baseUrl":"http://127.0.0.1:1/v1","api":"openai-completions","apiKey":"fixture-key","models":[{"id":"model-one","name":"Model One"}]}}}`
	if err := os.WriteFile(filepath.Join(m.opts.AgentDir, "models.json"), []byte(models), 0o600); err != nil {
		t.Fatal(err)
	}
	m.opts.ModelRegistry.Refresh()
	m.opts.Model = &ai.Model{ID: "model-one", Provider: captureStreamOptionsProvider{}}
	var posted atomic.Int32
	m.tuiInst.SetOwnerDispatcher(func(ctx context.Context, fn func()) error {
		posted.Add(1)
		return m.postToMain(ctx, fn)
	})
	m.setModalInputChannel(make(chan []byte, 3))
	input, release := m.acquireModalInputChannel()
	defer release()
	done := make(chan bool, 1)
	go func() {
		_, accepted := m.buildSlashContext(context.Background()).PickModel("")
		done <- accepted
	}()
	deadline := time.After(10 * time.Second)
	for posted.Load() == 0 {
		select {
		case <-deadline:
			t.Fatal("the catalog refresh outcome never reached the owner loop")
		case <-time.After(5 * time.Millisecond):
		}
	}
	input <- []byte("\x1b")
	if accepted := <-done; accepted {
		t.Fatal("Escape accepted a model")
	}
}
