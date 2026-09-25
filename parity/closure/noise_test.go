package closure

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// fakeGoName is the executable name under which this test binary acts as a
// stderr-noisy go wrapper instead of running tests.
const fakeGoName = "go"

func TestMain(m *testing.M) {
	if strings.TrimSuffix(filepath.Base(os.Args[0]), ".exe") == fakeGoName {
		os.Exit(runFakeGo(os.Args[1:]))
	}
	os.Exit(m.Run())
}

// runFakeGo runs the real go recorded beside this executable with its stderr
// discarded, and writes the scripted noise for the invocation's role to stderr
// instead, so a test controls every stderr byte a gate observes. The role is
// "baseline" or "mutant" for go test (the executor fixture's mutant returns
// 2), and the subcommand name otherwise.
func runFakeGo(args []string) int {
	executable, err := os.Executable()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	directory := filepath.Dir(executable)
	realGo, err := os.ReadFile(filepath.Join(directory, "real-go"))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	if noise, err := os.ReadFile(filepath.Join(directory, fakeGoRole(args)+".noise")); err == nil {
		_, _ = os.Stderr.Write(noise)
	}
	command := exec.Command(string(realGo), args...)
	command.Stdin, command.Stdout, command.Stderr = os.Stdin, os.Stdout, io.Discard
	err = command.Run()
	if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
		return exitErr.ExitCode()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	return 0
}

func fakeGoRole(args []string) string {
	if len(args) == 0 {
		return "none"
	}
	if args[0] != "test" {
		return args[0]
	}
	if data, err := os.ReadFile("target.go"); err == nil && bytes.Contains(data, []byte("return 2")) {
		return "mutant"
	}
	return "baseline"
}

// installFakeGo puts a copy of this test binary first on PATH as go and
// returns its directory, where setFakeGoNoise scripts per-role stderr.
func installFakeGo(t *testing.T) string {
	t.Helper()
	realGo, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	realGo, err = filepath.Abs(realGo)
	if err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	name := fakeGoName
	if filepath.Ext(executable) == ".exe" {
		name += ".exe"
	}
	if err := os.WriteFile(filepath.Join(directory, name), binary, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "real-go"), []byte(realGo), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", directory+string(os.PathListSeparator)+os.Getenv("PATH"))
	return directory
}

func setFakeGoNoise(t *testing.T, directory, role, noise string) {
	t.Helper()
	path := filepath.Join(directory, role+".noise")
	if noise == "" {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		return
	}
	if err := os.WriteFile(path, []byte(noise), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestExecuteMutationRequestVerdictIgnoresStderrNoise(t *testing.T) {
	directory := installFakeGo(t)
	telemetry := "error acquiring upload taken: statting token file: stat /home/pig/.config/go/telemetry/local/upload.token: operation not permitted\n"
	cases := []struct{ name, baseline, mutant string }{
		{name: "none"},
		{name: "baseline only", baseline: telemetry},
		{name: "mutant only", mutant: telemetry},
		{name: "identical", baseline: telemetry, mutant: telemetry},
		{name: "differing", baseline: "go: downloading example.com/noise v1.0.0\n", mutant: telemetry + "warning: GOCACHE is stale\n"},
	}
	var reference []MutationResult
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			setFakeGoNoise(t, directory, "baseline", testCase.baseline)
			setFakeGoNoise(t, directory, "mutant", testCase.mutant)
			fixture := newMutationExecutorFixture(t)
			run, err := ExecuteMutationRequest(t.Context(), fixture.databasePath, fixture.root, fixture.request.ID, "mutation")
			if err != nil {
				t.Fatal(err)
			}
			for stream, want := range map[string]string{"mutant-001-baseline.stderr": testCase.baseline, "mutant-001.stderr": testCase.mutant} {
				if got, err := os.ReadFile(filepath.Join(fixture.root, "mutation", stream)); err != nil || string(got) != want {
					t.Fatalf("%s = %q, %v; want the injected noise %q", stream, got, err, want)
				}
			}
			derived := loadCommittedMutationRun(t, fixture)
			if !derived.mutationRunKillsAll(fixture.request, run) || !derived.mutationRunFresh(fixture.request, run) {
				t.Fatalf("stderr noise changed the verdict: mutation run is not admissible: %#v", run.Results)
			}
			if reference == nil {
				reference = run.Results
			} else if !slices.Equal(run.Results, reference) {
				t.Fatalf("results with noise = %#v, want the noise-free results %#v", run.Results, reference)
			}
		})
	}
}

func TestCurrentToolchainHashIgnoresStderrNoise(t *testing.T) {
	directory := installFakeGo(t)
	quiet, err := CurrentToolchainHash(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, noise := range []string{
		"error acquiring upload taken: statting token file: operation not permitted\n",
		"go: warning: ignoring go.mod in $GOPATH\n",
	} {
		setFakeGoNoise(t, directory, "version", noise)
		noisy, err := CurrentToolchainHash(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if noisy != quiet {
			t.Fatalf("go version stderr %q changed the toolchain hash: %s, want %s", noise, noisy, quiet)
		}
	}
}
