package pigletbuild

import (
	"bytes"
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"golang.org/x/mod/modfile"

	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	piglet "github.com/MichaelKinsy/PiG/coding/piglet"
	pigletartifact "github.com/MichaelKinsy/PiG/coding/piglet/artifact"
)

const (
	vetScopeShlex = "github.com/google/shlex v0.0.0-20191202100458-e7afc7fbc510"
	// vetScopeShlexNote is how the note names that requirement.
	vetScopeShlexNote = "github.com/google/shlex@v0.0.0-20191202100458-e7afc7fbc510"
	vetScopeNoteLead  = "note: fused vet checked only"
)

// writeVetScopeModule writes a fused member module whose go.mod carries extra
// requirement lines. The vet reads only go.mod and local source, so no module
// cache or network is involved.
func writeVetScopeModule(t *testing.T, source string, requires ...string) string {
	t.Helper()
	root := writeFusedVetModule(t, source)
	mod := "module example.com/hazard\n\ngo 1.26.0\n\nrequire github.com/MichaelKinsy/PiG/extensions/sdk v0.0.0\n"
	for _, requirement := range requires {
		mod += "\nrequire " + requirement + "\n"
	}
	writeFixtureFile(t, filepath.Join(root, "go.mod"), mod)
	return root
}

// buildVetScopeArtifact runs the native build for one fused member against a
// fake Pig source that has only a go.mod. With GOPROXY=off the final go build
// fails offline, after the overlay, so the test sees the complete build log.
func buildVetScopeArtifact(t *testing.T, member string) (string, error) {
	t.Helper()
	t.Setenv("GOPROXY", "off")
	t.Setenv("GOFLAGS", "-mod=readonly")
	t.Setenv("GOTOOLCHAIN", "local")
	t.Setenv("GOWORK", "off")
	pigRoot := t.TempDir()
	writeFixtureFile(t, filepath.Join(pigRoot, "go.mod"), "module example.com/pig\n\ngo 1.26\n")
	plan := pigletartifact.Plan{Components: []pigletartifact.Component{{
		Kind: pigletartifact.ComponentKindExtension, Name: "hazard",
		Realization: pigletartifact.RealizationFused, Materialization: pigletartifact.MaterializationBinary,
	}}}
	var stderr bytes.Buffer
	_, err := buildNativeArtifact(context.Background(), pigSource{Root: pigRoot}, &piglet.Piglet{Name: "hazard"}, []subprocess.CellSpec{fusedVetCell(member)}, plan, nil, Options{}, filepath.Join(t.TempDir(), "pig-hazard"), &bytes.Buffer{}, &stderr)
	if err == nil || !strings.Contains(err.Error(), "build Piglet Binary") {
		t.Fatalf("build error = %v, want the offline go build to fail after the vet and the overlay", err)
	}
	return stderr.String(), err
}

// A build that fuses a member importing an external module names it in one
// note on stderr; the vet passes, so the note is the only sign of the
// unchecked code.
func TestBuildNativeArtifactNamesModulesTheFusedVetSkipped(t *testing.T) {
	source := "package hazard\n\nimport \"github.com/google/shlex\"\n\nfunc Extension() { _, _ = shlex.Split(\"a b\") }\n"
	stderr, _ := buildVetScopeArtifact(t, writeVetScopeModule(t, source, vetScopeShlex))
	want := vetScopeNoteLead + " the members' own and workspace modules; these external modules the members require were not vetted for process-global hazards:\n  " + vetScopeShlexNote + "\n"
	if !strings.Contains(stderr, want) || strings.Count(stderr, vetScopeNoteLead) != 1 {
		t.Fatalf("stderr = %q, want one note %q", stderr, want)
	}
}

func TestBuildNativeArtifactPrintsNoVetScopeNoteWithoutExternalModules(t *testing.T) {
	stderr, _ := buildVetScopeArtifact(t, writeVetScopeModule(t, "package hazard\nfunc Extension() {}\n"))
	if strings.Contains(stderr, "fused vet") {
		t.Fatalf("stderr = %q, want no vet scope note", stderr)
	}
}

// The note names each external module once, at the version the merged go.mod
// selects: the highest any member or Pig requires, whichever member is read
// first. A replacement's target is named, since its source is what the Binary
// links. Local member and workspace modules, Pig itself, and the SDK are
// vetted or host source and are not listed.
func TestMergeFusedModulesNamesUnvettedModulesAtTheSelectedVersion(t *testing.T) {
	parent := t.TempDir()
	alpha, beta, ws := filepath.Join(parent, "a-member"), filepath.Join(parent, "b-member"), filepath.Join(parent, "ws")
	// alpha sorts first and requires the higher example.com/dep.
	writeFixtureFile(t, filepath.Join(alpha, "go.mod"), "module example.com/alpha\n\ngo 1.26\n\nrequire (\n\tgithub.com/MichaelKinsy/PiG/extensions/sdk v0.0.0\n\texample.com/pig v0.9.0\n\texample.com/dep v1.1.0\n\texample.com/forked v1.0.0\n\texample.com/pigdep v1.2.0\n\t"+vetScopeShlex+" // indirect\n)\n\nreplace example.com/forked => example.com/fork v1.0.1\n")
	writeFixtureFile(t, filepath.Join(beta, "go.mod"), "module example.com/beta\n\ngo 1.26\n\nrequire (\n\texample.com/alpha v0.0.0\n\texample.com/dep v1.0.0\n\texample.com/vendored v0.1.0\n)\n\nreplace example.com/vendored => ./third_party/vendored\n")
	writeFixtureFile(t, filepath.Join(ws, "go.mod"), "module example.com/ws\n\ngo 1.26\n\nrequire (\n\texample.com/alpha v0.0.0\n\texample.com/ws/dep v0.1.0\n)\n")
	entries := []fusedEntry{
		{Name: "alpha", Root: alpha, ModulePath: "example.com/alpha", Package: "example.com/alpha", WorkspaceModules: []string{ws}},
		{Name: "beta", Root: beta, ModulePath: "example.com/beta", Package: "example.com/beta"},
	}
	want := []string{
		"example.com/dep@v1.1.0",
		"example.com/forked@v1.0.0 => example.com/fork@v1.0.1",
		"example.com/pigdep@v1.5.0",
		"example.com/vendored@v0.1.0 => " + filepath.Join(beta, "third_party", "vendored"),
		"example.com/ws/dep@v0.1.0",
		vetScopeShlexNote,
	}
	for range 2 {
		pig, err := modfile.Parse("go.mod", []byte("module example.com/pig\n\ngo 1.26\n\nrequire example.com/pigdep v1.5.0\n"), nil)
		if err != nil {
			t.Fatal(err)
		}
		graph, err := mergeFusedModules(pig, slices.Clone(entries))
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(graph.unvetted, want) {
			t.Fatalf("unvetted = %q, want %q", graph.unvetted, want)
		}
	}
}

// The frontend member is fused and vetted like an extension member, so its
// external modules are named too.
func TestOverlayFuseNamesTheFrontendMembersUnvettedModules(t *testing.T) {
	pigRoot := t.TempDir()
	writeFixtureFile(t, filepath.Join(pigRoot, "go.mod"), "module example.com/pig\n\ngo 1.26\n")
	member := t.TempDir()
	writeFixtureFile(t, filepath.Join(member, "go.mod"), "module example.com/frontend\n\ngo 1.26\n\nrequire "+vetScopeShlex+"\n")
	overlay, err := newBuildOverlay(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	graph, err := overlayFuse(overlay, pigRoot, nil, &fusedEntry{Name: "frontend", Pkg: "frontendmember", Factory: "Frontend", Root: member, ModulePath: "example.com/frontend", Package: "example.com/frontend"})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{vetScopeShlexNote}; !slices.Equal(graph.unvetted, want) {
		t.Fatalf("unvetted = %q, want %q", graph.unvetted, want)
	}
}

func TestFusedVetScopeNote(t *testing.T) {
	if note := fusedVetScopeNote(nil); note != "" {
		t.Fatalf("note without modules = %q", note)
	}
	note := fusedVetScopeNote([]string{"example.com/a@v1.0.0", "example.com/b@v2.0.0"})
	if want := "fused vet checked only the members' own and workspace modules; these external modules the members require were not vetted for process-global hazards:\n  example.com/a@v1.0.0\n  example.com/b@v2.0.0"; note != want {
		t.Fatalf("note = %q, want %q", note, want)
	}
}
