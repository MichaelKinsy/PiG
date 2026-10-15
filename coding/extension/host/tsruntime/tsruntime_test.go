package tsruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRequestFromPrefersEnvThenSetting(t *testing.T) {
	cases := []struct {
		env, setting string
		want         Request
	}{
		{"", "", Request{ID: Node, Source: SourceDefault}},
		{"", "bun", Request{ID: "bun", Source: SourceSetting}},
		{"aot", "bun", Request{ID: "aot", Source: SourceEnv}},
		{"  ", " bun ", Request{ID: "bun", Source: SourceSetting}},
	}
	for _, c := range cases {
		if got := RequestFrom(c.env, c.setting); got != c.want {
			t.Errorf("RequestFrom(%q, %q) = %+v, want %+v", c.env, c.setting, got, c.want)
		}
	}
}

func TestPlatformNamesPigTargets(t *testing.T) {
	cases := []struct {
		goos, goarch string
		termux       bool
		want         string
	}{
		{"linux", "amd64", false, "linux-x64"},
		{"linux", "arm64", false, "linux-arm64"},
		{"darwin", "arm64", false, "darwin-arm64"},
		{"darwin", "amd64", false, "darwin-x64"},
		{"windows", "amd64", false, "windows-x64"},
		{"windows", "arm64", false, "windows-arm64"},
		{"android", "arm64", false, "android-arm64"},
		{"linux", "amd64", true, "android-x64"},
	}
	for _, c := range cases {
		if got := platformOf(c.goos, c.goarch, c.termux); got != c.want {
			t.Errorf("platformOf(%s, %s, %v) = %s, want %s", c.goos, c.goarch, c.termux, got, c.want)
		}
	}
}

// Every lock entry names one of PiG's eight release targets with a digest; each target has at least one Bun executable.
func TestLockCoversEveryReleaseTarget(t *testing.T) {
	targets := map[string]bool{"linux-x64": false, "linux-arm64": false, "darwin-x64": false, "darwin-arm64": false, "android-x64": false, "android-arm64": false, "windows-x64": false, "windows-arm64": false}
	for _, entry := range Lock {
		if _, ok := targets[entry.Platform]; !ok {
			t.Errorf("lock entry %s names platform %s", entry.Variant, entry.Platform)
		}
		if !hexPattern.MatchString(entry.SHA256) || entry.Runtime != "bun" || entry.Version == "" {
			t.Errorf("lock entry %+v is malformed", entry)
		}
		targets[entry.Platform] = true
	}
	for target, covered := range targets {
		if !covered {
			t.Errorf("no Bun executable pinned for %s", target)
		}
	}
}

func TestParseManifestIsStrict(t *testing.T) {
	digest := strings.Repeat("a", 64)
	valid := `{"id":"bun","kind":"bun","version":"1.4.2","platform":"linux-x64","provider":"pig-bun","image":{"executable":"bun","sha256":"` + digest + `"}}`
	if _, err := ParseManifest("bun", []byte(valid)); err != nil {
		t.Fatalf("valid manifest: %v", err)
	}
	bad := map[string]string{
		"unknown field":   strings.Replace(valid, `"provider"`, `"extra":1,"provider"`, 1),
		"id mismatch":     strings.Replace(valid, `"id":"bun"`, `"id":"other"`, 1),
		"escaping path":   strings.Replace(valid, `"executable":"bun"`, `"executable":"../bun"`, 1),
		"absolute path":   strings.Replace(valid, `"executable":"bun"`, `"executable":"/bin/bun"`, 1),
		"bad digest":      strings.Replace(valid, digest, strings.Repeat("A", 64), 1),
		"unknown kind":    strings.Replace(valid, `"kind":"bun"`, `"kind":"deno"`, 1),
		"bun with sets":   strings.Replace(valid, `"image"`, `"sets":{"`+digest+`":{"executable":"x","sha256":"`+digest+`"}},"image"`, 1),
		"missing version": strings.Replace(valid, `"version":"1.4.2",`, ``, 1),
		"trailing data":   valid + "{}",
		"relative input":  strings.Replace(strings.Replace(valid, `"kind":"bun"`, `"kind":"executable"`, 1), `"sha256":"`+digest+`"}`, `"sha256":"`+digest+`","inputs":[{"path":"x","size":1,"mtimeNs":1}]}`, 1),
	}
	for name, data := range bad {
		if _, err := ParseManifest("bun", []byte(data)); err == nil {
			t.Errorf("%s: accepted %s", name, data)
		}
	}
}

type slot struct {
	t      *testing.T
	root   string
	probes int
	print  string
	fail   error
}

func newSlot(t *testing.T) *slot {
	return &slot{t: t, root: t.TempDir(), print: "1.4.2"}
}

func (s *slot) resolver(lock []LockEntry) *Resolver {
	v := NewVerifier(lock)
	v.probe = func(context.Context, string) (string, error) {
		s.probes++
		return s.print, s.fail
	}
	return &Resolver{Root: s.root, Platform: "linux-x64", verifier: v}
}

// install writes an executable with the given content and a manifest for it, and returns the executable's digest.
func (s *slot) install(id string, m Manifest, content string) string {
	s.t.Helper()
	dir := filepath.Join(s.root, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		s.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "exe"), []byte(content), 0o755); err != nil {
		s.t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(content))
	digest := hex.EncodeToString(sum[:])
	if m.Image != nil && m.Image.SHA256 == "" {
		m.Image.SHA256 = digest
	}
	data, err := json.Marshal(m)
	if err != nil {
		s.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ManifestFile), data, 0o644); err != nil {
		s.t.Fatal(err)
	}
	return digest
}

func bunManifest() Manifest {
	return Manifest{ID: "bun", Kind: KindBun, Version: "1.4.2", Platform: "linux-x64", Provider: "test", Image: &Image{Executable: "exe"}}
}

func bunLock(digest string) []LockEntry {
	return []LockEntry{{Runtime: "bun", Version: "1.4.2", Platform: "linux-x64", SHA256: digest}}
}

func reasonOf(sel Selection) Reason {
	if sel.Fallback == nil {
		return ""
	}
	return sel.Fallback.Reason
}

func TestResolveSelectsAVerifiedBunRuntime(t *testing.T) {
	s := newSlot(t)
	digest := s.install("bun", bunManifest(), "bun-binary")
	r := s.resolver(bunLock(digest))
	req := Request{ID: "bun", Source: SourceSetting}
	sel := r.Resolve(t.Context(), req, Set{})
	if sel.Fallback != nil || sel.Runtime != "bun" || sel.Kind != KindBun || sel.Executable != filepath.Join(s.root, "bun", "exe") || sel.Version != "1.4.2" {
		t.Fatalf("selection = %+v (fallback %+v)", sel, sel.Fallback)
	}
	// The digest and the start check run once per executable state.
	r.Resolve(t.Context(), req, Set{})
	if s.probes != 1 {
		t.Fatalf("probes = %d, want 1", s.probes)
	}
	// A changed executable is checked again: same size, new content and modification time.
	exe := filepath.Join(s.root, "bun", "exe")
	if err := os.WriteFile(exe, []byte("bun-binarY"), 0o755); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(time.Hour)
	if err := os.Chtimes(exe, future, future); err != nil {
		t.Fatal(err)
	}
	if got := reasonOf(r.Resolve(t.Context(), req, Set{})); got != ReasonVerifyFailed {
		t.Fatalf("changed executable reason = %q, want %q", got, ReasonVerifyFailed)
	}
}

func TestResolveFallsBackToNodeWithTypedReasons(t *testing.T) {
	s := newSlot(t)
	digest := s.install("bun", bunManifest(), "bun-binary")
	other := bunManifest()
	other.ID, other.Platform = "elsewhere", "darwin-arm64"
	s.install("elsewhere", other, "x")
	if err := os.MkdirAll(filepath.Join(s.root, "broken"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.root, "broken", ManifestFile), []byte(`{"id":"broken"`), 0o644); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		req  string
		lock []LockEntry
		want Reason
	}{
		{"node request", Node, nil, ""},
		{"invalid name", "../bun", bunLock(digest), ReasonInvalidRequest},
		{"not installed", "deno", bunLock(digest), ReasonNotInstalled},
		{"invalid manifest", "broken", bunLock(digest), ReasonInvalidManifest},
		{"other platform", "elsewhere", bunLock(digest), ReasonUnsupportedPlatform},
		{"not in lock", "bun", bunLock(strings.Repeat("0", 64)), ReasonVerifyFailed},
	}
	for _, c := range cases {
		sel := s.resolver(c.lock).Resolve(t.Context(), Request{ID: c.req, Source: SourceEnv}, Set{})
		if got := reasonOf(sel); got != c.want || sel.Runtime != Node {
			t.Errorf("%s: runtime %s reason %q, want node and %q (%+v)", c.name, sel.Runtime, got, c.want, sel.Fallback)
		}
	}
	// A manifest digest that differs from the executable fails verification even when the lock pins the manifest's digest.
	m := bunManifest()
	m.Image.SHA256 = strings.Repeat("b", 64)
	s.install("bun", m, "bun-binary")
	if got := reasonOf(s.resolver(bunLock(m.Image.SHA256)).Resolve(t.Context(), Request{ID: "bun"}, Set{})); got != ReasonVerifyFailed {
		t.Errorf("digest mismatch reason = %q", got)
	}
	// An executable that does not start, or reports another version, fails its start check.
	s.install("bun", bunManifest(), "bun-binary")
	s.fail = errors.New("exit status 1")
	if got := reasonOf(s.resolver(bunLock(digest)).Resolve(t.Context(), Request{ID: "bun"}, Set{})); got != ReasonStartFailed {
		t.Errorf("start failure reason = %q", got)
	}
	s.fail, s.print = nil, "1.4.3"
	if got := reasonOf(s.resolver(bunLock(digest)).Resolve(t.Context(), Request{ID: "bun"}, Set{})); got != ReasonStartFailed {
		t.Errorf("version mismatch reason = %q", got)
	}
	// No slot root: not installed.
	r := s.resolver(bunLock(digest))
	r.Root = ""
	if got := reasonOf(r.Resolve(t.Context(), Request{ID: "bun"}, Set{})); got != ReasonNotInstalled {
		t.Errorf("no root reason = %q", got)
	}
}

func TestResolveExecutableRuntimeImagesPerSet(t *testing.T) {
	s := newSlot(t)
	source := filepath.Join(t.TempDir(), "ext.ts")
	if err := os.WriteFile(source, []byte("export default () => {}"), 0o644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(source)
	if err != nil {
		t.Fatal(err)
	}
	members := []Member{{Name: "ext", Entry: source}}
	set := Set{Key: SetKey([]byte("tree"), members), Members: members, RuntimeTree: "/tree"}
	m := Manifest{ID: "aot", Kind: KindExecutable, Version: "0.7.4", Platform: "linux-x64", Provider: "test", Sets: map[string]Image{}}
	digest := s.install("aot", m, "image")
	m.Sets[set.Key] = Image{Executable: "exe", SHA256: digest, Inputs: []Input{{Path: source, Size: info.Size(), MtimeNs: info.ModTime().UnixNano()}}}
	s.install("aot", m, "image")
	r := s.resolver(nil)

	sel := r.Resolve(t.Context(), Request{ID: "aot"}, set)
	if sel.Fallback != nil || sel.Runtime != "aot" || sel.Kind != KindExecutable {
		t.Fatalf("selection = %+v (%+v)", sel, sel.Fallback)
	}
	if s.print = ""; s.probes != 1 {
		t.Fatalf("probes = %d", s.probes)
	}

	// Another set has no image: Node, and a build request for the runtime's provider.
	otherMembers := []Member{{Name: "other", Entry: source}}
	other := Set{Key: SetKey([]byte("tree"), otherMembers), Members: otherMembers, RuntimeTree: "/tree"}
	if got := reasonOf(r.Resolve(t.Context(), Request{ID: "aot"}, other)); got != ReasonNoImage {
		t.Fatalf("other set reason = %q", got)
	}
	var recorded Set
	data, err := os.ReadFile(filepath.Join(s.root, "aot", RequestsDir, other.Key+".json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &recorded); err != nil || recorded.Key != other.Key || len(recorded.Members) != 1 || recorded.Members[0] != otherMembers[0] || recorded.RuntimeTree != "/tree" {
		t.Fatalf("request = %s (%v)", data, err)
	}

	// An edited input makes the image stale and requests a rebuild.
	future := time.Now().Add(time.Hour)
	if err := os.Chtimes(source, future, future); err != nil {
		t.Fatal(err)
	}
	if got := reasonOf(r.Resolve(t.Context(), Request{ID: "aot"}, set)); got != ReasonStale {
		t.Fatalf("stale reason = %q", got)
	}
	if _, err := os.Stat(filepath.Join(s.root, "aot", RequestsDir, set.Key+".json")); err != nil {
		t.Fatalf("stale set has no build request: %v", err)
	}
}

func TestSetKeyDependsOnTreeAndOrderedMembers(t *testing.T) {
	a, b := Member{Name: "a", Entry: "/a.ts"}, Member{Name: "b", Entry: "/b.ts"}
	key := SetKey([]byte("tree"), []Member{a, b})
	for name, other := range map[string]string{
		"order":   SetKey([]byte("tree"), []Member{b, a}),
		"tree":    SetKey([]byte("tree2"), []Member{a, b}),
		"members": SetKey([]byte("tree"), []Member{a}),
		"joined":  SetKey([]byte("tree"), []Member{{Name: "a", Entry: "/a.ts\x00b"}, {Name: "/b.ts"}}),
	} {
		if other == key {
			t.Errorf("SetKey ignores the %s", name)
		}
	}
	if !hexPattern.MatchString(key) {
		t.Errorf("SetKey = %q", key)
	}
}

func TestListReportsEveryRuntimeDirectory(t *testing.T) {
	s := newSlot(t)
	s.install("bun", bunManifest(), "x")
	if err := os.MkdirAll(filepath.Join(s.root, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := List(s.root)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != "bun" || got[0].Manifest == nil || got[1].ID != "empty" || got[1].Error == "" {
		t.Fatalf("List = %+v", got)
	}
	if missing, err := List(filepath.Join(s.root, "missing")); err != nil || missing != nil {
		t.Fatalf("List(missing) = %v, %v", missing, err)
	}
}

// An executable runtime names a fallback slot runtime: PiG tries it before Node and records every skipped tier. A fallback's own
// fallback is not followed.
func TestResolveTriesTheManifestFallbackBeforeNode(t *testing.T) {
	s := newSlot(t)
	bunDigest := s.install("bun", bunManifest(), "bun-binary")
	aot := Manifest{ID: "aot", Kind: KindExecutable, Version: "1", Platform: "linux-x64", Provider: "test", Fallback: "bun", Sets: map[string]Image{strings.Repeat("e", 64): {Executable: "exe", SHA256: strings.Repeat("e", 64)}}}
	s.install("aot", aot, "image")
	members := []Member{{Name: "ext", Entry: "/ext.ts"}}
	set := Set{Key: SetKey([]byte("tree"), members), Members: members}

	sel := s.resolver(bunLock(bunDigest)).Resolve(t.Context(), Request{ID: "aot", Source: SourceSetting}, set)
	if sel.Runtime != "bun" || sel.Kind != KindBun || sel.Fallback == nil || sel.Fallback.Reason != ReasonNoImage || len(sel.Skipped) != 1 || sel.Skipped[0].Runtime != "aot" || sel.Skipped[0].Reason != ReasonNoImage {
		t.Fatalf("selection = %+v (fallback %+v, skipped %+v)", sel, sel.Fallback, sel.Skipped)
	}

	// Both tiers unusable: Node, with both reasons in order.
	sel = s.resolver(bunLock(strings.Repeat("0", 64))).Resolve(t.Context(), Request{ID: "aot"}, set)
	if !sel.IsNode() || len(sel.Skipped) != 2 || sel.Skipped[1].Runtime != "bun" || sel.Skipped[1].Reason != ReasonVerifyFailed || sel.Fallback.Reason != ReasonNoImage {
		t.Fatalf("selection = %+v (skipped %+v)", sel, sel.Skipped)
	}

	// The fallback's own fallback is not followed.
	middle := bunManifest()
	middle.ID, middle.Platform, middle.Fallback = "middle", "darwin-arm64", "bun"
	s.install("middle", middle, "x")
	aot.Fallback = "middle"
	s.install("aot", aot, "image")
	sel = s.resolver(bunLock(bunDigest)).Resolve(t.Context(), Request{ID: "aot"}, set)
	if !sel.IsNode() || len(sel.Skipped) != 2 || sel.Skipped[1].Runtime != "middle" || sel.Skipped[1].Reason != ReasonUnsupportedPlatform {
		t.Fatalf("selection = %+v (skipped %+v)", sel, sel.Skipped)
	}

	if _, err := ParseManifest("aot", []byte(`{"id":"aot","kind":"executable","version":"1","platform":"linux-x64","provider":"p","fallback":"aot","image":{"executable":"x","sha256":"`+strings.Repeat("a", 64)+`"}}`)); err == nil {
		t.Fatal("a manifest naming itself as fallback is valid")
	}
}

// A build request is written once per set and minute however often the set launches.
func TestResolveRecordsABuildRequestOncePerMinute(t *testing.T) {
	s := newSlot(t)
	s.install("aot", Manifest{ID: "aot", Kind: KindExecutable, Version: "1", Platform: "linux-x64", Provider: "test", Sets: map[string]Image{strings.Repeat("e", 64): {Executable: "exe", SHA256: strings.Repeat("e", 64)}}}, "image")
	members := []Member{{Name: "ext", Entry: "/ext.ts"}}
	set := Set{Key: SetKey([]byte("tree"), members), Members: members}
	r := s.resolver(nil)
	path := filepath.Join(s.root, "aot", RequestsDir, set.Key+".json")

	r.Resolve(t.Context(), Request{ID: "aot"}, set)
	if err := os.WriteFile(path, []byte("marker"), 0o644); err != nil {
		t.Fatal(err)
	}
	r.Resolve(t.Context(), Request{ID: "aot"}, set)
	if data, _ := os.ReadFile(path); string(data) != "marker" {
		t.Fatalf("a request written within the minute was rewritten: %s", data)
	}
	old := time.Now().Add(-2 * requestRefresh)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	r.Resolve(t.Context(), Request{ID: "aot"}, set)
	if data, _ := os.ReadFile(path); string(data) == "marker" {
		t.Fatal("a request older than the refresh interval was not rewritten")
	}
}

func TestExecutableManifestWithoutImagesIsValidAndRequestsBuilds(t *testing.T) {
	m := Manifest{ID: "aot", Kind: KindExecutable, Version: "0.7.4", Platform: "linux-x64", Provider: "test", Fallback: "bun"}
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseManifest("aot", data); err != nil {
		t.Fatalf("a provider that is installed and has built nothing yet: %v", err)
	}
	bun := bunManifest()
	bun.Image = nil
	data, _ = json.Marshal(bun)
	if _, err := ParseManifest("bun", data); err == nil {
		t.Error("a bun runtime without an image is valid")
	}
	s := newSlot(t)
	s.install("aot", m, "")
	members := []Member{{Name: "ext", Entry: "/x/ext.ts"}}
	set := Set{Key: SetKey([]byte("tree"), members), Members: members, RuntimeTree: "/tree"}
	if sel := s.resolver(nil).Resolve(t.Context(), Request{ID: "aot"}, set); sel.Runtime != Node || sel.Fallback == nil || sel.Fallback.Reason != ReasonNoImage {
		t.Fatalf("selection = %+v", sel)
	}
	if _, err := os.Stat(filepath.Join(s.root, "aot", RequestsDir, set.Key+".json")); err != nil {
		t.Errorf("a launch of a set with no image recorded no build request: %v", err)
	}
}

func TestResolveRecordsTheLastUseOfAnImageAtMostOncePerMinute(t *testing.T) {
	s := newSlot(t)
	members := []Member{{Name: "ext", Entry: "/x/ext.ts"}}
	set := Set{Key: SetKey([]byte("tree"), members), Members: members, RuntimeTree: "/tree"}
	m := Manifest{ID: "aot", Kind: KindExecutable, Version: "0.7.4", Platform: "linux-x64", Provider: "test", Sets: map[string]Image{}}
	digest := s.install("aot", m, "image")
	m.Sets[set.Key] = Image{Executable: "exe", SHA256: digest}
	s.install("aot", m, "image")
	r := s.resolver(nil)
	used := filepath.Join(s.root, "aot", UsedFile)
	if sel := r.Resolve(t.Context(), Request{ID: "aot"}, set); sel.Runtime != "aot" {
		t.Fatalf("selection = %+v", sel)
	}
	if _, err := os.Stat(used); err != nil {
		t.Fatalf("no use record: %v", err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(used, old, old); err != nil {
		t.Fatal(err)
	}
	r.Resolve(t.Context(), Request{ID: "aot"}, set)
	if second, _ := os.Stat(used); !second.ModTime().After(old.Add(time.Minute)) {
		t.Errorf("an hour-old use record was not refreshed: %v", second.ModTime())
	}
	fresh := time.Now().Add(-time.Second).Truncate(time.Millisecond)
	if err := os.Chtimes(used, fresh, fresh); err != nil {
		t.Fatal(err)
	}
	r.Resolve(t.Context(), Request{ID: "aot"}, set)
	if third, _ := os.Stat(used); !third.ModTime().Equal(fresh) {
		t.Error("a use record younger than a minute was rewritten")
	}
}

// A set that started only on Node is remembered until a file it was recorded from changes.
func TestNodeOnlyRecordLastsUntilAFileOfTheSetChanges(t *testing.T) {
	dir := t.TempDir()
	entry := filepath.Join(dir, "ext.ts")
	if err := os.WriteFile(entry, []byte("export default () => {}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	members := []Member{{Name: "ext", Entry: entry}}
	set := Set{Key: SetKey([]byte("tree"), members), Members: members}
	if _, ok := ReadNodeOnly(root, set); ok {
		t.Fatal("a set nobody recorded is Node-only")
	}
	if err := RecordNodeOnly(root, set, "node:inspector is not implemented"); err != nil {
		t.Fatal(err)
	}
	if detail, ok := ReadNodeOnly(root, set); !ok || detail != "node:inspector is not implemented" {
		t.Fatalf("ReadNodeOnly = %q, %v", detail, ok)
	}
	if listed := ListNodeOnly(root); len(listed) != 1 || listed[0].Members[0].Name != "ext" {
		t.Fatalf("ListNodeOnly = %+v", listed)
	}
	// A different set that holds the member is another set.
	if _, ok := ReadNodeOnly(root, Set{Key: strings.Repeat("c", 64), Members: members}); ok {
		t.Error("another set key read the record")
	}
	future := time.Now().Add(time.Hour)
	if err := os.Chtimes(filepath.Join(dir, "package.json"), future, future); err != nil {
		t.Fatal(err)
	}
	if _, ok := ReadNodeOnly(root, set); ok {
		t.Error("a record outlived a change to the package.json beside the entry")
	}
	if listed := ListNodeOnly(root); len(listed) != 0 {
		t.Errorf("ListNodeOnly kept a stale record: %+v", listed)
	}
}
