package aotruntime

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
  "version": "0.7.4",
  "platform": "linux-x64",
  "provider": "pig-aot/aot-runtime",
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
	if got, _ := st.cacheBudget(); got != defaultCacheBudget || defaultCacheBudget != 1<<30 {
		t.Errorf("default budget %d, want 1 GB", got)
	}
	if err := st.writeConfig(config{CacheMax: "2GB"}); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.cacheBudget(); got != 2<<30 {
		t.Errorf("configured budget %d", got)
	}
	t.Setenv(envCacheMax, "300MB")
	if got, _ := st.cacheBudget(); got != 300<<20 {
		t.Errorf("environment budget %d", got)
	}
}
