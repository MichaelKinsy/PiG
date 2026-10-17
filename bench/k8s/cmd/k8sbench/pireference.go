package main

import (
	"bytes"
	"compress/gzip"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

// piPin is the Pi build the reference packages come from: .upstream/pin, as pigversion records it.
type piPin struct {
	Ref, Commit, PackageVersion, Version string
}

func committedPiPin() piPin {
	return piPin{Ref: pigversion.UpstreamRef, Commit: pigversion.UpstreamCommit, PackageVersion: pigversion.UpstreamPackageVersion, Version: pigversion.UpstreamVersion}
}

var releaseRef = regexp.MustCompile(`^v?\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?$`)

// isRelease reports whether the pin is a release tag, whose packages npm publishes, rather than a commit.
func (p piPin) isRelease() bool { return releaseRef.MatchString(p.Ref) }

// piReference is one of Pi's npm projects a bundle or the image installs at the pin: the pi-durable reference of the
// Durable profiles, or Pi's coding agent, the reference of the agent profiles.
type piReference struct {
	// dir is the project's directory in a bundle and under bench/k8s/image (a release's published install pair).
	dir string
	// closure names a commit's frozen closure, automation/pin/frozen/<commit>.<closure>.json.gz, and packs the directory
	// of a commit's build where `python3 automation/pin/pin.py install-oracle` leaves the packs it installs with it
	// (automation/pin/oracle.py frozen_lock_path, ensure and ensure_durable).
	closure, packs string
	// pkg is the package the project exists for; at a commit it must come from the pinned source.
	pkg string
}

var (
	durableReference = piReference{dir: "pi-durable", closure: "durable-closure", packs: "durable-packs", pkg: "@earendil-works/pi-durable"}
	agentReference   = piReference{dir: "pi-agent", closure: "closure", packs: "packs", pkg: "@earendil-works/pi-coding-agent"}
)

// packPrefix marks a frozen closure entry that the pinned source packs (automation/pin/oracle.py PACK_PREFIX).
const packPrefix = "pig-pack:"

// piBuildStore is where `pin.py install-oracle` builds pinned commits (automation/pin/oracle.py store).
func piBuildStore(getenv func(string) string) string {
	if store := getenv("PIG_PI_BUILD_STORE"); store != "" {
		return store
	}
	base := getenv("PIG_DEV_HOME")
	if base == "" {
		cache := getenv("XDG_CACHE_HOME")
		if cache == "" {
			home, _ := os.UserHomeDir()
			cache = filepath.Join(home, ".cache")
		}
		base = filepath.Join(cache, "pig-dev")
	}
	return filepath.Join(base, "pi-builds")
}

// packFile is the file name npm pack gives a package: @scope/name 1.2.3 is scope-name-1.2.3.tgz.
func packFile(name, version string) string {
	return strings.ReplaceAll(strings.TrimPrefix(name, "@"), "/", "-") + "-" + version + ".tgz"
}

// pinnedProject is the npm project that installs ref at a commit pin: the frozen closure with each packed package
// resolved to packs/<its pack file>. It returns the package.json and package-lock.json bytes and the packs the lock
// names (file name to the closure's integrity).
func pinnedProject(root string, ref piReference, pin piPin) (manifest, lock []byte, packs map[string]string, err error) {
	frozen := filepath.Join(root, "automation", "pin", "frozen", pin.Commit+"."+ref.closure+".json.gz")
	compressed, err := os.ReadFile(frozen)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("the pin is Pi commit %s, and its frozen closure for %s: %w", pin.Commit, ref.dir, err)
	}
	zr, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return nil, nil, nil, fmt.Errorf("%s: %w", frozen, err)
	}
	raw, err := io.ReadAll(zr)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("%s: %w", frozen, err)
	}
	// The lock is rewritten member by member, so its members and each package's members stay JSON text.
	var closure map[string]json.RawMessage
	if err := json.Unmarshal(raw, &closure); err != nil {
		return nil, nil, nil, fmt.Errorf("%s: %w", frozen, err)
	}
	var packages map[string]map[string]json.RawMessage
	if text, ok := closure["packages"]; ok {
		if err := json.Unmarshal(text, &packages); err != nil {
			return nil, nil, nil, fmt.Errorf("%s: packages: %w", frozen, err)
		}
	}
	top := packages[""]
	if top == nil {
		return nil, nil, nil, fmt.Errorf("%s has no root package", frozen)
	}
	member := func(entry map[string]json.RawMessage, name string) string {
		var value string
		_ = json.Unmarshal(entry[name], &value)
		return value
	}
	packs = map[string]string{}
	for _, key := range slices.Sorted(maps.Keys(packages)) {
		entry := packages[key]
		name, ok := strings.CutPrefix(member(entry, "resolved"), packPrefix)
		if !ok {
			continue
		}
		version, integrity := member(entry, "version"), member(entry, "integrity")
		if version == "" || !strings.HasPrefix(integrity, "sha512-") {
			return nil, nil, nil, fmt.Errorf("%s: %s has no version or no sha512 integrity", frozen, key)
		}
		file := packFile(name, version)
		if other, ok := packs[file]; ok && other != integrity {
			return nil, nil, nil, fmt.Errorf("%s packs %s twice with different integrity", frozen, file)
		}
		packs[file] = integrity
		if entry["resolved"], err = json.Marshal("file:packs/" + file); err != nil {
			return nil, nil, nil, err
		}
	}
	if !strings.HasPrefix(member(packages["node_modules/"+ref.pkg], "resolved"), "file:packs/") {
		return nil, nil, nil, fmt.Errorf("%s does not install %s from the pinned source", frozen, ref.pkg)
	}
	//portlint:allow mapkeyorder npm ci reads package.json members by name; this file is benchmark input, not Pi output
	manifest, err = json.MarshalIndent(map[string]any{"name": closure["name"], "version": closure["version"], "private": true, "devDependencies": top["devDependencies"]}, "", "\t") //portlint:allow jsonescape npm parses the escapes back; benchmark input, not Pi output
	if err != nil {
		return nil, nil, nil, err
	}
	if closure["packages"], err = json.Marshal(packages); err != nil { //portlint:allow mapkeyorder npm ci reads lock entries by path; benchmark input, not Pi output
		return nil, nil, nil, err
	}
	//portlint:allow mapkeyorder npm ci reads package-lock.json members by name; benchmark input, not Pi output
	lock, err = json.MarshalIndent(closure, "", "\t") //portlint:allow jsonescape npm parses the escapes back; benchmark input, not Pi output
	if err != nil {
		return nil, nil, nil, err
	}
	return append(manifest, '\n'), append(lock, '\n'), packs, nil
}

// writePinnedInstall writes the npm project that installs ref at the pin into dir: for a release, bench/k8s/image/<dir>'s
// published install pair; for a commit, pinnedProject with the packs taken from packsDir by their integrity and copied to
// dir/packs. `npm ci` then installs exactly that tree, and the lock's sha512 rejects any other pack. It returns the
// version VERSIONS records.
func writePinnedInstall(root, dir, packsDir string, ref piReference, pin piPin) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	if pin.isRelease() {
		for _, f := range []string{"package.json", "package-lock.json"} {
			data, err := os.ReadFile(filepath.Join(root, "bench", "k8s", "image", ref.dir, f))
			if err != nil {
				return "", err
			}
			if err := os.WriteFile(filepath.Join(dir, f), data, 0o644); err != nil {
				return "", err
			}
		}
		return pin.PackageVersion, nil
	}
	manifest, lock, packs, err := pinnedProject(root, ref, pin)
	if err != nil {
		return "", err
	}
	byIntegrity, err := packIntegrities(packsDir)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Join(dir, "packs"), 0o755); err != nil {
		return "", err
	}
	for _, file := range slices.Sorted(maps.Keys(packs)) {
		src, ok := byIntegrity[packs[file]]
		if !ok {
			return "", fmt.Errorf("%s holds no pack %s with the frozen closure's integrity %s; build the pinned Pi with `python3 automation/pin/pin.py install-oracle`", packsDir, file, packs[file])
		}
		data, err := os.ReadFile(src)
		if err != nil {
			return "", err
		}
		if err := os.WriteFile(filepath.Join(dir, "packs", file), data, 0o644); err != nil {
			return "", err
		}
	}
	for name, data := range map[string][]byte{"package.json": manifest, "package-lock.json": lock} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			return "", err
		}
	}
	return pin.Version, nil
}

// checkInstalledVersion checks that root/<ref.dir> installed ref.pkg at the pin's package version.
func checkInstalledVersion(root string, ref piReference, pin piPin) error {
	path := filepath.Join(root, ref.dir, "node_modules", filepath.FromSlash(ref.pkg), "package.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var pkg struct{ Version string }
	if err := json.Unmarshal(data, &pkg); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if pkg.Version != pin.PackageVersion {
		return fmt.Errorf("the installed %s is %s, the Pi pin is %s", ref.pkg, pkg.Version, pin.PackageVersion)
	}
	return nil
}

// packIntegrities maps the npm integrity (sha512) of each .tgz in dir to its path.
func packIntegrities(dir string) (map[string]string, error) {
	tarballs, err := filepath.Glob(filepath.Join(dir, "*.tgz"))
	if err != nil {
		return nil, err
	}
	if len(tarballs) == 0 {
		return nil, fmt.Errorf("no packs of the pinned Pi in %s; build them with `python3 automation/pin/pin.py install-oracle`", dir)
	}
	out := make(map[string]string, len(tarballs))
	for _, path := range tarballs {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		sum := sha512.Sum512(data)
		out["sha512-"+base64.StdEncoding.EncodeToString(sum[:])] = path
	}
	return out, nil
}

// cmdPiPackages writes the npm projects of Pi's references at the pin for the image build (bench/k8s/image/build.sh passes
// the directory as the build context pi): -out DIR writes DIR/pi-durable and DIR/pi-agent, each with its version in
// VERSION. -check ROOT checks that ROOT/pi-durable and ROOT/pi-agent installed the pin's package version and Pi's coding
// agent against its pins (the image's build runs it).
func cmdPiPackages(args []string, stdout io.Writer, getenv func(string) string) error {
	fs := flag.NewFlagSet("pi-packages", flag.ContinueOnError)
	out := fs.String("out", "", "directory to write pi-durable/ and pi-agent/ to")
	check := fs.String("check", "", "check pi-durable and the coding agent installed under this root's pi-durable/ and pi-agent/")
	store := fs.String("pi-builds", "", "the build store of `pin.py install-oracle` (default: $PIG_PI_BUILD_STORE, else $PIG_DEV_HOME/pi-builds, else ~/.cache/pig-dev/pi-builds)")
	repo := fs.String("repo", ".", "repository root")
	if err := fs.Parse(args); err != nil {
		return err
	}
	switch {
	case *check != "" && *out == "":
		pin := committedPiPin()
		for _, ref := range []piReference{durableReference, agentReference} {
			if err := checkInstalledVersion(*check, ref, pin); err != nil {
				return err
			}
		}
		if err := checkPiAgent(*check); err != nil {
			return err
		}
		_, err := fmt.Fprintf(stdout, "%s: pi-durable and Pi's coding agent at %s\n", *check, pin.Version)
		return err
	case *out == "" || *check != "":
		return errors.New("pi-packages: one of -out and -check")
	}
	pin := committedPiPin()
	if *store == "" {
		*store = piBuildStore(getenv)
	}
	for _, ref := range []piReference{durableReference, agentReference} {
		dir := filepath.Join(*out, ref.dir)
		version, err := writePinnedInstall(*repo, dir, filepath.Join(*store, pin.Commit, ref.packs), ref, pin)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, "VERSION"), []byte(version+"\n"), 0o644); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(stdout, "%s %s\n", ref.dir, version); err != nil {
			return err
		}
	}
	return nil
}
