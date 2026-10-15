package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"debug/elf"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

func sumOf(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

const (
	fakeArmLock = `{"lockfileVersion":3,"packages":{"":{"dependencies":{"@acme/agent":"1.2.3"}},"node_modules/@acme/agent":{"version":"1.2.3","integrity":"sha512-abc","bin":{"agent":"bin/agent.js"}}}}`
	fakeArmBin  = "#!/usr/bin/env node\nconsole.log('agent')\n"
)

// fakeArmRoot is a repository root whose arms.lock pins the npm arm "agent" (package @acme/agent 1.2.3) for linux-amd64.
func fakeArmRoot(t *testing.T, edit func(pin map[string]any)) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "bench", "evals", "locks"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "bench", "evals", "locks", "agent.package-lock.json"), []byte(fakeArmLock), 0o644); err != nil {
		t.Fatal(err)
	}
	pin := map[string]any{
		"source": "npm", "package": "@acme/agent", "version": "1.2.3", "integrity": "sha512-abc", "bin": "node_modules/.bin/agent",
		"bin_sha256":   map[string]string{"linux-amd64": sumOf(fakeArmBin)},
		"package_lock": "locks/agent.package-lock.json", "package_lock_sha256": sumOf(fakeArmLock),
	}
	if edit != nil {
		edit(pin)
	}
	data, err := json.Marshal(map[string]any{"arms": map[string]any{"agent": pin, "other": map[string]any{"source": "release"}, "checkout": map[string]any{"source": "checkout"}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(armsLock)), data, 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

// fakeArmInstall is what npm ci leaves for the fake arm: the package, its executable and the .bin link to it.
func fakeArmInstall(t *testing.T, dir, version, bin string) {
	t.Helper()
	pkg := filepath.Join(dir, "node_modules", "@acme", "agent")
	for _, sub := range []string{filepath.Join(pkg, "bin"), filepath.Join(dir, "node_modules", ".bin")} {
		if err := os.MkdirAll(sub, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(pkg, "package.json"), []byte(`{"version":"`+version+`"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkg, "bin", "agent.js"), []byte(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	testenv.Symlink(t, "../@acme/agent/bin/agent.js", filepath.Join(dir, "node_modules", ".bin", "agent"))
}

func TestParseArmIDs(t *testing.T) {
	ids, err := parseArmIDs(" codex, rpi ,")
	if err != nil || !slices.Equal(ids, []string{"codex", "rpi"}) {
		t.Errorf("parseArmIDs: %v %v", ids, err)
	}
	if ids, err := parseArmIDs(""); err != nil || len(ids) != 0 {
		t.Errorf("no arms: %v %v", ids, err)
	}
	for text, want := range map[string]string{
		"codex,codex": "appears twice",
		"pi-1.1.0":    "already carries",
		"pig-0.4.1":   "already carries",
		"../x":        "use the ids",
		"Codex":       "use the ids",
	} {
		if _, err := parseArmIDs(text); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: %v, want %q", text, err, want)
		}
	}
}

// The arm's lockfile goes in byte for byte once its sha256 and its record of the package match arms.lock; the
// package.json depends on the pinned version, as pigbench's install writes it.
func TestWriteArmPackage(t *testing.T) {
	root := fakeArmRoot(t, nil)
	pins, err := readArmsLock(root, []string{"agent"})
	if err != nil {
		t.Fatal(err)
	}
	dest := t.TempDir()
	if err := writeArmPackage(root, dest, "agent", pins["agent"]); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(dest, "package-lock.json")); string(got) != fakeArmLock {
		t.Errorf("package-lock.json %s", got)
	}
	var pkg struct {
		Name         string
		Private      bool
		Dependencies map[string]string
	}
	data, _ := os.ReadFile(filepath.Join(dest, "package.json"))
	if err := json.Unmarshal(data, &pkg); err != nil || pkg.Name != "pigbench-arm-agent" || !pkg.Private || pkg.Dependencies["@acme/agent"] != "1.2.3" || len(pkg.Dependencies) != 1 {
		t.Errorf("package.json %s (%v)", data, err)
	}
	for name, c := range map[string]struct {
		edit func(map[string]any)
		want string
	}{
		"lock sha256":    {func(p map[string]any) { p["package_lock_sha256"] = sumOf("other") }, "the lock pins"},
		"lock version":   {func(p map[string]any) { p["version"] = "1.2.4" }, "disagrees with"},
		"lock integrity": {func(p map[string]any) { p["integrity"] = "sha512-xyz" }, "disagrees with"},
		"no integrity":   {func(p map[string]any) { delete(p, "integrity") }, "needs package, version, integrity"},
		"package.json": {func(p map[string]any) {
			p["package_json_file"] = "locks/agent.package-lock.json"
			p["package_json_sha256"] = sumOf("x")
		}, "the lock pins"},
	} {
		root := fakeArmRoot(t, c.edit)
		pins, err := readArmsLock(root, []string{"agent"})
		if err != nil {
			t.Fatal(err)
		}
		if err := writeArmPackage(root, t.TempDir(), "agent", pins["agent"]); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want %q", name, err, c.want)
		}
	}
}

// An installed arm passes only with the pinned version and the pinned executable for the platform; its path is the
// link's target inside the arm, which ARTIFACTS.sha256 lists.
func TestCheckArm(t *testing.T) {
	root := fakeArmRoot(t, nil)
	pins, err := readArmsLock(root, []string{"agent"})
	if err != nil {
		t.Fatal(err)
	}
	pin := pins["agent"]
	dir := t.TempDir()
	fakeArmInstall(t, dir, "1.2.3", fakeArmBin)
	if got, err := checkArm(dir, "agent", pin, "linux-amd64"); err != nil || got != "node_modules/@acme/agent/bin/agent.js" {
		t.Errorf("checkArm: %q %v", got, err)
	}
	sized := pin
	sized.BinSize = map[string]int64{"linux-amd64": int64(len(fakeArmBin)) + 1}
	if _, err := checkArm(dir, "agent", sized, "linux-amd64"); err == nil || !strings.Contains(err.Error(), "bytes") {
		t.Errorf("size: %v", err)
	}
	if _, err := checkArm(dir, "agent", pin, "linux-arm64"); err == nil || !strings.Contains(err.Error(), "pins") {
		t.Errorf("another platform: %v", err)
	}
	other := t.TempDir()
	fakeArmInstall(t, other, "1.2.3", fakeArmBin+"// changed\n")
	if _, err := checkArm(other, "agent", pin, "linux-amd64"); err == nil || !strings.Contains(err.Error(), "has sha256") {
		t.Errorf("changed executable: %v", err)
	}
	old := t.TempDir()
	fakeArmInstall(t, old, "1.2.2", fakeArmBin)
	if _, err := checkArm(old, "agent", pin, "linux-amd64"); err == nil || !strings.Contains(err.Error(), "installed 1.2.2") {
		t.Errorf("another version: %v", err)
	}
	outside := t.TempDir()
	fakeArmInstall(t, outside, "1.2.3", fakeArmBin)
	elsewhere := filepath.Join(t.TempDir(), "agent.js")
	if err := os.WriteFile(elsewhere, []byte(fakeArmBin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(outside, "node_modules", ".bin", "agent")); err != nil {
		t.Fatal(err)
	}
	testenv.Symlink(t, elsewhere, filepath.Join(outside, "node_modules", ".bin", "agent"))
	if _, err := checkArm(outside, "agent", pin, "linux-amd64"); err == nil || !strings.Contains(err.Error(), "outside") {
		t.Errorf("link out of the arm: %v", err)
	}
}

// Arms that npm cannot install as pinned are refused before npm runs.
func TestInstallArmsRefusesBeforeNpm(t *testing.T) {
	root := fakeArmRoot(t, func(p map[string]any) { p["bin_sha256"] = map[string]string{"linux-arm64": "x"} })
	for ids, want := range map[string]string{"agent": "pins no executable sha256 for linux-amd64", "other": "pins no release archive for linux-amd64", "checkout": "the bundle installs npm and release arms", "missing": "has no arm"} {
		_, err := installArms(context.Background(), root, t.TempDir(), "amd64", []string{ids}, io.Discard)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v, want %q", ids, err, want)
		}
	}
	if _, err := installArms(context.Background(), root, t.TempDir(), "riscv64", []string{"agent"}, io.Discard); err == nil || !strings.Contains(err.Error(), "not amd64 or arm64") {
		t.Errorf("arch: %v", err)
	}
}

func TestBundleArmsNeedAgents(t *testing.T) {
	err := cmdBundle(context.Background(), []string{"-out", t.TempDir(), "-arms", "codex"}, io.Discard, func(string) string { return "" })
	if err == nil || !strings.Contains(err.Error(), "-arms needs -agents") {
		t.Errorf("cmdBundle: %v", err)
	}
}

// An installed arm's lockfile and executable are artifacts: the bundle lists them and the pod checks them.
func TestArmArtifacts(t *testing.T) {
	dir := fakeAgentBundle(t)
	arm := filepath.Join(dir, armsDir, "agent")
	fakeArmInstall(t, arm, "1.2.3", fakeArmBin)
	for name, content := range map[string]string{"BIN": "node_modules/@acme/agent/bin/agent.js\n", "package-lock.json": fakeArmLock} {
		if err := os.WriteFile(filepath.Join(arm, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := writeArtifactList(dir); err != nil {
		t.Fatal(err)
	}
	b, err := inspectBundle(dir)
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, a := range b.artifacts {
		paths = append(paths, a[0])
	}
	if want := []string{"arms/agent/package-lock.json", "arms/agent/node_modules/@acme/agent/bin/agent.js"}; !slices.Equal(paths[len(paths)-2:], want) {
		t.Errorf("artifacts end %v, want %v", paths[len(paths)-2:], want)
	}
	if err := os.WriteFile(filepath.Join(arm, "BIN"), []byte("../../bin/pig\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeArtifactList(dir); err == nil || !strings.Contains(err.Error(), "not a path inside the arm") {
		t.Errorf("BIN outside the arm: %v", err)
	}
}

// The overhead-codex profile runs Codex's executable at the path an install of the pinned lockfile gives it (the
// package's bin entry), which the bundle lists; arms.lock pins that executable for linux-amd64.
func TestCodexProfileRunsThePinnedArm(t *testing.T) {
	root := filepath.Join("..", "..", "..", "..")
	pins, err := readArmsLock(root, []string{"codex"})
	if err != nil {
		t.Fatal(err)
	}
	pin := pins["codex"]
	if pin.Source != "npm" || pin.BinSHA256["linux-amd64"] == "" {
		t.Fatalf("codex pin %+v", pin)
	}
	lock, err := readPinned(filepath.Join(root, "bench", "evals", filepath.FromSlash(pin.PackageLock)), pin.PackageLockSHA256)
	if err != nil {
		t.Fatal(err)
	}
	var parsed struct {
		Packages map[string]struct {
			Bin map[string]string `json:"bin"`
		} `json:"packages"`
	}
	if err := json.Unmarshal(lock, &parsed); err != nil {
		t.Fatal(err)
	}
	bin := parsed.Packages["node_modules/"+pin.Package].Bin[filepath.Base(pin.Bin)]
	want := "{root}/" + armsDir + "/codex/node_modules/" + pin.Package + "/" + strings.TrimPrefix(bin, "./")
	prof, err := loadProfile("overhead-codex")
	if err != nil {
		t.Fatal(err)
	}
	ref, _ := prof.byRole(roleReference)
	if !slices.Contains(ref.Sample, want) {
		t.Errorf("overhead-codex's reference runs %v, want the arm's executable %s", ref.Sample, want)
	}
}

// The image installs the arms its ARMS build argument names with the same command, and lists them as the bundle does.
func TestImageInstallsTheArms(t *testing.T) {
	dockerfile, err := os.ReadFile(filepath.Join("..", "..", "image", "Dockerfile"))
	if err != nil {
		t.Fatal(err)
	}
	build, err := os.ReadFile(filepath.Join("..", "..", "image", "build.sh"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"ARG ARMS=", "/opt/pig/bin/k8sbench arms -root /src -out /opt/pig", "cat /tmp/arm-versions"} {
		if !strings.Contains(string(dockerfile), want) {
			t.Errorf("bench/k8s/image/Dockerfile does not have %s", want)
		}
	}
	if strings.Count(string(build), `--build-arg ARMS="${ARMS:-}"`) != 2 {
		t.Error("bench/k8s/image/build.sh does not pass ARMS to both builds")
	}
}

// The image lists the arms' files in ARTIFACTS.sha256 with its own shell loop; it must list what armArtifacts lists for
// a bundle: an npm arm's lockfile and executable, and a release arm's executable alone (it has no lockfile).
func TestImageListsTheArmsAsTheBundleDoes(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh")
	}
	dockerfile, err := os.ReadFile(filepath.Join("..", "..", "image", "Dockerfile"))
	if err != nil {
		t.Fatal(err)
	}
	start := strings.Index(string(dockerfile), "$(for d in arms/*/;")
	end := strings.Index(string(dockerfile)[max(start, 0):], "; done)")
	if start < 0 || end < 0 {
		t.Fatal("bench/k8s/image/Dockerfile has no arms loop in its ARTIFACTS.sha256 step")
	}
	loop := string(dockerfile)[start : start+end+len("; done)")]
	dir := t.TempDir()
	for path, content := range map[string]string{
		"arms/codex/package-lock.json":                       "{}",
		"arms/codex/node_modules/@openai/codex/bin/codex.js": "js",
		"arms/codex/BIN": "node_modules/@openai/codex/bin/codex.js\n",
		"arms/rpi/rpi":   "elf",
		"arms/rpi/BIN":   "rpi\n",
	} {
		if err := os.MkdirAll(filepath.Join(dir, filepath.Dir(path)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, path), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := armArtifacts(dir)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(sh, "-c", "set -eu; export LC_ALL=C; for p in "+loop+"; do [ -f \"$p\" ]; echo \"$p\"; done")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("the image's arms loop: %v\n%s", err, out)
	}
	if got := strings.Fields(string(out)); !slices.Equal(got, want) {
		t.Errorf("the image lists %v, a bundle %v", got, want)
	}
}

// overhead-pi runs on any agent bundle; overhead-codex only on one that carries the codex arm, and the runner says so
// before a Job is submitted.
func TestOverheadProfilesNeedTheirArms(t *testing.T) {
	plain, err := inspectBundle(fakeAgentBundle(t))
	if err != nil {
		t.Fatal(err)
	}
	pi, err := loadProfile("overhead-pi")
	if err != nil {
		t.Fatal(err)
	}
	if err := checkBundlePrograms(pi, []string{"pig", "pi"}, plain); err != nil {
		t.Errorf("an agent bundle runs overhead-pi: %v", err)
	}
	codex, err := loadProfile("overhead-codex")
	if err != nil {
		t.Fatal(err)
	}
	const exe = "arms/codex/node_modules/@openai/codex/bin/codex.js"
	if err := checkBundlePrograms(codex, []string{"pig", "codex"}, plain); err == nil || !strings.Contains(err.Error(), "codex needs "+exe) || !strings.HasSuffix(err.Error(), "with -agents -arms codex") {
		t.Errorf("an agent bundle without the arm runs overhead-codex: %v", err)
	}
	dir := fakeAgentBundle(t)
	arm := filepath.Join(dir, armsDir, "codex")
	if err := os.MkdirAll(filepath.Join(arm, filepath.Dir(strings.TrimPrefix(exe, "arms/codex/"))), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{"BIN": strings.TrimPrefix(exe, "arms/codex/") + "\n", "package-lock.json": "{}", strings.TrimPrefix(exe, "arms/codex/"): fakeArmBin} {
		if err := os.WriteFile(filepath.Join(arm, filepath.FromSlash(name)), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := writeArtifactList(dir); err != nil {
		t.Fatal(err)
	}
	withArm, err := inspectBundle(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := checkBundlePrograms(codex, []string{"pig", "codex"}, withArm); err != nil {
		t.Errorf("a bundle with the codex arm runs overhead-codex: %v", err)
	}
}

// tarGz is a .tar.gz archive of the regular files files (name -> content), in name order.
func tarGz(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, name := range slices.Sorted(maps.Keys(files)) {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(files[name])), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(files[name]); err != nil {
			t.Fatal(err)
		}
	}
	if err := errors.Join(tw.Close(), gz.Close()); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func elfBytes(t *testing.T, machine elf.Machine) []byte {
	t.Helper()
	path := filepath.Join(t.TempDir(), "exe")
	writeELF(t, path, machine)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// A release arm is its pinned archive for the platform, of which only the executable member stays, and it must be an
// executable of the bundle's architecture.
func TestInstallReleaseArm(t *testing.T) {
	good := tarGz(t, map[string][]byte{"rpi-v1/rpi": elfBytes(t, elf.EM_X86_64), "rpi-v1/README.md": []byte("readme\n")})
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1.0.0/rpi-good.tar.gz":
			_, _ = w.Write(good)
		case "/v1.0.0/rpi-arm.tar.gz":
			_, _ = w.Write(tarGz(t, map[string][]byte{"rpi": elfBytes(t, elf.EM_AARCH64)}))
		case "/v1.0.0/rpi-two.tar.gz":
			_, _ = w.Write(tarGz(t, map[string][]byte{"a/rpi": elfBytes(t, elf.EM_X86_64), "b/rpi": elfBytes(t, elf.EM_X86_64)}))
		case "/v1.0.0/rpi-none.tar.gz":
			_, _ = w.Write(tarGz(t, map[string][]byte{"README.md": []byte("x")}))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	pinOf := func(name string, body []byte) armPin {
		var pin armPin
		pin.Source, pin.Version = "release", "1.0.0"
		pin.Release.Base, pin.Release.Member = srv.URL+"/v{version}/", "rpi"
		pin.Assets = map[string]releaseAsset{"linux-amd64": {Name: name, SHA256: sumOf(string(body)), Size: int64(len(body))}}
		return pin
	}
	dest := t.TempDir()
	bin, err := installReleaseArm(context.Background(), srv.Client(), dest, "rpi", pinOf("rpi-good.tar.gz", good), "amd64", io.Discard)
	if err != nil || bin != "rpi" {
		t.Fatalf("installReleaseArm: %q %v", bin, err)
	}
	if entries, _ := os.ReadDir(dest); len(entries) != 1 || entries[0].Name() != "rpi" {
		t.Errorf("the arm keeps only the executable: %v", entries)
	}
	if info, err := os.Stat(filepath.Join(dest, "rpi")); err != nil || info.Mode().Perm()&0o111 == 0 {
		t.Errorf("executable: %v %v", info, err)
	}
	other := tarGz(t, map[string][]byte{"rpi": elfBytes(t, elf.EM_AARCH64)})
	two := tarGz(t, map[string][]byte{"a/rpi": elfBytes(t, elf.EM_X86_64), "b/rpi": elfBytes(t, elf.EM_X86_64)})
	none := tarGz(t, map[string][]byte{"README.md": []byte("x")})
	changed := pinOf("rpi-good.tar.gz", good)
	changed.Assets["linux-amd64"] = releaseAsset{Name: "rpi-good.tar.gz", SHA256: sumOf("other"), Size: int64(len(good))}
	xz := pinOf("rpi-good.tar.xz", good)
	plain := pinOf("rpi-good.tar.gz", good)
	plain.Release.Base = "http://example.invalid/"
	for name, c := range map[string]struct {
		pin  armPin
		arch string
		want string
	}{
		"sha256":       {changed, "amd64", "want the pinned"},
		"architecture": {pinOf("rpi-arm.tar.gz", other), "amd64", "linux/arm64 executable, not linux/amd64"},
		"two members":  {pinOf("rpi-two.tar.gz", two), "amd64", "more than one rpi"},
		"no member":    {pinOf("rpi-none.tar.gz", none), "amd64", "no rpi"},
		"platform":     {pinOf("rpi-good.tar.gz", good), "arm64", "pins no release archive for linux-arm64"},
		"xz":           {xz, "amd64", "not a .tar.gz archive"},
		"http":         {plain, "amd64", "release.base (https)"},
	} {
		if _, err := installReleaseArm(context.Background(), srv.Client(), t.TempDir(), "rpi", c.pin, c.arch, io.Discard); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want %q", name, err, c.want)
		}
	}
}

// A release arm lists only its executable: it has no lockfile.
func TestReleaseArmArtifacts(t *testing.T) {
	dir := fakeAgentBundle(t)
	arm := filepath.Join(dir, armsDir, "rpi")
	if err := os.MkdirAll(arm, 0o755); err != nil {
		t.Fatal(err)
	}
	writeELF(t, filepath.Join(arm, "rpi"), elf.EM_X86_64)
	if err := os.WriteFile(filepath.Join(arm, "BIN"), []byte("rpi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	paths, err := armArtifacts(dir)
	if err != nil || !slices.Equal(paths, []string{"arms/rpi/rpi"}) {
		t.Errorf("armArtifacts: %v %v", paths, err)
	}
}

// An agent bundle carries bench/evals' arm definitions beside pigbench, so a sample can name an arm (its harness
// settings), and lists them as artifacts.
func TestBundleCarriesTheArmDefinitions(t *testing.T) {
	root := filepath.Join("..", "..", "..", "..")
	dir := fakeAgentBundle(t)
	if err := copyPigbench(root, dir); err != nil {
		t.Fatal(err)
	}
	for _, f := range pigbenchArmFiles {
		want, err := os.ReadFile(filepath.Join(root, "bench", filepath.FromSlash(f)))
		if err != nil {
			t.Fatal(err)
		}
		if got, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(f))); err != nil || !bytes.Equal(got, want) {
			t.Errorf("%s: not bench/%s (%v)", f, f, err)
		}
	}
	if err := writeArtifactList(dir); err != nil {
		t.Fatal(err)
	}
	b, err := inspectBundle(dir)
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, a := range b.artifacts {
		paths = append(paths, a[0])
	}
	for _, f := range pigbenchArmFiles {
		if !slices.Contains(paths, f) {
			t.Errorf("ARTIFACTS.sha256 does not list %s", f)
		}
	}
}

// A profile with a base image runs from a bundle on that image: it fills an unset base image and refuses another one
// and the image delivery; it must be pinned by digest.
func TestProfileBaseImage(t *testing.T) {
	const trixie = "node:24.19.0-trixie-slim@sha256:ab3eebe934147fee049b5eb83c570f68c849a13c930bdfa482de99fcdfa3b3de"
	p := &profile{Name: "x", BaseImage: trixie}
	v := values{Bundle: "/b"}
	if err := applyBaseImage(p, &v); err != nil || v.podImage() != trixie {
		t.Errorf("unset base image: %v %q", err, v.podImage())
	}
	v = values{Bundle: "/b", BaseImage: defaultBaseImage}
	if err := applyBaseImage(p, &v); err == nil || !strings.Contains(err.Error(), "not "+defaultBaseImage) {
		t.Errorf("another base image: %v", err)
	}
	v = values{Image: "registry.example/pig@sha256:" + strings.Repeat("0", 64)}
	if err := applyBaseImage(p, &v); err == nil || !strings.Contains(err.Error(), "runs from a bundle") {
		t.Errorf("image delivery: %v", err)
	}
	v = values{Bundle: "/b"}
	if err := applyBaseImage(&profile{Name: "y"}, &v); err != nil || v.podImage() != defaultBaseImage {
		t.Errorf("no profile base image: %v %q", err, v.podImage())
	}
	bad := profile{Name: "x", Rule: "x", BaseImage: "node:24.19.0-trixie-slim", Targets: []target{
		{Name: "pig", Role: rolePig, Seed: []string{"s"}, SeedMeta: "m", Sample: []string{"s"}},
		{Name: "pi", Role: roleReference, Seed: []string{"s"}, SeedMeta: "m", Sample: []string{"s"}}}}
	if err := bad.validate(); err == nil || !strings.Contains(err.Error(), "not pinned by digest") {
		t.Errorf("unpinned base image: %v", err)
	}
	bad.BaseImage = trixie
	if err := bad.validate(); err != nil {
		t.Errorf("pinned base image: %v", err)
	}
}

// overhead-rpi measures arms.lock's release arm rpi as that arm (its harness settings), at the executable a bundle
// installs, on the base image its glibc needs.
func TestRpiProfileRunsThePinnedArm(t *testing.T) {
	pins, err := readArmsLock(filepath.Join("..", "..", "..", ".."), []string{"rpi"})
	if err != nil {
		t.Fatal(err)
	}
	pin := pins["rpi"]
	if pin.Source != "release" || pin.Release.Member == "" || pin.Assets["linux-amd64"].SHA256 == "" {
		t.Fatalf("rpi pin %+v", pin)
	}
	prof, err := loadProfile("overhead-rpi")
	if err != nil {
		t.Fatal(err)
	}
	ref, _ := prof.byRole(roleReference)
	exe := "{root}/" + armsDir + "/rpi/" + pin.Release.Member
	for _, want := range [][]string{{"--arm", "rpi"}, {"--binary", exe}} {
		i := slices.Index(ref.Sample, want[0])
		if i < 0 || i+1 >= len(ref.Sample) || ref.Sample[i+1] != want[1] {
			t.Errorf("overhead-rpi's reference runs %v, want %s %s", ref.Sample, want[0], want[1])
		}
	}
	if !strings.HasPrefix(prof.BaseImage, "node:"+nodeVersion+"-trixie-slim@sha256:") {
		t.Errorf("overhead-rpi runs on %q, want the pinned trixie node image", prof.BaseImage)
	}
	pi, err := loadProfile("overhead-pi")
	if err != nil {
		t.Fatal(err)
	}
	if !maps.Equal(prof.Fingerprints, pi.Fingerprints) {
		t.Errorf("overhead-rpi defines another workload than overhead-pi: %v vs %v", prof.Fingerprints, pi.Fingerprints)
	}
}
