package pigletbuild

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/toolchain"
)

func neverCalled(t *testing.T, name string) func() (bool, error) {
	return func() (bool, error) {
		t.Errorf("%s was consulted", name)
		return false, nil
	}
}

func answer(v bool) func() (bool, error) { return func() (bool, error) { return v, nil } }

func TestStaticCgoDefaultForCgoFreeBuild(t *testing.T) {
	target := Target{OS: "linux", Arch: "amd64"}
	got, err := withStaticCgoDefault([]string{"PATH=/bin"}, target, answer(false), answer(false))
	if err != nil || !slices.Equal(got, []string{"PATH=/bin", "CGO_ENABLED=0"}) {
		t.Fatalf("cgo-free build env = %v, %v; want CGO_ENABLED=0", got, err)
	}
}

func TestStaticCgoDefaultKeepsExplicitEnvironment(t *testing.T) {
	target := Target{OS: "linux", Arch: "amd64"}
	for _, value := range []string{"CGO_ENABLED=1", "CGO_ENABLED=0", "CGO_ENABLED="} {
		env := []string{"PATH=/bin", value}
		got, err := withStaticCgoDefault(env, target, neverCalled(t, "go env"), neverCalled(t, "go list"))
		if err != nil || !slices.Equal(got, env) {
			t.Fatalf("explicit %q: env = %v, %v", value, got, err)
		}
	}
}

func TestStaticCgoDefaultKeepsGoEnvFileSetting(t *testing.T) {
	env := []string{"PATH=/bin"}
	got, err := withStaticCgoDefault(env, Target{OS: "linux", Arch: "amd64"}, answer(true), neverCalled(t, "go list"))
	if err != nil || !slices.Equal(got, env) {
		t.Fatalf("go env -w CGO_ENABLED: env = %v, %v", got, err)
	}
}

func TestStaticCgoDefaultKeepsCgoWhenBuildNeedsIt(t *testing.T) {
	env := []string{"PATH=/bin"}
	got, err := withStaticCgoDefault(env, Target{OS: "linux", Arch: "amd64"}, answer(false), answer(true))
	if err != nil || !slices.Equal(got, env) {
		t.Fatalf("cgo build env = %v, %v; want the Go default", got, err)
	}
}

func TestStaticCgoDefaultFollowsAndroidLinkerRequirement(t *testing.T) {
	got, err := withStaticCgoDefault(nil, Target{OS: "android", Arch: "amd64"}, answer(false), answer(false))
	if err != nil || !slices.Equal(got, []string{"CGO_ENABLED=1"}) {
		t.Fatalf("android/amd64 env = %v, %v; want CGO_ENABLED=1", got, err)
	}
}

func TestStaticCgoDefaultPropagatesProbeErrors(t *testing.T) {
	boom := errors.New("boom")
	fail := func() (bool, error) { return false, boom }
	target := Target{OS: "linux", Arch: "amd64"}
	if _, err := withStaticCgoDefault(nil, target, fail, answer(false)); !errors.Is(err, boom) {
		t.Fatalf("go env error = %v", err)
	}
	if _, err := withStaticCgoDefault(nil, target, answer(false), fail); !errors.Is(err, boom) {
		t.Fatalf("go list error = %v", err)
	}
}

// buildNeedsCgo reads the real go command: a package of the build with cgo
// sources makes it true, and the standard library's own cgo files do not.
func TestBuildNeedsCgoListsNonStandardCgoPackages(t *testing.T) {
	goCommand, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go command not available")
	}
	module := func(t *testing.T, files map[string]string) string {
		dir := t.TempDir()
		files["go.mod"] = "module example.com/m\n\ngo 1.21\n"
		for name, content := range files {
			path := filepath.Join(dir, name)
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		return dir
	}
	t.Setenv("GOWORK", "off")
	t.Setenv("GOTOOLCHAIN", "local")
	cases := []struct {
		name  string
		files map[string]string
		want  bool
	}{
		{"pure Go with net and os/user", map[string]string{"cmd/pig/main.go": "package main\nimport (_ \"net\"; _ \"os/user\")\nfunc main() {}\n"}, false},
		{"cgo dependency", map[string]string{
			"cmd/pig/main.go":  "package main\nimport _ \"example.com/m/dep\"\nfunc main() {}\n",
			"dep/dep.go":       "package dep\n// int one(void) { return 1; }\nimport \"C\"\nfunc One() int { return int(C.one()) }\n",
			"dep/dep_other.go": "package dep\n",
		}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := module(t, tc.files)
			got, err := buildNeedsCgo(context.Background(), pigSource{Root: dir}, toolchain.GoToolchain{Command: goCommand}, "", nil)
			if err != nil || got != tc.want {
				t.Fatalf("buildNeedsCgo = %v, %v; want %v", got, err, tc.want)
			}
		})
	}
}
