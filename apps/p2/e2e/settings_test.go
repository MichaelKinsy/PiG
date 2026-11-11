// Ports test/p2/settings.test.ts (Pi main 57060fb5): the Settings sheet's contract (plugins/settings): settings.json
// changed by some of its top-level settings, owner only. (The sheet itself, ui-settings.test.ts, is a browser test.)

package e2e_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/apps/p2/hosts"
	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	settingscontract "github.com/MichaelKinsy/PiG/apps/p2/plugins/settings/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"

	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/models"
	_ "github.com/MichaelKinsy/PiG/apps/p2/plugins/settings"
)

// asMap is value as the plain JSON a test compares (key order does not matter, as in Pi's deepEqual).
func asMap(t *testing.T, value any) map[string]any {
	t.Helper()
	text, err := chordjson.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(text, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestSettingsSetsAndRemovesTopLevelSettingsKeepsTheOthersAndRefusesInvalidOnes(t *testing.T) {
	fixture := p2test.StartFixture(t, hosts.ServerOptions{})
	file := filepath.Join(fixture.DataDir, "settings.json")
	read := func() map[string]any {
		t.Helper()
		text, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		var out map[string]any
		if err := json.Unmarshal(text, &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	write := func(value map[string]any) {
		t.Helper()
		text, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, text, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(map[string]any{"detail": 2, "ownerName": "Mario", "thinkingLevel": "low"})
	ref, err := p2test.HoldIn(fixture.Browser(), settingscontract.Settings, delta.NewJsonObject(0), "owner")
	if err != nil {
		t.Fatal(err)
	}
	// The file written before the hold may still be loading.
	if _, err := p2test.Until(ref.Member("settings"), func(value delta.JsonValue) bool {
		object, _ := value.(*delta.JsonObject)
		return object != nil && object.Value("ownerName") == "Mario"
	}, 0); err != nil {
		t.Fatal(err)
	}
	if value, _ := ref.Member("error").Value(); value != nil {
		t.Fatalf("error = %v", value)
	}

	// maxRetries is undefined in Pi's call, which the wire drops.
	if _, err := ref.Call("update", delta.JsonObjectOf(
		"set", delta.JsonObjectOf("detail", float64(4), "stream", delta.JsonObjectOf("transport", "sse", "timeoutMs", float64(1000))),
		"unset", []any{"ownerName"},
	), p2test.Background); err != nil {
		t.Fatal(err)
	}
	// The answer follows its state: the page holds the saved settings when the call resolves.
	want := map[string]any{"detail": 4.0, "thinkingLevel": "low", "stream": map[string]any{"transport": "sse", "timeoutMs": 1000.0}}
	held, _ := ref.Member("settings").Value()
	if got := asMap(t, held); !reflect.DeepEqual(got, want) {
		t.Fatalf("settings = %v, want %v", got, want)
	}
	if got := read(); !reflect.DeepEqual(got, want) {
		t.Fatalf("file = %v, want %v", got, want)
	}

	// The server merges into the file as it is now, not into the page's copy.
	current := read()
	current["codemode"] = false
	write(current)
	if _, err := ref.Call("update", delta.JsonObjectOf("set", delta.JsonObjectOf("cacheWarming", "off"), "unset", []any{"stream"}), p2test.Background); err != nil {
		t.Fatal(err)
	}
	merged := map[string]any{"detail": 4.0, "thinkingLevel": "low", "codemode": false, "cacheWarming": "off"}
	if got := read(); !reflect.DeepEqual(got, merged) {
		t.Fatalf("file = %v, want %v", got, merged)
	}

	for _, set := range []any{
		delta.JsonObjectOf("detail", float64(9)),
		delta.JsonObjectOf("unknownSetting", true),
		delta.JsonObjectOf("ownerName", "<script>"),
		"detail",
	} {
		if _, err := ref.Call("update", delta.JsonObjectOf("set", set, "unset", []any{}), p2test.Background); err == nil {
			t.Errorf("refuses %v", set)
		}
	}
	if got := read(); !reflect.DeepEqual(got, merged) {
		t.Fatalf("file = %v after refused updates, want %v", got, merged)
	}

	guest := fixture.As(p2runtime.GuestPrincipal{ID: "g1", Name: "Guest", Home: "s1", Sessions: map[string]p2runtime.GuestMode{"s1": "full"}, Revision: 1})
	denied, err := guest.Hold(p2test.Background, settingscontract.Settings, delta.NewJsonObject(0), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	p2test.ExpectChord(t, p2runtime.WhenLive(p2test.Background, denied), data.CodeForbidden)
	denied.Release()
}
