package cli

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

// The getSettingsJSON host action gives the getSettings() object as JSON, before a Session exists and before and after a settings write, and
// the state push carries those bytes.
func TestSessionSettingsJSONIsTheEncodingOfGetSettings(t *testing.T) {
	var live *coding.Session
	current := func() *coding.Session { return live }
	actions, _ := sessionExtensionActions(current, nil)
	settingsJSON := sessionSettingsJSON(current)
	bridge := subprocess.NewUIBridge(nil)
	bindSessionExtensionActions(nil, bridge, current, extension.ContextActions{}, nil)
	check := func(step string) {
		t.Helper()
		encoded, err := json.Marshal(actions.GetSettings())
		if err != nil {
			t.Fatal(err)
		}
		var want, decoded any
		if err := json.Unmarshal(encoded, &want); err != nil {
			t.Fatal(err)
		}
		got := settingsJSON()
		if err := json.Unmarshal(got, &decoded); err != nil || !reflect.DeepEqual(decoded, want) {
			t.Fatalf("%s: getSettingsJSON\n got %s\nwant %s (%v)", step, got, encoded, err)
		}
		if pushed := bridge.Snapshot(nil, 0, false).Settings; !bytes.Equal(pushed, got) {
			t.Fatalf("%s: state push settings\n got %s\nwant %s", step, pushed, got)
		}
	}
	check("no Session")

	t.Setenv("PIG_HOME", t.TempDir())
	t.Setenv("PIG_CODING_AGENT_DIR", t.TempDir())
	t.Setenv("PI_CODING_AGENT_DIR", t.TempDir())
	services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	live, err = coding.NewSession(services, coding.SessionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := live.Close(); err != nil {
			t.Error(err)
		}
	})
	check("live Session")
	if err := services.SettingsManager().SetDefaultModel("written <&> model"); err != nil {
		t.Fatal(err)
	}
	check("after a settings write")
	if !bytes.Contains(settingsJSON(), []byte(`"written <&> model"`)) {
		t.Fatalf("getSettingsJSON misses the write: %s", settingsJSON())
	}
}
