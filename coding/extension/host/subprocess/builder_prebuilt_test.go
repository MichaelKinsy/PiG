package subprocess

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension/host/runtimecell"
)

// fakeResolver serves one composition and records what it was asked for.
type fakeResolver struct {
	wantLang, wantKey string
	bin               string
	gotReq            runtimecell.PrebuiltRequest
}

func (f *fakeResolver) ResolvePrebuilt(req runtimecell.PrebuiltRequest) (string, bool) {
	f.gotReq = req
	if req.Language == f.wantLang && req.Key == f.wantKey {
		return f.bin, true
	}
	return "", false
}

// brokenGoSource writes a Go extension dir that hashes and detects as Go but
// would fail to compile, so any code path that reaches the real build errors.
func brokenGoSource(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module broken\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\nthis is not go\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestBuilderBuild_PrebuiltShortCircuits(t *testing.T) {
	src := brokenGoSource(t)
	stagedBin := filepath.Join(t.TempDir(), "prebuilt-web-search")
	if err := os.WriteFile(stagedBin, []byte("BIN"), 0o755); err != nil {
		t.Fatal(err)
	}
	fr := &fakeResolver{wantLang: "go", wantKey: "web-search", bin: stagedBin}
	runtimecell.SetPrebuiltResolver(fr)
	defer runtimecell.SetPrebuiltResolver(nil)

	b := NewBuilder(t.TempDir()) // fresh cache: no accidental hit
	res, err := b.Build("web-search", src)
	if err != nil {
		t.Fatalf("expected short-circuit to skip the (broken) build, got error: %v", err)
	}
	if res.BinaryPath != stagedBin || !res.Cached {
		t.Errorf("expected prebuilt binary %q (cached), got %q (cached=%v)", stagedBin, res.BinaryPath, res.Cached)
	}
	// The host must hand the resolver the correct composition.
	if fr.gotReq.Language != "go" || fr.gotReq.Key != "web-search" ||
		fr.gotReq.GOOS != runtime.GOOS || fr.gotReq.GOARCH != runtime.GOARCH ||
		len(fr.gotReq.Extensions) != 1 || fr.gotReq.Extensions[0].Name != "web-search" {
		t.Errorf("resolver got wrong composition: %+v", fr.gotReq)
	}
	if fr.gotReq.Extensions[0].Hash == "" {
		t.Error("source-mode request must carry the source content hash")
	}
}

func TestBuilderBuild_NilResolverBuildsFromSource(t *testing.T) {
	// With no resolver (stock pig), the broken source must actually be built
	// and fail: proving the short-circuit is the only thing that skipped it.
	runtimecell.SetPrebuiltResolver(nil)
	b := NewBuilder(t.TempDir())
	if _, err := b.Build("web-search", brokenGoSource(t)); err == nil {
		t.Fatal("expected build of broken source to fail with nil resolver")
	}
}
