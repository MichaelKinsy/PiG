package pigletbuild

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding"
)

// checkoutPigSource returns the Pig checkout these tests run from.
func checkoutPigSource(t *testing.T) pigSource {
	t.Helper()
	root, err := pigSourceRoot()
	if err != nil {
		t.Fatal(err)
	}
	return pigSource{Root: root}
}

// withoutPigCheckout makes the native builder see a binary-only install: no
// PIG_SOURCE_ROOT and a working directory outside any Pig checkout. PIG_HOME is a
// test directory, so staged release source never reaches the user's ~/.pig cache.
func withoutPigCheckout(t *testing.T) {
	t.Helper()
	t.Setenv("PIG_HOME", t.TempDir())
	t.Setenv("PIG_SOURCE_ROOT", "")
	t.Chdir(t.TempDir())
	if root, err := pigSourceRoot(); err == nil {
		t.Fatalf("test working directory is inside Pig checkout %s", root)
	}
}

// fakeModuleTree writes a module cache stand-in for the fetched release.
func fakeModuleTree(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":             "module github.com/MichaelKinsy/PiG\n\ngo 1.26.0\n",
		"go.work":            "go 1.26.0\n\nuse ./extensions/sdk\n",
		"cmd/pig/main.go":    "package main\n\nfunc main() {}\n",
		"coding/upstream.go": "package coding\n",
	}
	for name, content := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

type fakeModDownload struct {
	queries []string
	output  []byte
	err     error
}

func (f *fakeModDownload) run(_ context.Context, query string) ([]byte, error) {
	f.queries = append(f.queries, query)
	return f.output, f.err
}

func releaseBuilder(version string, download *fakeModDownload) nativeBuilder {
	return nativeBuilder{
		runningRelease: func() (string, bool) { return version, version != "" },
		modDownload:    download.run,
	}
}

func hostRequest() BuilderRequest {
	return BuilderRequest{Options: Options{Targets: []Target{{OS: runtime.GOOS, Arch: runtime.GOARCH}}}}
}

func TestNativeProbeDevBuildWithoutCheckoutIsSourceUnavailable(t *testing.T) {
	withoutPigCheckout(t)
	download := &fakeModDownload{}
	readiness := releaseBuilder("", download).Probe(context.Background(), hostRequest())
	if readiness.Ready || readiness.Code != "source-unavailable" || readiness.Remedy != sourceUnavailableRemedy || !strings.Contains(readiness.Message, "not a tagged release") {
		t.Fatalf("readiness = %+v", readiness)
	}
	if len(download.queries) != 0 {
		t.Fatalf("probe downloaded source: %v", download.queries)
	}
}

func TestNativeProbeReleaseWithoutCheckoutIsFetchableWithoutDownloading(t *testing.T) {
	withoutPigCheckout(t)
	download := &fakeModDownload{err: errors.New("probe must not download")}
	readiness := releaseBuilder("v9.8.7", download).Probe(context.Background(), hostRequest())
	if !readiness.Ready || readiness.Code != "source-fetchable" || !strings.Contains(readiness.Message, "PiG v9.8.7 source") {
		t.Fatalf("readiness = %+v", readiness)
	}
	if len(download.queries) != 0 {
		t.Fatalf("probe downloaded source: %v", download.queries)
	}
}

func TestNativeProbeCheckoutIsReadyWithoutFetch(t *testing.T) {
	download := &fakeModDownload{}
	readiness := releaseBuilder("v9.8.7", download).Probe(context.Background(), hostRequest())
	if !readiness.Ready || readiness.Code != "" {
		t.Fatalf("readiness = %+v", readiness)
	}
}

func TestSelectBuilderAutoSelectsFetchableNative(t *testing.T) {
	withoutPigCheckout(t)
	download := &fakeModDownload{}
	container := &fakeBuilder{name: "container", readiness: BuilderReadiness{Code: "engine-unavailable"}}
	selected, readiness, err := selectBuilder(context.Background(), []BuilderBackend{container, releaseBuilder("v9.8.7", download)}, "auto", hostRequest())
	if err != nil {
		t.Fatal(err)
	}
	if selected.Name() != "native" || readiness.Code != "source-fetchable" || len(download.queries) != 0 {
		t.Fatalf("selected=%s readiness=%+v downloads=%v", selected.Name(), readiness, download.queries)
	}
}

func TestResolveSourceFetchesExactlyTheRunningRelease(t *testing.T) {
	withoutPigCheckout(t)
	dir := fakeModuleTree(t)
	output, err := json.Marshal(map[string]any{
		"Path": pigModulePath, "Version": "v9.8.7", "Dir": dir, "Sum": "h1:abc=",
		"Origin": map[string]string{"VCS": "git", "Hash": "0123456789abcdef0123456789abcdef01234567"},
	})
	if err != nil {
		t.Fatal(err)
	}
	download := &fakeModDownload{output: output}
	var stderr bytes.Buffer
	source, err := releaseBuilder("v9.8.7", download).resolveSource(context.Background(), &stderr)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(download.queries, []string{"github.com/MichaelKinsy/PiG@v9.8.7"}) {
		t.Fatalf("queries = %v", download.queries)
	}
	if filepath.Dir(source.Root) != pigSourceCacheDir() || !isPigModule(source.Root) || source.ModuleVersion != "v9.8.7" || source.Revision != "0123456789abcdef0123456789abcdef01234567" {
		t.Fatalf("source = %+v", source)
	}
	if home := os.Getenv("PIG_HOME"); home == "" || !strings.HasPrefix(source.Root, home+string(filepath.Separator)) {
		t.Fatalf("staged source %s is outside the test PIG_HOME %q", source.Root, home)
	}
	if got := stderr.String(); got != "fetching PiG v9.8.7 source (cached after first build)\n" {
		t.Fatalf("stderr = %q", got)
	}
	revision, digest, err := pigSourceIdentity(source)
	if err != nil {
		t.Fatal(err)
	}
	if revision != source.Revision || !strings.HasPrefix(digest, "sha256:") {
		t.Fatalf("identity = %s %s", revision, digest)
	}
	again, err := releaseBuilder("v9.8.7", download).resolveSource(context.Background(), &bytes.Buffer{})
	if err != nil || again.Root != source.Root {
		t.Fatalf("second fetch = %+v, %v; want reuse of %s", again, err, source.Root)
	}
	env := source.buildEnv([]string{"GOWORK=" + filepath.Join(dir, "go.work"), "GOFLAGS=-mod=mod -modcacherw", "HOME=/h"})
	if !slices.Equal(env, []string{"HOME=/h", "GOWORK=off", "GOFLAGS=-modcacherw"}) {
		t.Fatalf("module source env = %v", env)
	}
}

func TestResolveSourceRevisionFallsBackToModuleVersion(t *testing.T) {
	source := pigSource{Root: fakeModuleTree(t), ModuleVersion: "v9.8.7"}
	revision, _, err := pigSourceIdentity(source)
	if err != nil || revision != "v9.8.7" {
		t.Fatalf("revision=%q err=%v", revision, err)
	}
}

func TestResolveSourcePrefersCheckout(t *testing.T) {
	download := &fakeModDownload{}
	var stderr bytes.Buffer
	source, err := releaseBuilder("v9.8.7", download).resolveSource(context.Background(), &stderr)
	if err != nil {
		t.Fatal(err)
	}
	if source.ModuleVersion != "" || len(download.queries) != 0 || stderr.Len() != 0 {
		t.Fatalf("source=%+v downloads=%v stderr=%q", source, download.queries, stderr.String())
	}
}

func TestResolveSourceDevBuildKeepsSourceUnavailableRemedy(t *testing.T) {
	withoutPigCheckout(t)
	download := &fakeModDownload{}
	_, err := releaseBuilder("", download).resolveSource(context.Background(), &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "source-unavailable") || !strings.Contains(err.Error(), sourceUnavailableRemedy) {
		t.Fatalf("error = %v", err)
	}
	if len(download.queries) != 0 {
		t.Fatalf("dev build downloaded source: %v", download.queries)
	}
}

func TestResolveSourceReportsDownloadFailureWithRemedy(t *testing.T) {
	withoutPigCheckout(t)
	download := &fakeModDownload{
		output: []byte(`{"Path":"github.com/MichaelKinsy/PiG","Version":"v9.8.7","Error":"github.com/MichaelKinsy/PiG@v9.8.7: invalid version: unknown revision v9.8.7"}`),
		err:    errors.New("exit status 1"),
	}
	_, err := releaseBuilder("v9.8.7", download).resolveSource(context.Background(), &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "unknown revision v9.8.7") || !strings.Contains(err.Error(), sourceUnavailableRemedy) {
		t.Fatalf("error = %v", err)
	}
}

func TestResolveSourceRejectsAnotherModule(t *testing.T) {
	withoutPigCheckout(t)
	// Each case carries a module sum so it is rejected by the identity check under test, not by
	// the later missing-sum check.
	cases := map[string]map[string]any{
		"path":    {"Path": "example.com/other", "Version": "v9.8.7", "Dir": fakeModuleTree(t), "Sum": "h1:abc="},
		"version": {"Path": pigModulePath, "Version": "v9.8.6", "Dir": fakeModuleTree(t), "Sum": "h1:abc="},
		"tree":    {"Path": pigModulePath, "Version": "v9.8.7", "Dir": t.TempDir(), "Sum": "h1:abc="},
	}
	for name, result := range cases {
		t.Run(name, func(t *testing.T) {
			output, err := json.Marshal(result)
			if err != nil {
				t.Fatal(err)
			}
			_, err = releaseBuilder("v9.8.7", &fakeModDownload{output: output}).resolveSource(context.Background(), &bytes.Buffer{})
			if err == nil || !strings.Contains(err.Error(), "source-unavailable") || !strings.Contains(err.Error(), "not the PiG v9.8.7 source") {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

// TestGoModDownloadFetchesPublishedPigSource runs the real `go mod download`
// against the configured module proxy for a published PiG release. It skips
// when the proxy is unreachable.
func TestGoModDownloadFetchesPublishedPigSource(t *testing.T) {
	if testing.Short() {
		t.Skip("network download")
	}
	conn, err := net.DialTimeout("tcp", "proxy.golang.org:443", 3*time.Second)
	if err != nil {
		t.Skipf("Go module proxy unreachable: %v", err)
	}
	_ = conn.Close()
	withoutPigCheckout(t)
	t.Setenv("GOMODCACHE", t.TempDir())
	t.Setenv("GOFLAGS", "-modcacherw")
	t.Setenv("GOPROXY", "https://proxy.golang.org")
	t.Setenv("GOSUMDB", "sum.golang.org")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	const published = "v0.3.1"
	builder := nativeBuilder{runningRelease: func() (string, bool) { return published, true }}
	var stderr bytes.Buffer
	source, err := builder.resolveSource(ctx, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	if source.ModuleVersion != published || !isPigModule(source.Root) || len(source.Revision) != 40 {
		t.Fatalf("source = %+v", source)
	}
	if _, err := os.Stat(filepath.Join(source.Root, "cmd", "pig", "main.go")); err != nil {
		t.Fatalf("fetched source lacks cmd/pig: %v", err)
	}
	if !strings.Contains(stderr.String(), "fetching PiG v0.3.1 source") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

// TestMaterializeModuleSourceNeverReplacesAPublishedTree runs concurrent first builds of one
// release. A build whose staging loses the publish race must reuse the winner's tree. It must
// not delete that tree, because the winner is already compiling from it.
func TestMaterializeModuleSourceNeverReplacesAPublishedTree(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	moduleDir := fakeModuleTree(t)
	for i := range 400 {
		path := filepath.Join(moduleDir, "pkg", fmt.Sprintf("f%03d.go", i))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("package pkg\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	const builds = 8
	type published struct {
		root   string
		marker os.FileInfo
		err    error
	}
	results := make([]published, builds)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range builds {
		wg.Go(func() {
			<-start
			root, err := materializeModuleSource(moduleDir, "v9.8.7", "h1:race=")
			if err != nil {
				results[i].err = err
				return
			}
			marker, err := os.Stat(filepath.Join(root, ".pig-source-sum"))
			results[i] = published{root: root, marker: marker, err: err}
		})
	}
	close(start)
	wg.Wait()
	for i, result := range results {
		if result.err != nil {
			t.Fatalf("build %d: %v", i, result.err)
		}
		current, err := os.Stat(filepath.Join(result.root, ".pig-source-sum"))
		if err != nil {
			t.Fatal(err)
		}
		if !os.SameFile(result.marker, current) {
			t.Fatalf("build %d's published tree %s was replaced by a later build", i, result.root)
		}
	}
}

func TestMaterializeModuleSourceReplacesAnUnpublishedTarget(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	moduleDir := fakeModuleTree(t)
	first, err := materializeModuleSource(moduleDir, "v9.8.7", "h1:leftover=")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(first, ".pig-source-sum")); err != nil {
		t.Fatal(err)
	}
	again, err := materializeModuleSource(moduleDir, "v9.8.7", "h1:leftover=")
	if err != nil || again != first {
		t.Fatalf("root=%s err=%v, want %s", again, err, first)
	}
	if data, err := os.ReadFile(filepath.Join(again, ".pig-source-sum")); err != nil || string(data) != "h1:leftover=" {
		t.Fatalf("marker = %q, %v", data, err)
	}
}

// TestRunningPigReleaseRequiresTheExactStampedVersion checks the release identity the fetch
// trusts. The test binary is a development build. Only a stamp equal to v<PigVersion> makes it
// a release, so a stale or foreign stamp can never fetch another version's source.
func TestRunningPigReleaseRequiresTheExactStampedVersion(t *testing.T) {
	saved := releaseSourceVersion
	t.Cleanup(func() { releaseSourceVersion = saved })
	want := "v" + coding.PigVersion
	cases := []struct {
		stamp   string
		release bool
	}{
		{stamp: "", release: false},
		{stamp: "v0.0.1", release: false},
		{stamp: coding.PigVersion, release: false},
		{stamp: want + "-dirty", release: false},
		{stamp: want, release: true},
		{stamp: " " + want + "\n", release: true},
	}
	for _, test := range cases {
		releaseSourceVersion = test.stamp
		version, ok := runningPigRelease()
		if ok != test.release || (ok && version != want) || (!ok && version != "") {
			t.Fatalf("stamp %q: runningPigRelease() = %q, %v; want release %v", test.stamp, version, ok, test.release)
		}
	}
}
