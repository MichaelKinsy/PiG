package host_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/apps/p2/tui/host"
)

func jsonOf(t *testing.T, text string) any {
	t.Helper()
	var value any
	if err := json.Unmarshal([]byte(text), &value); err != nil {
		t.Fatalf("%s: %v", text, err)
	}
	return value
}

func TestSavePrefsPatchesPrivilegedBindingsTheEditorCommandAndLauncherOnlyKeysAreRefused(t *testing.T) {
	refused := func(patch string) bool {
		_, problem := host.ValidatePrefsPatch(jsonOf(t, patch))
		return problem != ""
	}
	for _, patch := range []string{
		`{"keybindings":{"app.clipboard.pasteImage":"ctrl+y"}}`, `{"keybindings":{"app.editor.external":null}}`, `{"keybindings":{"p2.attach":"f5"}}`,
		`{"settings":{"externalEditor":"vim"}}`, `{"externalEditor":"vim"}`, `{"mouse":"off"}`, `{"downloads":"/tmp"}`,
		`{"keybindings":{"app.model.select":"hyper+q"}}`, `{"settings":{"editorPaddingX":9}}`, `{"theme":"a/b/c"}`, `"nope"`,
	} {
		if !refused(patch) {
			t.Fatalf("%s was accepted", patch)
		}
	}
	ok, problem := host.ValidatePrefsPatch(jsonOf(t, `{"keybindings":{"app.model.select":"ctrl+k","app.tools.expand":null,"p2.palette":[]},"theme":"light","settings":{"hideThinkingBlock":true,"terminal":{"showImages":false,"imageWidthCells":null}}}`))
	if problem != "" {
		t.Fatalf("valid patch refused: %s", problem)
	}
	before := jsonOf(t, `{"keybindings":{"app.tools.expand":"ctrl+e"},"settings":{"terminal":{"imageWidthCells":80}},"mouse":"on","externalEditor":"vim"}`).(map[string]any)
	after := host.ApplyPrefsPatch(before, ok)
	want := jsonOf(t, `{"keybindings":{"app.model.select":["ctrl+k"],"p2.palette":[]},"settings":{"terminal":{"showImages":false},"hideThinkingBlock":true},"mouse":"on","externalEditor":"vim","theme":"light"}`)
	if !reflect.DeepEqual(any(after), want) {
		t.Fatalf("after = %v", after)
	}
	if got := host.PrefsDiff(map[string]any{}, map[string]any{"theme": "x"}); !reflect.DeepEqual(got, []string{`+ theme: "x"`}) {
		t.Fatalf("a new file shows only what is added: %v", got)
	}
	wantDiff := []string{
		`+ keybindings.app.model.select: "ctrl+k"`, `- keybindings.app.tools.expand: "ctrl+e"`, "+ keybindings.p2.palette: []",
		"+ settings.hideThinkingBlock: true", "- settings.terminal.imageWidthCells: 80", "+ settings.terminal.showImages: false", `+ theme: "light"`,
	}
	if got := host.PrefsDiff(before, after); !reflect.DeepEqual(got, wantDiff) {
		t.Fatalf("diff = %q", got)
	}
}

func TestSavePrefsActionTheDiffIsShownFirstTheWriteIsAtomicAndKeepsEverythingElse(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "prefs.json")
	if err := os.WriteFile(path, []byte(`{"mouse":"off","theme":"dark"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	address := mustURL("http://127.0.0.1:1")
	save := host.LauncherActions(host.LauncherActionOptions{URL: address, Role: "guest", PrefsDirectory: directory})["savePrefs"]
	if save.Kind != host.GrantC {
		t.Fatal("savePrefs is not a C action")
	}
	ctx := host.ConsentContext{Origin: address}
	patch := func(text string) any { return map[string]any{"patch": jsonOf(t, text)} }
	if save.Consent(patch(`{"keybindings":{"app.message.copy":"ctrl+y"}}`), ctx) != nil {
		t.Fatal("a privileged binding was offered")
	}
	if save.Consent(patch(`{"theme":"dark"}`), ctx) != nil {
		t.Fatal("nothing changes: nothing to ask")
	}
	consent := save.Consent(patch(`{"theme":"light"}`), ctx)
	if consent == nil || consent.Preview != "- theme: \"dark\"\n+ theme: \"light\"" || len(consent.Facts) != 1 || consent.Facts[0] != (host.ConsentFact{Label: "File", Value: path}) {
		t.Fatalf("consent = %+v", consent)
	}
	read := func() map[string]any {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var value map[string]any
		_ = json.Unmarshal(data, &value)
		return value
	}
	if read()["theme"] != "dark" {
		t.Fatal("written before running")
	}
	if _, err := save.Run(patch(`{"theme":"light"}`), host.ActionContext{}); err != nil {
		t.Fatal(err)
	}
	if got := read(); !reflect.DeepEqual(got, map[string]any{"mouse": "off", "theme": "light"}) {
		t.Fatalf("prefs.json = %v", got)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", info.Mode())
	}
	if _, err := save.Run(patch(`{"settings":{"externalEditor":"x"}}`), host.ActionContext{}); err == nil || strings.TrimSpace(err.Error()) == "" {
		t.Fatal("a launcher-only key was saved")
	}
}
