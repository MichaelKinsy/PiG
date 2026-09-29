//go:build parity

package runner

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestTmuxEnvPrefixDropsAmbientAgentHomes(t *testing.T) {
	for _, key := range []string{"PIG_CODING_AGENT_DIR", "PI_CODING_AGENT_DIR", "PIG_HOME", "PI_HOME"} {
		t.Setenv(key, "operator-state")
	}
	command := exec.Command("sh", "-c", tmuxEnvPrefix("")+`printf '%s|%s|%s|%s' "${PIG_CODING_AGENT_DIR-unset}" "${PI_CODING_AGENT_DIR-unset}" "${PIG_HOME-unset}" "${PI_HOME-unset}"`)
	out, err := command.CombinedOutput()
	if err != nil || string(out) != "unset|unset|unset|unset" {
		t.Fatalf("terminal base environment = %q, error = %v", out, err)
	}
}

func TestHeadlessHomeOverridesKeepExplicitFixtureHomes(t *testing.T) {
	t.Setenv("PIG_CODING_AGENT_DIR", "operator-state")
	command := exec.Command("sh", "-c", `printf '%s|%s|%s' "${PIG_CODING_AGENT_DIR:-unset}" "$PIG_HOME" "$PI_CODING_AGENT_DIR"`)
	command.Env = append(os.Environ(), clearedAgentHomeEnv()...)
	command.Env = append(command.Env, "PIG_HOME=fixture-home", "PI_CODING_AGENT_DIR=fixture-agent")
	out, err := command.CombinedOutput()
	if err != nil || string(out) != "unset|fixture-home|fixture-agent" {
		t.Fatalf("headless environment = %q, error = %v", out, err)
	}
}

// Worker-level config overrides must not outrank a scenario's snapshotted home.
// Scenario overrides are appended explicitly by every driver after this base.
func TestHermeticEnvironDropsAmbientAgentHomes(t *testing.T) {
	keys := []string{"PIG_CODING_AGENT_DIR", "PI_CODING_AGENT_DIR", "PIG_HOME", "PI_HOME"}
	for _, key := range keys {
		t.Setenv(key, "operator-state-must-not-be-read")
	}
	t.Setenv("HARNESS_UNRELATED_ENV", "preserved")
	env := hermeticEnviron()
	for _, key := range keys {
		for _, entry := range env {
			if strings.HasPrefix(entry, key+"=") {
				t.Errorf("ambient %s escaped into driver environment", key)
			}
		}
	}
	found := false
	for _, entry := range env {
		if entry == "HARNESS_UNRELATED_ENV=preserved" {
			found = true
		}
	}
	if !found {
		t.Fatal("unrelated environment was stripped")
	}
}

// literalHostSignalKeys names the ambient host signals independently of the production key
// constants, so deleting a key from those constants fails these tests.
var literalHostSignalKeys = strings.Fields("WT_SESSION WSL_DISTRO_NAME WSL_INTEROP WSLENV SSH_CONNECTION SSH_CLIENT SSH_TTY " +
	"KITTY_WINDOW_ID GHOSTTY_RESOURCES_DIR ITERM_SESSION_ID WEZTERM_PANE WARP_SESSION_ID WARP_TERMINAL_SESSION_UUID TERMINAL_EMULATOR TERMUX_VERSION MOSH_CONNECTION COLORFGBG " +
	"HERDR_ENV HERDR_KITTY_GRAPHICS HERDR_PANE_ID HERDR_SOCKET_PATH HERDR_TAB_ID HERDR_WORKSPACE_ID ZELLIJ STY FORCE_COLOR TF_BUILD AGENT_NAME CI TEAMCITY_VERSION")

// literalPresenceKeys are presence-tested by Pi and PiG and must never be assigned empty.
var literalPresenceKeys = strings.Fields("ZELLIJ STY FORCE_COLOR TF_BUILD AGENT_NAME CI TEAMCITY_VERSION")

func TestHermeticEnvironDropsAmbientHostTerminalSignals(t *testing.T) {
	for _, key := range literalHostSignalKeys {
		t.Setenv(key, "host-value")
	}
	for _, entry := range hermeticEnviron() {
		key, _, _ := strings.Cut(entry, "=")
		if slices.Contains(literalHostSignalKeys, key) {
			t.Errorf("ambient %s escaped into driver environment", key)
		}
	}
}

// Windows environment names are case-insensitive: Node process.env resolves CI, WT_SESSION and
// FORCE_COLOR from a host variable spelled Ci, Wt_Session or Force_Color, so the scrub must fold.
func TestHermeticEnvironFromFoldsWindowsKeyCase(t *testing.T) {
	var environ []string
	for _, key := range literalHostSignalKeys {
		environ = append(environ, strings.ToUpper(key[:1])+strings.ToLower(key[1:])+"=host-value")
	}
	environ = append(environ, "Https_Proxy=proxy", "Pig_Home=operator-state", "Colorterm=24bit", "Harness_Unrelated=kept")

	got := hermeticEnvironFrom(environ, true)
	want := []string{"Colorterm=truecolor", "Harness_Unrelated=kept"}
	if !slices.Equal(got, want) {
		t.Fatalf("Windows scrub = %q, want %q", got, want)
	}

	// Elsewhere names are case-sensitive, so a differently cased variable is a different variable.
	got = hermeticEnvironFrom([]string{"Ci=1", "CI=1", "Colorterm=24bit"}, false)
	want = []string{"Ci=1", "Colorterm=24bit", "COLORTERM=truecolor"}
	if !slices.Equal(got, want) {
		t.Fatalf("POSIX scrub = %q, want %q", got, want)
	}
}

func TestTmuxEnvPrefixDropsAmbientHostTerminalSignals(t *testing.T) {
	for _, key := range literalHostSignalKeys {
		t.Setenv(key, "host-value")
	}
	names := strings.Join(literalHostSignalKeys, `|`)
	script := tmuxEnvPrefix("") + `env | grep -E '^(` + names + `)=' || true`
	out, err := exec.Command("sh", "-c", script).CombinedOutput()
	if err != nil || len(out) != 0 {
		t.Fatalf("host terminal signals reached the pane shell: %q, error = %v", out, err)
	}
}

func TestHTEnvPairsClearHostSignalsBeforeScenarioEnv(t *testing.T) {
	pairs := htEnvPairs([]string{"BINARY_KEY=binary"}, []string{"WT_SESSION=scenario-value"})
	for _, key := range literalHostSignalKeys {
		if slices.Contains(literalPresenceKeys, key) {
			continue
		}
		if !slices.Contains(pairs, key+"=") {
			t.Errorf("%s is not cleared in the ht environment", key)
		}
	}
	for _, key := range literalPresenceKeys {
		for _, pair := range pairs {
			if strings.HasPrefix(pair, key+"=") {
				t.Errorf("%s is presence-tested upstream and must not be assigned: %q", key, pair)
			}
		}
	}
	cleared := slices.Index(pairs, "WT_SESSION=")
	scenario := slices.Index(pairs, "WT_SESSION=scenario-value")
	if cleared < 0 || scenario < cleared {
		t.Fatalf("scenario env must follow the scrub: clear at %d, scenario at %d in %q", cleared, scenario, pairs)
	}
	if slices.Index(pairs, "BINARY_KEY=binary") < cleared {
		t.Fatalf("binary env must follow the scrub: %q", pairs)
	}
}

func TestHTLaunchArgsRemovePresenceTestedSignals(t *testing.T) {
	args := htLaunchArgs("/usr/bin/env", "/bin/pi", nil, nil, []string{"--flag"})
	if len(args) < 2 || args[0] != "/usr/bin/env" {
		t.Fatalf("ht child must start through the resolved env executable: %q", args)
	}
	binary := slices.Index(args, "/bin/pi")
	if binary < 0 || !slices.Equal(args[binary:], []string{"/bin/pi", "--flag"}) {
		t.Fatalf("binary and args must end the command: %q", args)
	}
	for _, key := range append([]string{"TMUX", "TMUX_PANE"}, literalPresenceKeys...) {
		i := slices.Index(args, key)
		if i < 1 || args[i-1] != "-u" || i > binary {
			t.Errorf("%s is presence-tested upstream and must be removed with env -u: %q", key, args)
		}
	}
	for _, key := range []string{"TMUX", "TMUX_PANE"} {
		if slices.Contains(htEnvPairs(nil, nil), key+"=") {
			t.Errorf("%s must be removed, not emptied", key)
		}
	}
}

func TestHTLaunchArgsKeepSignalsTheScenarioSets(t *testing.T) {
	args := htLaunchArgs("/usr/bin/env", "/bin/pi", []string{"CI=binary"}, []string{"FORCE_COLOR=3", "ZELLIJ=x"}, []string{"--flag"})
	for _, key := range []string{"CI", "FORCE_COLOR", "ZELLIJ"} {
		if slices.Contains(args, key) {
			t.Errorf("%s is set by the binary or scenario environment and must not be removed: %q", key, args)
		}
	}
	for _, key := range []string{"TMUX", "TMUX_PANE", "STY", "TF_BUILD", "AGENT_NAME", "TEAMCITY_VERSION"} {
		i := slices.Index(args, key)
		if i < 1 || args[i-1] != "-u" {
			t.Errorf("%s must still be removed: %q", key, args)
		}
	}
}

func TestExplicitScenarioEnvOverridesHostTerminalScrub(t *testing.T) {
	t.Setenv("WT_SESSION", "host-value")
	t.Run("hermetic", func(t *testing.T) {
		command := exec.Command("sh", "-c", `printf '%s' "$WT_SESSION"`)
		command.Env = append(hermeticEnviron(), "WT_SESSION=scenario-value")
		if out, err := command.CombinedOutput(); err != nil || string(out) != "scenario-value" {
			t.Fatalf("WT_SESSION = %q, error = %v", out, err)
		}
	})
	t.Run("tmux", func(t *testing.T) {
		out, err := exec.Command("sh", "-c", tmuxEnvPrefix("")+`WT_SESSION=scenario-value sh -c 'printf %s "$WT_SESSION"'`).CombinedOutput()
		if err != nil || string(out) != "scenario-value" {
			t.Fatalf("WT_SESSION = %q, error = %v", out, err)
		}
	})
}

// writeFakeEnv puts an executable named env in a new directory and returns its path. A Windows
// PATH lookup finds only names with a PATHEXT extension.
func writeFakeEnv(t *testing.T) string {
	t.Helper()
	name := "env"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestHTEnvExecutableIsAbsolute(t *testing.T) {
	want := writeFakeEnv(t)
	t.Setenv("PATH", filepath.Dir(want))
	got, err := htEnvExecutable()
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("htEnvExecutable() = %q, want the absolute %q: ht resolves Cmd[0] in the daemon PATH, so a bare name can fail", got, want)
	}
	t.Setenv("PATH", "")
	if got, err := htEnvExecutable(); err == nil {
		t.Fatalf("htEnvExecutable() = %q with empty PATH, want an error", got)
	}
}

// Run must launch the child through the resolved env executable and keep a presence key the
// scenario assigns. The helpers above do not prove that Run passes them the right inputs.
func TestHTDriverRunLaunchesThroughResolvedEnv(t *testing.T) {
	installFakeHT(t)
	envPath := writeFakeEnv(t)
	t.Setenv("PATH", filepath.Dir(envPath)+string(os.PathListSeparator)+os.Getenv("PATH"))
	cwd := t.TempDir()
	scenario := &Scenario{
		Name: "ht-launch-wiring", SourcePath: filepath.Join(cwd, "scenario.toml"),
		Env:  EnvOverrides{Pig: []string{"FORCE_COLOR=3", "WT_SESSION=scenario-value"}},
		Tmux: TmuxDriverConfig{CWD: cwd},
	}
	bin := BinaryRef{Label: "pig", Path: "/abs/pig", Args: []string{"--flag"}}
	// The fake ht echoes its arguments, which are not the session JSON, so Run reports them.
	result := (htDriver{}).Run(t.Context(), t, bin, scenario)
	const prefix = "ht run: unexpected output: "
	if result.Err == nil || !strings.HasPrefix(result.Err.Error(), prefix) {
		t.Fatalf("Run error = %v, want the echoed ht arguments", result.Err)
	}
	// The fake ht joins its arguments with spaces, so compare text: a temp path may contain a space.
	echoed := strings.TrimSuffix(strings.TrimPrefix(result.Err.Error(), prefix), "\n")
	htFlags, child, ok := strings.Cut(echoed, " "+envPath+" ")
	if !ok {
		t.Fatalf("ht must start the resolved %q: %q", envPath, echoed)
	}
	for _, pair := range []string{"FORCE_COLOR=3", "WT_SESSION=scenario-value"} {
		if !strings.Contains(htFlags, " --env "+pair+" ") {
			t.Errorf("scenario pair %s is not passed to ht: %q", pair, htFlags)
		}
	}
	// FORCE_COLOR is assigned by the scenario, so env keeps it; every other presence key goes.
	const want = "-u TMUX -u TMUX_PANE -u ZELLIJ -u STY -u TF_BUILD -u AGENT_NAME -u CI -u TEAMCITY_VERSION /abs/pig --flag"
	if child != want || !strings.Contains(htFlags, " --cwd ") {
		t.Errorf("child command after %s = %q, want %q after ht's --cwd", envPath, child, want)
	}
}
