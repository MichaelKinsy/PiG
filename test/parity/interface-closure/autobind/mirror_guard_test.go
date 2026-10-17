package main

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

// autobindMainEnv makes the test binary run the command itself, so a test sees the exit status a make target sees.
const autobindMainEnv = "PIG_AUTOBIND_TEST_RUN_MAIN"

func TestMain(m *testing.M) {
	if os.Getenv(autobindMainEnv) == "1" {
		main()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// mirrorFixture writes the fixture module with its ledger under the pinned version (what make interface-gaps-update reads) and an
// empty reviewed-decisions.json, and returns its root and a reach list outside it.
func mirrorFixture(t *testing.T) (root, reach string) {
	t.Helper()
	fixtureLedger(t, nil, nil, nil, nil)
	root = fixtureLast.root
	base := filepath.Join(root, "test/parity/interfaces")
	for _, name := range []string{"upstream", "mapping"} {
		if err := os.Rename(filepath.Join(base, name+"-v0.0.0.json"), filepath.Join(base, name+"-v"+pigversion.UpstreamVersion+".json")); err != nil {
			t.Fatal(err)
		}
	}
	decisions := filepath.Join(root, reviewedDecisionsFile)
	if err := os.MkdirAll(filepath.Dir(decisions), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(decisions, []byte("{\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	reach = filepath.Join(t.TempDir(), "reach.json")
	if err := os.WriteFile(reach, js([]string{"lib/use.go#Members"}), 0o644); err != nil {
		t.Fatal(err)
	}
	return root, reach
}

// writeMirror makes the fixture's .upstream/current the mirror of a Pi build: its provenance marker and coding-agent package.json.
func writeMirror(t *testing.T, root, version, tag, commit, pkgVersion string) {
	t.Helper()
	current := filepath.Join(root, ".upstream/current")
	if err := os.MkdirAll(filepath.Join(current, "packages/coding-agent"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(current, ".pig-upstream-source.json"), js(m{"version": version, "tag": tag, "commit": commit}), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(current, "packages/coding-agent/package.json"), js(m{"version": pkgVersion}), 0o644); err != nil {
		t.Fatal(err)
	}
}

// treeSnapshot reads every file under root.
func treeSnapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		out[filepath.ToSlash(rel)] = string(data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// runAutobind runs the command in a child process and returns its exit code and output.
func runAutobind(t *testing.T, args ...string) (int, string) {
	t.Helper()
	cmd := exec.Command(os.Args[0], args...)
	cmd.Env = append(os.Environ(), autobindMainEnv+"=1")
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return 0, out.String()
	case errors.As(err, &exit):
		return exit.ExitCode(), out.String()
	}
	t.Fatalf("run autobind: %v", err)
	return 0, ""
}

// A run that rewrites committed files (the mapping, the gap baseline, reviewed-types.json, reviewed-gaps.json,
// reviewed-decisions.json, renames.json, mutation-checked.json) derives rows from Pi's source under .upstream/current. With no
// mirror it stops on the designed-out rows its rules can no longer prove (after rewriting reviewed-types.json); with another
// Pi's mirror it reopens rows and exits 0. Each must fail before writing anything, naming the fix.
func TestWritingRunsRefuseAMirrorThatIsNotThePinnedBuild(t *testing.T) {
	commit103 := strings.Repeat("3", 40)
	cases := []struct {
		name   string
		mirror func(t *testing.T, root string)
		args   []string
	}{
		{"update without a mirror", func(t *testing.T, root string) {
			if err := os.RemoveAll(filepath.Join(root, ".upstream")); err != nil {
				t.Fatal(err)
			}
		}, []string{"-update"}},
		{"update over a Pi 1.0.3 mirror", func(t *testing.T, root string) {
			writeMirror(t, root, "1.0.3", "v1.0.3", commit103, "1.0.3")
		}, []string{"-update"}},
		{"update over a mirror of the pinned version at another commit", func(t *testing.T, root string) {
			writeMirror(t, root, pigversion.UpstreamVersion, pigversion.UpstreamRef, commit103, pigversion.UpstreamPackageVersion)
		}, []string{"-update"}},
		{"update with -import-decisions over a Pi 1.0.3 mirror", func(t *testing.T, root string) {
			writeMirror(t, root, "1.0.3", "v1.0.3", commit103, "1.0.3")
		}, []string{"-update", "-import-decisions"}},
		{"seed-renames without a mirror", func(t *testing.T, root string) {
			if err := os.RemoveAll(filepath.Join(root, ".upstream")); err != nil {
				t.Fatal(err)
			}
		}, []string{"-seed-renames"}},
		{"mutate over a Pi 1.0.3 mirror", func(t *testing.T, root string) {
			writeMirror(t, root, "1.0.3", "v1.0.3", commit103, "1.0.3")
		}, []string{"-mutate"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root, reach := mirrorFixture(t)
			c.mirror(t, root)
			before := treeSnapshot(t, root)
			code, out := runAutobind(t, append([]string{"-root", root, "-reach", reach, "-out", filepath.Join(root, "build/interface-gaps")}, c.args...)...)
			if code == 0 {
				t.Fatalf("autobind %v exited 0 over a mirror that is not Pi %s; output:\n%s", c.args, pigversion.UpstreamVersion, out)
			}
			if !strings.Contains(out, "make upstream-mirror") {
				t.Errorf("the refusal does not name the fix (make upstream-mirror); output:\n%s", out)
			}
			after := treeSnapshot(t, root)
			for path, content := range after {
				if was, ok := before[path]; !ok {
					t.Errorf("the refused run wrote %s", path)
				} else if was != content {
					t.Errorf("the refused run rewrote %s", path)
				}
			}
			for path := range before {
				if _, ok := after[path]; !ok {
					t.Errorf("the refused run removed %s", path)
				}
			}
		})
	}
}

// The same update over the pinned build's mirror writes the ledger: the refusals above are the mirror check, not a fixture that
// cannot be updated.
func TestUpdateOverThePinnedMirrorWritesTheLedger(t *testing.T) {
	root, reach := mirrorFixture(t)
	writeMirror(t, root, pigversion.UpstreamVersion, pigversion.UpstreamRef, pigversion.UpstreamCommit, pigversion.UpstreamPackageVersion)
	code, out := runAutobind(t, "-root", root, "-reach", reach, "-out", filepath.Join(root, "build/interface-gaps"), "-update")
	if code != 0 {
		t.Fatalf("autobind -update over the pinned mirror exited %d; output:\n%s", code, out)
	}
	baseline := filepath.Join(root, "test/parity/interface-closure/gaps/interface-gaps-v"+pigversion.UpstreamVersion+".tsv")
	if _, err := os.Stat(baseline); err != nil {
		t.Fatalf("the update wrote no gap baseline: %v", err)
	}
}
