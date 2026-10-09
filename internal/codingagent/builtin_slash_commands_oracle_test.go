package codingagent

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

// core/slash-commands.ts BUILTIN_SLASH_COMMANDS against pinned Pi: the same commands in the same order with the same descriptions and
// argument hints, which /help and the autocomplete popup show. /share (D64) and /bug (D62) differ on purpose.
func TestBuiltinSlashCommandsMatchPi(t *testing.T) {
	cmd := exec.CommandContext(t.Context(), "node", "testdata/builtin_slash_commands.mjs", pigversion.UpstreamVersion)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected []struct{ Name, Description, ArgumentHint string }
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	divergent := map[string]bool{"share": true, "bug": true}
	var got []BuiltinSlashCommand
	for _, command := range BuiltinSlashCommands() {
		if !command.Hidden {
			got = append(got, command)
		}
	}
	for len(got) > 0 && len(got[len(got)-1].Name) > 6 && got[len(got)-1].Name[:6] == "probe-" {
		got = got[:len(got)-1]
	}
	if len(got) != len(expected) {
		t.Fatalf("%d visible commands, Pi has %d", len(got), len(expected))
	}
	for i, want := range expected {
		command := got[i]
		if command.Name != want.Name {
			t.Errorf("command %d = /%s, Pi has /%s", i, command.Name, want.Name)
			continue
		}
		if divergent[want.Name] {
			if command.ArgumentHint != want.ArgumentHint && want.Name == "bug" {
				t.Errorf("/bug argument hint %q, Pi %q", command.ArgumentHint, want.ArgumentHint)
			}
			continue
		}
		if want.Name == "quit" {
			// Pi's description is `Quit ${APP_NAME}`; PiG's application name is AppName.
			want.Description = "Quit " + AppName
		}
		if command.Description != want.Description || command.ArgumentHint != want.ArgumentHint {
			t.Errorf("/%s: PiG %q hint %q, Pi %q hint %q", want.Name, command.Description, command.ArgumentHint, want.Description, want.ArgumentHint)
		}
	}
}
