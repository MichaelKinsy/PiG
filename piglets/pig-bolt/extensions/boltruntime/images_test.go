package boltruntime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fakeBuilt(t *testing.T, content string, inputs []inputFile) *buildResult {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "out")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(dir, "pig-ts-host")
	if err := os.WriteFile(exe, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
	return &buildResult{Executable: exe, Dir: dir, Mode: modeLean, Inputs: inputs, Bytes: int64(len(content))}
}

func key(c string) string { return strings.Repeat(c, 64) }

func TestEnsureManifestWritesTheInertManifestPiGReads(t *testing.T) {
	st := newStore(t.TempDir(), "linux-x64")
	if err := st.ensureManifest(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(st.manifestPath())
	if err != nil {
		t.Fatal(err)
	}
	want := `{
  "id": "aot",
  "kind": "executable",
  "version": "1.1.2",
  "platform": "linux-x64",
  "provider": "pig-bolt/bolt-runtime",
  "fallback": "bun"
}
`
	if string(data) != want {
		t.Errorf("runtime.json =\n%s\nwant\n%s", data, want)
	}
	before, _ := os.Stat(st.manifestPath())
	time.Sleep(10 * time.Millisecond)
	if err := st.ensureManifest(); err != nil {
		t.Fatal(err)
	}
	if after, _ := os.Stat(st.manifestPath()); !after.ModTime().Equal(before.ModTime()) {
		t.Error("ensureManifest rewrote an unchanged manifest")
	}
}

func TestPublishPointsTheManifestAtTheImageAndReplacesAKeysImage(t *testing.T) {
	st := newStore(t.TempDir(), "linux-x64")
	inputs := []inputFile{{Path: "/ext/index.ts", Size: 10, MtimeNs: 20}}
	previous, err := st.publish(key("a"), fakeBuilt(t, "one", inputs))
	if err != nil || previous != "" {
		t.Fatalf("publish = %q, %v", previous, err)
	}
	m, err := st.readManifest()
	if err != nil {
		t.Fatal(err)
	}
	img := m.Sets[key("a")]
	if !strings.HasPrefix(img.Executable, "images/aaaaaaaaaaaaaaaa-") || !strings.HasSuffix(img.Executable, "/pig-ts-host") {
		t.Errorf("executable = %q", img.Executable)
	}
	if digest, _ := fileSHA256(filepath.Join(st.dir, filepath.FromSlash(img.Executable))); digest != img.SHA256 {
		t.Errorf("manifest digest %s, file %s", img.SHA256, digest)
	}
	if len(img.Inputs) != 1 || img.Inputs[0] != inputs[0] {
		t.Errorf("inputs = %v", img.Inputs)
	}
	st.now = func() time.Time { return time.Now().Add(time.Second) }
	previous, err = st.publish(key("a"), fakeBuilt(t, "two", nil))
	if err != nil || previous != filepath.Join(st.dir, filepath.FromSlash(filepath.Dir(img.Executable))) {
		t.Fatalf("second publish previous = %q, %v", previous, err)
	}
}

func TestPrunesLeastRecentlyUsedImagesToTheBudgetAndKeepsTheNewest(t *testing.T) {
	st := newStore(t.TempDir(), "linux-x64")
	base := time.Now().Add(-time.Hour)
	for i, k := range []string{"a", "b", "c"} {
		st.now = func() time.Time { return base.Add(time.Duration(i) * time.Minute) }
		if _, err := st.publish(key(k), fakeBuilt(t, strings.Repeat(k, 100), nil)); err != nil {
			t.Fatal(err)
		}
	}
	// A launch of the oldest image makes it the most recently used.
	imgs, _ := st.images()
	for _, set := range imgs {
		if set.Key == key("a") {
			now := time.Now()
			if err := os.Chtimes(filepath.Join(set.Dir, ".used"), now, now); err != nil {
				t.Fatal(err)
			}
		}
	}
	// An orphan directory from an interrupted publish goes first.
	orphan := filepath.Join(st.imagesDir(), "orphan")
	if err := os.MkdirAll(orphan, 0o755); err != nil {
		t.Fatal(err)
	}
	removed, err := st.prune(250, key("c"))
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 1 || removed[0] != key("b") {
		t.Fatalf("removed %v, want only the least recently used image b", removed)
	}
	if _, err := os.Stat(orphan); err == nil {
		t.Error("the orphan directory survived")
	}
	m, _ := st.readManifest()
	if _, ok := m.Sets[key("b")]; ok || len(m.Sets) != 2 {
		t.Errorf("manifest sets = %v", sortedKeys(m.Sets))
	}
	// A budget of zero still keeps the image of the set just built.
	removed, err = st.prune(0, key("c"))
	if err != nil || len(removed) != 1 || removed[0] != key("a") {
		t.Fatalf("zero budget removed %v, %v; want a only", removed, err)
	}
	if m, _ := st.readManifest(); len(m.Sets) != 1 || m.Sets[key("c")].Executable == "" {
		t.Errorf("the kept image is gone: %v", sortedKeys(m.Sets))
	}
}

func TestFailureMemoHoldsWhileTheInputsAreUnchanged(t *testing.T) {
	st := newStore(t.TempDir(), "linux-x64")
	inputs := []inputFile{{Path: "/ext/index.ts", Size: 10, MtimeNs: 20}}
	if err := st.recordFailure(key("a"), failure{Reason: "build_failed", Detail: "boom", Inputs: inputs}); err != nil {
		t.Fatal(err)
	}
	if f, ok := st.knownFailure(key("a"), inputs); !ok || f.Detail != "boom" {
		t.Fatalf("knownFailure = %+v %v", f, ok)
	}
	changed := []inputFile{{Path: "/ext/index.ts", Size: 11, MtimeNs: 20}}
	if _, ok := st.knownFailure(key("a"), changed); ok {
		t.Fatal("a changed input kept the memo")
	}
	if _, ok := st.knownFailure(key("a"), inputs); ok {
		t.Fatal("the memo outlived the change that invalidated it")
	}
}

func TestParseSizeAndBudget(t *testing.T) {
	for text, want := range map[string]int64{"1GB": 1 << 30, "1g": 1 << 30, "512MB": 512 << 20, "1.5GB": 3 << 29, "100": 100, "0": 0, "2 KB": 2048} {
		if got, err := parseSize(text); err != nil || got != want {
			t.Errorf("parseSize(%q) = %d, %v; want %d", text, got, err, want)
		}
	}
	for _, text := range []string{"", "lots", "-1GB", "GB", "inf", "+Inf", "infGB", "nan", "NaN MB", "1e30GB", "9223372036854775807"} {
		if _, err := parseSize(text); err == nil {
			t.Errorf("parseSize(%q) succeeded", text)
		}
	}
	st := newStore(t.TempDir(), "linux-x64")
	if got, _ := st.cacheBudget(); got != defaultCacheBudget || defaultCacheBudget != 2<<30 {
		t.Errorf("default budget %d, want 2 GB", got)
	}
	if err := st.writeConfig(config{CacheMax: "3GB"}); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.cacheBudget(); got != 3<<30 {
		t.Errorf("configured budget %d", got)
	}
	t.Setenv(envCacheMax, "300MB")
	if got, _ := st.cacheBudget(); got != 300<<20 {
		t.Errorf("environment budget %d", got)
	}
}

// A manifest written for an earlier compiler release loses its images: the sets are requested again and built by the pinned compiler. A manifest of
// this release keeps its images when only the provider name differs (the Piglet's rename from pig-aot).
func TestEnsureManifestDropsImagesOfAnotherCompilerRelease(t *testing.T) {
	st := newStore(t.TempDir(), "linux-x64")
	inputs := []inputFile{{Path: "/ext/index.ts", Size: 10, MtimeNs: 20}}
	if _, err := st.publish(key("a"), fakeBuilt(t, "one", inputs)); err != nil {
		t.Fatal(err)
	}
	sets, err := st.images()
	if err != nil || len(sets) != 1 {
		t.Fatalf("images = %v, %v", sets, err)
	}
	dir := sets[0].Dir

	rewrite := func(edit func(*manifest)) {
		t.Helper()
		if err := st.withManifest(func(m *manifest) (bool, error) { edit(m); return true, nil }); err != nil {
			t.Fatal(err)
		}
	}
	rewrite(func(m *manifest) { m.Provider = "pig-aot/aot-runtime" })
	if err := st.ensureManifest(); err != nil {
		t.Fatal(err)
	}
	if m, _ := st.readManifest(); m.Provider != providerName || len(m.Sets) != 1 {
		t.Fatalf("after the provider rename the manifest is %+v, want the new provider and the image kept", m)
	}

	rewrite(func(m *manifest) { m.Version = "0.7.4" })
	if err := st.ensureManifest(); err != nil {
		t.Fatal(err)
	}
	m, _ := st.readManifest()
	if m.Version != toolchainVersion || len(m.Sets) != 0 {
		t.Errorf("manifest = %+v, want version %s and no sets", m, toolchainVersion)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("the image of the earlier compiler stayed on disk: %v", err)
	}
}

// publish, prune and ensureManifest remove an image's directory named by runtime.json, so a manifest whose executable leaves images/<id>/ is refused
// before any of them can delete the provider directory or a parent.
func TestReadManifestRefusesAnImagePathOutsideImages(t *testing.T) {
	for _, executable := range []string{"", "pig-ts-host", "images/pig-ts-host", "../x/pig-ts-host", "images/../../x", "images/./pig-ts-host", "images//pig-ts-host", "/images/a/pig-ts-host", "images/a/b/pig-ts-host", `images\a\pig-ts-host`} {
		st := newStore(t.TempDir(), "linux-x64")
		if err := os.MkdirAll(st.dir, 0o755); err != nil {
			t.Fatal(err)
		}
		data := `{"id":"aot","kind":"executable","version":"` + toolchainVersion + `","platform":"linux-x64","provider":"` + providerName + `","sets":{"` + key("a") + `":{"executable":"` + strings.ReplaceAll(executable, `\`, `\\`) + `","sha256":"` + key("b") + `"}},"fallback":"bun"}`
		if err := os.WriteFile(st.manifestPath(), []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := st.readManifest(); err == nil || !strings.Contains(err.Error(), "images/<id>/<file>") {
			t.Errorf("executable %q: readManifest error = %v, want a refusal", executable, err)
		}
		if err := st.ensureManifest(); err == nil {
			t.Errorf("executable %q: ensureManifest accepted the manifest", executable)
		}
		if _, err := os.Stat(st.dir); err != nil {
			t.Errorf("executable %q: the provider directory is gone: %v", executable, err)
		}
	}
}
