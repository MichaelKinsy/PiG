package codingagent_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/factoryload"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// interactiveInprocBuild is an interactive mode over a real Session whose runner carries one compiled-in Go extension, the way a fused or built-in factory reaches the mode. pi is the extension's registering API.
type interactiveInprocBuild struct {
	services *coding.AgentSessionServices
	session  *coding.Session
	harness  *icodingagent.TestHarness
	pi       extension.API
	model    *ai.Model
	other    *ai.Model
}

func newInteractiveInprocBuild(t *testing.T) *interactiveInprocBuild {
	t.Helper()
	t.Setenv("PIG_HOME", t.TempDir())
	cwd := t.TempDir()
	services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: cwd, AgentDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(services.Close)
	if err := configureFauxAuth(services, "faux-key"); err != nil {
		t.Fatal(err)
	}
	b := &interactiveInprocBuild{services: services}
	runtime := extension.CreateExtensionRuntime()
	ext, err := factoryload.LoadExtensionFromFactory(func(pi extension.API) error {
		b.pi = pi
		return nil
	}, cwd, extension.CreateEventBus(), runtime, "fused:probe")
	if err != nil {
		t.Fatal(err)
	}
	runner := inproc.NewRunner([]extension.Extension{ext}, cwd, runtime)
	provider := &loadoutProvider{}
	capabilities := ai.ModelCapabilities{ContextWindow: 100000}
	b.model = &ai.Model{ID: "faux-1", DisplayName: "faux-1", Provider: provider, Capabilities: capabilities, ProviderMeta: ai.ProviderMetadata{Reasoning: true}}
	b.other = &ai.Model{ID: "faux-2", DisplayName: "faux-2", Provider: provider, Capabilities: capabilities, ProviderMeta: ai.ProviderMetadata{Reasoning: true}}
	b.session, err = coding.NewSession(services, coding.SessionOptions{Model: b.model, Runner: runner, NoSession: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.session.Close() })
	b.harness = icodingagent.NewTestHarness(t, icodingagent.InteractiveModeOptions{
		CWD: cwd, AgentDir: services.AgentDir(), Model: b.model, SessionHandle: b.session,
		SettingsManager: services.SettingsManager(), Settings: services.SettingsManager().Get(), ExtensionRunner: runner,
	}, nil)
	return b
}

// requireEveryActionBound fails for each ExtensionActions field the runner's runtime leaves nil. A nil field is a pi.* call that panics "not initialized" in a compiled-in extension, as pi.sendMessage did.
func requireEveryActionBound(t *testing.T, when string, runner *inproc.Runner, allowed map[string]string) {
	t.Helper()
	actions := reflect.ValueOf(runner.Runtime().ExtensionActions)
	for i := range actions.NumField() {
		name := actions.Type().Field(i).Name
		if !actions.Field(i).IsNil() {
			if _, ok := allowed[name]; ok {
				t.Errorf("%s: ExtensionActions.%s is bound now; delete its allowance", when, name)
			}
			continue
		}
		if _, ok := allowed[name]; !ok {
			t.Errorf("%s: interactive mode leaves ExtensionActions.%s unbound", when, name)
		}
	}
}

// Pi's _bindExtensionCore gives every mode all of ExtensionActions (agent-session.ts:3416-3465). The in-process runner is rebound by the mode after the Session's own bind and again after /reload builds a new runner; the runtime a compiled-in extension reads must keep every action each time.
func TestInteractiveModeBindsEveryInprocExtensionAction(t *testing.T) {
	b := newInteractiveInprocBuild(t)
	allowed := map[string]string{}
	requireEveryActionBound(t, "after the Session bind and the mode bind", b.harness.Runner(), allowed)
	if errs := b.harness.ReplaceExtensionRunner(nil); len(errs) != 0 {
		t.Fatal(errs)
	}
	requireEveryActionBound(t, "after replaceExtensionRunner", b.harness.Runner(), allowed)
}

// A compiled-in Go extension's pi.* calls reach the Session in interactive mode as they do under the isolated SDKs and in print mode (agent-session.ts:3416-3465).
func TestInteractiveFusedGoExtensionReachesSessionActions(t *testing.T) {
	b := newInteractiveInprocBuild(t)
	t.Run("sendMessage", func(t *testing.T) {
		b.pi.SendMessage(extension.SendMessagePayload{CustomType: "fused-note", Content: "hello", Display: true}, nil)
		entries, err := json.Marshal(b.session.Entries())
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(entries), `"customType":"fused-note"`) {
			t.Fatalf("session entries lack the sent message: %s", entries)
		}
	})
	t.Run("appendEntry", func(t *testing.T) {
		b.pi.AppendEntry("fused-entry", map[string]any{"n": 1})
		entries, err := json.Marshal(b.session.Entries())
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(entries), `"customType":"fused-entry"`) {
			t.Fatalf("session entries lack the appended entry: %s", entries)
		}
	})
	t.Run("session name", func(t *testing.T) {
		b.pi.SetSessionName("fused name")
		if got := b.pi.GetSessionName(); got != "fused name" || b.session.SessionName() != "fused name" {
			t.Fatalf("GetSessionName=%q Session name=%q", got, b.session.SessionName())
		}
	})
	t.Run("model", func(t *testing.T) {
		switched, err := b.pi.SetModel(b.other)
		if err != nil || !switched {
			t.Fatalf("SetModel = %v, %v", switched, err)
		}
		if got := b.session.Model(); got != b.other {
			t.Fatalf("Session model = %v, want the model the extension set", got)
		}
	})
	t.Run("thinking level", func(t *testing.T) {
		b.pi.SetThinkingLevel(extension.ThinkingLevel(ai.ThinkingHigh))
		if got := b.pi.GetThinkingLevel(); got != extension.ThinkingLevel(ai.ThinkingHigh) || b.session.ThinkingLevel() != ai.ThinkingHigh {
			t.Fatalf("GetThinkingLevel=%q Session level=%q", got, b.session.ThinkingLevel())
		}
	})
}
