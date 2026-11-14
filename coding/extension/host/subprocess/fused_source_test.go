package subprocess

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeFusedSource writes a Go module with one package, one embedded file and one locally replaced module below root.
func writeFusedSource(t *testing.T, root string) string {
	t.Helper()
	member := filepath.Join(root, "member")
	for path, content := range map[string]string{
		"member/go.mod":       "module example.test/member\n\ngo 1.26\n\nrequire example.test/shared v0.0.0\n\nreplace example.test/shared => ../shared\n",
		"member/extension.go": "package member\n\nimport _ \"embed\"\n\n//go:embed prompt.txt\nvar prompt string\n",
		"member/prompt.txt":   "hello\n",
		"shared/go.mod":       "module example.test/shared\n\ngo 1.26\n",
		"shared/shared.go":    "package shared\n",
	} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, path)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, path), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return member
}

func fusedDigest(t *testing.T, dir string) string {
	t.Helper()
	digest, err := FusedSourceDigest(dir)
	if err != nil {
		t.Fatal(err)
	}
	return digest
}

// The digest a build bakes for a fused member is content alone: equal for a fresh checkout at another absolute path and for a file
// that was only touched, different after a one-byte edit of anything `go build` reads (a source, an embedded file, go.mod, a locally
// replaced module), and unmoved by what the build does not read (_test.go, testdata, vendor, dot directories, other files).
func TestFusedSourceDigestIsTheContentTheBuildReads(t *testing.T) {
	first := writeFusedSource(t, t.TempDir())
	baked := fusedDigest(t, first)

	second := writeFusedSource(t, filepath.Join(t.TempDir(), "elsewhere", "deeper"))
	if got := fusedDigest(t, second); got != baked {
		t.Fatalf("a fresh checkout at another path has digest %s, the first %s", got, baked)
	}
	later := time.Now().Add(48 * time.Hour)
	for _, name := range []string{"extension.go", "go.mod", "prompt.txt"} {
		if err := os.Chtimes(filepath.Join(first, name), later, later); err != nil {
			t.Fatal(err)
		}
	}
	if got := fusedDigest(t, first); got != baked {
		t.Fatalf("touched files changed the digest: %s, was %s", got, baked)
	}

	// In order: a nested module's go.mod stands before its package, which without it would be a package of the member.
	for _, file := range [][2]string{
		{"member/extension_test.go", "package member\n"},
		{"member/testdata/case.go", "package testdata\n"},
		{"member/vendor/v/v.go", "package v\n"},
		{"member/.cache/c.go", "package c\n"},
		{"member/_scratch/s.go", "package s\n"},
		{"member/README.md", "notes\n"},
		{"shared/shared_test.go", "package shared\n"},
		{"member/nested/go.mod", "module example.test/nested\n"},
		{"member/nested/nested.go", "package nested\n"},
		{"member/internal/README.md", "notes\n"},
	} {
		path, content := file[0], file[1]
		full := filepath.Join(filepath.Dir(first), path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := fusedDigest(t, first); got != baked {
			t.Fatalf("%s, which go build does not read, changed the digest", path)
		}
	}

	for _, path := range []string{"member/extension.go", "member/prompt.txt", "member/go.mod", "shared/shared.go"} {
		root := t.TempDir()
		member := writeFusedSource(t, root)
		f, err := os.OpenFile(filepath.Join(root, path), os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.WriteString("\n"); err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
		if got := fusedDigest(t, member); got == baked {
			t.Errorf("one byte added to %s left the digest unchanged", path)
		}
	}

	if _, err := FusedSourceDigest(filepath.Join(first, "missing")); err == nil {
		t.Error("a missing directory has a digest")
	}
}

// bakedResolver is a Piglet Binary's resolver with one fused member; withKey makes it a FusedSourceResolver.
type bakedResolver struct{ name, digest string }

func (r bakedResolver) FusedServe(cfg ExtConfig) (func(net.Conn) error, bool) {
	return func(net.Conn) error { return nil }, cfg.Name == r.name
}

type bakedSourceResolver struct{ bakedResolver }

func (r bakedSourceResolver) BakedSourceDigest(name string) (string, bool) {
	return r.digest, name == r.name
}

func TestResolveFused(t *testing.T) {
	member := writeFusedSource(t, t.TempDir())
	baked := fusedDigest(t, member)
	edited := writeFusedSource(t, t.TempDir())
	if err := os.WriteFile(filepath.Join(edited, "extension.go"), []byte("package member\n\nimport _ \"embed\"\n\n//go:embed prompt.txt\nvar prompt string\n\nvar edited bool\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	withKey := bakedSourceResolver{bakedResolver{"member", baked}}
	t.Cleanup(func() { SetFusedResolver(nil) })
	for _, tc := range []struct {
		name        string
		resolver    FusedResolver
		cfg         ExtConfig
		noToolchain bool
		fused       bool
		rebuiltFrom string
		reason      string
		warning     string
	}{
		{name: "not a fused member", resolver: withKey, cfg: ExtConfig{Name: "other", Source: edited}},
		{name: "no source configured", resolver: withKey, cfg: ExtConfig{Name: "member"}, fused: true},
		{name: "built without the key", resolver: bakedResolver{"member", baked}, cfg: ExtConfig{Name: "member", Source: edited}, fused: true},
		{name: "no digest baked for the member", resolver: bakedSourceResolver{bakedResolver{"member", baked}}, cfg: ExtConfig{Name: "member", Source: member}, fused: true},
		{name: "source unchanged", resolver: withKey, cfg: ExtConfig{Name: "member", Source: member}, fused: true},
		{name: "source changed", resolver: withKey, cfg: ExtConfig{Name: "member", Source: edited}, rebuiltFrom: edited},
		{name: "source changed, no toolchain", resolver: withKey, cfg: ExtConfig{Name: "member", Source: edited}, noToolchain: true, fused: true,
			reason:  "fused (source differs; toolchain missing)",
			warning: "source at " + edited + ` differs from the fused copy; no Go toolchain: running the fused copy: cannot build go extension: the "go" toolchain is not installed; install it, or run this extension from a Piglet Binary or prebuilt package (which need no toolchain)`},
		{name: "source missing", resolver: withKey, cfg: ExtConfig{Name: "member", Source: filepath.Join(member, "gone")}, fused: true,
			reason: "fused (source unreadable)", warning: "cannot be read as the Go module of the fused copy: running the fused copy"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.noToolchain {
				t.Setenv("PATH", "")
			} else if tc.rebuiltFrom != "" {
				// The toolchain is the builder's readiness check: a `go` on PATH.
				bin := t.TempDir()
				name := "go"
				if os.PathSeparator == '\\' {
					name = "go.exe"
				}
				if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"), 0o755); err != nil {
					t.Fatal(err)
				}
				t.Setenv("PATH", bin)
			}
			SetFusedResolver(tc.resolver)
			choice := resolveFused(tc.cfg)
			if (choice.serve != nil) != tc.fused || choice.rebuiltFrom != tc.rebuiltFrom || choice.reason != tc.reason || !strings.Contains(choice.warning, tc.warning) || (tc.warning == "") != (choice.warning == "") {
				t.Fatalf("choice fused=%t rebuiltFrom=%q reason=%q warning=%q", choice.serve != nil, choice.rebuiltFrom, choice.reason, choice.warning)
			}
			if RunsFused(tc.cfg) != tc.fused {
				t.Fatalf("RunsFused = %t", !tc.fused)
			}
			planned := choice.cellConfig(tc.cfg)
			if planned.fusedRebuiltFrom != tc.rebuiltFrom || (tc.rebuiltFrom != "") != (planned.Isolation == "isolated") {
				t.Fatalf("cell config: rebuilt from %q, isolation %q", planned.fusedRebuiltFrom, planned.Isolation)
			}
		})
	}
}

// Stock PiG installs no fused resolver, so no load compares a source with a baked digest: a configuration that would get the
// unreadable-source warning from a Binary built with the key is an ordinary source extension, at a start and on a reload.
func TestStockLoadComparesNoFusedSource(t *testing.T) {
	SetFusedResolver(nil)
	missing := ExtConfig{Name: "member", Source: filepath.Join(t.TempDir(), "gone"), Enabled: true}
	if RunsFused(missing) {
		t.Fatal("stock pig runs a member fused")
	}
	h := NewHost(t.TempDir())
	t.Cleanup(func() { h.Shutdown("done") })
	h.SetConfigLoader(func() ([]ExtConfig, error) { return []ExtConfig{missing}, nil })
	loaded, errs := h.LoadAll(t.Context(), []ExtConfig{missing})
	if len(loaded) != 0 || len(errs) != 1 || strings.Contains(errs[0].Error(), "fused") {
		t.Fatalf("stock load: %d loaded, errors %q, want the one ordinary load failure", len(loaded), errs)
	}
	if _, err := h.Reload(t.Context()); err != nil {
		t.Fatal(err)
	}
	report := h.LastReloadReport()
	if len(report.Issues) != 1 || strings.Contains(report.Issues[0], "fused") {
		t.Fatalf("stock reload issues %q, want the one ordinary load failure", report.Issues)
	}
	for _, cell := range report.Cells {
		if cell.Strategy == fusedPlacement || strings.Contains(cell.Reason, "fused") || strings.Contains(cell.Reason, "rebuilt from") {
			t.Fatalf("stock reload placement %+v", cell)
		}
	}
}
