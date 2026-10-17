package main

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"crypto/sha512"
	"debug/elf"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

func npmIntegrity(data []byte) string {
	sum := sha512.Sum512(data)
	return "sha512-" + base64.StdEncoding.EncodeToString(sum[:])
}

// fakePinnedSource writes a frozen durable closure for commit under root and the packs it names under packs.
func fakePinnedSource(t *testing.T, root, packs, commit string) map[string][]byte {
	t.Helper()
	return fakeReference(t, root, packs, commit, durableReference, "1.2.3")
}

// fakeReference writes ref's frozen closure for commit under root, installing ref.pkg and pi-ai at version from their
// packs, which it writes under packs, and one registry package.
func fakeReference(t *testing.T, root, packs, commit string, ref piReference, version string) map[string][]byte {
	t.Helper()
	pkgPack, aiPack := packFile(ref.pkg, version), packFile("@earendil-works/pi-ai", version)
	tarballs := map[string][]byte{pkgPack: []byte(ref.dir + " pack"), aiPack: []byte("ai pack")}
	if err := os.MkdirAll(packs, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, data := range tarballs {
		if err := os.WriteFile(filepath.Join(packs, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	lock := map[string]any{"name": "pi-oracle", "version": "0.0.0", "lockfileVersion": 3, "packages": map[string]any{
		"":                        map[string]any{"name": "pi-oracle", "version": "0.0.0", "devDependencies": map[string]any{ref.pkg: version, "typebox": "1.3.27"}},
		"node_modules/" + ref.pkg: map[string]any{"version": version, "resolved": packPrefix + ref.pkg, "integrity": npmIntegrity(tarballs[pkgPack])},
		"node_modules/" + ref.pkg + "/node_modules/@earendil-works/pi-ai": map[string]any{"version": version, "resolved": packPrefix + "@earendil-works/pi-ai", "integrity": npmIntegrity(tarballs[aiPack])},
		"node_modules/typebox": map[string]any{"version": "1.3.27", "resolved": "https://registry.npmjs.org/typebox/-/typebox-1.3.27.tgz", "integrity": "sha512-registry"},
	}}
	data, err := json.Marshal(lock)
	if err != nil {
		t.Fatal(err)
	}
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	if _, err := zw.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	frozen := filepath.Join(root, "automation", "pin", "frozen")
	if err := os.MkdirAll(frozen, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(frozen, commit+"."+ref.closure+".json.gz"), gz.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return tarballs
}

// `k8sbench pi-packages -out` writes both projects of the committed pin, each with its version, from the build store's
// packs; -check checks an installed coding agent; one of the two is required.
func TestCmdPiPackages(t *testing.T) {
	pin := committedPiPin()
	if pin.isRelease() {
		t.Skip("the commit pin path")
	}
	root, store, out := t.TempDir(), t.TempDir(), filepath.Join(t.TempDir(), "pi")
	for _, ref := range []piReference{durableReference, agentReference} {
		fakeReference(t, root, filepath.Join(store, pin.Commit, ref.packs), pin.Commit, ref, pin.PackageVersion)
	}
	var stdout bytes.Buffer
	if err := cmdPiPackages([]string{"-repo", root, "-pi-builds", store, "-out", out}, &stdout, func(string) string { return "" }); err != nil {
		t.Fatal(err)
	}
	for _, ref := range []piReference{durableReference, agentReference} {
		if v, err := os.ReadFile(filepath.Join(out, ref.dir, "VERSION")); err != nil || string(v) != pin.Version+"\n" {
			t.Errorf("%s/VERSION %q %v", ref.dir, v, err)
		}
		if _, err := os.Stat(filepath.Join(out, ref.dir, "packs", packFile(ref.pkg, pin.PackageVersion))); err != nil {
			t.Errorf("%s: %v", ref.dir, err)
		}
	}
	if want := "pi-durable " + pin.Version + "\npi-agent " + pin.Version + "\n"; stdout.String() != want {
		t.Errorf("stdout %q, want %q", stdout.String(), want)
	}
	if err := cmdPiPackages([]string{"-check", out}, io.Discard, func(string) string { return "" }); err == nil {
		t.Error("-check passed a directory without an installed coding agent")
	}
	if err := cmdPiPackages(nil, io.Discard, func(string) string { return "" }); err == nil || !strings.Contains(err.Error(), "one of -out and -check") {
		t.Errorf("no flag: %v", err)
	}
}

// At a commit pin the reference is the pinned source's pi-durable: the install project is the frozen durable closure with
// every packed package resolved to its pack, copied beside it, and the registry packages left as frozen.
func TestPiDurableInstallAtACommitPin(t *testing.T) {
	commit := strings.Repeat("ab", 20)
	pin := piPin{Ref: commit, Commit: commit, PackageVersion: "1.2.3", Version: "1.2.3+abababab"}
	root, packs, out := t.TempDir(), filepath.Join(t.TempDir(), "durable-packs"), filepath.Join(t.TempDir(), "pi-durable")
	tarballs := fakePinnedSource(t, root, packs, commit)
	version, err := writePinnedInstall(root, out, packs, durableReference, pin)
	if err != nil {
		t.Fatal(err)
	}
	if version != "1.2.3+abababab" {
		t.Errorf("version %q", version)
	}
	var manifest struct {
		DevDependencies map[string]string `json:"devDependencies"`
	}
	data, err := os.ReadFile(filepath.Join(out, "package.json"))
	if err != nil || json.Unmarshal(data, &manifest) != nil {
		t.Fatalf("package.json %s: %v", data, err)
	}
	if manifest.DevDependencies["@earendil-works/pi-durable"] != "1.2.3" || manifest.DevDependencies["typebox"] != "1.3.27" {
		t.Errorf("package.json dependencies %v", manifest.DevDependencies)
	}
	var lock struct {
		Packages map[string]struct{ Resolved, Integrity string } `json:"packages"`
	}
	data, err = os.ReadFile(filepath.Join(out, "package-lock.json"))
	if err != nil || json.Unmarshal(data, &lock) != nil {
		t.Fatalf("package-lock.json %s: %v", data, err)
	}
	for key, want := range map[string]string{
		"node_modules/@earendil-works/pi-durable":                                    "file:packs/earendil-works-pi-durable-1.2.3.tgz",
		"node_modules/@earendil-works/pi-durable/node_modules/@earendil-works/pi-ai": "file:packs/earendil-works-pi-ai-1.2.3.tgz",
		"node_modules/typebox": "https://registry.npmjs.org/typebox/-/typebox-1.3.27.tgz",
	} {
		if got := lock.Packages[key].Resolved; got != want {
			t.Errorf("%s resolves %q, want %q", key, got, want)
		}
	}
	if got := lock.Packages["node_modules/@earendil-works/pi-durable"].Integrity; got != npmIntegrity(tarballs["earendil-works-pi-durable-1.2.3.tgz"]) {
		t.Errorf("pi-durable integrity %q", got)
	}
	for name, want := range tarballs {
		if got, err := os.ReadFile(filepath.Join(out, "packs", name)); err != nil || !bytes.Equal(got, want) {
			t.Errorf("packs/%s: %q %v", name, got, err)
		}
	}
}

// A pack that is not the bytes the closure was frozen with, or no packs at all, is refused with the command that builds them.
func TestPiDurableInstallRefusesOtherPacks(t *testing.T) {
	commit := strings.Repeat("cd", 20)
	pin := piPin{Ref: commit, Commit: commit, PackageVersion: "1.2.3", Version: "1.2.3+cdcdcdcd"}
	root, packs := t.TempDir(), filepath.Join(t.TempDir(), "durable-packs")
	fakePinnedSource(t, root, packs, commit)
	if err := os.WriteFile(filepath.Join(packs, "earendil-works-pi-ai-1.2.3.tgz"), []byte("another build"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := writePinnedInstall(root, filepath.Join(t.TempDir(), "out"), packs, durableReference, pin)
	if err == nil || !strings.Contains(err.Error(), "no pack earendil-works-pi-ai-1.2.3.tgz") || !strings.Contains(err.Error(), "pin.py install-oracle") {
		t.Errorf("rebuilt pack: %v", err)
	}
	_, err = writePinnedInstall(root, filepath.Join(t.TempDir(), "out"), t.TempDir(), durableReference, pin)
	if err == nil || !strings.Contains(err.Error(), "no packs of the pinned Pi") {
		t.Errorf("no packs: %v", err)
	}
	other := piPin{Ref: strings.Repeat("ef", 20), Commit: strings.Repeat("ef", 20), PackageVersion: "1.2.3", Version: "1.2.3+efefefef"}
	if _, err := writePinnedInstall(root, filepath.Join(t.TempDir(), "out"), packs, durableReference, other); err == nil || !strings.Contains(err.Error(), "frozen closure for pi-durable") {
		t.Errorf("no frozen closure: %v", err)
	}
}

// At a release pin the reference is the published packages of bench/k8s/image/pi-durable's lockfile.
func TestPiDurableInstallAtAReleasePin(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	out := t.TempDir()
	version, err := writePinnedInstall(root, out, "/nonexistent", durableReference, piPin{Ref: "v1.1.0", Commit: strings.Repeat("0", 40), PackageVersion: "1.1.0", Version: "1.1.0"})
	if err != nil || version != "1.1.0" {
		t.Fatalf("%q %v", version, err)
	}
	for _, f := range []string{"package.json", "package-lock.json"} {
		want, _ := os.ReadFile(filepath.Join(root, "bench", "k8s", "image", "pi-durable", f))
		if got, err := os.ReadFile(filepath.Join(out, f)); err != nil || !bytes.Equal(got, want) {
			t.Errorf("%s differs from bench/k8s/image/pi-durable: %v", f, err)
		}
	}
	if _, err := os.Stat(filepath.Join(out, "packs")); !os.IsNotExist(err) {
		t.Errorf("a release install has packs: %v", err)
	}
}

// The committed pin names a reference the bundle and the image can install: a release's lockfile, or a commit's frozen
// durable closure that takes pi-durable, pi-ai and chord from the pinned source at the pinned package version.
func TestCommittedPinHasADurableReference(t *testing.T) {
	pin := committedPiPin()
	if pin.Version != pigversion.UpstreamVersion {
		t.Fatalf("pin %+v", pin)
	}
	if pin.isRelease() {
		t.Skip("a release pin installs bench/k8s/image/pi-durable (TestImageReferenceIsThePinnedPiVersion)")
	}
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "automation", "pin", "frozen", pin.Commit+".durable-closure.json.gz"))
	if err != nil {
		t.Fatal(err)
	}
	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	var lock struct {
		Packages map[string]struct {
			Version, Resolved, Integrity string
			DevDependencies              map[string]string
		} `json:"packages"`
	}
	if err := json.NewDecoder(zr).Decode(&lock); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"@earendil-works/pi-durable", "@earendil-works/pi-ai", "@earendil-works/chord"} {
		entry := lock.Packages["node_modules/"+name]
		if entry.Resolved != packPrefix+name || entry.Version != pin.PackageVersion || !strings.HasPrefix(entry.Integrity, "sha512-") {
			t.Errorf("%s: %+v, want the pinned source's pack at %s with its integrity", name, entry, pin.PackageVersion)
		}
		if got := lock.Packages[""].DevDependencies[name]; got != pin.PackageVersion {
			t.Errorf("the closure's root needs %s %q", name, got)
		}
	}
}

// The image installs the same projects: build.sh writes them with `k8sbench pi-packages` and passes them to both builds as
// the context pi, which the build stage mounts and installs from, and the build checks the coding agent with k8sbench's pins.
func TestImageInstallsThePinnedReferences(t *testing.T) {
	dockerfile, err := os.ReadFile(filepath.Join("..", "..", "image", "Dockerfile"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"--mount=type=bind,from=pi,target=/pi", "cp /pi/pi-durable/package.json /pi/pi-durable/package-lock.json durable/interop/bench.mjs /opt/pig/pi-durable/",
		"cp -r /pi/pi-durable/packs /opt/pig/pi-durable/", `echo "pi-durable=$(cat /pi/pi-durable/VERSION)"`, "pi-durable/package-lock.json $(ls pi-durable/packs/*.tgz 2>/dev/null)",
		"cp /pi/pi-agent/package.json /pi/pi-agent/package-lock.json /opt/pig/pi-agent/", "cp -r /pi/pi-agent/packs /opt/pig/pi-agent/", "/opt/pig/bin/k8sbench pi-packages -check /opt/pig",
		`echo "pi-agent=$(cat /pi/pi-agent/VERSION)"`, "pi-agent/package-lock.json $(ls pi-agent/packs/*.tgz 2>/dev/null) " + piAgentCLI} {
		if !strings.Contains(string(dockerfile), want) {
			t.Errorf("bench/k8s/image/Dockerfile does not carry %s", want)
		}
	}
	for _, dir := range []string{"pi-durable", "pi-agent"} {
		if strings.Contains(string(dockerfile), "bench/k8s/image/"+dir+"/package") {
			t.Errorf("bench/k8s/image/Dockerfile installs bench/k8s/image/%s whatever the pin", dir)
		}
	}
	script, err := os.ReadFile(filepath.Join("..", "..", "image", "build.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(script), `go run ./bench/k8s/cmd/k8sbench pi-packages -out "$pi"`) || strings.Count(string(script), `--build-context pi="$pi"`) != 2 {
		t.Error("bench/k8s/image/build.sh does not give both builds the pinned Pi projects")
	}
}

// At a commit pin Pi's coding agent comes from the pinned source too: the install project is a pure function of the frozen
// closure, so its sha256 is the one sourceAgentPins records, and every Pi package is resolved to its pack.
func TestCommittedPinHasAgentPins(t *testing.T) {
	pin := committedPiPin()
	pins, err := pinnedAgentPins(pin)
	if err != nil {
		t.Fatal(err)
	}
	if pin.isRelease() {
		if !maps.Equal(pins.files, publishedAgentPins.files) || pins.tree != publishedAgentPins.tree {
			t.Error("a release pin does not check the published install")
		}
		return
	}
	root := filepath.Join("..", "..", "..", "..")
	manifest, lock, packs, err := pinnedProject(root, agentReference, pin)
	if err != nil {
		t.Fatal(err)
	}
	for path, data := range map[string][]byte{"pi-agent/package.json": manifest, "pi-agent/package-lock.json": lock} {
		if got := fmt.Sprintf("%x", sha256.Sum256(data)); got != pins.files[path] {
			t.Errorf("%s from the frozen closure has sha256 %s, sourceAgentPins[%s] says %s", path, got, pin.Commit[:8], pins.files[path])
		}
	}
	for _, name := range []string{"@earendil-works/pi-coding-agent", "@earendil-works/pi-ai", "@earendil-works/pi-tui", "@earendil-works/pi-agent-core"} {
		if _, ok := packs[packFile(name, pin.PackageVersion)]; !ok {
			t.Errorf("the coding agent's project does not install %s from its pack (packs %v)", name, slices.Sorted(maps.Keys(packs)))
		}
	}
	if pins.tree == publishedAgentPins.tree || pins.files["pi-agent/package-lock.json"] == publishedAgentPins.files["pi-agent/package-lock.json"] {
		t.Error("the commit pin's package tree or lockfile is the published release's")
	}
	if !maps.Equal(piAgentPins, pins.files) || piAgentPackagesTree != pins.tree {
		t.Error("checkPiAgent does not apply the committed pin's pins")
	}
}

func TestPackFile(t *testing.T) {
	if got := packFile("@earendil-works/pi-coding-agent", "1.1.0"); got != "earendil-works-pi-coding-agent-1.1.0.tgz" {
		t.Errorf("packFile: %s", got)
	}
}

// A closure that does not take the reference's own package from the pinned source is refused: it would install a
// published build under the pin's name.
func TestPinnedProjectNeedsThePackageFromItsPack(t *testing.T) {
	commit := strings.Repeat("12", 20)
	pin := piPin{Ref: commit, Commit: commit, PackageVersion: "1.2.3", Version: "1.2.3+12121212"}
	root := t.TempDir()
	fakeReference(t, root, t.TempDir(), commit, piReference{dir: "pi-durable", closure: durableReference.closure, pkg: "@earendil-works/pi-other"}, "1.2.3")
	if _, _, _, err := pinnedProject(root, durableReference, pin); err == nil || !strings.Contains(err.Error(), "does not install @earendil-works/pi-durable from the pinned source") {
		t.Errorf("closure without a pi-durable pack: %v", err)
	}
}

// The installed reference must report the pin's package version (the bundle checks pi-durable, the image's -check both).
func TestCheckInstalledVersion(t *testing.T) {
	root := fakeBundle(t, elf.EM_X86_64)
	pin := piPin{Ref: "v1.1.0", Commit: strings.Repeat("0", 40), PackageVersion: "1.1.0", Version: "1.1.0"}
	if err := checkInstalledVersion(root, durableReference, pin); err != nil {
		t.Errorf("pi-durable 1.1.0 at pin 1.1.0: %v", err)
	}
	pin.PackageVersion = "1.2.0"
	if err := checkInstalledVersion(root, durableReference, pin); err == nil || !strings.Contains(err.Error(), "the installed @earendil-works/pi-durable is 1.1.0, the Pi pin is 1.2.0") {
		t.Errorf("another version: %v", err)
	}
	if err := checkInstalledVersion(root, agentReference, pin); err == nil || !os.IsNotExist(err) {
		t.Errorf("no coding agent: %v", err)
	}
	if err := cmdPiPackages([]string{"-check", root}, io.Discard, func(string) string { return "" }); err == nil {
		t.Error("-check passed a root without a coding agent")
	}
	// fakeAgentBundle's coding agent passes its pins but reports 9.8.7; its pi-durable is set to the committed pin, which -check reads.
	agents := fakeAgentBundle(t)
	durable := filepath.Join(agents, "pi-durable", "node_modules", "@earendil-works", "pi-durable", "package.json")
	if err := os.WriteFile(durable, []byte(`{"name":"@earendil-works/pi-durable","version":"`+pigversion.UpstreamPackageVersion+`"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := cmdPiPackages([]string{"-check", agents}, io.Discard, func(string) string { return "" }); err == nil || !strings.Contains(err.Error(), "the installed @earendil-works/pi-coding-agent is 9.8.7") {
		t.Errorf("-check of another coding agent version: %v", err)
	}
	// The image's -check also checks pi-durable, before the coding agent.
	if err := os.WriteFile(durable, []byte(`{"name":"@earendil-works/pi-durable","version":"0.0.1"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := cmdPiPackages([]string{"-check", agents}, io.Discard, func(string) string { return "" }); err == nil || !strings.Contains(err.Error(), "the installed @earendil-works/pi-durable is 0.0.1") {
		t.Errorf("-check of another pi-durable version: %v", err)
	}
}

// One pack file may stand for one content only: a closure that packs the same name and version with two integrities
// cannot name both in packs/.
func TestPinnedProjectRefusesOnePackWithTwoIntegrities(t *testing.T) {
	commit := strings.Repeat("34", 20)
	pin := piPin{Ref: commit, Commit: commit, PackageVersion: "1.2.3", Version: "1.2.3+34343434"}
	root := t.TempDir()
	entry := func(integrity string) map[string]any {
		return map[string]any{"version": "1.2.3", "resolved": packPrefix + "@earendil-works/pi-durable", "integrity": integrity}
	}
	closure := map[string]any{"name": "x", "version": "0.0.0", "packages": map[string]any{
		"": map[string]any{"devDependencies": map[string]any{"@earendil-works/pi-durable": "1.2.3"}},
		"node_modules/@earendil-works/pi-durable":                entry("sha512-AAAA"),
		"node_modules/a/node_modules/@earendil-works/pi-durable": entry("sha512-BBBB"),
	}}
	raw, err := json.Marshal(closure)
	if err != nil {
		t.Fatal(err)
	}
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	if _, err := zw.Write(raw); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	frozen := filepath.Join(root, "automation", "pin", "frozen", commit+"."+durableReference.closure+".json.gz")
	if err := os.MkdirAll(filepath.Dir(frozen), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(frozen, gz.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := pinnedProject(root, durableReference, pin); err == nil || !strings.Contains(err.Error(), "packs earendil-works-pi-durable-1.2.3.tgz twice with different integrity") {
		t.Errorf("one pack with two integrities: %v", err)
	}
}

// ARTIFACTS.sha256 lists each reference's packs after its lockfile, so the runner checks the bytes a commit pin installed.
func TestArtifactListHasThePacks(t *testing.T) {
	dir := fakeAgentBundle(t)
	for _, f := range []string{"pi-durable/packs/earendil-works-pi-durable-1.1.0.tgz", "pi-agent/packs/earendil-works-pi-coding-agent-1.1.0.tgz", "pi-agent/packs/earendil-works-pi-ai-1.1.0.tgz"} {
		if err := os.MkdirAll(filepath.Join(dir, filepath.Dir(f)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, f), []byte(f), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := writeArtifactList(dir); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "ARTIFACTS.sha256"))
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for line := range strings.Lines(string(data)) {
		if _, path, ok := strings.Cut(strings.TrimSpace(line), "  "); ok && strings.HasPrefix(path, "pi-") {
			paths = append(paths, path)
		}
	}
	want := []string{"pi-durable/bench.mjs", "pi-durable/package-lock.json", "pi-durable/packs/earendil-works-pi-durable-1.1.0.tgz",
		"pi-agent/package-lock.json", "pi-agent/packs/earendil-works-pi-ai-1.1.0.tgz", "pi-agent/packs/earendil-works-pi-coding-agent-1.1.0.tgz", piAgentCLI}
	if !slices.Equal(paths, want) {
		t.Errorf("ARTIFACTS.sha256 lists\n%v\nwant\n%v", paths, want)
	}
	if _, err := inspectBundle(dir); err != nil {
		t.Errorf("a bundle with packs: %v", err)
	}
}

// checkPiAgent fails closed: a commit pin without sourceAgentPins refuses every install instead of checking nothing.
func TestCheckPiAgentRefusesACommitWithoutPins(t *testing.T) {
	pin := committedPiPin()
	if pin.isRelease() {
		t.Skip("the commit pin path")
	}
	savedPins, savedTree, savedSource := piAgentPins, piAgentPackagesTree, sourceAgentPins
	t.Cleanup(func() { piAgentPins, piAgentPackagesTree, sourceAgentPins = savedPins, savedTree, savedSource })
	piAgentPins, piAgentPackagesTree, sourceAgentPins = nil, "", map[string]agentPins{}
	if err := checkPiAgent(t.TempDir()); err == nil || !strings.Contains(err.Error(), "no pins for Pi's coding agent built from commit "+pin.Commit) {
		t.Errorf("no pins: %v", err)
	}
}

// rewriteClosure edits ref's frozen closure for commit under root.
func rewriteClosure(t *testing.T, root, commit string, ref piReference, edit func(packages map[string]any)) {
	t.Helper()
	frozen := filepath.Join(root, "automation", "pin", "frozen", commit+"."+ref.closure+".json.gz")
	compressed, err := os.ReadFile(frozen)
	if err != nil {
		t.Fatal(err)
	}
	zr, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		t.Fatal(err)
	}
	var lock map[string]any
	if err := json.NewDecoder(zr).Decode(&lock); err != nil {
		t.Fatal(err)
	}
	edit(lock["packages"].(map[string]any))
	data, err := json.Marshal(lock)
	if err != nil {
		t.Fatal(err)
	}
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	if _, err := zw.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(frozen, gz.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A frozen closure is refused when it installs the reference's package from anywhere but a pack of the pinned source,
// pins a pack without a sha512, or packs one file twice with different bytes.
func TestPinnedProjectRefusesALooseClosure(t *testing.T) {
	commit := strings.Repeat("12", 20)
	pin := piPin{Ref: commit, Commit: commit, PackageVersion: "1.2.3", Version: "1.2.3+12121212"}
	ai := "node_modules/" + agentReference.pkg + "/node_modules/@earendil-works/pi-ai"
	for _, c := range []struct {
		name, want string
		edit       func(map[string]any)
	}{
		{"registry package", "does not install " + agentReference.pkg + " from the pinned source", func(p map[string]any) {
			p["node_modules/"+agentReference.pkg].(map[string]any)["resolved"] = "https://registry.npmjs.org/x.tgz"
		}},
		{"sha1 pack", "no version or no sha512 integrity", func(p map[string]any) {
			p[ai].(map[string]any)["integrity"] = "sha1-abc"
		}},
		{"two builds of one pack", "twice with different integrity", func(p map[string]any) {
			p["node_modules/@earendil-works/pi-ai"] = map[string]any{"version": "1.2.3", "resolved": packPrefix + "@earendil-works/pi-ai", "integrity": npmIntegrity([]byte("another ai pack"))}
		}},
	} {
		root := t.TempDir()
		fakeReference(t, root, filepath.Join(t.TempDir(), "packs"), commit, agentReference, "1.2.3")
		if _, _, _, err := pinnedProject(root, agentReference, pin); err != nil {
			t.Fatalf("%s: the unedited closure: %v", c.name, err)
		}
		rewriteClosure(t, root, commit, agentReference, c.edit)
		if _, _, _, err := pinnedProject(root, agentReference, pin); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: want %q, got %v", c.name, c.want, err)
		}
	}
}

// A bundle refuses an installed pi-durable of another version than the pin's, and a missing one.
func TestCheckInstalledPiDurable(t *testing.T) {
	pin := piPin{PackageVersion: "1.2.3"}
	root := t.TempDir()
	dir := filepath.Join(root, durableReference.dir)
	if err := checkInstalledVersion(root, durableReference, pin); err == nil {
		t.Error("a missing pi-durable passed")
	}
	pkg := filepath.Join(dir, "node_modules", "@earendil-works", "pi-durable")
	if err := os.MkdirAll(pkg, 0o755); err != nil {
		t.Fatal(err)
	}
	for version, ok := range map[string]bool{"1.2.3": true, "1.2.4": false} {
		if err := os.WriteFile(filepath.Join(pkg, "package.json"), []byte(`{"version":"`+version+`"}`), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := checkInstalledVersion(root, durableReference, pin); (err == nil) != ok {
			t.Errorf("installed %s: %v", version, err)
		}
	}
}

// A commit pin's packs are artifacts: ARTIFACTS.sha256 lists each pack of both references, so the pod checks them.
func TestArtifactListCarriesThePinnedPacks(t *testing.T) {
	dir := fakeAgentBundle(t)
	var want []string
	for _, ref := range []string{"pi-durable", "pi-agent"} {
		packs := filepath.Join(dir, ref, "packs")
		if err := os.MkdirAll(packs, 0o755); err != nil {
			t.Fatal(err)
		}
		name := "earendil-works-pi-ai-1.2.3.tgz"
		if err := os.WriteFile(filepath.Join(packs, name), []byte(ref), 0o644); err != nil {
			t.Fatal(err)
		}
		want = append(want, ref+"/packs/"+name)
	}
	if err := writeArtifactList(dir); err != nil {
		t.Fatal(err)
	}
	list, err := os.ReadFile(filepath.Join(dir, "ARTIFACTS.sha256"))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range want {
		if !strings.Contains(string(list), "  "+path+"\n") {
			t.Errorf("ARTIFACTS.sha256 does not list %s:\n%s", path, list)
		}
	}
}

// A commit pin without sourceAgentPins is refused rather than checked against another build's pins.
func TestPinnedAgentPinsRefusesAnUnknownCommit(t *testing.T) {
	commit := strings.Repeat("34", 20)
	if _, err := pinnedAgentPins(piPin{Ref: commit, Commit: commit}); err == nil || !strings.Contains(err.Error(), "no pins for Pi's coding agent built from commit "+commit) {
		t.Errorf("unknown commit: %v", err)
	}
	if pins, err := pinnedAgentPins(piPin{Ref: "v1.1.0"}); err != nil || pins.tree != publishedAgentPins.tree {
		t.Errorf("release: %v", err)
	}
}
