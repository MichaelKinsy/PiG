package extensionconformance

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// thinkingSessionAPI is the Go reference for the thinking-level members of extension.API: the production Session that answers and
// applies them for every SDK.
type thinkingSessionAPI struct {
	extension.API
	session *coding.Session
}

func (a thinkingSessionAPI) GetThinkingLevel() extension.ThinkingLevel {
	return a.session.ThinkingLevel()
}

func (a thinkingSessionAPI) SetThinkingLevel(level extension.ThinkingLevel) {
	_ = a.session.SetThinkingLevel(level)
}

// TestConformance_ExtensionAPIThinkingLevel pins Pi's pi.setThinkingLevel and pi.getThinkingLevel
// (packages/coding-agent/src/core/extensions/types.ts:1747-1751). Every SDK sets the level through the host, which applies it to a real
// Session through extension.API, and then reads back the level that Session holds.
func TestConformance_ExtensionAPIThinkingLevel(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	t.Parallel()

	for _, tc := range sdkHarnessCases() {
		t.Run(tc.name, func(t *testing.T) {
			h := tc.make(t)
			t.Cleanup(func() {
				if h.cleanup != nil {
					h.cleanup()
				}
				if h.host != nil {
					h.host.Shutdown("test done")
				}
			})
			if h.bridge == nil {
				t.Skip("no host bridge for " + tc.name)
			}
			model := ai.GetBuiltinModel("anthropic", "claude-sonnet-4-5")
			if model == nil || len(ai.GetSupportedThinkingLevels(model)) < 3 {
				t.Fatalf("the catalog model has no thinking levels: %+v", model)
			}
			services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: t.TempDir(), AgentDir: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			session, err := coding.NewSession(services, coding.SessionOptions{NoSession: true, SkipBuiltinTools: true, Model: model})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = session.Close() })
			var api extension.API = thinkingSessionAPI{session: session}
			h.bridge.SetHostAction("getThinkingLevel", func() string { return string(api.GetThinkingLevel()) })
			h.bridge.SetHostAction("setThinkingLevel", func(level string) { api.SetThinkingLevel(extension.ThinkingLevel(level)) })

			for _, level := range []ai.ModelThinkingLevel{ai.ThinkingHigh, ai.ThinkingLow} {
				runConformanceCommandArgs(t, h, "thinking_set", string(level))
				pollUntilConformance(t, 5*time.Second, "the host never applied "+string(level), func() bool {
					return api.GetThinkingLevel() == level
				})
				*h.notify = (*h.notify)[:0]
				runConformanceCommand(t, h, "thinking_get")
				pollUntilConformance(t, 5*time.Second, "the SDK never reported "+string(level), func() bool {
					return slices.ContainsFunc(*h.notify, func(n string) bool { return strings.HasPrefix(n, "thinking_get:"+string(level)) })
				})
			}
		})
	}
}

// modelSessionAPI is the Go reference for extension.API.SetModel: the production Session that switches the model for every SDK.
type modelSessionAPI struct {
	extension.API
	session *coding.Session
}

func (a modelSessionAPI) SetModel(model extension.Model) (bool, error) {
	if model == nil {
		return false, nil
	}
	if err := a.session.SetModel(model); err != nil {
		return false, err
	}
	return true, nil
}

// TestConformance_ExtensionAPISetModel pins Pi's pi.setModel (packages/coding-agent/src/core/extensions/types.ts:1741). Every SDK asks
// the host to switch to a catalog model; the host applies it to a real Session through extension.API, the Session must hold that
// model, and the SDK must report the success the host returned.
func TestConformance_ExtensionAPISetModel(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	// The Session refuses a model without credentials; t.Setenv forbids t.Parallel.
	t.Setenv("ANTHROPIC_API_KEY", "conformance-key")

	for _, tc := range sdkHarnessCases() {
		t.Run(tc.name, func(t *testing.T) {
			h := tc.make(t)
			t.Cleanup(func() {
				if h.cleanup != nil {
					h.cleanup()
				}
				if h.host != nil {
					h.host.Shutdown("test done")
				}
			})
			if h.bridge == nil {
				t.Skip("no host bridge for " + tc.name)
			}
			start := ai.GetBuiltinModel("anthropic", "claude-sonnet-4-5")
			target := ai.GetBuiltinModel("anthropic", "claude-haiku-4-5")
			if start == nil || target == nil {
				t.Fatalf("catalog models missing: %v %v", start, target)
			}
			services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: t.TempDir(), AgentDir: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			session, err := coding.NewSession(services, coding.SessionOptions{NoSession: true, SkipBuiltinTools: true, Model: start})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = session.Close() })
			var api extension.API = modelSessionAPI{session: session}
			h.bridge.SetHostAction("setModel", func(_ context.Context, ref string) (bool, error) {
				provider, id, _ := strings.Cut(ref, "/")
				return api.SetModel(ai.GetBuiltinModel(provider, id))
			})
			if got := session.Model().ID; got != start.ID {
				t.Fatalf("the Session starts on %s, want %s", got, start.ID)
			}
			*h.notify = (*h.notify)[:0]
			runConformanceCommandArgs(t, h, "model_set", "anthropic/"+target.ID)
			pollUntilConformance(t, 5*time.Second, "the SDK never reported the switch", func() bool {
				return slices.Contains(*h.notify, "model_set:true:info")
			})
			if got := session.Model().ID; got != target.ID {
				t.Fatalf("the Session holds %s after the switch, want %s", got, target.ID)
			}
		})
	}
}

// sessionNameAPI is the Go reference for extension.API.GetSessionName: the production Session's name, which the host answers for
// every SDK.
type sessionNameAPI struct {
	extension.API
	session *coding.Session
}

func (a sessionNameAPI) GetSessionName() string { return a.session.SessionName() }

// TestConformance_ExtensionAPIGetSessionName pins Pi's pi.getSessionName (packages/coding-agent/src/core/extensions/types.ts:1718). The
// Session is named through its own setter and every SDK must read that name through the host, and read it as absent before.
func TestConformance_ExtensionAPIGetSessionName(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	t.Parallel()

	for _, tc := range sdkHarnessCases() {
		t.Run(tc.name, func(t *testing.T) {
			h := tc.make(t)
			t.Cleanup(func() {
				if h.cleanup != nil {
					h.cleanup()
				}
				if h.host != nil {
					h.host.Shutdown("test done")
				}
			})
			if h.bridge == nil {
				t.Skip("no host bridge for " + tc.name)
			}
			services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: t.TempDir(), AgentDir: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			session, err := coding.NewSession(services, coding.SessionOptions{NoSession: true, SkipBuiltinTools: true})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = session.Close() })
			var api extension.API = sessionNameAPI{session: session}
			h.bridge.SetHostAction("getSessionName", func() string { return api.GetSessionName() })
			h.bridge.SetHostAction("getSessionID", session.ID)
			h.bridge.SetHostAction("getSessionFile", func() string { return "" })
			h.bridge.SetHostAction("getLeafID", func() string { return "" })

			readName := func() *string {
				t.Helper()
				*h.notify = nil
				runConformanceCommand(t, h, "session-identity")
				var got []*string
				pollUntilConformance(t, 5*time.Second, "the SDK never reported its session identity", func() bool {
					return len(*h.notify) == 1
				})
				if err := json.Unmarshal([]byte(strings.TrimSuffix((*h.notify)[0], ":info")), &got); err != nil || len(got) != 4 {
					t.Fatalf("identity = %v (%v)", *h.notify, err)
				}
				return got[3]
			}
			if name := readName(); name != nil {
				t.Fatalf("an unnamed Session reads as %q, want absent", *name)
			}
			if err := session.SetSessionName("conformance-name"); err != nil {
				t.Fatal(err)
			}
			if name := readName(); name == nil || *name != api.GetSessionName() || *name != "conformance-name" {
				t.Fatalf("the SDK read the session name %v, the Session holds %q", name, api.GetSessionName())
			}
		})
	}
}

// settingsSessionAPI is the Go reference for extension.API.GetSettings: the production Session's SettingsManager, which reads the
// settings file the host answers from for every SDK.
type settingsSessionAPI struct {
	extension.API
	session *coding.Session
}

func (a settingsSessionAPI) GetSettings() extension.Settings {
	return a.session.SettingsManager().ExtensionSettings()
}

// TestConformance_ExtensionAPIGetSettings pins Pi's pi.getSettings (packages/coding-agent/src/core/extensions/types.ts:1708). The
// settings come from a real settings.json read by the production SettingsManager; the expectation is not stored by the test, and the
// test requires the file's own values in it, so a SettingsManager that dropped them would fail. Every SDK must report the same settings.
func TestConformance_ExtensionAPIGetSettings(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	t.Parallel()

	for _, tc := range sdkHarnessCases() {
		t.Run(tc.name, func(t *testing.T) {
			h := tc.make(t)
			t.Cleanup(func() {
				if h.cleanup != nil {
					h.cleanup()
				}
				if h.host != nil {
					h.host.Shutdown("test done")
				}
			})
			if h.bridge == nil {
				t.Skip("no host bridge for " + tc.name)
			}
			agentDir := t.TempDir()
			if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(`{"defaultProvider":"conformance-provider","fullscreenWheelScrollLines":7,"nested":{"list":["a","b"]}}`), 0o600); err != nil {
				t.Fatal(err)
			}
			services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: t.TempDir(), AgentDir: agentDir})
			if err != nil {
				t.Fatal(err)
			}
			session, err := coding.NewSession(services, coding.SessionOptions{NoSession: true, SkipBuiltinTools: true})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = session.Close() })
			var api extension.API = settingsSessionAPI{session: session}
			got := api.GetSettings()
			if got["defaultProvider"] != "conformance-provider" || got["fullscreenWheelScrollLines"] != float64(7) && got["fullscreenWheelScrollLines"] != 7 {
				t.Fatalf("the SettingsManager dropped the file's values: %#v", got)
			}
			h.bridge.SetHostAction("getSettings", func() extension.Settings { return api.GetSettings() })
			wire, err := json.Marshal(got)
			if err != nil {
				t.Fatal(err)
			}
			var want any
			if err := json.Unmarshal(wire, &want); err != nil {
				t.Fatal(err)
			}
			pollUntilConformance(t, 10*time.Second, "the SDK never reported the host's settings", func() bool {
				*h.notify = nil
				runConformanceCommand(t, h, "settings_probe")
				time.Sleep(100 * time.Millisecond)
				for _, n := range *h.notify {
					if rest, ok := strings.CutPrefix(n, "settings_probe:"); ok {
						var reported any
						if json.Unmarshal([]byte(strings.TrimSuffix(rest, ":info")), &reported) == nil && reflect.DeepEqual(reported, want) {
							return true
						}
					}
				}
				return false
			})
		})
	}
}
