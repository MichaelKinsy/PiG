package boundary

import (
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const corePrefix = "github.com/MichaelKinsy/PiG/durable/core"

// repoRoot is the module root: this package lives at durable/core/contracttest/boundary.
func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("../../../..")
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func corePatterns(wasm bool) []string {
	var out []string
	for _, p := range CorePackages {
		out = append(out, "./durable/core/"+p)
	}
	if wasm {
		out = append(out, "./durable/core/"+WasmMain)
	}
	return out
}

// The production core's dependency closure links no encoding/json, reflect or template package, in the Go configuration and in
// the Wasm one (the reactor's main package is a root of the latter). make lint and the gate (contracttest/gate.sh stage 3) run it.
func TestCoreImportBoundary(t *testing.T) {
	root := repoRoot(t)
	for _, c := range []struct {
		name string
		env  []string
		wasm bool
	}{
		{"native", nil, false},
		{"wasip1", []string{"GOOS=wasip1", "GOARCH=wasm"}, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			violations, err := CheckImportBoundary(root, c.env, corePatterns(c.wasm)...)
			if err != nil {
				t.Fatal(err)
			}
			for _, v := range violations {
				t.Errorf("%s", v)
			}
		})
	}
}

// Every package under durable/core is either built into the Wasm module (CorePackages, the Wasm main) or named non-core: a new
// package cannot escape the boundary by being forgotten.
func TestEveryPackageIsCoreOrNamedNonCore(t *testing.T) {
	out, err := exec.Command("go", "list", "-C", repoRoot(t), "./durable/core/...").Output()
	if err != nil {
		t.Fatal(err)
	}
	known := append(append(append([]string{}, CorePackages...), WasmMain), NonCorePackages...)
	for path := range strings.FieldsSeq(string(out)) {
		rel := strings.TrimPrefix(strings.TrimPrefix(path, corePrefix), "/")
		if rel == "" {
			rel = "."
		}
		if !slices.Contains(known, rel) {
			t.Errorf("%s is neither a core package nor listed in NonCorePackages (boundary.go): classify it", path)
		}
	}
	for _, rel := range NonCorePackages {
		if slices.Contains(CorePackages, rel) {
			t.Errorf("%s is both core and non-core", rel)
		}
	}
}

// The fixtures are deliberate violations: the checker must fail each with the chain that reaches the forbidden package, and
// accept the clean one, so a regression of the checker itself cannot make the core guard pass vacuously.
func TestFixturesProveTheCheckerFails(t *testing.T) {
	const fixtures = corePrefix + "/contracttest/boundary/testdata/"
	cases := []struct {
		fixture, want string
	}{
		{"direct", fixtures + "direct -> encoding/json"},
		{"reflected", fixtures + "reflected -> reflect"},
		{"viahelper", fixtures + "viahelper -> " + fixtures + "helper -> text/template"},
		{"viafmt", fixtures + "viafmt -> fmt -> reflect"},
	}
	for _, c := range cases {
		t.Run(c.fixture, func(t *testing.T) {
			violations, err := CheckImportBoundary(repoRoot(t), nil, "./durable/core/contracttest/boundary/testdata/"+c.fixture)
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, v := range violations {
				got = append(got, strings.Join(v.Chain, " -> "))
			}
			if !slices.Contains(got, c.want) {
				t.Fatalf("violations %q do not include the chain %q", got, c.want)
			}
		})
	}
	t.Run("clean", func(t *testing.T) {
		violations, err := CheckImportBoundary(repoRoot(t), nil, "./durable/core/contracttest/boundary/testdata/clean")
		if err != nil {
			t.Fatal(err)
		}
		if len(violations) != 0 {
			t.Fatalf("the clean fixture (strconv, encoding/binary) reported %v", violations)
		}
	})
}

// The matcher on a synthetic graph: encoding/json/v2 and the template packages are forbidden, the shortest chain is reported,
// the accepted standard-library route is not, and the same route from a non-standard package is.
func TestCheckGraph(t *testing.T) {
	g := Graph{
		"core":            {"mid", "encoding/binary", "strconv"},
		"mid":             {"leaf", "net/url"},
		"leaf":            {"encoding/json/v2"},
		"net/url":         {"strconv"},
		"encoding/binary": {"reflect"},
		"core2":           {"text/template/parse", "html/template"},
		"example.com/x":   {"reflect"},
		"core3":           {"example.com/x"},
	}
	got := CheckGraph(g, []string{"core", "core2", "core3"}, ForbiddenPackages)
	var lines []string
	for _, v := range got {
		lines = append(lines, strings.Join(v.Chain, " -> "))
	}
	want := []string{"core -> mid -> leaf -> encoding/json/v2", "core2 -> text/template/parse", "core2 -> html/template", "core3 -> example.com/x -> reflect"}
	slices.Sort(lines)
	slices.Sort(want)
	if !slices.Equal(lines, want) {
		t.Fatalf("violations %q, want %q", lines, want)
	}
}
