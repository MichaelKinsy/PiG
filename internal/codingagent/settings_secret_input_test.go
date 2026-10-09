package codingagent

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/tui"
)

func TestMaskSecretInputSettingsRoundTrip(t *testing.T) {
	item := settingsSelectorRow(t, SettingsConfig{MaskSecretInput: true}, "mask-secret-input")
	if item.Label != "Mask secret input" || item.CurrentValue != "true" || !strings.Contains(item.Description, "Pi") || !strings.Contains(item.Description, "false") {
		t.Fatalf("setting metadata = %+v", item)
	}
	for _, value := range []string{"false", "true"} {
		sm := NewSettingsManager(t.TempDir(), t.TempDir())
		var reported []bool
		list := NewSettingsSelectorComponent(SettingsConfig{MaskSecretInput: value == "false"}, SettingsCallbacks{OnMaskSecretInputChange: func(enabled bool) {
			reported = append(reported, enabled)
			if err := sm.SetMaskSecretInput(enabled); err != nil {
				t.Fatal(err)
			}
		}}).GetSettingsList()
		list.SelectItem("mask-secret-input")
		list.HandleInput("\r")
		if len(reported) != 1 || strconv.FormatBool(reported[0]) != value {
			t.Fatalf("cycling reported %v, want [%s]", reported, value)
		}
		s := sm.Get()
		encoded, err := json.Marshal(s)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(encoded), `"maskSecretInput":`+value) {
			t.Fatalf("serialized settings = %s", encoded)
		}
		var restored Settings
		if err := json.Unmarshal(encoded, &restored); err != nil {
			t.Fatal(err)
		}
		if strconv.FormatBool(restored.GetMaskSecretInput()) != value {
			t.Fatal("setting did not round trip")
		}
		sm.Reload()
		if strconv.FormatBool(sm.Get().GetMaskSecretInput()) != value {
			t.Fatal("setting was not persisted")
		}
	}
}

func TestMaskSecretInputSettingsMenuAppliesToNextDialog(t *testing.T) {
	m := newPostLoginTestMode(t)
	sc := m.buildSlashContext(t.Context())
	sc.ShowSettingsSelector = func(build func(done func()) *SettingsSelectorComponent) {
		list := build(func() {}).GetSettingsList()
		list.SelectItem("mask-secret-input")
		list.HandleInput("\r")
		for _, item := range list.Items() {
			if item.ID == "mask-secret-input" && item.CurrentValue != "false" {
				t.Errorf("menu shows %q after the change, want false", item.CurrentValue)
			}
		}
	}
	if err := settingsHandler(sc); err != nil {
		t.Fatal(err)
	}
	d := m.newLoginDialog("Test", nil)
	d.ShowSecretInput("API key", "")
	d.HandleInput("plain-after-toggle")
	if !strings.Contains(plainRender(d), "plain-after-toggle") {
		t.Fatal("new login ignored /settings change")
	}
}

func TestMaskedLoginErrorDoesNotEnterFramesOrSession(t *testing.T) {
	m := newPostLoginTestMode(t)
	inner, err := m.newSessionManager().Create("secret-diagnostic", "")
	if err != nil {
		t.Fatal(err)
	}
	m.opts.SessionHandle = &recordingCompactHandle{agent: m.agent, inner: inner}
	const secret = "synthetic-private-key-abcd"
	m.opts.ModelBuilder = func(string) (*ai.Model, error) { return nil, errors.New("server echoed " + secret) }
	if err := setPostLoginAPIKey(m, "openai", secret); err != nil {
		t.Fatal(err)
	}
	waitPostLoginStatus(t, m, "selecting its default model failed")
	frame := plainRender(m.chatContainer)
	if strings.Contains(frame, secret) || !strings.Contains(frame, "••••••••abcd") {
		t.Fatal("authentication error was not redacted")
	}
	data, err := json.Marshal(inner.GetEntries())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), secret) {
		t.Fatal("login secret entered Session entries")
	}
	// Credentials intentionally belong in auth.json, not in diagnostic/session output.
	if err := filepath.WalkDir(m.opts.AgentDir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || entry.Name() == "auth.json" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(data), secret) {
			t.Errorf("secret leaked outside credential storage: %s", path)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestPiIgnoresMaskSecretInputInSharedSettings(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []bool{true, false} {
		dir := t.TempDir()
		// Exercise the integration's shared lock backend as well as the extra JSON key.
		sm := NewSettingsManager(t.TempDir(), dir)
		if err := sm.UpdateGlobal(func(s *Settings) { s.MaskSecretInput = &value }); err != nil {
			t.Fatal(err)
		}
		data, err := exec.CommandContext(t.Context(), "node", filepath.Join(root, "test/parity/testdata/login-dialog-privacy.mjs"), root, "settings", dir).CombinedOutput()
		if err != nil {
			t.Fatalf("Pi settings reader: %v: %s", err, data)
		}
		var result struct {
			Value  bool
			Errors []any
			Theme  string
		}
		if err := json.Unmarshal(data, &result); err != nil {
			t.Fatal(err)
		}
		if result.Value != value || len(result.Errors) != 0 || result.Theme != "light" {
			t.Fatalf("Pi rejected or misused shared setting: %s", data)
		}
		sm.Reload()
		if sm.Get().GetMaskSecretInput() != value || sm.Get().Theme != "light" {
			t.Fatal("Pi's settings update lost the Pig-only preference")
		}
	}
}

func TestLoginMaskSettingReachesStandardDialog(t *testing.T) {
	m := newPostLoginTestMode(t)
	m.layout = tui.NewContainer(m.chatContainer, m.editorContainer)
	if err := os.WriteFile(filepath.Join(m.opts.AgentDir, "settings.json"), []byte(`{"maskSecretInput":false}`), 0o600); err != nil {
		t.Fatal(err)
	}
	m.opts.SettingsManager.Reload()
	done := make(chan error, 1)
	go func() { done <- m.runAPIKeyLogin(tui.OAuthProvider{ID: "openai", Name: "OpenAI", AuthType: "api_key"}) }()
	waitForRender(t, m.editorContainer, "Enter OpenAI API key")
	const key = "visible-like-pi"
	deliverModalInput(t, m, []byte(key))
	deliverModalInput(t, m, []byte(""))
	frame := plainRender(m.editorContainer)
	deliverModalInput(t, m, []byte("\r"))
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	store, err := ai.NewAuthStorage(filepath.Join(m.opts.AgentDir, "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	credential, ok, err := store.Get("openai")
	if err != nil || !ok || credential.Key != key {
		t.Fatal("changed submitted value")
	}
	if !strings.Contains(frame, key) || strings.Contains(frame, "Input hidden") {
		t.Fatal("false did not restore Pi input rendering")
	}
}
