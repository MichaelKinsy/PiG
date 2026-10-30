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
	cmd := exec.CommandContext(t.Context(), "node", "testdata/builtin_slash_commands.mjs", pigversion.UpstreamPackageVersion)
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
		// pig divergence (D217) allowance, Pi 2a46c420 (#11532): PiG describes /import and /resume with Pi's newer wording; TestBuiltinSlashCommandsCarryPi2a46c420Descriptions pins it.
		switch want.Name {
		case "import":
			want.Description = "Import a JSONL session, preserving its ID and working directory"
		case "resume":
			want.Description = "Resume another session"
		}
		if command.Description != want.Description || command.ArgumentHint != want.ArgumentHint {
			t.Errorf("/%s: PiG %q hint %q, Pi %q hint %q", want.Name, command.Description, command.ArgumentHint, want.Description, want.ArgumentHint)
		}
	}
}

// Pi 2a46c420 (#11532): /import keeps the session ID and working directory, /resume resumes another session. Pinned Pi still has "Import and resume a session from a JSONL file" and "Resume a different session"; TestBuiltinSlashCommandsMatchPi accepts the newer text under D217.
func TestBuiltinSlashCommandsCarryPi2a46c420Descriptions(t *testing.T) {
	want := map[string]string{
		"import": "Import a JSONL session, preserving its ID and working directory",
		"resume": "Resume another session",
	}
	for _, command := range BuiltinSlashCommands() {
		if description, ok := want[command.Name]; ok {
			if command.Description != description {
				t.Errorf("/%s description = %q, want %q", command.Name, command.Description, description)
			}
			delete(want, command.Name)
		}
	}
	for name := range want {
		t.Errorf("/%s is not a built-in command", name)
	}
}
