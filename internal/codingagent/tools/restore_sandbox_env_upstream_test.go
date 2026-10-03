package tools

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
	"github.com/MichaelKinsy/PiG/internal/nodespawn"
)

// Equivalence port of packages/coding-agent/test/restore-sandbox-env.test.ts (Pi 1.0.0). Pi's restoreSandboxEnv
// repairs a Bun sandbox start whose process.env is empty although the kernel passed an environment, by reading
// /proc/self/environ; without it every child process (bash tool, extensions) would start with no environment. PiG
// has the same concern: the environment it hands to children ({...process.env}, nodespawn.ProcessEnv, and the bash
// tool's GetShellEnv) must be the environment the kernel passed. Go reads that block directly at startup, so the
// invariant is that PiG's child environment equals /proc/self/environ in every start: an ordinary one, one with
// added entries, and the minimal sandbox start Pi's third case simulates. The bash tool's GetShellEnv(<agentDir>/bin)
// changes only PATH, as Pi's getShellEnv does; an empty start gains only that PATH and nothing else is fabricated.

// shellEnvProbeArg makes the tools test binary report its child environment instead of running tests.
const shellEnvProbeArg = "pig-restore-sandbox-env-probe"

// shellEnvProbeBinDir stands in for <agentDir>/bin, which every production bash tool passes to GetShellEnv
// (coding/session_tool_registry.go, internal/codingagent/reload_resources.go).
const shellEnvProbeBinDir = "/pig-restore-sandbox-env/agent/bin"

type shellEnvReport struct {
	ProcessEnv      []string `json:"processEnv"`
	ShellEnv        []string `json:"shellEnv"`
	ShellEnvWithBin []string `json:"shellEnvWithBin"`
	Kernel          []string `json:"kernel"`
	HasKernel       bool     `json:"hasKernel"`
}

// reportShellEnvIfRequested runs before TestMain changes the environment (RunScoped sets TMPDIR).
func reportShellEnvIfRequested() {
	if len(os.Args) != 2 || os.Args[1] != shellEnvProbeArg {
		return
	}
	report := shellEnvReport{ProcessEnv: nodespawn.ProcessEnv(), ShellEnv: GetShellEnv(""), ShellEnvWithBin: GetShellEnv(shellEnvProbeBinDir)}
	if data, err := os.ReadFile("/proc/self/environ"); err == nil {
		report.HasKernel = true
		report.Kernel = []string{}
		for entry := range strings.SplitSeq(string(data), "\x00") {
			if entry != "" {
				report.Kernel = append(report.Kernel, entry)
			}
		}
	}
	if json.NewEncoder(os.Stdout).Encode(report) != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

func runShellEnvProbe(t *testing.T, env []string) shellEnvReport {
	t.Helper()
	cmd := exec.Command(os.Args[0], shellEnvProbeArg)
	cmd.Env = env
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("probe: %v\n%s", err, output)
	}
	var report shellEnvReport
	if err := json.Unmarshal(output, &report); err != nil {
		t.Fatalf("probe output %q: %v", output, err)
	}
	return report
}

// wantPassedEnvironment checks that PiG's child environment is the block the probe was started with.
func wantPassedEnvironment(t *testing.T, report shellEnvReport, passed []string) {
	t.Helper()
	if runtime.GOOS == "linux" {
		if !report.HasKernel {
			t.Fatal("/proc/self/environ is unreadable on Linux")
		}
		if !slices.Equal(report.Kernel, passed) {
			t.Fatalf("/proc/self/environ = %q, want the passed block %q", report.Kernel, passed)
		}
	}
	if !slices.Equal(report.ProcessEnv, passed) {
		t.Errorf("nodespawn.ProcessEnv = %q, want the passed block %q", report.ProcessEnv, passed)
	}
	if !slices.Equal(report.ShellEnv, passed) {
		t.Errorf("GetShellEnv = %q, want the passed block %q", report.ShellEnv, passed)
	}
	if want := upstreamShellEnv(passed, shellEnvProbeBinDir); !slices.Equal(report.ShellEnvWithBin, want) {
		t.Errorf("GetShellEnv(binDir) = %q, want the passed block with only PATH updated %q", report.ShellEnvWithBin, want)
	}
}

// upstreamShellEnv is packages/coding-agent/src/utils/shell.ts getShellEnv: { ...process.env, [pathKey]: updatedPath },
// where pathKey is the first name equal to "path" ignoring case (else PATH, added last) and binDir is prepended unless
// PATH already lists it. Every other entry passes through unchanged, so the bash tool never starts from a fabricated
// or emptied environment; an empty start gains only PATH, as in Pi.
func upstreamShellEnv(passed []string, binDir string) []string {
	env := slices.Clone(passed)
	for i, entry := range env {
		name, current, _ := strings.Cut(entry, "=")
		if !strings.EqualFold(name, "path") {
			continue
		}
		if !slices.Contains(strings.Split(current, string(os.PathListSeparator)), binDir) {
			updated := binDir
			if current != "" {
				updated += string(os.PathListSeparator) + current
			}
			env[i] = name + "=" + updated
		}
		return env
	}
	return append(env, "PATH="+binDir)
}

// uniqueEnvironment is env with one entry per name, as a test's parent environment normally is.
func uniqueEnvironment() []string {
	seen := map[string]bool{}
	var env []string
	for _, entry := range os.Environ() {
		name, _, ok := strings.Cut(entry, "=")
		if !ok || name == "" || seen[name] || strings.HasPrefix(name, "=") {
			continue
		}
		seen[name] = true
		env = append(env, entry)
	}
	return env
}

func TestRestoreSandboxEnvUpstream(t *testing.T) {
	if runtime.GOOS == "windows" {
		// Windows adds hidden "=C:" names and matches names case-insensitively; ProcessEnv's tests cover that.
		t.Skip("the block comparison is exact on Unix; nodespawn's Windows tests cover the case-insensitive block")
	}
	byLine := map[int]func(t *testing.T){
		// does nothing when not running under bun: an ordinary start hands children exactly the passed environment.
		12: func(t *testing.T) {
			env := uniqueEnvironment()
			wantPassedEnvironment(t, runShellEnvProbe(t, env), env)
		},
		// does nothing when process.env already has entries: an added entry reaches children, nothing is replaced.
		28: func(t *testing.T) {
			env := append(uniqueEnvironment(), "RESTORE_SANDBOX_ENV_TEST=1")
			report := runShellEnvProbe(t, env)
			wantPassedEnvironment(t, report, env)
			if !slices.Contains(report.ShellEnv, "RESTORE_SANDBOX_ENV_TEST=1") {
				t.Fatal("added entry missing from the shell environment")
			}
		},
		// restores environment from /proc/self/environ when bun env is empty: Pi's sandbox start has only FOO=bar and
		// BAZ=qux in /proc/self/environ. PiG's children get exactly those (plus the bash tool's PATH), and an empty
		// start fabricates nothing beyond that PATH.
		46: func(t *testing.T) {
			env := []string{"FOO=bar", "BAZ=qux"}
			wantPassedEnvironment(t, runShellEnvProbe(t, env), env)
			wantPassedEnvironment(t, runShellEnvProbe(t, []string{}), []string{})
		},
	}
	cases := upstreamToolCases(t, "packages/coding-agent/test/restore-sandbox-env.test.ts")
	if len(cases) != len(byLine) {
		t.Fatalf("upstream denominator has %d cases, want the %d this port maps", len(cases), len(byLine))
	}
	for _, tc := range cases {
		run, ok := byLine[tc.Line]
		if !ok {
			t.Fatalf("unmapped upstream case %s at line %d", tc.ID, tc.Line)
		}
		t.Run(tc.ID, func(t *testing.T) {
			t.Logf(".upstream/current/packages/coding-agent/test/restore-sandbox-env.test.ts:%d", tc.Line)
			run(t)
		})
	}
}

type upstreamToolCase struct {
	ID   string `json:"id"`
	Line int    `json:"line"`
}

// upstreamToolCases reads the compiler-derived case inventory of one pinned upstream test file.
func upstreamToolCases(t *testing.T, path string) []upstreamToolCase {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "test", "parity", "interfaces", "upstream-tests-v"+pigversion.UpstreamVersion+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var inventory struct {
		Files []struct {
			Path  string             `json:"path"`
			Cases []upstreamToolCase `json:"cases"`
		} `json:"files"`
	}
	if err := json.Unmarshal(raw, &inventory); err != nil {
		t.Fatal(err)
	}
	for _, file := range inventory.Files {
		if file.Path == path {
			return file.Cases
		}
	}
	t.Fatalf("missing upstream case inventory for %s", path)
	return nil
}
