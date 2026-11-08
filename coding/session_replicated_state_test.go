package coding

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// Pi's extension context reads the live Session at every call (runner.ts:906 `get model()`, loader.ts:411-414 getSettings, agent-session.ts getSessionName and thinkingLevel), so a change the Session makes with no extension call, a user's model switch, a settings edit, the /name command, a resumed Session, reaches an extension that reads between requests. A runtime replicates that state, so the Session reports each change to its owner.
func TestSessionReportsEveryChangeOfTheStateExtensionsReplicate(t *testing.T) {
	h := newRecoveryHarness(t, harnessOptions{})
	s := h.session
	var reports atomic.Int64
	stop := s.ObserveReplicatedState(func() { reports.Add(1) })
	t.Cleanup(stop)
	other := *s.Model()
	other.ID, other.DisplayName = "faux-2", "faux-2"

	for _, step := range []struct {
		name   string
		change func() error
	}{
		{"SetModel", func() error { return s.SetModel(&other) }},
		{"SetThinkingLevel", func() error { return s.SetThinkingLevel(ai.ThinkingHigh) }},
		{"SetSessionName", func() error { return s.SetSessionName("renamed") }},
		{"a settings setter", func() error { return s.SettingsManager().SetEditorPaddingX(2) }},
		{"ReplaceInner", func() error {
			s.ReplaceInner(icodingagent.NewSession("replacement", t.TempDir()))
			return nil
		}},
	} {
		before := reports.Load()
		if err := step.change(); err != nil {
			t.Fatalf("%s: %v", step.name, err)
		}
		if reports.Load() == before {
			t.Errorf("%s changed replicated state without a report", step.name)
		}
	}
}

// The report follows the change: an owner that reads the Session from the report sees the new model and the new name.
func TestSessionReplicatedStateReportFollowsTheChange(t *testing.T) {
	h := newRecoveryHarness(t, harnessOptions{})
	s := h.session
	var mu sync.Mutex
	var model, name string
	stop := s.ObserveReplicatedState(func() {
		mu.Lock()
		defer mu.Unlock()
		model, name = s.Model().ID, s.SessionName()
	})
	t.Cleanup(stop)
	seen := func() (string, string) {
		mu.Lock()
		defer mu.Unlock()
		return model, name
	}

	other := *s.Model()
	other.ID, other.DisplayName = "faux-2", "faux-2"
	if err := s.SetModel(&other); err != nil {
		t.Fatal(err)
	}
	if got, _ := seen(); got != "faux-2" {
		t.Fatalf("the report after SetModel read model %q, want faux-2", got)
	}
	if err := s.SetSessionName("renamed"); err != nil {
		t.Fatal(err)
	}
	if _, got := seen(); got != "renamed" {
		t.Fatalf("the report after SetSessionName read name %q, want renamed", got)
	}
}

// A model whose registry metadata changed is the same selection with other data, and an extension that reads ctx.model sees the refreshed model.
func TestSessionReportsAModelRefreshedFromTheRegistry(t *testing.T) {
	services := newTestServices(t)
	const providerID = "state-refresh"
	register := func(config extension.ProviderConfig) {
		t.Helper()
		if err := services.Registry().RegisterExtensionProvider(providerID, config); err != nil {
			t.Fatal(err)
		}
	}
	register(extension.ProviderConfig{API: "openai-completions", BaseURL: "https://before.test", APIKey: "key", Models: []extension.ProviderModelConfig{{ID: "model", Name: "Model", ContextWindow: 128000, MaxTokens: 16384}}})
	model := services.ModelRuntime().GetModel(providerID, "model")
	if model == nil {
		t.Fatal("missing registered model")
	}
	session, err := NewSession(services, SessionOptions{Model: model, NoSession: true, SkipBuiltinTools: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	drainSessionEvents(t, session)
	var url atomic.Value
	stop := session.ObserveReplicatedState(func() { url.Store(session.Model().ProviderMeta.BaseURL) })
	t.Cleanup(stop)

	register(extension.ProviderConfig{BaseURL: "https://after.test"})
	if got, _ := url.Load().(string); got != "https://after.test" {
		t.Fatalf("the report after a registry refresh read base URL %q", got)
	}
}

// stop removes the observer, and a Session without one pays an atomic load: no allocation on the path every model, name and thinking change takes.
func TestSessionReplicatedStateObserversStopAndCostNothingWhenIdle(t *testing.T) {
	h := newRecoveryHarness(t, harnessOptions{})
	s := h.session
	if allocs := testing.AllocsPerRun(1000, s.notifyReplicatedState); allocs != 0 {
		t.Fatalf("a report with no observer allocated %v times", allocs)
	}
	var reports atomic.Int64
	stop := s.ObserveReplicatedState(func() { reports.Add(1) })
	if allocs := testing.AllocsPerRun(1000, s.notifyReplicatedState); allocs != 0 {
		t.Fatalf("a report to one observer allocated %v times", allocs)
	}
	stop()
	stop()
	before := reports.Load()
	if err := s.SetSessionName("after stop"); err != nil {
		t.Fatal(err)
	}
	if err := s.SettingsManager().SetEditorPaddingX(1); err != nil {
		t.Fatal(err)
	}
	if got := reports.Load(); got != before {
		t.Fatalf("a stopped observer was told of %d changes", got-before)
	}
}

// A change of the model, session id or name is bracketed: begin runs while the old state is still the Session's, end after the report that follows the change, in pairs. The extension host flushes its state before it replies to a call between the two, because a call can read the new state before the owner reports it.
func TestSessionBracketsEveryChangeOfIdentityState(t *testing.T) {
	h := newRecoveryHarness(t, harnessOptions{})
	s := h.session
	var mu sync.Mutex
	var begun, ended, reported int
	var atBegin, atEnd []string
	read := func() string { return s.Model().ID + "|" + s.SessionName() + "|" + s.ID() }
	stopState := s.ObserveReplicatedState(func() {
		mu.Lock()
		defer mu.Unlock()
		if ended != begun-1 {
			t.Errorf("a report ran outside a bracket (begun %d, ended %d)", begun, ended)
		}
		reported++
	})
	t.Cleanup(stopState)
	stop := s.ObserveReplicatedStateScope(func() {
		mu.Lock()
		defer mu.Unlock()
		begun++
		atBegin = append(atBegin, read())
	}, func() {
		mu.Lock()
		defer mu.Unlock()
		ended++
		atEnd = append(atEnd, read())
	})
	t.Cleanup(stop)
	other := *s.Model()
	other.ID, other.DisplayName = "faux-2", "faux-2"

	for _, step := range []struct {
		name   string
		change func() error
	}{
		{"SetModel", func() error { return s.SetModel(&other) }},
		{"SetSessionName", func() error { return s.SetSessionName("renamed") }},
		{"ReplaceInner", func() error {
			s.ReplaceInner(icodingagent.NewSession("replacement", t.TempDir()))
			return nil
		}},
	} {
		mu.Lock()
		before := begun
		old := read()
		mu.Unlock()
		if err := step.change(); err != nil {
			t.Fatalf("%s: %v", step.name, err)
		}
		mu.Lock()
		if begun != before+1 || ended != begun || reported < begun {
			t.Errorf("%s: begun %d ended %d reported %d, want one bracket and a report in it", step.name, begun, ended, reported)
		}
		if atBegin[before] != old {
			t.Errorf("%s: begin saw %q, want the state before the change %q", step.name, atBegin[before], old)
		}
		if atEnd[before] == old {
			t.Errorf("%s: end saw the state before the change %q", step.name, old)
		}
		mu.Unlock()
	}
}
