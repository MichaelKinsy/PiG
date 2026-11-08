// Ports test/p2/codemode-setting.test.ts (Pi main c23b9ec1): the `codemode` setting (Pi Mobile's, default on in p2): `false`
// in settings.json uninstalls code mode live from a running worker, the codemode plugin staying loaded and the agent tool
// installed; `true` (or nothing) offers it again. ("Pi Mobile's settings.json with `codemode` validates" is
// runtime/settings_test.go.)

package e2e_test

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/apps/p2/hosts"
	"github.com/MichaelKinsy/PiG/chord/delta"
)

func TestTheCodemodeSettingInstallsAndUninstallsCodeModeLiveTheAgentToolStays(t *testing.T) {
	f := newAgentFixture(t, hosts.ServerOptions{})
	// exchange sends message and waits for the run it starts to end.
	exchange := func(message string) delta.JsonValue {
		t.Helper()
		current, err := f.Chat.Member("view").Value()
		if err != nil {
			t.Fatal(err)
		}
		before := len(list(object(current).Value("entries")))
		f.send(message)
		return f.until(f.Chat.Member("view"), func(view delta.JsonValue) bool {
			return len(list(object(view).Value("entries"))) > before+1 && !running(view)
		}, 30*time.Second)
	}
	// settle sends until the worker applied the settings file (its observer is asynchronous): ready(view) holds.
	settle := func(ready func(delta.JsonValue) bool) delta.JsonValue {
		t.Helper()
		for attempt := 0; attempt < 20; attempt++ {
			view := exchange("probe " + itoa(attempt))
			if ready(view) {
				return view
			}
			time.Sleep(200 * time.Millisecond)
		}
		t.Fatal("the worker never applied the settings change")
		return nil
	}
	last := func(view delta.JsonValue) *delta.JsonObject {
		results := codemodeResults(view)
		if len(results) == 0 {
			return nil
		}
		return results[len(results)-1]
	}
	completed := regexp.MustCompile(`^Script completed`)
	settings := filepath.Join(f.Fixture.DataDir, "settings.json")

	// Default on: scripts run; code mode's section lists the tools scripts call, the agent tool among them.
	view := exchange("codemode")
	if !completed.MatchString(codemodeText(last(view))) {
		t.Fatalf("codemode: %s", codemodeText(last(view)))
	}
	if !strings.Contains(sectionsOf(view)["codemode"], "Run a one-shot subagent in your cwd") {
		t.Error("code mode's section does not list the agent tool")
	}
	if _, has := sectionsOf(view)["agent"]; has {
		t.Error("the agent tool has a section of its own")
	}

	// Off: no tool, no listing; the plugin stays loaded and the agent tool installed (only scripts can call it).
	if err := os.WriteFile(settings, []byte(`{"codemode":false}`), 0o600); err != nil {
		t.Fatal(err)
	}
	settle(func(view delta.JsonValue) bool { _, has := sectionsOf(view)["codemode"]; return !has })
	if !slices.Contains(f.Fixture.Server.Plugins, "codemode") {
		t.Error("the codemode plugin is not loaded")
	}
	view = exchange("codemode")
	refused := last(view)
	if refused == nil || refused.Value("isError") != true || !strings.Contains(codemodeText(refused), "not available") {
		t.Fatalf("refused = %v", codemodeText(refused))
	}

	// On again (an absent setting means on): offered and working, the agent tool listed again.
	if err := os.WriteFile(settings, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	view = settle(func(view delta.JsonValue) bool { _, has := sectionsOf(view)["codemode"]; return has })
	if !strings.Contains(sectionsOf(view)["codemode"], "Run a one-shot subagent in your cwd") {
		t.Error("code mode's section does not list the agent tool again")
	}
	view = exchange("codemode")
	if !completed.MatchString(codemodeText(last(view))) {
		t.Fatalf("codemode again: %s", codemodeText(last(view)))
	}
}
