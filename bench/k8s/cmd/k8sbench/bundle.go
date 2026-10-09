package main

import (
	"archive/tar"
	"bufio"
	"cmp"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"debug/elf"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// Delivery modes. In image mode the benchmark image holds every artifact. In bundle mode the pod runs a public base
// image and the runner copies a prebuilt artifact bundle into it with kubectl cp, so no registry is needed.
const (
	deliveryImage  = "image"
	deliveryBundle = "bundle"
)

// nodeVersion is the Node release every session runs: the base image's and the benchmark image's (bench/k8s/image/Dockerfile
// NODE_VERSION). The bundle records it in VERSIONS and the pod refuses another release, because the programs were built
// and checked on this one and Node's APIs move between releases (Node 26.11.0 renames node:sqlite's DatabaseSync and
// StatementSync to Database and Statement, keeping the old names as deprecated aliases, DEP0210 and DEP0211).
const nodeVersion = "24.19.0"

// defaultBaseImage is the official Node image the bundle runs on, pinned by its multi-architecture index digest. It has
// taskset, sha256sum and tar.
const defaultBaseImage = "node:" + nodeVersion + "-bookworm-slim@sha256:a9f5f7c91a432850b2a8a7797adf5eadb6c733ceed61167806cee7ea7fbc29df"

// bundleDir is where the pod receives the bundle; the session's install root is its pig/ subdirectory.
const bundleDir = "/bundle"

// requiredArtifacts are the files every bundle carries and lists in ARTIFACTS.sha256: the bench harness (k8sbench,
// durableperf), the Durable builds (the Go and TinyGo Wasm cores; durableperf is the native one), and the pi-durable
// reference bundle (bench.mjs and the lockfile its node_modules were installed from).
var requiredArtifacts = []string{"bin/k8sbench", "bin/durableperf", "dist/core-go.wasm", "dist/core-tinygo.wasm", "pi-durable/bench.mjs", "pi-durable/package-lock.json"}

// podBundleScript runs in the base image: it checks that the node has the bundle's architecture, waits for the runner's copy, checks the bundle's sha256 against the one
// in the Job, unpacks it, checks every artifact against ARTIFACTS.sha256, and starts the session. A failure prints the
// session's error line, so the runner reports it like any other.
const podBundleScript = `set -eu
b=${K8SBENCH_BUNDLE_DIR:-/bundle}
fail() { printf '{"t":"error","message":"%s"}\n' "$1"; exit 1; }
case "$(uname -m)" in x86_64) arch=amd64 ;; aarch64 | arm64) arch=arm64 ;; *) arch=$(uname -m) ;; esac
[ "$arch" = "$K8SBENCH_BUNDLE_ARCH" ] || fail "the bundle is linux/$K8SBENCH_BUNDLE_ARCH but the node is linux/$arch; build it with -arch $arch"
wait=${K8SBENCH_DELIVERY_TIMEOUT:-1800}
end=$(( $(date +%s) + wait ))
while [ ! -f "$b/READY" ]; do
	[ "$(date +%s)" -lt "$end" ] || fail "no bundle arrived within $wait s"
	sleep 1
done
echo "$K8SBENCH_BUNDLE_SHA256  $b/bundle.tar.gz" | sha256sum -c --status - || fail "bundle.tar.gz does not have the sha256 $K8SBENCH_BUNDLE_SHA256 the runner sent"
mkdir -p "$b/pig"
tar -xzf "$b/bundle.tar.gz" -C "$b/pig" --no-same-owner || fail "bundle.tar.gz does not unpack"
rm -f "$b/bundle.tar.gz"
(cd "$b/pig" && sha256sum -c --status --strict ARTIFACTS.sha256) || fail "an artifact does not match ARTIFACTS.sha256"
exec "$b/pig/bin/k8sbench" pod
`

// bundleInfo is a checked bundle directory.
type bundleInfo struct {
	dir       string
	commit    string
	arch      string
	artifacts [][2]string // path, sha256 in ARTIFACTS.sha256 order
}

// inspectBundle checks a bundle directory before it is sent: every required artifact is listed, every listed file
// has its sha256, the binaries are Linux ELF executables of one architecture, and the reference packages are installed.
func inspectBundle(dir string) (*bundleInfo, error) {
	b := &bundleInfo{dir: dir}
	commit, err := os.ReadFile(filepath.Join(dir, "COMMIT"))
	if err != nil {
		return nil, fmt.Errorf("bundle %s: %w", dir, err)
	}
	b.commit = strings.TrimSpace(string(commit))
	list, err := os.ReadFile(filepath.Join(dir, "ARTIFACTS.sha256"))
	if err != nil {
		return nil, fmt.Errorf("bundle %s: %w", dir, err)
	}
	listed := map[string]bool{}
	for line := range strings.Lines(string(list)) {
		sum, path, ok := strings.Cut(strings.TrimSpace(line), "  ")
		if !ok {
			continue
		}
		got, err := fileSHA256(filepath.Join(dir, path))
		if err != nil {
			return nil, fmt.Errorf("bundle %s: %w", dir, err)
		}
		if got != sum {
			return nil, fmt.Errorf("bundle %s: %s has sha256 %s, ARTIFACTS.sha256 says %s", dir, path, got, sum)
		}
		listed[path] = true
		b.artifacts = append(b.artifacts, [2]string{path, sum})
	}
	for _, path := range requiredArtifacts {
		if !listed[path] {
			return nil, fmt.Errorf("bundle %s: ARTIFACTS.sha256 does not list %s", dir, path)
		}
	}
	// Every program under bin/ runs on the node: each must be a Linux executable of the bundle's one architecture.
	var binaries []string
	for _, a := range b.artifacts {
		if strings.HasPrefix(a[0], "bin/") {
			binaries = append(binaries, a[0])
		}
	}
	for _, path := range binaries {
		arch, err := linuxArch(filepath.Join(dir, path))
		if err != nil {
			return nil, fmt.Errorf("bundle %s: %s: %w", dir, path, err)
		}
		if b.arch != "" && arch != b.arch {
			return nil, fmt.Errorf("bundle %s: %s is %s, other binaries are %s", dir, path, arch, b.arch)
		}
		b.arch = arch
	}
	if _, err := os.Stat(filepath.Join(dir, "pi-durable", "node_modules", "@earendil-works", "pi-durable", "package.json")); err != nil {
		return nil, fmt.Errorf("bundle %s: the pi-durable reference packages are not installed: %w", dir, err)
	}
	return b, nil
}

// linuxArch reports the Go architecture of a Linux ELF executable, and refuses anything else (a Mach-O binary from
// a host build without GOOS=linux, for example).
func linuxArch(path string) (string, error) {
	f, err := elf.Open(path)
	if err != nil {
		return "", fmt.Errorf("not a Linux ELF executable (build it with GOOS=linux): %w", err)
	}
	defer func() { _ = f.Close() }()
	switch f.Machine {
	case elf.EM_X86_64:
		return "amd64", nil
	case elf.EM_AARCH64:
		return "arm64", nil
	}
	return "", fmt.Errorf("unsupported ELF machine %s", f.Machine)
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// packBundle writes the bundle directory as one gzip tar file (paths relative to the directory, fixed times, no
// owners) and returns its sha256 and size.
func packBundle(dir, out string) (sum string, size int64, err error) {
	f, err := os.Create(out)
	if err != nil {
		return "", 0, err
	}
	h := sha256.New()
	counter := &countWriter{w: io.MultiWriter(f, h)}
	gz := gzip.NewWriter(counter)
	tw := tar.NewWriter(gz)
	walkErr := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil || rel == "." {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		link := ""
		if info.Mode()&fs.ModeSymlink != 0 {
			if link, err = os.Readlink(path); err != nil {
				return err
			}
		}
		hdr, err := tar.FileInfoHeader(info, link)
		if err != nil {
			return err
		}
		hdr.Name = filepath.ToSlash(rel)
		if d.IsDir() {
			hdr.Name += "/"
		}
		hdr.ModTime, hdr.AccessTime, hdr.ChangeTime = time.Unix(0, 0), time.Time{}, time.Time{}
		hdr.Uid, hdr.Gid, hdr.Uname, hdr.Gname = 0, 0, "", ""
		hdr.Format = tar.FormatPAX
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		src, err := os.Open(path)
		if err != nil {
			return err
		}
		_, err = io.Copy(tw, src)
		_ = src.Close()
		return err
	})
	err = errors.Join(walkErr, tw.Close(), gz.Close(), f.Close())
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), counter.n, nil
}

type countWriter struct {
	w io.Writer
	n int64
}

func (c *countWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

// cmdBundle builds a bundle directory on this machine: the harness and the native Durable benchmark cross-compiled for
// Linux, the Go and TinyGo Wasm cores, and pi-durable at the pinned Pi version installed from its lockfile for Linux.
// It needs Go, TinyGo and npm. bench/k8s/image/build.sh with BUNDLE=DIR builds the same layout in Docker instead.
func cmdBundle(ctx context.Context, args []string, stderr io.Writer, getenv func(string) string) error {
	fsFlags := flag.NewFlagSet("bundle", flag.ContinueOnError)
	fsFlags.SetOutput(stderr)
	out := fsFlags.String("out", "", "bundle directory to create (must not exist or be empty)")
	arch := fsFlags.String("arch", "amd64", "node architecture: amd64 or arm64")
	allowDirty := fsFlags.Bool("allow-dirty", false, "build a tree with uncommitted or untracked files (the commit gets -dirty)")
	tinygoFlag := fsFlags.String("tinygo", "", "TinyGo binary (default $TINYGO, else tinygo on PATH)")
	if err := fsFlags.Parse(args); err != nil {
		return err
	}
	// Resolve TinyGo before anything is built, so a missing toolchain fails first and build-wasm.sh gets one absolute path.
	tinygoPath, err := resolveTinyGo(*tinygoFlag, getenv)
	if err != nil {
		return err
	}
	tinygo := &tinygoPath
	if *out == "" {
		return errors.New("bundle needs -out DIR")
	}
	npmCPU := map[string]string{"amd64": "x64", "arm64": "arm64"}[*arch]
	if npmCPU == "" {
		return fmt.Errorf("-arch %q is not amd64 or arm64", *arch)
	}
	if entries, err := os.ReadDir(*out); err == nil && len(entries) > 0 {
		return fmt.Errorf("%s is not empty", *out)
	}
	outDir, err := filepath.Abs(*out)
	if err != nil {
		return err
	}
	root, err := output(ctx, "", nil, "git", "rev-parse", "--show-toplevel")
	if err != nil {
		return err
	}
	commit, err := output(ctx, root, nil, "git", "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	if status, err := output(ctx, root, nil, "git", "status", "--porcelain"); err != nil {
		return err
	} else if status != "" {
		if !*allowDirty {
			return errors.New("the tree has uncommitted or untracked files; commit them or pass -allow-dirty")
		}
		commit += "-dirty"
	}
	for _, dir := range []string{"bin", "dist", "pi-durable"} {
		if err := os.MkdirAll(filepath.Join(outDir, dir), 0o755); err != nil {
			return err
		}
	}
	linux := []string{"GOOS=linux", "GOARCH=" + *arch, "CGO_ENABLED=0", "GOWORK=off", "GOFLAGS=-buildvcs=false"}
	steps := [][]string{
		{"go", "build", "-trimpath", "-o", filepath.Join(outDir, "bin", "k8sbench"), "./bench/k8s/cmd/k8sbench"},
		{"go", "build", "-trimpath", "-o", filepath.Join(outDir, "bin", "durableperf"), "./bench/durableperf"},
	}
	for _, step := range steps {
		_, _ = fmt.Fprintf(stderr, "k8sbench bundle: %s\n", strings.Join(step, " "))
		if _, err := output(ctx, root, linux, step[0], step[1:]...); err != nil {
			return err
		}
	}
	if _, err := os.Stat(filepath.Join(root, "durable", "core", "sqlhost", "cmd", "bench")); err == nil {
		if _, err := output(ctx, root, linux, "go", "build", "-trimpath", "-o", filepath.Join(outDir, "bin", "sqlhost-bench"), "./durable/core/sqlhost/cmd/bench"); err != nil {
			return err
		}
	}
	for _, tc := range []string{"go", "tinygo"} {
		_, _ = fmt.Fprintf(stderr, "k8sbench bundle: %s Wasm core\n", tc)
		if err := buildWasmCore(ctx, root, tc, filepath.Join(outDir, "dist", "core-"+tc+".wasm"), *tinygo); err != nil {
			return err
		}
	}
	for _, f := range [][2]string{{"bench/k8s/image/pi-durable/package.json", "package.json"}, {"bench/k8s/image/pi-durable/package-lock.json", "package-lock.json"}, {"durable/interop/bench.mjs", "bench.mjs"}} {
		data, err := os.ReadFile(filepath.Join(root, f[0]))
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(outDir, "pi-durable", f[1]), data, 0o644); err != nil {
			return err
		}
	}
	_, _ = fmt.Fprintf(stderr, "k8sbench bundle: npm ci for linux/%s\n", npmCPU)
	if _, err := output(ctx, filepath.Join(outDir, "pi-durable"), nil, "npm", "ci", "--ignore-scripts", "--no-audit", "--no-fund", "--loglevel=error", "--os=linux", "--cpu="+npmCPU); err != nil {
		return err
	}
	goVersion, err := output(ctx, root, nil, "go", "env", "GOVERSION")
	if err != nil {
		return err
	}
	tinygoVersion, err := output(ctx, root, nil, *tinygo, "version")
	if err != nil {
		return err
	}
	if fields := strings.Fields(tinygoVersion); len(fields) >= 3 {
		tinygoVersion = fields[2]
	}
	var pkg struct{ Version string }
	if data, err := os.ReadFile(filepath.Join(outDir, "pi-durable", "node_modules", "@earendil-works", "pi-durable", "package.json")); err == nil {
		_ = json.Unmarshal(data, &pkg)
	}
	piReference := ""
	if pin, err := os.ReadFile(filepath.Join(root, "durable", "contract", "PIN")); err == nil {
		for line := range strings.Lines(string(pin)) {
			if f := strings.Fields(line); len(f) == 2 && f[0] == "main" {
				piReference = f[1]
			}
		}
	}
	if err := os.WriteFile(filepath.Join(outDir, "VERSIONS"), []byte(bundleVersions(goVersion, tinygoVersion, pkg.Version, piReference, *arch)), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(outDir, "COMMIT"), []byte(commit+"\n"), 0o644); err != nil {
		return err
	}
	if err := writeArtifactList(outDir); err != nil {
		return err
	}
	b, err := inspectBundle(outDir)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(stderr, "k8sbench bundle: %s for linux/%s at %s\n", outDir, b.arch, b.commit)
	return nil
}

// bundleVersions is the bundle's VERSIONS file. Its node entry is what `node --version` prints for nodeVersion, the
// form the pod compares with its runtime.
func bundleVersions(goVersion, tinygoVersion, piDurable, piReference, arch string) string {
	return fmt.Sprintf("go=%s\ntinygo=%s\nnode=v%s\npi-durable=%s\npi-reference=%s\narch=%s\nbuilder=k8sbench bundle\n", goVersion, tinygoVersion, nodeVersion, piDurable, piReference, arch)
}

// resolveTinyGo finds the TinyGo binary: the -tinygo flag, else $TINYGO, else tinygo on PATH. It returns an absolute
// path, because build-wasm.sh runs with the TINYGO the bundle step passes, not the caller's.
func resolveTinyGo(flagValue string, getenv func(string) string) (string, error) {
	name := cmp.Or(flagValue, getenv("TINYGO"), "tinygo")
	path, err := exec.LookPath(name)
	if err != nil {
		return "", fmt.Errorf("TinyGo not found as %q: pass -tinygo PATH, set TINYGO, or put tinygo on PATH: %w", name, err)
	}
	return filepath.Abs(path)
}

// buildWasmCore builds the core main package with one toolchain through durable/core/budget/build-wasm.sh, the script
// the image and the conformance gate use, with no VCS stamp so builders of one commit agree.
func buildWasmCore(ctx context.Context, root, toolchain, out, tinygo string) error {
	_, err := output(ctx, root, []string{"TINYGO=" + tinygo, "GOWORK=off", "GOFLAGS=-buildvcs=false"}, "sh", "durable/core/budget/build-wasm.sh", toolchain, "durable/core/cmd/corewasm", out)
	return err
}

// writeArtifactList writes ARTIFACTS.sha256 in sha256sum's format: every file of bin/ and dist/, then the reference's
// script and lockfile.
func writeArtifactList(dir string) error {
	var paths []string
	for _, sub := range []string{"bin", "dist"} {
		entries, err := os.ReadDir(filepath.Join(dir, sub))
		if err != nil {
			return err
		}
		for _, e := range entries {
			if e.Type().IsRegular() {
				paths = append(paths, sub+"/"+e.Name())
			}
		}
	}
	slices.Sort(paths)
	paths = append(paths, "pi-durable/bench.mjs", "pi-durable/package-lock.json")
	var b strings.Builder
	for _, p := range paths {
		sum, err := fileSHA256(filepath.Join(dir, p))
		if err != nil {
			return err
		}
		b.WriteString(sum + "  " + p + "\n")
	}
	return os.WriteFile(filepath.Join(dir, "ARTIFACTS.sha256"), []byte(b.String()), 0o644)
}

// output runs a command in dir with extra environment and returns its trimmed stdout; a failure carries its stderr.
func output(ctx context.Context, dir string, env []string, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	if env != nil {
		cmd.Env = append(os.Environ(), env...)
	}
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, lastLines(stderr.String(), 20))
	}
	return strings.TrimSpace(stdout.String()), nil
}

func lastLines(text string, n int) string {
	var lines []string
	scanner := bufio.NewScanner(strings.NewReader(text))
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
