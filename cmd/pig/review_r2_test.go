package main

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// Print-mode twin of the CH-009 RPC test: a Package extension failure and a
// missing -e path are both reported before the hint.
func TestReviewR2PrintReportsPackageAndCLIExtensionFailuresTogether(t *testing.T) {
	home := t.TempDir()
	agentDir := filepath.Join(home, "agent")
	cwd := filepath.Join(home, "cwd")
	for _, dir := range []string{agentDir, cwd} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	f := writeStartupPackages(t, filepath.Join(home, "packages"), false)
	settings, _ := json.Marshal(map[string]any{"packages": []string{f.mixed, f.healthy}})
	writeStartupFixtureFile(t, filepath.Join(agentDir, "settings.json"), string(settings))
	missing := filepath.Join(home, "missing-ext")
	binary := buildPigBinaryForSignalTest(t)
	cmd := exec.Command(binary, "--model", "test-faux/echo", "--offline", "--no-session", "-e", missing, "--print", "hi")
	cmd.Dir = cwd
	cmd.Env = append(os.Environ(), "HOME="+home, "PIG_HOME="+filepath.Join(home, "pig"), "PIG_CODING_AGENT_DIR="+agentDir, "PIG_TEST_FAUX=1")
	out, err := cmd.CombinedOutput()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
		t.Fatalf("print startup error = %v, want exit 1\n%s", err, out)
	}
	for _, want := range []string{
		`Failed to load extension "` + f.badExtension + `"`,
		`Failed to load extension "` + missing + `": Extension path does not exist: ` + missing,
		`Hint: Start without extensions using "pig -ne".`,
	} {
		if !strings.Contains(string(out), want) {
			t.Errorf("output lacks %q\n%s", want, out)
		}
	}
}

// Upstream resource-loader.ts loadFinalExtensionSet returns
// errors: [...preTrustExtensions.errors, ...remainingExtensions.errors], so a
// user-scope (pre-trust) failure and a trusted project-scope failure are both
// printed. Checked for print and rpc.
func TestReviewR2PreTrustAndProjectFailuresTogether(t *testing.T) {
	binary := buildPigBinaryForSignalTest(t)
	for _, mode := range []string{"print", "rpc"} {
		t.Run(mode, func(t *testing.T) {
			home := t.TempDir()
			agentDir := filepath.Join(home, "agent")
			cwd := filepath.Join(home, "cwd")
			for _, dir := range []string{agentDir, cwd} {
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			f := writeStartupPackages(t, filepath.Join(home, "packages"), false)
			other := filepath.Join(home, "packages", "other")
			otherBad := filepath.Join(other, "extensions", "worse")
			writeStartupFixtureFile(t, filepath.Join(other, "package.json"), `{"name":"other","pi":{"extensions":["extensions/worse"]}}`)
			writeStartupFixtureFile(t, filepath.Join(otherBad, "go.mod"), "module example.com/worse\n\ngo 1.26\n")
			writeStartupFixtureFile(t, filepath.Join(otherBad, "extension.go"), "package worse\n\nfunc NotAFactory() {}\n")
			user, _ := json.Marshal(map[string]any{"packages": []string{f.mixed}})
			writeStartupFixtureFile(t, filepath.Join(agentDir, "settings.json"), string(user))
			project, _ := json.Marshal(map[string]any{"packages": []string{other}})
			writeStartupFixtureFile(t, filepath.Join(cwd, codingagent.CONFIG_DIR_NAME, "settings.json"), string(project))
			trusted := true
			if err := codingagent.NewProjectTrustStore(agentDir).Set(cwd, &trusted); err != nil {
				t.Fatal(err)
			}
			args := []string{"--model", "test-faux/echo", "--offline", "--no-session"}
			if mode == "rpc" {
				args = append(args, "--mode", "rpc")
			} else {
				args = append(args, "--print", "hi")
			}
			cmd := exec.Command(binary, args...)
			cmd.Dir = cwd
			cmd.Stdin = strings.NewReader(`{"id":"commands","type":"get_commands"}` + "\n")
			cmd.Env = append(os.Environ(), "HOME="+home, "PIG_HOME="+filepath.Join(home, "pig"), "PIG_CODING_AGENT_DIR="+agentDir, "PIG_TEST_FAUX=1")
			var stderr strings.Builder
			cmd.Stderr = &stderr
			err := cmd.Run()
			var exitErr *exec.ExitError
			if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
				t.Fatalf("startup error = %v, want exit 1\nstderr:\n%s", err, stderr.String())
			}
			for _, want := range []string{
				`Failed to load extension "` + f.badExtension + `"`,
				`Failed to load extension "` + otherBad + `"`,
			} {
				if !strings.Contains(stderr.String(), want) {
					t.Errorf("stderr lacks %q\nstderr:\n%s", want, stderr.String())
				}
			}
		})
	}
}
