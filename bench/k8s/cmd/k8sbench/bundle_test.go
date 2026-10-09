package main

import (
	"archive/tar"
	"bytes"
	"cmp"
	"compress/gzip"
	"context"
	"debug/elf"
	"encoding/binary"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

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
		"VERSIONS": "go=go1.27.1\narch=amd64\n",
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
	// A TinyGo stand-in outside PATH that writes its -o argument, as tinygo build does.
	dir := t.TempDir()
	fake := filepath.Join(dir, "tinygo")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nwhile [ $# -gt 0 ]; do [ \"$1\" = -o ] && printf 'tinygo stand-in' > \"$2\"; shift; done\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	// build-wasm.sh needs sh and the base tools. A system TinyGo on this PATH does not hide the bug: it would not write
	// the stand-in's output.
	t.Setenv("PATH", "/usr/bin:/bin")
	getenv := func(k string) string { return map[string]string{"TINYGO": fake}[k] }
	path, err := resolveTinyGo("", getenv)
	if err != nil || path != fake {
		t.Fatalf("TINYGO=%s resolved to %q, %v", fake, path, err)
	}
	out := filepath.Join(t.TempDir(), "core-tinygo.wasm")
	if err := buildWasmCore(context.Background(), root, "tinygo", out, path); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(out); err != nil || string(data) != "tinygo stand-in" {
		t.Errorf("build-wasm.sh did not run the TinyGo from TINYGO: %q %v", data, err)
	}

	if p, err := resolveTinyGo(fake, func(string) string { return "/nowhere/tinygo" }); err != nil || p != fake {
		t.Errorf("-tinygo wins over TINYGO: %q %v", p, err)
	}
	t.Setenv("PATH", t.TempDir())
	if _, err := resolveTinyGo("", func(string) string { return "" }); err == nil || !strings.Contains(err.Error(), "set TINYGO") {
		t.Errorf("no TinyGo anywhere must fail before any build: %v", err)
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
