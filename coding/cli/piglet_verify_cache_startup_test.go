package cli

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/piglet/signature"
	"github.com/MichaelKinsy/PiG/coding/pigletbuild"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// pig additive (D18): a signed Piglet Binary that cannot remember its passing startup check still runs. runStableCLI
// prints the verification cache write error as a warning, startup continues, and the next start checks the Binary in
// full again, so it warns again. Only a failed check stops the Binary.
//
// The cache warning comes first because the check runs before any other startup work. In a read-only state directory
// the extension SDK staging warns after it, so only that case lets other lines follow.
func TestSignedPigletBinaryRunsWhenItsVerifyCacheCannotBeWritten(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a signed Piglet Binary")
	}
	binary := buildSignedFauxPigletBinary(t)
	for _, test := range []struct {
		name string
		// block makes the verification cache directory under state unwritable, and returns that directory.
		block func(t *testing.T, state string) string
		// alone means the cache warning is all of stderr.
		alone bool
	}{
		{"cache directory is a file", func(t *testing.T, state string) string {
			cacheDir := filepath.Join(state, "piglet-verify")
			writeStartupFixtureFile(t, cacheDir, "")
			return cacheDir
		}, true},
		{"state directory is read-only", func(t *testing.T, state string) string {
			if runtime.GOOS == "windows" || os.Geteuid() == 0 {
				t.Skip("directory permissions do not stop this user from writing")
			}
			if err := os.MkdirAll(state, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(state, 0o500); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Chmod(state, 0o700) })
			return filepath.Join(state, "piglet-verify")
		}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			home, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			cacheDir := test.block(t, filepath.Join(home, ".pig", "state"))
			// The warning carries the error the cache write gets from the file system, as the Binary's own MkdirAll reports it.
			mkdirErr := os.MkdirAll(cacheDir, 0o700)
			if mkdirErr == nil {
				t.Fatalf("the blocked cache directory %s can be created", cacheDir)
			}
			want := "pig: warning: write Piglet verification cache: " + mkdirErr.Error() + "\n" +
				"pig: startup will continue; the next start verifies this Piglet Binary in full again\n"
			for _, start := range []string{"first start", "next start"} {
				code, stdout, stderr := runFauxPigletPrompt(t, binary, home)
				if code != 0 || strings.TrimSpace(stdout) != "42" || !strings.HasPrefix(stderr, want) || strings.Count(stderr, "Piglet verification cache") != 1 || (test.alone && stderr != want) {
					t.Fatalf("%s: exit %d, stdout %q, want exit 0 and 42 with stderr starting (alone=%t)\n%s\ngot stderr\n%s", start, code, stdout, test.alone, want, stderr)
				}
			}
			if _, err := os.Stat(filepath.Join(cacheDir, "verified.json")); err == nil {
				t.Fatal("the blocked verification cache was written")
			}
		})
	}
}

// buildSignedFauxPigletBinary runs a real `pig piglet build --format binary --sign-key` of a Piglet with no extensions
// whose default model is test-faux/faux-1, with HOME and PIG_HOME isolated, and returns the signed Binary.
func buildSignedFauxPigletBinary(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// The isolated HOME must not move the Go caches: the build compiles against the caller's module and build caches.
	for _, key := range []string{"GOPATH", "GOMODCACHE", "GOCACHE"} {
		value, err := exec.Command("go", "env", key).Output()
		if err != nil {
			t.Fatalf("go env %s: %v", key, err)
		}
		t.Setenv(key, strings.TrimSpace(string(value)))
	}
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("USERPROFILE", filepath.Join(root, "home"))
	t.Setenv("PIG_HOME", filepath.Join(root, "home", ".pig"))
	// Go makes downloaded module-cache files read-only by default, which would prevent t.TempDir from removing them.
	t.Setenv("GOFLAGS", strings.TrimSpace(os.Getenv("GOFLAGS")+" -modcacherw"))
	pigletPath := filepath.Join(root, "faux.yaml")
	writeStartupFixtureFile(t, pigletPath, "name: faux\nmodel:\n  provider: test-faux\n  name: faux-1\n")
	keyPath := filepath.Join(root, "author.key")
	if _, err := signature.GenerateKey(keyPath); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(root, "pig-faux")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	var stdout, stderr bytes.Buffer
	if code := pigletbuild.RunPigletBuildCommand([]string{pigletPath, "--format", "binary", "--builder", "native", "--out", binary, "--sign-key", keyPath}, &stdout, &stderr); code != 0 {
		t.Fatalf("pig piglet build exit %d\nstdout:\n%s\nstderr:\n%s", code, &stdout, &stderr)
	}
	return binary
}

// runFauxPigletPrompt runs `-p "What is 20+22?"` in binary against the test-faux provider, offline, with home as HOME
// and home/.pig as PIG_HOME. It returns the exit code, stdout and stderr.
func runFauxPigletPrompt(t *testing.T, binary, home string) (int, string, string) {
	t.Helper()
	cmd := exec.CommandContext(testbudget.Context(t), binary, "--no-session", "-p", "What is 20+22?")
	cmd.Dir = t.TempDir()
	cmd.Env = append(os.Environ(), "HOME="+home, "USERPROFILE="+home, "XDG_CONFIG_HOME="+filepath.Join(home, ".config"),
		"PIG_HOME="+filepath.Join(home, ".pig"), "PIG_CODING_AGENT_DIR="+filepath.Join(home, ".pig", "agent"), "PI_CODING_AGENT_DIR="+filepath.Join(home, ".pi", "agent"),
		"PIG_TEST_FAUX=1", "PIG_OFFLINE=1")
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return 0, stdout.String(), stderr.String()
	case errors.As(err, &exit):
		return exit.ExitCode(), stdout.String(), stderr.String()
	default:
		t.Fatalf("run %s: %v", binary, err)
		return 0, "", ""
	}
}
