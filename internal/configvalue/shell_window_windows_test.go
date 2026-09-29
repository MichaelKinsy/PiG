//go:build windows

package configvalue

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/shellconfig"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// TestMain lets the test binary stand in for the shell and report how it was
// started.
func TestMain(m *testing.M) {
	testenv.ReportStartupIfRequested()
	os.Exit(m.Run())
}

// Pi's executeWithConfiguredShell (core/resolve-config-value.ts) runs
// spawnSync(shell, [...args, command]) with stdio ["ignore", "pipe",
// "ignore"] (["pipe", "pipe", "ignore"] for the stdin transport) and
// windowsHide: true, so libuv starts the shell with SW_HIDE and
// CREATE_NO_WINDOW. Node runs that call with the test binary as the shell;
// PiG's runConfiguredShell runs the same shell.
func TestRunConfiguredShellHidesTheWindowAsPiDoes(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(testenv.StartupHelper, "1")
	transports := []string{"", "stdin"}
	input, err := json.Marshal(map[string]any{"shell": exe, "transports": transports})
	if err != nil {
		t.Fatal(err)
	}
	node := exec.CommandContext(t.Context(), "node", "-e", `
const { spawnSync } = require("node:child_process");
const { shell, transports } = JSON.parse(require("node:fs").readFileSync(0, "utf8"));
const reports = [];
for (const transport of transports) {
	const commandFromStdin = transport === "stdin";
	const command = "report";
	const result = spawnSync(shell, commandFromStdin ? [] : [command], {
		encoding: "utf-8",
		input: commandFromStdin ? command : undefined,
		timeout: 10000,
		stdio: [commandFromStdin ? "pipe" : "ignore", "pipe", "ignore"],
		shell: false,
		windowsHide: true,
	});
	if (result.error || result.status !== 0) throw result.error ?? new Error("status " + result.status);
	reports.push(JSON.parse(result.stdout));
}
process.stdout.write(JSON.stringify(reports));
`)
	node.Stdin = bytes.NewReader(input)
	out, err := node.Output()
	if err != nil {
		t.Fatalf("node spawnSync: %v; output %s", err, out)
	}
	var want []testenv.Startup
	if err := json.Unmarshal(out, &want); err != nil || len(want) != len(transports) {
		t.Fatalf("decode node reports %q: %v", out, err)
	}
	previous := getShellConfig
	t.Cleanup(func() { getShellConfig = previous })
	for i, transport := range transports {
		getShellConfig = func() (shellconfig.Config, error) {
			return shellconfig.Config{Path: exe, CommandTransport: transport}, nil
		}
		value, executed := runConfiguredShell(t.Context(), "report")
		if !executed {
			t.Fatalf("transport %q: shell did not run", transport)
		}
		var got testenv.Startup
		if err := json.Unmarshal([]byte(value), &got); err != nil {
			t.Fatalf("transport %q: decode %q: %v", transport, value, err)
		}
		if got != want[i] {
			t.Errorf("transport %q: shell started with %+v, want Pi's %+v", transport, got, want[i])
		}
	}
}
