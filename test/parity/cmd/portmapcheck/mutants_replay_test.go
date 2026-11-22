package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// failingFixture runs the tests of a throwaway module holding one test file with go test, and returns what go test printed. The
// output is generated here, not written by hand, so it has the shape the installed go prints. command builds the go test to run;
// env is added to the environment.
func failingFixture(t *testing.T, testSource string, command func(root string) *exec.Cmd, env ...string) string {
	t.Helper()
	root := t.TempDir()
	for name, content := range map[string]string{"go.mod": "module fixture\n", "a_test.go": testSource} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cmd := command(root)
	cmd.Env = append(os.Environ(), append([]string{"GOWORK=off", "GOFLAGS="}, env...)...)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("the fixture's tests passed:\n%s", out)
	}
	return string(out)
}

// replayOf is the command of a replay of the tests named in the fixture, with an overlay that replaces nothing.
func replayOf(t *testing.T, names ...string) func(root string) *exec.Cmd {
	t.Helper()
	overlay := filepath.Join(t.TempDir(), "overlay.json")
	if err := os.WriteFile(overlay, []byte(`{"Replace":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	return func(root string) *exec.Cmd { return replayCommand(root, ".", overlay, names, 0) }
}

// TestReplayReadsPlainOutputWhateverGOFLAGSHolds: a replay classifies the plain output of go test, so its command line must not take
// its shape from the caller's GOFLAGS. With -v=true go test prints a failed test's lines before its "--- FAIL:" header, where
// failedAssertions does not look for them, and with -json it prints events, not text. The failing test is run for real in a throwaway
// module, once without the replay's flags (a control: the output must have the shape that misleads the classifier) and once through
// replayCommand.
func TestReplayReadsPlainOutputWhateverGOFLAGSHolds(t *testing.T) {
	const source = "package fixture\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) { t.Fatal(\"want 1\") }\n"
	bare := func(root string) *exec.Cmd {
		cmd := exec.Command("go", "test", "-count=1", "-run", "^(TestA)$", ".")
		cmd.Dir = root
		return cmd
	}
	for _, goflags := range []string{"-v=true", "-json=true"} {
		t.Run(goflags, func(t *testing.T) {
			control := failingFixture(t, source, bare, "GOFLAGS="+goflags)
			if got := failedAssertions(control); len(got) != 0 {
				t.Fatalf("GOFLAGS=%s no longer hides the assertion from a plain run, so this test proves nothing; counted %v in:\n%s", goflags, got, control)
			}
			out := failingFixture(t, source, replayOf(t, "TestA"), "GOFLAGS="+goflags)
			if got := failedAssertions(out); len(got) != 1 || got[0] != "TestA" {
				t.Errorf("failed assertions = %v, want [TestA] in:\n%s", got, out)
			}
			if verdict := classifyReplay(out, os.ErrInvalid); !verdict.killed {
				t.Errorf("the replay's failed assertion is not a kill: %q", verdict.note)
			}
			if strings.Contains(out, `"Action"`) {
				t.Errorf("the replay printed test2json events:\n%s", out)
			}
		})
	}
}

// TestReplayPanicInASubtestKeepsTheSiblingThatFailedFirst: TestA/first fails by an assertion, then TestA/second panics. go test prints
// both headers under TestA before the panic line; the panic discards the failure of the panicking subtest and of the test that holds
// it, and nothing else: the finished subtest is still an assertion that failed, so the replay is a kill that also panicked. The output
// is that of a real run of the two subtests in a throwaway module.
func TestReplayPanicInASubtestKeepsTheSiblingThatFailedFirst(t *testing.T) {
	const source = "package fixture\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) {\n" +
		"\tt.Run(\"first\", func(t *testing.T) { t.Error(\"first failed\") })\n" +
		"\tt.Run(\"second\", func(t *testing.T) { var p *int; _ = *p })\n}\n"
	out := failingFixture(t, source, replayOf(t, "TestA"))
	if got := failedAssertions(out); len(got) != 1 || got[0] != "TestA/first" {
		t.Errorf("failed assertions = %v, want [TestA/first] in:\n%s", got, out)
	}
	verdict := classifyReplay(out, os.ErrInvalid)
	if !verdict.killed || !strings.Contains(verdict.note, "the mutant also panicked: panic: runtime error: invalid memory address") || !strings.Contains(verdict.note, "a_test.go:") {
		t.Errorf("verdict killed %v note %q, want a kill that also reports the panic of TestA/second", verdict.killed, verdict.note)
	}
}
