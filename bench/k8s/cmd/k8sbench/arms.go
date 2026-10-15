package main

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// armsLock is bench/evals/arms.lock, the one place the benchmarks pin the agents they compare with PiG. The bundle and
// the image install an arm from it exactly as bench/evals' pigbench does (pigbench/arms.py), so a cluster run measures
// the bytes the local bench measures.
const armsLock = "bench/evals/arms.lock"

// armsDir holds the extra arms of an agent bundle or image, one directory per arm id. Each has the arm's lockfile and
// BIN, the executable's path relative to the arm's directory (symbolic links resolved), which ARTIFACTS.sha256 lists.
const armsDir = "arms"

// armPin is one arm of arms.lock: the fields an npm or a release install reads.
type armPin struct {
	Source            string            `json:"source"`
	Package           string            `json:"package"`
	Version           string            `json:"version"`
	Integrity         string            `json:"integrity"`
	Bin               string            `json:"bin"`
	BinByPlatform     map[string]string `json:"bin_by_platform"`
	BinSHA256         map[string]string `json:"bin_sha256"`
	BinSize           map[string]int64  `json:"bin_size"`
	PackageLock       string            `json:"package_lock"`
	PackageLockSHA256 string            `json:"package_lock_sha256"`
	PackageJSONFile   string            `json:"package_json_file"`
	PackageJSONSHA256 string            `json:"package_json_sha256"`
	// Assets are a release arm's archives by platform; Release says where they are and which member is the executable.
	Assets  map[string]releaseAsset `json:"assets"`
	Release struct {
		Base   string `json:"base"`
		Member string `json:"member"`
	} `json:"release"`
}

// releaseAsset is one platform's release archive, pinned by name, size and sha256.
type releaseAsset struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

var armIDPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9.-]{0,30}[a-z0-9])?$`)

// readArmsLock reads the pins of ids from root's arms.lock.
func readArmsLock(root string, ids []string) (map[string]armPin, error) {
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(armsLock)))
	if err != nil {
		return nil, err
	}
	var lock struct {
		Arms map[string]armPin `json:"arms"`
	}
	if err := json.Unmarshal(data, &lock); err != nil {
		return nil, fmt.Errorf("%s: %w", armsLock, err)
	}
	out := map[string]armPin{}
	for _, id := range ids {
		pin, ok := lock.Arms[id]
		if !ok {
			return nil, fmt.Errorf("%s has no arm %q", armsLock, id)
		}
		out[id] = pin
	}
	return out, nil
}

// parseArmIDs splits -arms. Pi's coding agent is not an extra arm: every agent bundle carries it as pi-agent/.
func parseArmIDs(text string) ([]string, error) {
	var ids []string
	for id := range strings.SplitSeq(text, ",") {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if !armIDPattern.MatchString(id) || id == "." || strings.Contains(id, "..") {
			return nil, fmt.Errorf("arm id %q: use the ids of %s", id, armsLock)
		}
		if strings.HasPrefix(id, "pi-") || strings.HasPrefix(id, "pig") {
			return nil, fmt.Errorf("arm %s: an agent bundle already carries PiG (bin/pig, built from the commit) and Pi's coding agent (pi-agent/)", id)
		}
		if slices.Contains(ids, id) {
			return nil, fmt.Errorf("arm %s appears twice", id)
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// installArms installs the arms ids of root's arms.lock under outDir/arms for linux/arch and returns their VERSIONS
// lines (arm-<id>=<version>). An npm arm is installed by npm ci --ignore-scripts from its pinned lockfile, whose sha256
// and record of the package arms.lock pins; the lockfile's integrity covers every downloaded package, and the
// executable must then have the sha256 (and size, when pinned) arms.lock gives for linux-<arch>. A release arm is its
// release archive for linux-<arch>, which must have the pinned size and sha256, with only the executable member kept.
// An arm with nothing pinned for that platform is refused.
func installArms(ctx context.Context, root, outDir, arch string, ids []string, stderr io.Writer) (string, error) {
	pins, err := readArmsLock(root, ids)
	if err != nil {
		return "", err
	}
	if arch != "amd64" && arch != "arm64" {
		return "", fmt.Errorf("arch %q is not amd64 or arm64", arch)
	}
	var versions strings.Builder
	for _, id := range ids {
		pin := pins[id]
		dest := filepath.Join(outDir, armsDir, id)
		var bin string
		switch pin.Source {
		case "npm":
			bin, err = installNpmArm(ctx, root, dest, id, pin, arch, stderr)
		case "release":
			bin, err = installReleaseArm(ctx, http.DefaultClient, dest, id, pin, arch, stderr)
		default:
			err = fmt.Errorf("arm %s: source %q; the bundle installs npm and release arms", id, pin.Source)
		}
		if err != nil {
			return "", err
		}
		if err := os.WriteFile(filepath.Join(dest, "BIN"), []byte(bin+"\n"), 0o644); err != nil {
			return "", err
		}
		versions.WriteString("arm-" + id + "=" + pin.Version + "\n")
	}
	return versions.String(), nil
}

// installNpmArm installs an npm arm in dest and returns its executable's path relative to dest.
func installNpmArm(ctx context.Context, root, dest, id string, pin armPin, arch string, stderr io.Writer) (string, error) {
	npmCPU := map[string]string{"amd64": "x64", "arm64": "arm64"}[arch]
	platform := "linux-" + arch
	if pin.BinSHA256[platform] == "" {
		return "", fmt.Errorf("arm %s: %s pins no executable sha256 for %s; pin it first (pigbench pin-binaries on that platform)", id, armsLock, platform)
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return "", err
	}
	if err := writeArmPackage(root, dest, id, pin); err != nil {
		return "", err
	}
	_, _ = fmt.Fprintf(stderr, "k8sbench bundle: npm ci arm %s (%s@%s) for linux/%s\n", id, pin.Package, pin.Version, npmCPU)
	if _, err := output(ctx, dest, nil, "npm", "ci", "--ignore-scripts", "--no-audit", "--no-fund", "--loglevel=error", "--os=linux", "--cpu="+npmCPU); err != nil {
		return "", fmt.Errorf("arm %s: %w", id, err)
	}
	return checkArm(dest, id, pin, platform)
}

// installReleaseArm downloads a release arm's archive for linux/arch, checks its pinned size and sha256, and keeps only
// the executable member (a regular file whose base name is release.member) as dest/<member>. The executable must be a
// Linux ELF of arch. It returns the member's name.
func installReleaseArm(ctx context.Context, client *http.Client, dest, id string, pin armPin, arch string, stderr io.Writer) (string, error) {
	platform := "linux-" + arch
	asset, ok := pin.Assets[platform]
	if !ok || asset.Name == "" || asset.SHA256 == "" || asset.Size <= 0 {
		return "", fmt.Errorf("arm %s: %s pins no release archive for %s", id, armsLock, platform)
	}
	member := pin.Release.Member
	if !strings.HasPrefix(pin.Release.Base, "https://") || member == "" || path.Base(member) != member || path.Base(asset.Name) != asset.Name {
		return "", fmt.Errorf("arm %s: %s needs release.base (https), release.member and asset names without directories", id, armsLock)
	}
	if !strings.HasSuffix(asset.Name, ".tar.gz") && !strings.HasSuffix(asset.Name, ".tgz") {
		return "", fmt.Errorf("arm %s: %s is not a .tar.gz archive", id, asset.Name)
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return "", err
	}
	archive := filepath.Join(dest, asset.Name)
	url := strings.ReplaceAll(pin.Release.Base, "{version}", pin.Version) + asset.Name
	_, _ = fmt.Fprintf(stderr, "k8sbench bundle: arm %s %s from %s\n", id, pin.Version, url)
	if err := fetchPinned(ctx, client, pinnedArchive{url: url, size: asset.Size, sha256: asset.SHA256}, "", archive); err != nil {
		return "", fmt.Errorf("arm %s: %w", id, err)
	}
	defer func() { _ = os.Remove(archive) }()
	exe := filepath.Join(dest, member)
	if err := extractMember(archive, member, exe); err != nil {
		return "", fmt.Errorf("arm %s: %s: %w", id, asset.Name, err)
	}
	got, err := linuxArch(exe)
	if err != nil {
		return "", fmt.Errorf("arm %s: %w", id, err)
	}
	if got != arch {
		return "", fmt.Errorf("arm %s: %s is a linux/%s executable, not linux/%s", id, member, got, arch)
	}
	return member, nil
}

// extractMember writes the one regular file of a .tar.gz archive whose base name is member to dst, executable. More
// than one such file, or none, is refused.
func extractMember(archive, member, dst string) (err error) {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	found := false
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		if h.Typeflag != tar.TypeReg || path.Base(h.Name) != member {
			continue
		}
		if found {
			return fmt.Errorf("more than one %s", member)
		}
		found = true
		out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
		if err != nil {
			return err
		}
		_, cerr := io.Copy(out, io.LimitReader(tr, h.Size))
		if err := errors.Join(cerr, out.Close()); err != nil {
			return err
		}
	}
	if !found {
		return fmt.Errorf("no %s", member)
	}
	return nil
}

// writeArmPackage writes the arm's package.json and package-lock.json to dest as pigbench's install does: the
// vendor's own package.json when arms.lock names one, else one that depends on the pinned version.
func writeArmPackage(root, dest, id string, pin armPin) error {
	if pin.PackageLock == "" || pin.Package == "" || pin.Version == "" || pin.Integrity == "" {
		return fmt.Errorf("arm %s: %s needs package, version, integrity and package_lock", id, armsLock)
	}
	evals := filepath.Join(root, "bench", "evals")
	lock, err := readPinned(filepath.Join(evals, filepath.FromSlash(pin.PackageLock)), pin.PackageLockSHA256)
	if err != nil {
		return fmt.Errorf("arm %s: %w", id, err)
	}
	var parsed struct {
		Packages map[string]struct {
			Version   string `json:"version"`
			Integrity string `json:"integrity"`
		} `json:"packages"`
	}
	if err := json.Unmarshal(lock, &parsed); err != nil {
		return fmt.Errorf("arm %s: %s: %w", id, pin.PackageLock, err)
	}
	main, ok := parsed.Packages["node_modules/"+pin.Package]
	if !ok || main.Version != pin.Version || main.Integrity != pin.Integrity {
		return fmt.Errorf("arm %s: %s disagrees with %s about %s@%s (version %q, integrity %q)", id, pin.PackageLock, armsLock, pin.Package, pin.Version, main.Version, main.Integrity)
	}
	var pkg []byte
	if pin.PackageJSONFile != "" {
		if pkg, err = readPinned(filepath.Join(evals, filepath.FromSlash(pin.PackageJSONFile)), pin.PackageJSONSHA256); err != nil {
			return fmt.Errorf("arm %s: %w", id, err)
		}
	} else {
		//portlint:allow mapkeyorder benchmark tool output, not Pi-facing
		pkg, err = json.Marshal(map[string]any{"name": "pigbench-arm-" + id, "version": "0.0.0", "private": true, "dependencies": map[string]string{pin.Package: pin.Version}})
		if err != nil {
			return err
		}
	}
	if err := os.WriteFile(filepath.Join(dest, "package.json"), pkg, 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dest, "package-lock.json"), lock, 0o644)
}

// readPinned reads a file that must have the sha256 want.
func readPinned(path, want string) ([]byte, error) {
	if want == "" {
		return nil, fmt.Errorf("%s has no pinned sha256", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if sum := sha256.Sum256(data); hex.EncodeToString(sum[:]) != want {
		got := hex.EncodeToString(sum[:])
		return nil, fmt.Errorf("%s has sha256 %s, the lock pins %s", path, got, want)
	}
	return data, nil
}

// checkArm checks an installed npm arm in dir: the package's installed version and its executable's sha256 and size
// for platform. It returns the executable's path relative to dir with symbolic links resolved.
func checkArm(dir, id string, pin armPin, platform string) (string, error) {
	var installed struct{ Version string }
	data, err := os.ReadFile(filepath.Join(dir, "node_modules", filepath.FromSlash(pin.Package), "package.json"))
	if err != nil {
		return "", fmt.Errorf("arm %s: %w", id, err)
	}
	if err := json.Unmarshal(data, &installed); err != nil {
		return "", fmt.Errorf("arm %s: %w", id, err)
	}
	if installed.Version != pin.Version {
		return "", fmt.Errorf("arm %s: installed %s, %s pins %s", id, installed.Version, armsLock, pin.Version)
	}
	rel := pin.Bin
	if p, ok := pin.BinByPlatform[platform]; ok {
		rel = p
	}
	base, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", err
	}
	real, err := filepath.EvalSymlinks(filepath.Join(dir, filepath.FromSlash(rel)))
	if err != nil {
		return "", fmt.Errorf("arm %s: no executable %s: %w", id, rel, err)
	}
	inside, err := filepath.Rel(base, real)
	if err != nil || !filepath.IsLocal(inside) {
		return "", fmt.Errorf("arm %s: executable %s resolves outside the arm's directory", id, rel)
	}
	got, err := fileSHA256(real)
	if err != nil {
		return "", err
	}
	if want := pin.BinSHA256[platform]; got != want {
		return "", fmt.Errorf("arm %s: executable %s has sha256 %s, %s pins %s for %s", id, rel, got, armsLock, want, platform)
	}
	if size, ok := pin.BinSize[platform]; ok {
		info, err := os.Stat(real)
		if err != nil {
			return "", err
		}
		if info.Size() != size {
			return "", fmt.Errorf("arm %s: executable %s has %d bytes, %s pins %d for %s", id, rel, info.Size(), armsLock, size, platform)
		}
	}
	return filepath.ToSlash(inside), nil
}

// armArtifacts are the ARTIFACTS.sha256 paths of the arms installed in dir: each arm's lockfile, when it has one, and
// executable, in arm id order.
func armArtifacts(dir string) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(dir, armsDir))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		bin, err := os.ReadFile(filepath.Join(dir, armsDir, e.Name(), "BIN"))
		if err != nil {
			return nil, fmt.Errorf("arm %s: %w", e.Name(), err)
		}
		rel := strings.TrimSpace(string(bin))
		if !filepath.IsLocal(filepath.FromSlash(rel)) {
			return nil, fmt.Errorf("arm %s: BIN %q is not a path inside the arm", e.Name(), rel)
		}
		// A release arm has no lockfile: its executable is the whole arm.
		if _, err := os.Stat(filepath.Join(dir, armsDir, e.Name(), "package-lock.json")); err == nil {
			paths = append(paths, armsDir+"/"+e.Name()+"/package-lock.json")
		}
		paths = append(paths, armsDir+"/"+e.Name()+"/"+rel)
	}
	return paths, nil
}

// cmdArms installs arms into an existing directory, as the bundle does, and prints their VERSIONS lines: the image's
// build stage runs it.
func cmdArms(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("arms", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", ".", "repository root (its "+armsLock+" pins the arms)")
	out := fs.String("out", "", "directory that receives arms/<id>/ (an agent bundle or the image's /opt/pig)")
	arch := fs.String("arch", "amd64", "node architecture: amd64 or arm64")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *out == "" || fs.NArg() == 0 {
		return errors.New("arms needs -out DIR and at least one arm id")
	}
	ids, err := parseArmIDs(strings.Join(fs.Args(), ","))
	if err != nil {
		return err
	}
	versions, err := installArms(ctx, *root, *out, *arch, ids, stderr)
	if err != nil {
		return err
	}
	_, err = io.WriteString(stdout, versions)
	return err
}
