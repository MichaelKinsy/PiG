package main

import (
	"archive/tar"
	"bytes"
	"cmp"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"debug/elf"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// writeELF writes the smallest ELF64 header debug/elf accepts, for the given machine.
func writeELF(t *testing.T, path string, machine elf.Machine) {
	t.Helper()
	header := make([]byte, 64)
	copy(header, []byte{0x7f, 'E', 'L', 'F', byte(elf.ELFCLASS64), byte(elf.ELFDATA2LSB), byte(elf.EV_CURRENT)})
	binary.LittleEndian.PutUint16(header[16:], uint16(elf.ET_EXEC))
	binary.LittleEndian.PutUint16(header[18:], uint16(machine))
	binary.LittleEndian.PutUint32(header[20:], uint32(elf.EV_CURRENT))
	binary.LittleEndian.PutUint16(header[52:], 64)
	if err := os.WriteFile(path, header, 0o755); err != nil {
		t.Fatal(err)
	}
}

// fakeBundle lays out a bundle as k8sbench bundle and the image's bundle stage write it.
func fakeBundle(t *testing.T, machine elf.Machine) string {
	t.Helper()
	dir := t.TempDir()
	for _, sub := range []string{"bin", "dist", "pi-durable/node_modules/@earendil-works/pi-durable", "pi-durable/node_modules/.bin"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeELF(t, filepath.Join(dir, "bin", "k8sbench"), machine)
	writeELF(t, filepath.Join(dir, "bin", "durableperf"), machine)
	files := map[string]string{
		"dist/core-go.wasm":            "\x00asm go",
		"dist/core-tinygo.wasm":        "\x00asm tinygo",
		"pi-durable/bench.mjs":         "// bench\n",
		"pi-durable/package.json":      `{"name":"x"}`,
		"pi-durable/package-lock.json": `{"lockfileVersion":3}`,
		"pi-durable/node_modules/@earendil-works/pi-durable/package.json": `{"version":"1.1.0"}`,
		"COMMIT":   "0123456789abcdef\n",
		"VERSIONS": "go=go1.27.2\narch=amd64\n",
	}
	for path, content := range files {
		if err := os.WriteFile(filepath.Join(dir, path), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	testenv.Symlink(t, "../@earendil-works/pi-durable/package.json", filepath.Join(dir, "pi-durable/node_modules/.bin/link"))
	if err := writeArtifactList(dir); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestInspectBundle(t *testing.T) {
	dir := fakeBundle(t, elf.EM_X86_64)
	b, err := inspectBundle(dir)
	if err != nil {
		t.Fatal(err)
	}
	if b.commit != "0123456789abcdef" || b.arch != "amd64" || len(b.artifacts) != 6 || b.artifacts[0][0] != "bin/durableperf" {
		t.Errorf("bundle %+v", b)
	}
	if b, err := inspectBundle(fakeBundle(t, elf.EM_AARCH64)); err != nil || b.arch != "arm64" {
		t.Errorf("arm64 bundle: %v %v", b, err)
	}

	tampered := fakeBundle(t, elf.EM_X86_64)
	if err := os.WriteFile(filepath.Join(tampered, "dist", "core-tinygo.wasm"), []byte("changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := inspectBundle(tampered); err == nil || !strings.Contains(err.Error(), "dist/core-tinygo.wasm has sha256") {
		t.Errorf("a changed artifact must be refused: %v", err)
	}

	hostBuild := fakeBundle(t, elf.EM_X86_64)
	if err := os.WriteFile(filepath.Join(hostBuild, "bin", "durableperf"), []byte("\xcf\xfa\xed\xfe Mach-O"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := writeArtifactList(hostBuild); err != nil {
		t.Fatal(err)
	}
	if _, err := inspectBundle(hostBuild); err == nil || !strings.Contains(err.Error(), "GOOS=linux") {
		t.Errorf("a binary built for the host must be refused: %v", err)
	}

	mixed := fakeBundle(t, elf.EM_X86_64)
	writeELF(t, filepath.Join(mixed, "bin", "durableperf"), elf.EM_AARCH64)
	if err := writeArtifactList(mixed); err != nil {
		t.Fatal(err)
	}
	if _, err := inspectBundle(mixed); err == nil {
		t.Error("binaries of two architectures must be refused")
	}

	missing := fakeBundle(t, elf.EM_X86_64)
	if err := os.WriteFile(filepath.Join(missing, "ARTIFACTS.sha256"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := inspectBundle(missing); err == nil || !strings.Contains(err.Error(), "does not list bin/k8sbench") {
		t.Errorf("an unlisted artifact must be refused: %v", err)
	}

	noModules := fakeBundle(t, elf.EM_X86_64)
	if err := os.RemoveAll(filepath.Join(noModules, "pi-durable", "node_modules")); err != nil {
		t.Fatal(err)
	}
	if _, err := inspectBundle(noModules); err == nil {
		t.Error("a bundle without the reference packages must be refused")
	}
}

func TestPackBundleRoundTrips(t *testing.T) {
	dir := fakeBundle(t, elf.EM_X86_64)
	out := filepath.Join(t.TempDir(), "bundle.tar.gz")
	sum, size, err := packBundle(dir, out)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := fileSHA256(out)
	info, _ := os.Stat(out)
	if got != sum || info.Size() != size {
		t.Errorf("reported sha256 %s size %d, file has %s %d", sum, size, got, info.Size())
	}
	f, _ := os.Open(out)
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	seen := map[string]*tar.Header{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		seen[hdr.Name] = hdr
		if hdr.Typeflag == tar.TypeReg {
			data, _ := io.ReadAll(tr)
			want, _ := os.ReadFile(filepath.Join(dir, hdr.Name))
			if !bytes.Equal(data, want) {
				t.Errorf("%s differs", hdr.Name)
			}
		}
	}
	if h := seen["bin/k8sbench"]; h == nil || h.Mode&0o111 == 0 || h.Uid != 0 || h.ModTime.Unix() != 0 {
		t.Errorf("executable header %+v", h)
	}
	if h := seen["pi-durable/node_modules/.bin/link"]; h == nil || h.Typeflag != tar.TypeSymlink {
		t.Errorf("symlinks stay symlinks: %+v", h)
	}
	if _, ok := seen["ARTIFACTS.sha256"]; !ok {
		t.Error("ARTIFACTS.sha256 travels with the bundle")
	}
}

func bundleValues(dir string) values {
	v := testValues()
	v.Image, v.Bundle = "", dir
	return v
}

func TestRenderBundleMode(t *testing.T) {
	v := bundleValues("/some/bundle")
	sum := strings.Repeat("c", 64)
	out, err := renderJob(v, testPlan(t, v, modeCalibrate), bundleRef{sha256: sum, arch: "arm64"})
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(out, &doc); err != nil {
		t.Fatal(err)
	}
	spec := podSpec(t, doc)
	c := at(t, spec, "containers", 0).(map[string]any)
	if c["image"] != defaultBaseImage || !imageDigest.MatchString(defaultBaseImage) {
		t.Errorf("image %v", c["image"])
	}
	command := c["command"].([]any)
	if len(command) != 3 || command[0] != "sh" || command[2] != podBundleScript {
		t.Errorf("command %v", command)
	}
	env := map[string]any{}
	for _, e := range c["env"].([]any) {
		m := e.(map[string]any)
		env[m["name"].(string)] = m["value"]
	}
	if env["K8SBENCH_BUNDLE_SHA256"] != sum || env["K8SBENCH_BUNDLE_ARCH"] != "arm64" || env["K8SBENCH_ROOT"] != "/bundle/pig" || env["K8SBENCH_DELIVERY"] != deliveryBundle || !strings.HasPrefix(env["PATH"].(string), "/bundle/pig/bin:") {
		t.Errorf("env %v", env)
	}
	if at(t, c, "volumeMounts", 1, "mountPath") != "/bundle" || at(t, spec, "volumes", 1, "emptyDir", "sizeLimit") != "2Gi" {
		t.Error("the bundle goes into its own emptyDir at /bundle")
	}
	res := at(t, c, "resources").(map[string]any)
	if !mapEqual(res["requests"], res["limits"].(map[string]any)) {
		t.Error("bundle mode keeps Guaranteed QoS")
	}
	for _, ref := range []bundleRef{{}, {sha256: sum}, {arch: "amd64"}} {
		if _, err := renderJob(v, testPlan(t, v, modeCalibrate), ref); err == nil {
			t.Errorf("bundle mode without the bundle's sha256 and architecture must be refused: %+v", ref)
		}
	}
	v.BaseImage = "node:24"
	if err := v.validate(); err == nil {
		t.Error("an unpinned base image must be refused")
	}
	v.Image = testImage
	v.BaseImage = ""
	if err := v.validate(); err == nil || !strings.Contains(err.Error(), "both image") {
		t.Errorf("image and bundle together must be refused: %v", err)
	}
	v.Image, v.Bundle = "", ""
	if err := v.validate(); err == nil || !strings.Contains(err.Error(), "neither image") {
		t.Errorf("a session needs an image or a bundle: %v", err)
	}
}

// The dry run of a bundle session checks and packs the bundle and prints the manifest and the copy step; it never runs
// kubectl.
func TestDryRunBundle(t *testing.T) {
	dir := fakeBundle(t, elf.EM_X86_64)
	env := map[string]string{"K8SBENCH_NAMESPACE": "bench-ns", "K8SBENCH_BUNDLE": dir}
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"calibrate", "-dry-run", "-values", os.DevNull, "-kubectl", "/nonexistent/kubectl", "-turns", "50", "-runs", "6"}, &stdout, &stderr, func(k string) string { return env[k] })
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), defaultBaseImage) || !strings.Contains(stdout.String(), "K8SBENCH_BUNDLE_SHA256") {
		t.Errorf("manifest:\n%s", stdout.String())
	}
	if !strings.Contains(stderr.String(), "cp <bundle.tar.gz> <pod>:/bundle/bundle.tar.gz") || !strings.Contains(stderr.String(), "(linux/amd64)") {
		t.Errorf("stderr:\n%s", stderr.String())
	}
	if !strings.Contains(stdout.String(), "k8sbench-cal-0123456789-") {
		t.Error("the run is named after the bundle's commit")
	}
	stderr.Reset()
	code = run(context.Background(), []string{"calibrate", "-dry-run", "-values", os.DevNull, "-kubectl", "/nonexistent/kubectl", "-commit", "fedcba9876"}, &bytes.Buffer{}, &stderr, func(k string) string { return env[k] })
	if code != 1 || !strings.Contains(stderr.String(), "not the requested commit") {
		t.Errorf("a bundle from another commit must be refused before submission: %d %s", code, stderr.String())
	}
}

// The pod script, run with sh: it waits for READY, checks the bundle's sha256, unpacks it, checks every artifact,
// and starts the session.
func TestPodBundleScript(t *testing.T) {
	for _, tool := range []string{"sh", "sha256sum", "tar", "uname"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not installed", tool)
		}
	}
	build := func(t *testing.T, mutate func(dir string)) (string, string) {
		dir := fakeBundle(t, elf.EM_X86_64)
		if err := os.WriteFile(filepath.Join(dir, "bin", "k8sbench"), []byte("#!/bin/sh\necho \"session $1 $K8SBENCH_ROOT\"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := writeArtifactList(dir); err != nil {
			t.Fatal(err)
		}
		if mutate != nil {
			mutate(dir)
		}
		target := t.TempDir()
		sum, _, err := packBundle(dir, filepath.Join(target, "bundle.tar.gz"))
		if err != nil {
			t.Fatal(err)
		}
		return target, sum
	}
	// The node's architecture as the script names it: Go's name for amd64 and arm64, uname's otherwise.
	machine, err := exec.Command("uname", "-m").Output()
	if err != nil {
		t.Fatalf("uname -m: %v", err)
	}
	arch := strings.TrimSpace(string(machine))
	arch = cmp.Or(map[string]string{"x86_64": "amd64", "aarch64": "arm64", "arm64": "arm64"}[arch], arch)
	runScript := func(target, sum string, ready bool, timeout string) string {
		return runScriptArch(t, target, sum, ready, timeout, arch)
	}

	target, sum := build(t, nil)
	if out := runScript(target, sum, true, "5"); strings.TrimSpace(out) != "session pod "+target+"/pig" {
		t.Errorf("good bundle: %q", out)
	}
	if _, err := os.Stat(filepath.Join(target, "pig", "dist", "core-tinygo.wasm")); err != nil {
		t.Error("the bundle unpacks into pig/")
	}

	target, _ = build(t, nil)
	if out := runScript(target, strings.Repeat("0", 64), true, "5"); !strings.Contains(out, `{"t":"error","message":"bundle.tar.gz does not have the sha256`) {
		t.Errorf("wrong bundle sha256: %q", out)
	}

	target, sum = build(t, func(dir string) {
		if err := os.WriteFile(filepath.Join(dir, "dist", "core-go.wasm"), []byte("swapped after listing"), 0o644); err != nil {
			t.Fatal(err)
		}
	})
	if out := runScript(target, sum, true, "5"); !strings.Contains(out, "an artifact does not match ARTIFACTS.sha256") {
		t.Errorf("an artifact that differs from its listed sha256: %q", out)
	}

	target, sum = build(t, nil)
	if out := runScript(target, sum, false, "1"); !strings.Contains(out, "no bundle arrived within 1 s") {
		t.Errorf("no copy: %q", out)
	}

	other := "amd64"
	if arch == "amd64" {
		other = "arm64"
	}
	target, sum = build(t, nil)
	if out := runScriptArch(t, target, sum, true, "5", other); !strings.Contains(out, "the bundle is linux/"+other+" but the node is linux/"+arch) {
		t.Errorf("a bundle for another architecture: %q", out)
	}
}

func runScriptArch(t *testing.T, target, sum string, ready bool, timeout, arch string) string {
	t.Helper()
	if ready {
		if err := os.WriteFile(filepath.Join(target, "READY"), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command("sh", "-c", podBundleScript)
	cmd.Env = append(os.Environ(), "K8SBENCH_BUNDLE_DIR="+target, "K8SBENCH_BUNDLE_SHA256="+sum, "K8SBENCH_DELIVERY_TIMEOUT="+timeout, "K8SBENCH_ROOT="+target+"/pig", "K8SBENCH_BUNDLE_ARCH="+arch)
	out, _ := cmd.CombinedOutput()
	return string(out)
}

// Regression: an exported TINYGO reaches build-wasm.sh. The bundle step used to pass its own -tinygo default
// ("tinygo") as TINYGO, so a TinyGo that is not on PATH was "not found" even with TINYGO set.
func TestBundleUsesTheTinyGoFromTheEnvironment(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	// A TinyGo stand-in outside PATH: it reports a release no build accepts and, as tinygo build does, writes its -o
	// argument. A build-wasm.sh that checks the release (the Durable core's, whose runtime fork is for one TinyGo) refuses
	// it naming that release; one that does not check builds with it and leaves the stand-in's output. Either shows that
	// the TINYGO in the environment is the one build-wasm.sh ran.
	dir := t.TempDir()
	fake := filepath.Join(dir, "tinygo")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\n[ \"$1\" = version ] && echo 'tinygo version 9.9.9-standin linux/amd64' && exit 0\nwhile [ $# -gt 0 ]; do [ \"$1\" = -o ] && printf 'tinygo stand-in' > \"$2\"; shift; done\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	// build-wasm.sh needs sh and the base tools. A system TinyGo on this PATH does not hide the bug: it would neither
	// report the stand-in's release nor write the stand-in's output.
	t.Setenv("PATH", "/usr/bin:/bin")
	getenv := func(k string) string { return map[string]string{"TINYGO": fake}[k] }
	path, err := resolveTinyGo("", getenv)
	if err != nil || path != fake {
		t.Fatalf("TINYGO=%s resolved to %q, %v", fake, path, err)
	}
	out := filepath.Join(t.TempDir(), "core-tinygo.wasm")
	err = buildWasmCore(context.Background(), root, "tinygo", out, path, goBuildEnv(root))
	if err == nil || !strings.Contains(err.Error(), "tinygo 9.9.9-standin") {
		t.Errorf("build-wasm.sh did not run the TinyGo from TINYGO: %v", err)
	}

	if p, err := resolveTinyGo(fake, func(string) string { return "/nowhere/tinygo" }); err != nil || p != fake {
		t.Errorf("-tinygo wins over TINYGO: %q %v", p, err)
	}
	t.Setenv("PATH", t.TempDir())
	if _, err := resolveTinyGo("", func(string) string { return "" }); err == nil || !strings.Contains(err.Error(), "set TINYGO") {
		t.Errorf("no TinyGo anywhere must fail before any build: %v", err)
	}
}

// The durable-core profile's pieces in a bundle: its Node programs under core/ are listed and checked like every other
// artifact, and the native bench binary must be a Linux executable of the bundle's architecture.
func TestBundleCarriesTheDurableCorePrograms(t *testing.T) {
	dir := fakeBundle(t, elf.EM_X86_64)
	if err := os.MkdirAll(filepath.Join(dir, "core"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"bench-pi.mjs", "bench-pig.mjs"} {
		if err := os.WriteFile(filepath.Join(dir, "core", f), []byte("// "+f+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeELF(t, filepath.Join(dir, "bin", "sqlhost-bench"), elf.EM_X86_64)
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
	for _, want := range []string{"core/bench-pi.mjs", "core/bench-pig.mjs", "bin/sqlhost-bench"} {
		if !slices.Contains(paths, want) {
			t.Errorf("ARTIFACTS.sha256 lists %v, without %s", paths, want)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "core", "bench-pig.mjs"), []byte("changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := inspectBundle(dir); err == nil || !strings.Contains(err.Error(), "core/bench-pig.mjs has sha256") {
		t.Errorf("a changed program must be refused: %v", err)
	}
	writeELF(t, filepath.Join(dir, "bin", "sqlhost-bench"), elf.EM_AARCH64)
	if err := writeArtifactList(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := inspectBundle(dir); err == nil {
		t.Error("a native bench binary of another architecture must be refused")
	}
}

// The programs build only from the Pi commit durable/contract/PIN names, and the builder says how to name the checkout.
func TestCoreProgramsNeedThePinnedPiCheckout(t *testing.T) {
	root := t.TempDir()
	for path, content := range map[string]string{
		"durable/core/host/cf/bench/build.mjs": "throw new Error('not run')\n",
		"durable/contract/PIN":                 "main   0123456789012345678901234567890123456789\n",
	} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, path)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, path), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var log strings.Builder
	if err := buildCorePrograms(context.Background(), root, t.TempDir(), "", &log); err == nil || !strings.Contains(err.Error(), "-pi-source") {
		t.Errorf("no Pi checkout: %v", err)
	}
	pi := t.TempDir()
	for _, args := range [][]string{{"init", "-q"}, {"-c", "user.email=t@example.invalid", "-c", "user.name=t", "commit", "-q", "--allow-empty", "--no-gpg-sign", "-m", "x"}} {
		if out, err := exec.Command("git", append([]string{"-C", pi}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	if err := buildCorePrograms(context.Background(), root, t.TempDir(), pi, &log); err == nil || !strings.Contains(err.Error(), "durable/contract/PIN names main 0123456789012345678901234567890123456789") {
		t.Errorf("a Pi checkout at another commit: %v", err)
	}
	if err := buildCorePrograms(context.Background(), t.TempDir(), t.TempDir(), "", &log); err != nil {
		t.Errorf("a tree without the builder has nothing to build: %v", err)
	}
	// At the pinned commit but with a tracked file changed: the programs would not be built from the pinned sources.
	if err := os.WriteFile(filepath.Join(pi, "f"), []byte("pinned\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "f"}, {"-c", "user.email=t@example.invalid", "-c", "user.name=t", "commit", "-q", "--no-gpg-sign", "-m", "f"}} {
		if out, err := exec.Command("git", append([]string{"-C", pi}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	head, err := exec.Command("git", "-C", pi, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "durable/contract/PIN"), []byte("main "+strings.TrimSpace(string(head))+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pi, "f"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := buildCorePrograms(context.Background(), root, t.TempDir(), pi, &log); err == nil || !strings.Contains(err.Error(), "has changes to tracked files") {
		t.Errorf("a Pi checkout with a changed tracked file: %v", err)
	}
}

// The bundle's base image and the benchmark image run the one Node release the bundle records.
func TestNodeReleaseIsOneEverywhere(t *testing.T) {
	dockerfile, err := os.ReadFile(filepath.Join("..", "..", "image", "Dockerfile"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(dockerfile), "\nARG NODE_VERSION="+nodeVersion+"\n") {
		t.Errorf("bench/k8s/image/Dockerfile does not pin NODE_VERSION=%s", nodeVersion)
	}
	if !strings.HasPrefix(defaultBaseImage, "node:"+nodeVersion+"-") {
		t.Errorf("base image %s is not Node %s", defaultBaseImage, nodeVersion)
	}
}

// The bundle's programs build through the repository's go.work even when the caller exports GOWORK=off: with the published
// extensions/sdk pin the root module does not compile (internal/jsstring uses a newer json.Encoder). The image does the same.
func TestBundleBuildsThroughTheWorkspace(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOWORK", "off")
	got, err := output(context.Background(), root, goBuildEnv(root), "go", "env", "GOWORK")
	if want := filepath.Join(root, "go.work"); err != nil || got != want {
		t.Errorf("go env GOWORK under the bundle's environment: %q %v, want %s", got, err, want)
	}
	dockerfile, err := os.ReadFile(filepath.Join("..", "..", "image", "Dockerfile"))
	if err != nil {
		t.Fatal(err)
	}
	if text := string(dockerfile); !strings.Contains(text, "GOWORK=/src/go.work") || !strings.Contains(text, "\nWORKDIR /src\n") || strings.Contains(text, "GOWORK=off") {
		t.Error("bench/k8s/image/Dockerfile must build through /src/go.work")
	}
}

// fakeAgentBundle is fakeBundle with the agents: PiG, the interpreter, and Pi's coding agent CLI and another of Pi's
// packages beside its lockfile and a third-party package. The pins are the fixture's.
func fakeAgentBundle(t *testing.T) string {
	t.Helper()
	dir := fakeAgentInstall(t)
	writeELF(t, filepath.Join(dir, "bin", "pig"), elf.EM_X86_64)
	writeELF(t, filepath.Join(dir, "bin", "sessionscale"), elf.EM_X86_64)
	if err := os.MkdirAll(filepath.Join(dir, filepath.Dir(cpythonPython)), 0o755); err != nil {
		t.Fatal(err)
	}
	writeELF(t, filepath.Join(dir, cpythonPython), elf.EM_X86_64)
	if err := os.MkdirAll(filepath.Join(dir, pigbenchDir), 0o755); err != nil {
		t.Fatal(err)
	}
	for path, content := range map[string]string{pigbenchLauncher: "import runpy\n", pigbenchDir + "/__main__.py": "def main(): pass\n", pigbenchDir + "/k8s.py": "SIZES = {}\n"} {
		if err := os.WriteFile(filepath.Join(dir, path), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := writeArtifactList(dir); err != nil {
		t.Fatal(err)
	}
	return dir
}

// fakePiAgentPackage is a file of one of Pi's own packages other than the CLI.
const fakePiAgentPackage = piAgentPackages + "/pi-ai/dist/index.js"

// fakePiAgentDependency is a third-party package nested in one of Pi's, which the lockfile's integrity covers.
const fakePiAgentDependency = piAgentPackages + "/pi-ai/node_modules/openai/index.js"

// fakeAgentInstall is fakeBundle with an installed pi-agent and piAgentPins and piAgentPackagesTree set to it.
func fakeAgentInstall(t *testing.T) string {
	t.Helper()
	dir := fakeBundle(t, elf.EM_X86_64)
	pins := maps.Clone(piAgentPins)
	for path, content := range map[string]string{
		"pi-agent/package.json":      `{"name":"x"}`,
		"pi-agent/package-lock.json": `{"lockfileVersion":3}`,
		piAgentCLI:                   "#!/usr/bin/env node\n",
		piAgentPackages + "/pi-coding-agent/package.json": `{"version":"9.8.7"}`,
		fakePiAgentPackage:    "export {}\n",
		fakePiAgentDependency: "module.exports = {}\n",
	} {
		if err := os.MkdirAll(filepath.Join(dir, filepath.Dir(path)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, path), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, ok := pins[path]; ok {
			pins[path] = fmt.Sprintf("%x", sha256.Sum256([]byte(content)))
		}
	}
	savedPins, savedTree := piAgentPins, piAgentPackagesTree
	t.Cleanup(func() { piAgentPins, piAgentPackagesTree = savedPins, savedTree })
	piAgentPins = pins
	tree, err := packagesTreeSHA256(filepath.Join(dir, "pi-agent"), "node_modules/@earendil-works")
	if err != nil {
		t.Fatal(err)
	}
	piAgentPackagesTree = tree
	return dir
}

// An agent bundle lists both agents, so a profile that runs PiG and Pi's coding agent can check it before a Job is
// submitted; half of one is refused, and a Durable bundle cannot run an agent profile.
func TestInspectAgentBundle(t *testing.T) {
	dir := fakeAgentBundle(t)
	b, err := inspectBundle(dir)
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, a := range b.artifacts {
		paths = append(paths, a[0])
	}
	if !slices.Contains(paths, "bin/pig") || !slices.Contains(paths, "pi-agent/package-lock.json") || !slices.Contains(paths, piAgentCLI) || !slices.Contains(paths, cpythonPython) {
		t.Errorf("artifacts: %v", paths)
	}
	prof := &profile{Name: "agents", Rule: "agents", Targets: []target{
		{Name: "pig", Role: rolePig, Seed: []string{"k8sbench", "x"}, SeedMeta: "{dir}/m.json", Sample: []string{"pig", "--mode", "rpc"}},
		{Name: "pi", Role: roleReference, Seed: []string{"k8sbench", "x"}, SeedMeta: "{dir}/m.json", Sample: []string{"node", "{root}/" + piAgentCLI, "--mode", "rpc"}},
	}}
	if err := checkBundlePrograms(prof, []string{"pig", "pi"}, b); err != nil {
		t.Errorf("an agent bundle runs an agent profile: %v", err)
	}
	plain, err := inspectBundle(fakeBundle(t, elf.EM_X86_64))
	if err != nil {
		t.Fatal(err)
	}
	if err := checkBundlePrograms(prof, []string{"pig", "pi"}, plain); err == nil || !strings.Contains(err.Error(), "pig needs bin/pig") || !strings.Contains(err.Error(), "pi needs "+piAgentCLI) {
		t.Errorf("a Durable bundle cannot run an agent profile: %v", err)
	}

	half := fakeBundle(t, elf.EM_X86_64)
	writeELF(t, filepath.Join(half, "bin", "pig"), elf.EM_X86_64)
	if err := writeArtifactList(half); err != nil {
		t.Fatal(err)
	}
	if _, err := inspectBundle(half); err == nil || !strings.Contains(err.Error(), "must list pi-agent/package-lock.json") {
		t.Errorf("PiG without Pi's coding agent: %v", err)
	}

	// Any one agent file makes it an agent bundle, which must then carry the rest.
	for _, only := range []string{"pi-agent/package-lock.json", cpythonPython} {
		partial := fakeAgentBundle(t)
		var keep []string
		list, err := os.ReadFile(filepath.Join(partial, "ARTIFACTS.sha256"))
		if err != nil {
			t.Fatal(err)
		}
		for line := range strings.Lines(string(list)) {
			if _, path, _ := strings.Cut(strings.TrimSpace(line), "  "); path == only || !slices.Contains(agentArtifacts, path) {
				keep = append(keep, line)
			}
		}
		if err := os.WriteFile(filepath.Join(partial, "ARTIFACTS.sha256"), []byte(strings.Join(keep, "")), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := inspectBundle(partial); err == nil || !strings.Contains(err.Error(), "an agent bundle must list bin/pig") {
			t.Errorf("a bundle that lists only %s: %v", only, err)
		}
	}

	// The agent profiles run pigbench, so an agent bundle that leaves it out is refused before a Job runs it.
	nobench := fakeAgentBundle(t)
	list, err := os.ReadFile(filepath.Join(nobench, "ARTIFACTS.sha256"))
	if err != nil {
		t.Fatal(err)
	}
	var keep []string
	for line := range strings.Lines(string(list)) {
		if _, path, _ := strings.Cut(strings.TrimSpace(line), "  "); path != pigbenchLauncher && !strings.HasPrefix(path, "evals/") {
			keep = append(keep, line)
		}
	}
	if err := os.WriteFile(filepath.Join(nobench, "ARTIFACTS.sha256"), []byte(strings.Join(keep, "")), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := inspectBundle(nobench); err == nil || !strings.Contains(err.Error(), "an agent bundle must list pigbench.py") {
		t.Errorf("an agent bundle without pigbench: %v", err)
	}

	foreign := fakeAgentBundle(t)
	writeELF(t, filepath.Join(foreign, cpythonPython), elf.EM_AARCH64)
	if err := writeArtifactList(foreign); err != nil {
		t.Fatal(err)
	}
	if _, err := inspectBundle(foreign); err == nil || !strings.Contains(err.Error(), "is not a linux/amd64 executable") {
		t.Errorf("an arm64 CPython in an amd64 bundle: %v", err)
	}

	// A lockfile other than Pi's published one installs other packages, though npm ci checks each against it.
	unpinned := fakeAgentBundle(t)
	if err := os.WriteFile(filepath.Join(unpinned, "pi-agent", "package-lock.json"), []byte(`{"lockfileVersion":3,"x":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeArtifactList(unpinned); err != nil {
		t.Fatal(err)
	}
	if _, err := inspectBundle(unpinned); err == nil || !strings.Contains(err.Error(), "pi-agent/package-lock.json has sha256") || !strings.Contains(err.Error(), "the install of Pi "+committedPiPin().Version+" pins") {
		t.Errorf("an unpinned lockfile: %v", err)
	}

	// Pi's lockfile has no integrity for Pi's own packages, so npm ci would install any content under their names;
	// the content pin refuses a changed file in any of them, not only the CLI. A third-party file is the lockfile's.
	changed := fakeAgentBundle(t)
	if err := os.WriteFile(filepath.Join(changed, fakePiAgentPackage), []byte("export const x = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := inspectBundle(changed); err == nil || !strings.Contains(err.Error(), piAgentPackages+" has content sha256") {
		t.Errorf("a changed file of Pi's packages: %v", err)
	}
	dependency := fakeAgentBundle(t)
	if err := os.WriteFile(filepath.Join(dependency, fakePiAgentDependency), []byte("module.exports = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := inspectBundle(dependency); err != nil {
		t.Errorf("a nested third-party package is the lockfile's, not the content pin's: %v", err)
	}
}

// The content digest of Pi's packages is the one the image's shell pipeline prints: sha256sum lines of every file but
// nested node_modules, in bytewise path order (not directory walk order), and anything but files and directories is
// refused.
func TestPiAgentPackagesTree(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"node_modules/@earendil-works/a/b/x.js":                "b",
		"node_modules/@earendil-works/a/b.x":                   "b.x",
		"node_modules/@earendil-works/a/node_modules/dep/i.js": "dep",
		"node_modules/@earendil-works/z.json":                  "{}",
	}
	for path, content := range files {
		if err := os.MkdirAll(filepath.Join(dir, filepath.Dir(path)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, path), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var lines strings.Builder
	for _, path := range []string{"node_modules/@earendil-works/a/b.x", "node_modules/@earendil-works/a/b/x.js", "node_modules/@earendil-works/z.json"} {
		fmt.Fprintf(&lines, "%x  %s\n", sha256.Sum256([]byte(files[path])), path)
	}
	want := fmt.Sprintf("%x", sha256.Sum256([]byte(lines.String())))
	got, err := packagesTreeSHA256(dir, "node_modules/@earendil-works")
	if err != nil || got != want {
		t.Fatalf("packagesTreeSHA256 = %s, %v; want %s", got, err, want)
	}
	pipeline := `find node_modules/@earendil-works -type d -name node_modules -prune -o -type f -print | LC_ALL=C sort | xargs -d '\n' sha256sum | sha256sum`
	if out, err := exec.Command("sh", "-c", "cd \"$1\" && "+pipeline, "sh", dir).Output(); err != nil {
		t.Logf("no shell pipeline here: %v", err)
	} else if shell, _, _ := strings.Cut(string(out), " "); shell != want {
		t.Errorf("the shell pipeline prints %s, packagesTreeSHA256 %s", shell, want)
	}
	testenv.Symlink(t, "z.json", filepath.Join(dir, "node_modules/@earendil-works/link"))
	if _, err := packagesTreeSHA256(dir, "node_modules/@earendil-works"); err == nil || !strings.Contains(err.Error(), "is not a regular file") {
		t.Errorf("a symlink: %v", err)
	}
}

// finishAgentBundle refuses an unpinned install before it fetches anything, adds the interpreter without leaving the
// archive, and returns the VERSIONS lines of the agent and the interpreter.
func TestFinishAgentBundle(t *testing.T) {
	var archive bytes.Buffer
	gz := gzip.NewWriter(&archive)
	tw := tar.NewWriter(gz)
	for _, d := range []string{"python/", "python/bin/"} {
		if err := tw.WriteHeader(&tar.Header{Name: d, Typeflag: tar.TypeDir, Mode: 0o755}); err != nil {
			t.Fatal(err)
		}
	}
	elfDir := t.TempDir()
	writeELF(t, filepath.Join(elfDir, "python3.12"), elf.EM_X86_64)
	interp, err := os.ReadFile(filepath.Join(elfDir, "python3.12"))
	if err != nil {
		t.Fatal(err)
	}
	if err := tw.WriteHeader(&tar.Header{Name: cpythonPython, Typeflag: tar.TypeReg, Mode: 0o755, Size: int64(len(interp))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(interp); err != nil {
		t.Fatal(err)
	}
	if err := errors.Join(tw.Close(), gz.Close()); err != nil {
		t.Fatal(err)
	}
	local := filepath.Join(t.TempDir(), "cpython.tar.gz")
	if err := os.WriteFile(local, archive.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	saved := cpythonArchives
	t.Cleanup(func() { cpythonArchives = saved })
	cpythonArchives = map[string]pinnedArchive{"amd64": {url: "http://127.0.0.1:1/unused", size: int64(archive.Len()), sha256: fmt.Sprintf("%x", sha256.Sum256(archive.Bytes()))}}

	root, err := filepath.Abs(filepath.Join("..", "..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	dir := fakeAgentInstall(t)
	versions, err := finishAgentBundle(context.Background(), root, dir, "amd64", local, "", io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if want := "pi-agent=9.8.7\npython=" + cpythonRelease + "\n"; versions != want {
		t.Errorf("VERSIONS lines %q, want %q", versions, want)
	}
	if got, err := fileSHA256(filepath.Join(dir, cpythonPython)); err != nil || got != fmt.Sprintf("%x", sha256.Sum256(interp)) {
		t.Errorf("%s: %s, %v", cpythonPython, got, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "cpython.tar.gz")); !os.IsNotExist(err) {
		t.Errorf("the archive stays in the bundle: %v", err)
	}
	// pigbench: the launcher at the root and every module of bench/evals/pigbench, unchanged.
	for src, dst := range map[string]string{"bench/k8s/image/pigbench.py": pigbenchLauncher, "bench/evals/pigbench/k8s.py": pigbenchDir + "/k8s.py", "bench/evals/pigbench/longhistory.py": pigbenchDir + "/longhistory.py"} {
		want, _ := fileSHA256(filepath.Join(root, src))
		if got, err := fileSHA256(filepath.Join(dir, dst)); err != nil || got != want {
			t.Errorf("%s is not %s: %v", dst, src, err)
		}
	}
	if err := writeArtifactList(dir); err != nil {
		t.Fatal(err)
	}
	list, _ := os.ReadFile(filepath.Join(dir, "ARTIFACTS.sha256"))
	for _, want := range []string{"  " + pigbenchLauncher + "\n", "  " + pigbenchDir + "/k8s.py\n", "  " + pigbenchDir + "/__main__.py\n"} {
		if !strings.Contains(string(list), want) {
			t.Errorf("ARTIFACTS.sha256 does not list %q", strings.TrimSpace(want))
		}
	}

	unpinned := fakeAgentInstall(t)
	if err := os.WriteFile(filepath.Join(unpinned, fakePiAgentPackage), []byte("changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := finishAgentBundle(context.Background(), root, unpinned, "amd64", local, "", io.Discard); err == nil || !strings.Contains(err.Error(), "content sha256") {
		t.Errorf("an unpinned install: %v", err)
	}
	if _, err := os.Stat(filepath.Join(unpinned, "python")); !os.IsNotExist(err) {
		t.Errorf("an unpinned install still gets the interpreter: %v", err)
	}
}

// -python-archive without -agents would be ignored; it is refused before anything is built.
func TestBundlePythonArchiveNeedsAgents(t *testing.T) {
	err := cmdBundle(context.Background(), []string{"-out", t.TempDir(), "-python-archive", "x.tar.gz"}, io.Discard, func(string) string { return "" })
	if err == nil || !strings.Contains(err.Error(), "-python-archive needs -agents") {
		t.Errorf("cmdBundle: %v", err)
	}
}

// The committed install pair of a release pin is the one publishedAgentPins names (the image checks the installed agent
// with k8sbench's own pins: TestImageInstallsThePinnedReferences).
func TestPiAgentPins(t *testing.T) {
	if err := checkPinnedPair(filepath.Join("..", "..", "image")); err != nil {
		t.Error(err)
	}
}

func checkPinnedPair(image string) error {
	for _, f := range []string{"package.json", "package-lock.json"} {
		got, err := fileSHA256(filepath.Join(image, "pi-agent", f))
		if err != nil {
			return err
		}
		if want := publishedAgentPins.files["pi-agent/"+f]; got != want {
			return fmt.Errorf("bench/k8s/image/pi-agent/%s has sha256 %s, publishedAgentPins says %s", f, got, want)
		}
	}
	return nil
}

// The image carries what an agent bundle carries, under the same paths.
// A checkout without bench/evals/pigbench's modules gives no agent bundle: its profiles could not run.
func TestCopyPigbenchNeedsTheModules(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "bench", "k8s", "image"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "bench", "k8s", "image", pigbenchLauncher), []byte("import runpy\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := copyPigbench(root, t.TempDir()); err == nil || !strings.Contains(err.Error(), "bench/evals/pigbench has no modules") {
		t.Errorf("no modules: %v", err)
	}
}

func TestImageCarriesTheAgents(t *testing.T) {
	dockerfile, err := os.ReadFile(filepath.Join("..", "..", "image", "Dockerfile"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"-o /opt/pig/bin/pig ./cmd/pig", "-o /opt/pig/bin/sessionscale ./bench/sessionscale/cmd/sessionscale", "/pi/pi-agent/package-lock.json /opt/pig/pi-agent/", "pi-agent/package-lock.json", piAgentCLI,
		"cp bench/k8s/image/pigbench.py /opt/pig/" + pigbenchLauncher, "cp bench/evals/pigbench/*.py /opt/pig/" + pigbenchDir + "/", pigbenchLauncher + " " + pigbenchDir + "/*.py",
		"cp bench/evals/arms.toml bench/evals/arms.lock /opt/pig/evals/", pigbenchDir + "/*.py " + strings.Join(pigbenchArmFiles, " ")} {
		if !strings.Contains(string(dockerfile), want) {
			t.Errorf("bench/k8s/image/Dockerfile does not carry %s", want)
		}
	}
}

// Every built-in profile loads, and the agent profile longhistory runs only programs an agent bundle carries: a Durable
// bundle is refused before a Job is submitted.
func TestBuiltinProfilesLoad(t *testing.T) {
	names := builtinProfileNames()
	if !slices.Contains(names, "longhistory") {
		t.Fatalf("built-in profiles: %v", names)
	}
	for _, name := range names {
		if _, err := loadProfile(name); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	prof, err := loadProfile("longhistory")
	if err != nil {
		t.Fatal(err)
	}
	agents, err := inspectBundle(fakeAgentBundle(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := checkBundlePrograms(prof, []string{"pig", "pi"}, agents); err != nil {
		t.Errorf("an agent bundle runs longhistory: %v", err)
	}
	durable, err := inspectBundle(fakeBundle(t, elf.EM_X86_64))
	if err != nil {
		t.Fatal(err)
	}
	if err := checkBundlePrograms(prof, []string{"pig", "pi"}, durable); err == nil || !strings.Contains(err.Error(), "pig needs pigbench.py") || !strings.Contains(err.Error(), "pi needs "+piAgentCLI) {
		t.Errorf("a Durable bundle runs longhistory: %v", err)
	}
}

// The session-scale profiles run sessionscale and PiG from an agent bundle, which builds both, and Pi's coding agent; a
// Durable bundle is refused before a Job is submitted.
func TestSessionScaleProfilesRunAnAgentBundle(t *testing.T) {
	agents, err := inspectBundle(fakeAgentBundle(t))
	if err != nil {
		t.Fatal(err)
	}
	durable, err := inspectBundle(fakeBundle(t, elf.EM_X86_64))
	if err != nil {
		t.Fatal(err)
	}
	built := map[string]bool{}
	for _, step := range goBuildSteps("/out", true) {
		built[step[4]] = true
	}
	for _, name := range []string{"session-scale-context", "session-scale-entries"} {
		prof, err := loadProfile(name)
		if err != nil {
			t.Fatal(err)
		}
		if err := checkBundlePrograms(prof, []string{"pig", "pi"}, agents); err != nil {
			t.Errorf("an agent bundle runs %s: %v", name, err)
		}
		if err := checkBundlePrograms(prof, []string{"pig", "pi"}, durable); err == nil || !strings.Contains(err.Error(), "pig needs bin/sessionscale") {
			t.Errorf("a Durable bundle runs %s: %v", name, err)
		}
		for _, tg := range prof.Targets {
			for _, cmd := range [][]string{tg.Seed, tg.Sample} {
				if cmd[0] != "{root}/bin/sessionscale" || !built["/out/bin/sessionscale"] {
					t.Errorf("%s %s runs %s, which an agent bundle does not build", name, tg.Name, cmd[0])
				}
			}
			// The sample line carries -target as its target name; another name would file one target's samples
			// under the other.
			if i := slices.Index(tg.Sample, "-target"); i < 0 || i+1 >= len(tg.Sample) || tg.Sample[i+1] != tg.Name {
				t.Errorf("%s %s samples as %q", name, tg.Name, tg.Sample)
			}
			if !slices.Equal(tg.Seed, prof.Targets[0].Seed) {
				t.Errorf("%s %s seeds with %q, %s with %q", name, tg.Name, tg.Seed, prof.Targets[0].Name, prof.Targets[0].Seed)
			}
			if run := tg.Sample[len(tg.Sample)-1]; !strings.HasSuffix(run, " -ne") || (tg.Role == "pig" && (!strings.HasPrefix(run, "{root}/bin/pig ") || !built["/out/bin/pig"])) {
				t.Errorf("%s %s resumes with %q", name, tg.Name, run)
			}
		}
	}
	if !built["/out/bin/sessionscale"] || len(goBuildSteps("/out", false)) != 2 {
		t.Errorf("go build steps: %v", goBuildSteps("/out", false))
	}
}

// A bundle builds only with the Go release the tree pins in .go-version (CI's and the images' release; go.mod's toolchain
// line is only the minimum); a tree without .go-version pins none.
func TestBundleRefusesAnotherGoRelease(t *testing.T) {
	root := t.TempDir()
	if pinned, err := checkGoVersion(root, "go1.27.1"); err != nil || pinned != "" {
		t.Errorf("no .go-version: %q %v", pinned, err)
	}
	if err := os.WriteFile(filepath.Join(root, ".go-version"), []byte("1.27.2\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if pinned, err := checkGoVersion(root, "go1.27.2"); err != nil || pinned != "go1.27.2" {
		t.Errorf("the pinned release: %q %v", pinned, err)
	}
	_, err := checkGoVersion(root, "go1.27.1")
	if err == nil || !strings.Contains(err.Error(), "the tree pins Go go1.27.2 (.go-version) but go is go1.27.1; set GOTOOLCHAIN=go1.27.2") {
		t.Errorf("another release: %v", err)
	}
}

// Regression: k8sbench bundle checked the Go release against .go-version, but build-wasm.sh selects its own release
// (GOTOOLCHAIN defaults to go1.27.1 there), so on a tree pinning another release the Wasm cores built with a Go that was
// neither checked nor the one VERSIONS records. Every Go build of the bundle, the Wasm cores' included, now runs with the
// pinned GOTOOLCHAIN and the workspace, and another release is refused before anything is built. A stand-in go records
// the environment of each call; the stand-in fails the Wasm build, which ends the run there.
func TestBundleBuildsEveryProgramWithThePinnedGo(t *testing.T) {
	src, err := filepath.Abs(filepath.Join("..", "..", "..", "..", "durable", "core", "budget", "build-wasm.sh"))
	if err != nil {
		t.Fatal(err)
	}
	script, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	repo := t.TempDir()
	for _, f := range []struct {
		path string
		data []byte
	}{{"durable/core/budget/build-wasm.sh", script}, {"durable/core/cmd/corewasm/main.go", []byte("package main\n")}, {"go.work", []byte("go 1.26.0\n")}} {
		if err := os.MkdirAll(filepath.Join(repo, filepath.Dir(f.path)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(repo, f.path), f.data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "."}, {"-c", "user.email=t@example.invalid", "-c", "user.name=t", "commit", "-q", "--no-gpg-sign", "-m", "tree"}} {
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	tools := t.TempDir()
	log := filepath.Join(t.TempDir(), "go.log")
	standin := "#!/bin/sh\necho \"$* GOTOOLCHAIN=$GOTOOLCHAIN GOWORK=$GOWORK\" >> \"$STANDIN_LOG\"\n" +
		"[ \"$1 $2\" = \"env GOVERSION\" ] && echo go9.9.9\n" +
		"case \"$*\" in *c-shared*) echo 'stand-in: no Wasm build' >&2; exit 3 ;; esac\nexit 0\n"
	if err := os.WriteFile(filepath.Join(tools, "go"), []byte(standin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tools, "tinygo"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", tools+string(os.PathListSeparator)+"/usr/bin:/bin")
	t.Setenv("STANDIN_LOG", log)
	t.Setenv("GOWORK", "off")
	t.Setenv("GOTOOLCHAIN", "")
	t.Chdir(repo)
	root, err := filepath.EvalSymlinks(repo)
	if err != nil {
		t.Fatal(err)
	}
	bundle := func(pin string) (string, error) {
		if err := os.WriteFile(filepath.Join(repo, ".go-version"), []byte(pin+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(log); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		err := cmdBundle(context.Background(), []string{"-out", filepath.Join(t.TempDir(), "b"), "-allow-dirty", "-tinygo", filepath.Join(tools, "tinygo")}, io.Discard, func(string) string { return "" })
		data, _ := os.ReadFile(log)
		return string(data), err
	}

	calls, err := bundle("9.9.8")
	if err == nil || !strings.Contains(err.Error(), "the tree pins Go go9.9.8 (.go-version) but go is go9.9.9") {
		t.Errorf("another Go release: %v", err)
	}
	if strings.Contains(calls, "build") {
		t.Errorf("another Go release must be refused before anything is built:\n%s", calls)
	}

	calls, err = bundle("9.9.9")
	if err == nil || !strings.Contains(err.Error(), "stand-in: no Wasm build") {
		t.Fatalf("the run did not reach the Wasm build: %v\n%s", err, calls)
	}
	var builds int
	for line := range strings.Lines(calls) {
		if !strings.HasPrefix(line, "build ") {
			continue
		}
		builds++
		if want := " GOTOOLCHAIN=go9.9.9 GOWORK=" + filepath.Join(root, "go.work") + "\n"; !strings.HasSuffix(line, want) {
			t.Errorf("a build without the pinned Go and the workspace (want %q): %s", want, line)
		}
	}
	if !strings.Contains(calls, "-buildmode=c-shared") || builds != 3 {
		t.Errorf("want the two Linux programs and the Go Wasm core built, got:\n%s", calls)
	}
}
