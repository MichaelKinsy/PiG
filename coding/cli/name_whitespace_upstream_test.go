package cli

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func runNameWhitespaceCLI(t *testing.T, bin, name string, extra ...string) (string, string, int) {
	t.Helper()
	root := t.TempDir()
	agentDir := filepath.Join(root, "agent")
	project := filepath.Join(root, "project")
	if err := os.MkdirAll(agentDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(project, 0o700); err != nil {
		t.Fatal(err)
	}
	args := append([]string{"--name", name, "--no-extensions", "--offline"}, extra...)
	command := exec.CommandContext(t.Context(), bin, args...)
	command.Dir = project
	command.Env = append(os.Environ(), "HOME="+root, "PIG_HOME="+root, "PIG_CODING_AGENT_DIR="+agentDir, "PI_CODING_AGENT_DIR="+agentDir, "PIG_USE_PI_DIRS=", "PIG_OFFLINE=1", "PI_OFFLINE=1", "PIG_TEST_FAUX=1", "PIG_TEST_FAUX_SCENARIO=parity-basic", "FORCE_COLOR=0", "NO_COLOR=1")
	command.Stdin = strings.NewReader("{\"type\":\"get_state\",\"id\":\"name-state\"}\n")
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	code := 0
	if err := command.Run(); err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			t.Fatal(err)
		}
		code = exit.ExitCode()
	}
	return stdout.String(), stderr.String(), code
}

// upstream: packages/coding-agent/src/cli/args.ts:parseArgs retains a present empty --name argument for validation in main.
// Pi: packages/coding-agent/src/cli/args.ts:25 (Args.name).
func TestCLINameArgumentPresenceUpstream(t *testing.T) {
	if flags := parseArgs(nil); flags.NameSet {
		t.Fatal("absent --name marked present")
	} else if name, err := sessionNameFromFlags(flags); name != "" || err != nil {
		t.Fatalf("absent --name: name=%q err=%v", name, err)
	}
	for _, option := range []string{"--name", "-n"} {
		for _, tc := range []struct {
			raw, want string
			invalid   bool
		}{
			{"", "", true}, {"\ufeff", "", true}, {"\u0085", "\u0085", false}, {"  Named  ", "Named", false},
		} {
			flags := parseArgs([]string{option, tc.raw})
			if !flags.NameSet || flags.Name != tc.raw || len(flags.Diagnostics) != 0 {
				t.Fatalf("%s %q: %#v", option, tc.raw, flags)
			}
			name, err := sessionNameFromFlags(flags)
			if name != tc.want || (err != nil) != tc.invalid {
				t.Fatalf("%s %q: name=%q err=%v", option, tc.raw, name, err)
			}
		}
	}
}

// upstream: packages/coding-agent/src/cli/args.ts:66-69 and main.ts:694-701 use String.prototype.trim, not Unicode White_Space.
func TestCLINameUsesJavaScriptWhitespaceUpstream(t *testing.T) {
	bin := buildPigBinaryForSignalTest(t)
	for _, tc := range []struct {
		label, name string
		rejected    bool
	}{
		{"empty", "", true},
		{"ASCII spaces", " \t\r\n", true},
		{"non-breaking space", "\u00a0", true},
		{"BOM", "\ufeff", true},
		{"BOM with spaces", " \ufeff\t", true},
		{"NEL", "\u0085", false},
		{"NEL with spaces", " \u0085 ", false},
		{"zero-width space", "\u200b", false},
		{"ordinary name", "  Named Session  ", false},
	} {
		t.Run(tc.label, func(t *testing.T) {
			_, stderr, code := runNameWhitespaceCLI(t, bin, tc.name, "--help")
			if tc.rejected {
				if code != 1 || !strings.Contains(stderr, "Error: --name requires a non-empty value") {
					t.Fatalf("name=%q: exit=%d stderr=%q; want empty-name rejection", tc.name, code, stderr)
				}
			} else if code != 0 {
				t.Fatalf("name=%q: exit=%d stderr=%q; want accepted name", tc.name, code, stderr)
			}
		})
	}
}

// upstream: packages/coding-agent/src/main.ts:619-635 exits for version/export before normalizing a Session name.
func TestCLINameEarlyExitPrecedenceUpstream(t *testing.T) {
	bin := buildPigBinaryForSignalTest(t)
	for _, name := range []string{"", "   ", "\ufeff"} {
		t.Run(name+"/version", func(t *testing.T) {
			stdout, stderr, code := runNameWhitespaceCLI(t, bin, name, "--version")
			if code != 0 || stdout != cliVersionString()+"\n" {
				t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout, stderr)
			}
		})
		t.Run(name+"/selection", func(t *testing.T) {
			_, stderr, code := runNameWhitespaceCLI(t, bin, name, "--session", "notfound", "--model", "test-faux/faux-1", "-p", "hi")
			if code != 1 || !strings.Contains(stderr, "notfound") || strings.Contains(stderr, "--name requires") {
				t.Fatalf("Session selection did not own the failure: exit=%d stderr=%q", code, stderr)
			}
		})
		t.Run(name+"/export with help", func(t *testing.T) {
			_, stderr, code := runNameWhitespaceCLI(t, bin, name, "--export", "missing-session.jsonl", "--help")
			if code != 1 || !strings.Contains(stderr, "missing-session.jsonl") || strings.Contains(stderr, "--name requires") {
				t.Fatalf("export did not precede help/name: exit=%d stderr=%q", code, stderr)
			}
		})
		t.Run(name+"/export", func(t *testing.T) {
			_, stderr, code := runNameWhitespaceCLI(t, bin, name, "--export", "missing-session.jsonl")
			if code != 1 || !strings.Contains(stderr, "missing-session.jsonl") || strings.Contains(stderr, "--name requires") {
				t.Fatalf("export did not own the failure: exit=%d stderr=%q", code, stderr)
			}
		})
	}
}

// The accepted CLI value must reach Session metadata unchanged after JS trimming; this checks the real CLI -> Session -> RPC state path.
func TestCLINameWhitespaceReachesSessionUpstream(t *testing.T) {
	bin := buildPigBinaryForSignalTest(t)
	for _, tc := range []struct{ name, want string }{
		{" \u0085 ", "\u0085"},
		{"\ufeff Named Session \ufeff", "Named Session"},
	} {
		t.Run(tc.want, func(t *testing.T) {
			stdout, stderr, code := runNameWhitespaceCLI(t, bin, tc.name, "--mode", "rpc", "--model", "test-faux/faux-1")
			if code != 0 {
				t.Fatalf("exit=%d stderr=%q", code, stderr)
			}
			scanner := bufio.NewScanner(strings.NewReader(stdout))
			found := false
			for scanner.Scan() {
				var response struct {
					ID      string `json:"id"`
					Success bool   `json:"success"`
					Data    struct {
						SessionName string `json:"sessionName"`
					} `json:"data"`
				}
				if err := json.Unmarshal(scanner.Bytes(), &response); err != nil {
					t.Fatal(err)
				}
				if response.ID != "name-state" {
					continue
				}
				found = true
				if !response.Success || response.Data.SessionName != tc.want {
					t.Errorf("state=%s, want sessionName=%q", scanner.Text(), tc.want)
				}
			}
			if err := scanner.Err(); err != nil {
				t.Fatal(err)
			}
			if !found {
				t.Fatalf("missing get_state response: %s; stderr=%s", stdout, stderr)
			}
		})
	}
}
