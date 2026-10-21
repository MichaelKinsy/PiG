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
	"maps"
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

// piAgentCLI is Pi's coding agent CLI in an agent bundle, the reference program of the agent profiles.
const piAgentCLI = "pi-agent/node_modules/@earendil-works/pi-coding-agent/dist/bundle/cli.js"

// agentPins pin Pi's coding agent as an agent bundle or the image installs it: the sha256 of its install pair and CLI, and
// the content digest of Pi's own packages (packagesTreeSHA256).
type agentPins struct {
	files map[string]string
	tree  string
}

// publishedAgentPins pin a release pin's install: Pi's release-published install pair (pi-coding-agent-install-package.json
// and -package-lock.json at the pinned Pi version; bench/k8s/image/pi-agent holds them unmodified) and the CLI that npm ci
// installs from it. Pi's install lockfile records no integrity for Pi's own packages, so npm ci installs whatever the
// registry serves for their names and versions; the tree digest equals that of the registry tarballs at the pinned Pi
// version whose sha512 the registry publishes.
var publishedAgentPins = agentPins{
	files: map[string]string{
		"pi-agent/package.json":      "efa2ca2695c003c92febb3bca40c5de5f94d43a8aa78e1ba88420b30156d5e21",
		"pi-agent/package-lock.json": "65eacda789c30e1c3859a96eee2fd53cf4a18629d27dcf2aa6d7bdb3da02d719",
		piAgentCLI:                   "e79626f2dd6f94aa45d30f3fa63cd84319a6eefcd150b353cfaf274366926774",
	},
	tree: "2e12267f40fc87cec6a459fdb43486e1d707bb62aa37f7787bbef6b5f5e9dd18",
}

// sourceAgentPins pin a commit pin's install, by commit: the project pinnedProject writes from the frozen closure, whose
// lock holds the sha512 of every pack (so npm ci refuses any other build of Pi's packages, nested ones included), and
// the CLI and package tree npm ci installs from the packs `pin.py install-oracle` builds. Pi's own packages nest under
// the coding agent there, so the tree digest covers the coding agent's files and the lock's integrity the rest. The CLI
// is a launcher whose bytes equal the release's; its chunks differ. A pin bump to a commit adds its entry
// (TestCommittedPinHasAgentPins).
var sourceAgentPins = map[string]agentPins{
	"fdce48eb404ec014adaf2ac10a3a53bc6b1740c2": {
		files: map[string]string{
			"pi-agent/package.json":      "49bcc92772c7bb1fbb6597e3a3d5671b586f34627bd76c964b25b291af9a3d5e",
			"pi-agent/package-lock.json": "8576e51f320c1506e2a665934fa9d4dc5f21db920f6688b8efae7cfd5c1cdf41",
			piAgentCLI:                   "e79626f2dd6f94aa45d30f3fa63cd84319a6eefcd150b353cfaf274366926774",
		},
		tree: "40d223e54a40caa4fbe81637ade01a42dfdfe4e261eb932cce14ac7e1addec95",
	},
	"73f8de80bc87021375f8b525292ac07492253002": {
		files: map[string]string{
			"pi-agent/package.json":      "49bcc92772c7bb1fbb6597e3a3d5671b586f34627bd76c964b25b291af9a3d5e",
			"pi-agent/package-lock.json": "5c1fb4eb8812e40b9fd4eda11e044206d2598187e92fab14f2a9b24dbc8c4771",
			piAgentCLI:                   "e79626f2dd6f94aa45d30f3fa63cd84319a6eefcd150b353cfaf274366926774",
		},
		tree: "a887b2a5e318e7cf472f494d90ac61c2160cff2389b5518c0322bbdbf31295bd",
	},
	"42a3497d03ad17e308a2299fa824727894f2c0ec": {
		files: map[string]string{
			"pi-agent/package.json":      "1853ecaa7aaa617360c1a5630fc3a7b029ebf242099d8a822f98bbcb4e54b4cc",
			"pi-agent/package-lock.json": "dead0b6a26d6c7a203bbef5c6cba5c19947361a6cdb5e5f6ac81b2064abb3dd3",
			piAgentCLI:                   "e79626f2dd6f94aa45d30f3fa63cd84319a6eefcd150b353cfaf274366926774",
		},
		tree: "d2e7856625f6afcca8da489261d53bc9f925b637ff616b7103397f137dcaa609",
	},
	"ea448f454af474f3e2fe9733b331e17b0662f5d6": {
		files: map[string]string{
			"pi-agent/package.json":      "1853ecaa7aaa617360c1a5630fc3a7b029ebf242099d8a822f98bbcb4e54b4cc",
			"pi-agent/package-lock.json": "11302e580fd51a6f60ab1b18f44760cddda8b8dfb1e807d72c8feb00872d039f",
			piAgentCLI:                   "e79626f2dd6f94aa45d30f3fa63cd84319a6eefcd150b353cfaf274366926774",
		},
		tree: "d41ee68bd5ad70a0777e6a0692fbc6567dcbbd4078881fc05851dc772a64aa21",
	},
	"3daa20280f6a7c7cbe0935680c9a38fdc3863005": {
		files: map[string]string{
			"pi-agent/package.json":      "faa5f21540eb7cc1be13d23d0f6ff1491ef6d217fa7111839ba72db48f396ff4",
			"pi-agent/package-lock.json": "e68fd63b3effee126be09a732b191a0d426035ea61100839de34d0dfc150d5df",
			piAgentCLI:                   "e79626f2dd6f94aa45d30f3fa63cd84319a6eefcd150b353cfaf274366926774",
		},
		tree: "8438eb69422c0ecf0ab3c430920aca32ac0327aeab0c87636c99cba46a233107",
	},
	"c268154ca1171c2f9eefe75d3d7372a68aada2d5": {
		files: map[string]string{
			"pi-agent/package.json":      "49bcc92772c7bb1fbb6597e3a3d5671b586f34627bd76c964b25b291af9a3d5e",
			"pi-agent/package-lock.json": "16c9b47c29f6c90595d33853b005802d204be17ce31d70c2c1c49abb00cdfec9",
			piAgentCLI:                   "e79626f2dd6f94aa45d30f3fa63cd84319a6eefcd150b353cfaf274366926774",
		},
		tree: "7afb85c6edd364028f66ba0d332445f140df8b687d89a8d7839141936c8c0fb9",
	},
	"e17e2e10556cea6b674bb627726bef8ceed90065": {
		files: map[string]string{
			"pi-agent/package.json":      "49bcc92772c7bb1fbb6597e3a3d5671b586f34627bd76c964b25b291af9a3d5e",
			"pi-agent/package-lock.json": "09c38dd5a1b1e7998acd74078e1b205f53c80cdd45d15a7e8d544c219328c264",
			piAgentCLI:                   "e79626f2dd6f94aa45d30f3fa63cd84319a6eefcd150b353cfaf274366926774",
		},
		tree: "6d6721868233d8c00bfcbf6efb82d9cddaa1c778d60831b965c29c7951c88bdd",
	},
	"482c1381a00d8d00bc81697e4a980c593db6803e": {
		files: map[string]string{
			"pi-agent/package.json":      "49bcc92772c7bb1fbb6597e3a3d5671b586f34627bd76c964b25b291af9a3d5e",
			"pi-agent/package-lock.json": "1cf676948f45e017f810201e610faaeae645900ed856deb3a6db7ad8d54b424a",
			piAgentCLI:                   "e79626f2dd6f94aa45d30f3fa63cd84319a6eefcd150b353cfaf274366926774",
		},
		tree: "70c54d0f40935fe4fa1d65b2f68fb6391ebb74e562cb6d4a93a4936a19b1bdc2",
	},
	"1402973a9dbe8c627341e04c9744c54953e285ca": {
		files: map[string]string{
			"pi-agent/package.json":      "49bcc92772c7bb1fbb6597e3a3d5671b586f34627bd76c964b25b291af9a3d5e",
			"pi-agent/package-lock.json": "7a9e93040d613102a6ed3d0f27e395a28953dd0537d9f867d1ce983dd394ef7a",
			piAgentCLI:                   "e79626f2dd6f94aa45d30f3fa63cd84319a6eefcd150b353cfaf274366926774",
		},
		tree: "adb60a51ae5528a655308d856bdffef4ef4034c9976489511072f4e92d8cd3ee",
	},
	"8efd314fa3d3232ce197d8d7a6a5660530b9d83e": {
		files: map[string]string{
			"pi-agent/package.json":      "49bcc92772c7bb1fbb6597e3a3d5671b586f34627bd76c964b25b291af9a3d5e",
			"pi-agent/package-lock.json": "b1aad378d5c7d17ee90512d256d7ed0945798a9976d339a9c72b95e7e5cca684",
			piAgentCLI:                   "e79626f2dd6f94aa45d30f3fa63cd84319a6eefcd150b353cfaf274366926774",
		},
		tree: "611bba599e7f2fdb6d88c0f517018bb23b8a12cdd714839860157e8d62a52ddc",
	},
	"b06c1f2cf1dea6af8748ddeee18c23587d2cb7c6": {
		files: map[string]string{
			"pi-agent/package.json":      "faa5f21540eb7cc1be13d23d0f6ff1491ef6d217fa7111839ba72db48f396ff4",
			"pi-agent/package-lock.json": "ea5b7be656bfcb995140a9fe5e18e53e2867540d3b86ca265a1403fab0ef92a9",
			piAgentCLI:                   "e79626f2dd6f94aa45d30f3fa63cd84319a6eefcd150b353cfaf274366926774",
		},
		tree: "30de7287b09e741d24a4a07ecca610854bb2e96a1f02e88481a70ceab6576bb4",
	},
}

// pinnedAgentPins are the pins of the committed Pi pin.
func pinnedAgentPins(pin piPin) (agentPins, error) {
	if pin.isRelease() {
		return publishedAgentPins, nil
	}
	pins, ok := sourceAgentPins[pin.Commit]
	if !ok {
		return agentPins{}, fmt.Errorf("k8sbench has no pins for Pi's coding agent built from commit %s (sourceAgentPins)", pin.Commit)
	}
	return pins, nil
}

// piAgentPins and piAgentPackagesTree are the pins checkPiAgent applies: pinnedAgentPins of the committed pin.
var piAgentPins, piAgentPackagesTree = func() (map[string]string, string) {
	pins, _ := pinnedAgentPins(committedPiPin())
	return pins.files, pins.tree
}()

// piAgentPackages holds Pi's own packages in an agent bundle (the coding agent, pi-ai, pi-tui, chord and the rest).
const piAgentPackages = "pi-agent/node_modules/@earendil-works"

// checkPiAgent checks an installed pi-agent directory against piAgentPins and piAgentPackagesTree.
func checkPiAgent(dir string) error {
	if len(piAgentPins) == 0 {
		_, err := pinnedAgentPins(committedPiPin())
		return err
	}
	for _, path := range slices.Sorted(maps.Keys(piAgentPins)) {
		got, err := fileSHA256(filepath.Join(dir, path))
		if err != nil {
			return err
		}
		if want := piAgentPins[path]; got != want {
			return fmt.Errorf("%s has sha256 %s, but the install of Pi %s pins %s", path, got, committedPiPin().Version, want)
		}
	}
	got, err := packagesTreeSHA256(filepath.Join(dir, "pi-agent"), strings.TrimPrefix(piAgentPackages, "pi-agent/"))
	if err != nil {
		return err
	}
	if got != piAgentPackagesTree {
		return fmt.Errorf("%s has content sha256 %s, but Pi %s's packages pin %s", piAgentPackages, got, committedPiPin().Version, piAgentPackagesTree)
	}
	return nil
}

// packagesTreeSHA256 is the content digest piAgentPackagesTree describes, of the tree sub of dir. It refuses an entry
// that is neither a directory nor a regular file, and a path sha256sum would escape.
func packagesTreeSHA256(dir, sub string) (string, error) {
	var paths []string
	err := filepath.WalkDir(filepath.Join(dir, sub), func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		switch {
		case d.IsDir() && d.Name() == "node_modules" && rel != sub:
			return fs.SkipDir
		case d.IsDir():
			return nil
		case !d.Type().IsRegular():
			return fmt.Errorf("%s is not a regular file", rel)
		case strings.ContainsAny(rel, "\\\n\r"):
			return fmt.Errorf("%q has a character sha256sum escapes", rel)
		}
		paths = append(paths, rel)
		return nil
	})
	if err != nil {
		return "", err
	}
	if len(paths) == 0 {
		return "", fmt.Errorf("%s has no files", sub)
	}
	slices.Sort(paths)
	h := sha256.New()
	for _, p := range paths {
		sum, err := fileSHA256(filepath.Join(dir, p))
		if err != nil {
			return "", err
		}
		_, _ = fmt.Fprintf(h, "%s  %s\n", sum, p)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// pigbenchLauncher runs bench/evals' pigbench, which an agent bundle carries under evals/pigbench, with the bundle's
// CPython: the agent profiles' seed and sample commands.
const (
	pigbenchLauncher = "pigbench.py"
	pigbenchDir      = "evals/pigbench"
)

// pigbenchArmFiles are bench/evals' arm definitions an agent bundle carries beside pigbench, as the repository does.
var pigbenchArmFiles = []string{"evals/arms.toml", "evals/arms.lock"}

// agentArtifacts are the files an agent bundle lists in ARTIFACTS.sha256; a bundle that lists one lists all.
var agentArtifacts = []string{"bin/pig", "pi-agent/package-lock.json", piAgentCLI, cpythonPython, pigbenchLauncher, pigbenchDir + "/__main__.py"}

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
	// An agent bundle carries both agents (PiG and Pi's coding agent from its lockfile) and the interpreter.
	if slices.ContainsFunc(agentArtifacts, func(p string) bool { return listed[p] }) {
		for _, path := range agentArtifacts {
			if !listed[path] {
				return nil, fmt.Errorf("bundle %s: an agent bundle must list %s in ARTIFACTS.sha256", dir, path)
			}
		}
		if err := checkPiAgent(dir); err != nil {
			return nil, fmt.Errorf("bundle %s: %w", dir, err)
		}
		arch, err := linuxArch(filepath.Join(dir, cpythonPython))
		if err != nil {
			return nil, fmt.Errorf("bundle %s: %s: %w", dir, cpythonPython, err)
		}
		if arch != b.arch {
			return nil, fmt.Errorf("bundle %s: %s is not a linux/%s executable but linux/%s", dir, cpythonPython, b.arch, arch)
		}
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
	agents := fsFlags.Bool("agents", false, "also carry what agent profiles run: PiG built from the commit (bin/pig), Pi's coding agent at the pinned version (pi-agent/) and the pinned standalone CPython (python/)")
	pythonArchive := fsFlags.String("python-archive", "", "with -agents, an offline copy of the pinned CPython archive for -arch (default: download it)")
	armsFlag := fsFlags.String("arms", "", "with -agents, comma-separated ids of "+armsLock+" to install under arms/<id> as well (such as codex), each pinned there")
	tasksSrc := fsFlags.String("tasks", "", "with -agents, the dataset checkout's tasks directory: carry each task of "+tasksLockFile+" (instruction.md, tests/) and the pinned Node.js (node/) for GPU trials")
	nodeArchive := fsFlags.String("node-archive", "", "with -tasks, an offline copy of the pinned Node.js archive for -arch (default: download it)")
	tinygoFlag := fsFlags.String("tinygo", "", "TinyGo binary (default $TINYGO, else tinygo on PATH)")
	piFlag := fsFlags.String("pi-source", "", "Pi checkout at durable/contract/PIN main, for the durable-core programs (default $CONTRACT_PI_SOURCE)")
	piBuilds := fsFlags.String("pi-builds", "", "when the Pi pin is a commit, the build store where `python3 automation/pin/pin.py install-oracle` left its packs (default: $PIG_PI_BUILD_STORE, else $PIG_DEV_HOME/pi-builds, else ~/.cache/pig-dev/pi-builds)")
	if err := fsFlags.Parse(args); err != nil {
		return err
	}
	if *pythonArchive != "" && !*agents {
		return errors.New("-python-archive needs -agents")
	}
	armIDs, err := parseArmIDs(*armsFlag)
	if err != nil {
		return err
	}
	if len(armIDs) > 0 && !*agents {
		return errors.New("-arms needs -agents")
	}
	if (*tasksSrc != "" && !*agents) || (*nodeArchive != "" && *tasksSrc == "") {
		return errors.New("-tasks needs -agents, and -node-archive needs -tasks")
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
	// The toolchain is checked before anything is built: a bundle built with another Go runs other cores than the gate checked.
	goVersion, err := output(ctx, root, nil, "go", "env", "GOVERSION")
	if err != nil {
		return err
	}
	pinned, err := checkGoVersion(root, goVersion)
	if err != nil {
		return err
	}
	build := goBuildEnv(root)
	if pinned != "" {
		// build-wasm.sh selects its own Go release unless GOTOOLCHAIN is set: every program, the Wasm cores included, builds
		// with the release that was checked and that VERSIONS records.
		build = append(build, "GOTOOLCHAIN="+pinned)
	}
	dirs := []string{"bin", "dist", "pi-durable"}
	if *agents {
		dirs = append(dirs, "pi-agent")
	}
	for _, dir := range dirs {
		if err := os.MkdirAll(filepath.Join(outDir, dir), 0o755); err != nil {
			return err
		}
	}
	linux := append([]string{"GOOS=linux", "GOARCH=" + *arch, "CGO_ENABLED=0"}, build...)
	for _, step := range goBuildSteps(outDir, *agents) {
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
	if err := buildCorePrograms(ctx, root, outDir, cmp.Or(*piFlag, getenv("CONTRACT_PI_SOURCE")), stderr); err != nil {
		return err
	}
	for _, tc := range []string{"go", "tinygo"} {
		_, _ = fmt.Fprintf(stderr, "k8sbench bundle: %s Wasm core\n", tc)
		if err := buildWasmCore(ctx, root, tc, filepath.Join(outDir, "dist", "core-"+tc+".wasm"), *tinygo, build); err != nil {
			return err
		}
	}
	pin := committedPiPin()
	store := filepath.Join(cmp.Or(*piBuilds, piBuildStore(getenv)), pin.Commit)
	piDurable, err := writePinnedInstall(root, filepath.Join(outDir, "pi-durable"), filepath.Join(store, durableReference.packs), durableReference, pin)
	if err != nil {
		return err
	}
	benchMjs, err := os.ReadFile(filepath.Join(root, "durable", "interop", "bench.mjs"))
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(outDir, "pi-durable", "bench.mjs"), benchMjs, 0o644); err != nil {
		return err
	}
	npmDirs := []string{"pi-durable"}
	piAgent := ""
	if *agents {
		if piAgent, err = writePinnedInstall(root, filepath.Join(outDir, "pi-agent"), filepath.Join(store, agentReference.packs), agentReference, pin); err != nil {
			return err
		}
		npmDirs = append(npmDirs, "pi-agent")
	}
	for _, dir := range npmDirs {
		_, _ = fmt.Fprintf(stderr, "k8sbench bundle: npm ci %s for linux/%s\n", dir, npmCPU)
		if _, err := output(ctx, filepath.Join(outDir, dir), nil, "npm", "ci", "--ignore-scripts", "--no-audit", "--no-fund", "--loglevel=error", "--os=linux", "--cpu="+npmCPU); err != nil {
			return err
		}
	}
	tinygoVersion, err := output(ctx, root, nil, *tinygo, "version")
	if err != nil {
		return err
	}
	if fields := strings.Fields(tinygoVersion); len(fields) >= 3 {
		tinygoVersion = fields[2]
	}
	if err := checkInstalledVersion(outDir, durableReference, pin); err != nil {
		return err
	}
	piReference := ""
	if pin, err := os.ReadFile(filepath.Join(root, "durable", "contract", "PIN")); err == nil {
		for line := range strings.Lines(string(pin)) {
			if f := strings.Fields(line); len(f) == 2 && f[0] == "main" {
				piReference = f[1]
			}
		}
	}
	versions := bundleVersions(goVersion, tinygoVersion, piDurable, piReference, *arch)
	if *agents {
		agentVersions, err := finishAgentBundle(ctx, root, outDir, *arch, *pythonArchive, piAgent, stderr)
		if err != nil {
			return err
		}
		versions += agentVersions
		armVersions, err := installArms(ctx, root, outDir, *arch, armIDs, stderr)
		if err != nil {
			return err
		}
		versions += armVersions
		if *tasksSrc != "" {
			tl, err := readTasksLock(filepath.Join(root, tasksLockFile))
			if err != nil {
				return err
			}
			if err := addTasks(*tasksSrc, outDir, tl); err != nil {
				return err
			}
			if err := addNode(ctx, outDir, *arch, *nodeArchive, stderr); err != nil {
				return err
			}
			versions += "node=" + nodeVersion + "\ntasks=" + tl.Dataset + "@" + tl.Commit + "\n"
		}
	}
	if err := os.WriteFile(filepath.Join(outDir, "VERSIONS"), []byte(versions), 0o644); err != nil {
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

// finishAgentBundle checks the installed pi-agent against its pins, adds the pinned CPython for arch and returns the
// agent bundle's VERSIONS lines.
func finishAgentBundle(ctx context.Context, root, outDir, arch, pythonArchive, version string, stderr io.Writer) (string, error) {
	if err := checkPiAgent(outDir); err != nil {
		return "", err
	}
	if err := copyPigbench(root, outDir); err != nil {
		return "", err
	}
	if err := addCPython(ctx, outDir, arch, pythonArchive, stderr); err != nil {
		return "", err
	}
	var agent struct{ Version string }
	data, err := os.ReadFile(filepath.Join(outDir, filepath.FromSlash(piAgentPackages), "pi-coding-agent", "package.json"))
	if err != nil {
		return "", err
	}
	if err := json.Unmarshal(data, &agent); err != nil {
		return "", err
	}
	return "pi-agent=" + cmp.Or(version, agent.Version) + "\npython=" + cpythonRelease + "\n", nil
}

// copyPigbench copies bench/evals/pigbench's modules to evals/pigbench, bench/evals' arms.toml and arms.lock (the arms'
// harness settings and pins) to evals/, and the launcher to the bundle's root.
func copyPigbench(root, outDir string) error {
	modules, err := filepath.Glob(filepath.Join(root, "bench", "evals", "pigbench", "*.py"))
	if err != nil {
		return err
	}
	if len(modules) == 0 {
		return errors.New("bench/evals/pigbench has no modules; an agent bundle runs the agent profiles through it")
	}
	if err := os.MkdirAll(filepath.Join(outDir, filepath.FromSlash(pigbenchDir)), 0o755); err != nil {
		return err
	}
	copies := [][2]string{{filepath.Join(root, "bench", "k8s", "image", pigbenchLauncher), filepath.Join(outDir, pigbenchLauncher)}}
	for _, f := range pigbenchArmFiles {
		copies = append(copies, [2]string{filepath.Join(root, "bench", filepath.FromSlash(f)), filepath.Join(outDir, filepath.FromSlash(f))})
	}
	for _, m := range modules {
		copies = append(copies, [2]string{m, filepath.Join(outDir, filepath.FromSlash(pigbenchDir), filepath.Base(m))})
	}
	for _, c := range copies {
		data, err := os.ReadFile(c[0])
		if err != nil {
			return err
		}
		if err := os.WriteFile(c[1], data, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// bundleVersions is the bundle's VERSIONS file. Its node entry is what `node --version` prints for nodeVersion, the
// form the pod compares with its runtime.
func bundleVersions(goVersion, tinygoVersion, piDurable, piReference, arch string) string {
	return fmt.Sprintf("go=%s\ntinygo=%s\nnode=v%s\npi-durable=%s\npi-reference=%s\narch=%s\nbuilder=k8sbench bundle\n", goVersion, tinygoVersion, nodeVersion, piDurable, piReference, arch)
}

// buildCorePrograms builds the durable-core profile's Node programs into outDir/core when the tree has their builder
// (durable/core/host/cf/bench/build.mjs): bench.mjs on the Pi sources the conformance gate captures its corpus from, and on
// the core through the host's facade, each one self-contained file. The Pi checkout must be at durable/contract/PIN's main
// commit with its dependencies installed; the host's own dependencies (esbuild) are installed from its lockfile.
func buildCorePrograms(ctx context.Context, root, outDir, piSource string, stderr io.Writer) error {
	builder := filepath.Join(root, "durable", "core", "host", "cf", "bench", "build.mjs")
	if _, err := os.Stat(builder); err != nil {
		return nil
	}
	if piSource == "" {
		return errors.New("the durable-core programs need the Pi checkout at durable/contract/PIN main: pass -pi-source DIR or set CONTRACT_PI_SOURCE")
	}
	pin, err := os.ReadFile(filepath.Join(root, "durable", "contract", "PIN"))
	if err != nil {
		return err
	}
	want := ""
	for line := range strings.Lines(string(pin)) {
		if f := strings.Fields(line); len(f) == 2 && f[0] == "main" {
			want = f[1]
		}
	}
	head, err := output(ctx, piSource, nil, "git", "rev-parse", "HEAD")
	if err != nil {
		return fmt.Errorf("the Pi checkout %s: %w", piSource, err)
	}
	if head != want {
		return fmt.Errorf("the Pi checkout %s is at %s, durable/contract/PIN names main %s", piSource, head, want)
	}
	status, err := output(ctx, piSource, nil, "git", "status", "--porcelain", "--untracked-files=no")
	if err != nil {
		return fmt.Errorf("the Pi checkout %s: %w", piSource, err)
	}
	if status != "" {
		return fmt.Errorf("the Pi checkout %s has changes to tracked files: the programs must build from the pinned commit", piSource)
	}
	host := filepath.Join(root, "durable", "core", "host", "cf")
	_, _ = fmt.Fprintf(stderr, "k8sbench bundle: durable-core programs from Pi %s\n", head)
	if _, err := output(ctx, host, nil, "npm", "ci", "--ignore-scripts", "--no-audit", "--no-fund", "--loglevel=error"); err != nil {
		return err
	}
	_, err = output(ctx, host, nil, "node", builder, "--pi", piSource, "--out", filepath.Join(outDir, "core"))
	return err
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

// checkGoVersion refuses a Go toolchain other than the one the tree pins in .go-version, the release CI, the images and the
// gates build with (go.mod's toolchain line is only the minimum), and returns the pinned release. A tree without
// .go-version pins none ("").
func checkGoVersion(root, goVersion string) (string, error) {
	data, err := os.ReadFile(filepath.Join(root, ".go-version"))
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	want := "go" + strings.TrimSpace(string(data))
	if goVersion != want {
		return "", fmt.Errorf("the tree pins Go %s (.go-version) but go is %s; set GOTOOLCHAIN=%s", want, goVersion, want)
	}
	return want, nil
}

// buildWasmCore builds the core main package with one toolchain through durable/core/budget/build-wasm.sh, the script
// the image and the conformance gate use, in the bundle's build environment (goBuildEnv, and the pinned GOTOOLCHAIN).
func buildWasmCore(ctx context.Context, root, toolchain, out, tinygo string, build []string) error {
	_, err := output(ctx, root, append([]string{"TINYGO=" + tinygo}, build...), "sh", "durable/core/budget/build-wasm.sh", toolchain, "durable/core/cmd/corewasm", out)
	return err
}

// goBuildSteps are the go build commands of a bundle's Go programs; an agent bundle adds PiG and sessionscale.
func goBuildSteps(outDir string, agents bool) [][]string {
	programs := [][2]string{{"k8sbench", "./bench/k8s/cmd/k8sbench"}, {"durableperf", "./bench/durableperf"}}
	if agents {
		programs = append(programs, [2]string{"pig", "./cmd/pig"}, [2]string{"sessionscale", "./bench/sessionscale/cmd/sessionscale"})
	}
	steps := make([][]string, 0, len(programs))
	for _, p := range programs {
		steps = append(steps, []string{"go", "build", "-trimpath", "-o", filepath.Join(outDir, "bin", p[0]), p[1]})
	}
	return steps
}

// goBuildEnv is the build environment of every program in a bundle. The programs build through the repository's go.work,
// as the Makefile's development gates do: the root module uses the workspace's extensions/sdk, which can be newer than its
// published pin. GOWORK names the file, so a caller's GOWORK=off does not apply; no VCS stamp, so builders of one commit
// agree.
func goBuildEnv(root string) []string {
	return []string{"GOWORK=" + filepath.Join(root, "go.work"), "GOFLAGS=-buildvcs=false"}
}

// writeArtifactList writes ARTIFACTS.sha256 in sha256sum's format: every file of bin/, dist/ and core/ (the durable-core
// programs, when built), then the reference's script and lockfile and, for a commit pin, the packs it installs, and in an agent bundle Pi's coding agent lockfile and CLI,
// the CPython interpreter, pigbench (the launcher and its modules in name order) and each extra arm's lockfile and
// executable.
func writeArtifactList(dir string) error {
	var paths []string
	for _, sub := range []string{"bin", "dist", "core"} {
		entries, err := os.ReadDir(filepath.Join(dir, sub))
		if sub == "core" && os.IsNotExist(err) {
			continue
		}
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
	packs, err := filepath.Glob(filepath.Join(dir, "pi-durable", "packs", "*.tgz"))
	if err != nil {
		return err
	}
	for _, p := range packs {
		paths = append(paths, "pi-durable/packs/"+filepath.Base(p))
	}
	if _, err := os.Stat(filepath.Join(dir, "pi-agent", "package-lock.json")); err == nil {
		paths = append(paths, "pi-agent/package-lock.json")
		packs, err := filepath.Glob(filepath.Join(dir, "pi-agent", "packs", "*.tgz"))
		if err != nil {
			return err
		}
		for _, p := range packs {
			paths = append(paths, "pi-agent/packs/"+filepath.Base(p))
		}
		paths = append(paths, piAgentCLI)
	}
	if _, err := os.Stat(filepath.Join(dir, cpythonPython)); err == nil {
		paths = append(paths, cpythonPython)
	}
	if _, err := os.Stat(filepath.Join(dir, pigbenchLauncher)); err == nil {
		modules, err := filepath.Glob(filepath.Join(dir, filepath.FromSlash(pigbenchDir), "*.py"))
		if err != nil {
			return err
		}
		paths = append(paths, pigbenchLauncher)
		for _, m := range modules {
			paths = append(paths, pigbenchDir+"/"+filepath.Base(m))
		}
		for _, f := range pigbenchArmFiles {
			if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(f))); err == nil {
				paths = append(paths, f)
			}
		}
	}
	arms, err := armArtifacts(dir)
	if err != nil {
		return err
	}
	paths = append(paths, arms...)
	if _, err := os.Stat(filepath.Join(dir, nodeBinary)); err == nil {
		paths = append(paths, nodeBinary)
	}
	tasks, err := taskArtifacts(dir)
	if err != nil {
		return err
	}
	paths = append(paths, tasks...)
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
