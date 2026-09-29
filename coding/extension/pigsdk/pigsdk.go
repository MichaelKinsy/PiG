// Package pigsdk stages Pig's extension SDKs (Go, Python, and Rust) into the
// config root so out-of-tree extension builds and packed runtime cells can use
// the API that matches the running binary.
//
// # Why this exists
//
// An extension resolves its SDK through a local filesystem path: Go via a
// go.mod `replace`, Python via sys.path, Rust via a Cargo `path` dependency.
// Those SDK modules are nested/unpublished, so a machine with only the pig
// binary has nothing to point at and packed cells fail to build. The host SDK
// resolvers (coding/extension/host/runtimecell) use the staged copy under
// <config-root>/state/pigsdk/sdk{,-py,-rs}.
//
// The SDK bytes are embedded in the pig binary, so a staged copy always matches
// the running binary. Each language's stage is keyed on a content hash: any
// change to the embedded SDK re-stages on the next start; warm starts do a
// single marker read per language.
//
// # Boundary
//
// Staging only writes source files to disk. It does not install or require a
// language toolchain. This is generic extension-host infrastructure in Stock
// Pig, not an optional product extension.
//
//	<config-root>/state/pigsdk/
//	  sdk/      go.mod + *.go              (Go)
//	  sdk-py/   pig_sdk/__init__.py + ...  (Python)
//	  sdk-rs/   Cargo.toml + src/*.rs      (Rust)
//	each with a .pig-sdk-version content-hash marker.
package pigsdk

import (
	"cmp"
	"context"
	"errors"
	"slices"

	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/MichaelKinsy/PiG/coding/extension/host/runtimecell"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/internal/pigsdklock"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	pysdk "github.com/MichaelKinsy/PiG/extensions/sdk-py"
	rssdk "github.com/MichaelKinsy/PiG/extensions/sdk-rs"
)

// markerFile records a staged SDK's content hash so warm starts skip work and
// any SDK change forces a re-stage. One lives in each language's staged dir.
const markerFile = ".pig-sdk-version"

// sdkBundle describes one language's embedded SDK and where it stages.
type sdkBundle struct {
	lang   string // "go" | "python" | "rust"
	relDir string // staged dir relative to config root, forward slashes
	files  []string
	fsys   fs.FS
}

type bundleHashCache struct {
	once sync.Once
	hash string
	err  error
}

var bundleHashes = map[string]*bundleHashCache{
	"go": {}, "python": {}, "rust": {},
}

// Staged SDK directories relative to the config root. Checking that one exists must not enumerate the embedded files, which bundles does.
var stagedSDKDirs = [...]string{"state/pigsdk/sdk", "state/pigsdk/sdk-py", "state/pigsdk/sdk-rs"}

func bundles() []sdkBundle {
	return []sdkBundle{
		{lang: "go", relDir: stagedSDKDirs[0], files: sdk.BundledFiles(), fsys: sdk.Source},
		{lang: "python", relDir: stagedSDKDirs[1], files: pysdk.BundledFiles(), fsys: pysdk.Source},
		{lang: "rust", relDir: stagedSDKDirs[2], files: rssdk.BundledFiles(), fsys: rssdk.Source},
	}
}

func bundleFor(lang string) (sdkBundle, bool) {
	for _, b := range bundles() {
		if b.lang == lang {
			return b, true
		}
	}
	return sdkBundle{}, false
}

// SDKDir returns the staged Go SDK directory. Kept for callers that scaffold Go
// extensions (pig extension init).
func SDKDir(configRoot string) string {
	return filepath.Join(configRoot, filepath.FromSlash("state/pigsdk/sdk"))
}

// SDKDirFor returns the staged directory for a language, or an error for an
// unknown language.
func SDKDirFor(configRoot, lang string) (string, error) {
	b, ok := bundleFor(lang)
	if !ok {
		return "", fmt.Errorf("pig reload: unknown language %q", lang)
	}
	return filepath.Join(configRoot, filepath.FromSlash(b.relDir)), nil
}

func (b sdkBundle) hash() (string, error) {
	cache := bundleHashes[b.lang]
	cache.once.Do(func() {
		cache.hash, cache.err = b.computeHash()
	})
	return cache.hash, cache.err
}

func (b sdkBundle) computeHash() (string, error) {
	h := sha256.New()
	_, _ = h.Write([]byte("staging-v2\x00"))
	buffer := make([]byte, 64<<10)
	for _, name := range b.files {
		if err := hashEmbeddedFile(h, b, name, buffer); err != nil {
			return "", fmt.Errorf("pig reload: read embedded %s/%s: %w", b.lang, name, err)
		}
	}
	return hex.EncodeToString(h.Sum(nil))[:16], nil
}

// hashEmbeddedFile feeds "name\x00size\x00contents" to h, streaming the contents so the warm-start check does not copy every embedded SDK file into memory.
func hashEmbeddedFile(h io.Writer, b sdkBundle, name string, buffer []byte) error {
	f, err := b.fsys.Open(name)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(h, "%s\x00%d\x00", name, info.Size()); err != nil {
		return err
	}
	n, err := io.CopyBuffer(h, struct{ io.Reader }{f}, buffer)
	if err != nil {
		return err
	}
	if n != info.Size() {
		return fmt.Errorf("read %d bytes, size is %d", n, info.Size())
	}
	return nil
}

// sync replaces the staged directory with the embedded SDK. It builds the new tree beside the old one and swaps it in by renames that keep the previous tree until the new one is in place, so the marker only ever appears with every file it vouches for and a failed swap leaves the previous SDK. Callers hold the SDK stage lock, which excludes builds reading the tree.
func (b sdkBundle) sync(configRoot string) ([]string, error) {
	dir := filepath.Join(configRoot, filepath.FromSlash(b.relDir))
	hash, err := b.hash()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return nil, fmt.Errorf("pig reload: mkdir %s: %w", filepath.Dir(dir), err)
	}
	b.settleStaging(configRoot)
	staging, err := os.MkdirTemp(filepath.Dir(dir), ".stage-"+b.lang+"-")
	if err != nil {
		return nil, fmt.Errorf("pig reload: stage %s SDK: %w", b.lang, err)
	}
	renamed := false
	defer func() {
		if !renamed {
			_ = os.RemoveAll(staging)
		}
	}()
	if err := os.Chmod(staging, 0o755); err != nil {
		return nil, fmt.Errorf("pig reload: chmod %s: %w", staging, err)
	}
	written := make([]string, 0, len(b.files))
	made := map[string]bool{staging: true}
	for _, name := range b.files {
		data, err := fs.ReadFile(b.fsys, name)
		if err != nil {
			return nil, fmt.Errorf("pig reload: read embedded %s/%s: %w", b.lang, name, err)
		}
		target := filepath.Join(staging, filepath.FromSlash(name))
		if parent := filepath.Dir(target); !made[parent] {
			if err := os.MkdirAll(parent, 0o755); err != nil {
				return nil, fmt.Errorf("pig reload: mkdir %s: %w", parent, err)
			}
			made[parent] = true
		}
		if err := writeFile(target, data, 0o644); err != nil {
			return nil, err
		}
		written = append(written, name)
	}
	if err := writeFile(filepath.Join(staging, markerFile), []byte(hash+"\n"), 0o644); err != nil {
		return nil, err
	}
	if err := replaceDir(dir, staging); err != nil {
		return nil, fmt.Errorf("pig reload: replace staged %s SDK: %w", b.lang, err)
	}
	renamed = true
	sort.Strings(written)
	return written, nil
}

// stagePrefix names the private trees this bundle's syncs build beside its directory, and the aside copies of the tree they replace.
func (b sdkBundle) stagePrefix() string { return ".stage-" + b.lang + "-" }

// settleStaging removes the trees that syncs stranded by dying or failing mid-swap. The caller holds the SDK stage lock, so no tree found is owned by a live sync. An aside copy (`.old`) may be the only intact SDK, when the swap died or failed to restore it after moving it aside, so a missing target is restored from one before any tree is removed. If that restore fails, the aside copies stay.
func (b sdkBundle) settleStaging(configRoot string) {
	dir := filepath.Join(configRoot, filepath.FromSlash(b.relDir))
	leftovers, _ := filepath.Glob(filepath.Join(filepath.Dir(dir), b.stagePrefix()+"*"))
	if _, err := os.Lstat(dir); errors.Is(err, fs.ErrNotExist) {
		if asides := slices.DeleteFunc(slices.Clone(leftovers), func(path string) bool { return !strings.HasSuffix(path, ".old") }); len(asides) > 0 {
			// Glob returns lexical order; any aside is a complete previous tree.
			if err := renameDir(asides[len(asides)-1], dir); err != nil {
				leftovers = slices.DeleteFunc(leftovers, func(path string) bool { return strings.HasSuffix(path, ".old") })
			} else {
				leftovers = slices.DeleteFunc(leftovers, func(path string) bool { return path == asides[len(asides)-1] })
			}
		}
	}
	for _, leftover := range leftovers {
		_ = os.RemoveAll(leftover)
	}
}

// stagingNames lists the staging entries beside the staged SDKs, or nil when there are none. It reads one directory and takes no lock, so a live sync's tree also counts; callers take the stage lock before they remove anything.
func stagingNames(configRoot string) []string {
	entries, err := os.ReadDir(filepath.Join(configRoot, "state", "pigsdk"))
	if err != nil {
		return nil
	}
	var names []string
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".stage-") {
			names = append(names, entry.Name())
		}
	}
	return names
}

// ownsAny reports whether one of names is a staging entry of this bundle.
func (b sdkBundle) ownsAny(names []string) bool {
	return slices.ContainsFunc(names, func(name string) bool { return strings.HasPrefix(name, b.stagePrefix()) })
}

// renameDir is os.Rename; tests replace it to fail a swap step.
var renameDir = os.Rename

// replaceDir moves the complete tree staging to dir. An existing dir is renamed aside first and removed only after the new tree is in place, so a failed rename restores it. A crash or rollback failure between the two renames leaves dir absent and the previous tree aside; the next settleStaging under the stage lock restores it before anything is removed.
func replaceDir(dir, staging string) error {
	aside := staging + ".old"
	hadPrevious := true
	if err := renameDir(dir, aside); err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		hadPrevious = false
	}
	if err := renameDir(staging, dir); err != nil {
		if hadPrevious {
			if restoreErr := renameDir(aside, dir); restoreErr != nil {
				return errors.Join(err, restoreErr)
			}
		}
		return err
	}
	if hadPrevious {
		_ = os.RemoveAll(aside)
	}
	return nil
}

func (b sdkBundle) current(configRoot string) (bool, error) {
	hash, err := b.hash()
	if err != nil {
		return false, err
	}
	marker := filepath.Join(configRoot, filepath.FromSlash(b.relDir), markerFile)
	data, err := os.ReadFile(marker)
	if err != nil {
		return false, nil
	}
	return strings.TrimSpace(string(data)) == hash, nil
}

// ensure stages the bundle when the on-disk marker is missing or stale. It
// reports whether a stage actually happened, which is the event that strands
// every extension build compiled against the previous SDK.
func (b sdkBundle) ensure(configRoot string) (bool, error) {
	current, err := b.current(configRoot)
	if err != nil {
		return false, err
	}
	if current {
		b.settleStaging(configRoot)
		return false, nil
	}
	if _, err := b.sync(configRoot); err != nil {
		return false, err
	}
	return true, nil
}

// StageStatus reports one language SDK's staged state against the SDK embedded
// in the running binary. A staged copy that does not match the binary silently
// builds extensions against the wrong SDK, so the mismatch has to be
// observable rather than inferred from an extension misbehaving.
type StageStatus struct {
	Lang     string // "go" | "python" | "rust"
	Dir      string // absolute staged directory
	Staged   bool   // a marker file exists
	Current  bool   // the marker matches the embedded SDK
	Embedded string // hash of the SDK compiled into this binary
	OnDisk   string // hash recorded by the staged copy, "" when unstaged
}

// Status reports the staged state of every language SDK without changing
// anything. Used by diagnostics and by the extension builder to prove the
// staged SDK matches the binary before compiling against it.
func Status(configRoot string) ([]StageStatus, error) {
	out := make([]StageStatus, 0, 3)
	for _, b := range bundles() {
		embedded, err := b.hash()
		if err != nil {
			return nil, err
		}
		dir := filepath.Join(configRoot, filepath.FromSlash(b.relDir))
		st := StageStatus{Lang: b.lang, Dir: dir, Embedded: embedded}
		if data, err := os.ReadFile(filepath.Join(dir, markerFile)); err == nil {
			st.OnDisk = strings.TrimSpace(string(data))
			st.Staged = true
			st.Current = st.OnDisk == embedded
		}
		out = append(out, st)
	}
	return out, nil
}

// VerifyStaged reports whether the staged SDK for a build type matches the SDK
// embedded in this binary. Build types map to languages: "go" and "rust" stage
// an SDK, anything else needs none. Suitable for
// subprocess.Builder.SetStagedSDKVerifier.
func VerifyStaged(configRoot string) func(stagedDir, buildType string) error {
	return func(stagedDir, buildType string) error {
		lang := buildType
		if lang != "go" && lang != "rust" {
			return nil
		}
		b, ok := bundleFor(lang)
		if !ok {
			return nil
		}
		embedded, err := b.hash()
		if err != nil {
			return err
		}
		data, err := os.ReadFile(filepath.Join(stagedDir, markerFile))
		if err != nil {
			return fmt.Errorf("staged %s SDK at %s has no version marker; run `pig reload`", lang, stagedDir)
		}
		if onDisk := strings.TrimSpace(string(data)); onDisk != embedded {
			return fmt.Errorf(
				"staged %s SDK at %s is %s but this pig embeds %s; extensions would build against the wrong SDK. Run `pig reload`",
				lang, stagedDir, onDisk, embedded)
		}
		return nil
	}
}

// LiveFingerprints returns the staged-SDK fingerprint per build type. A cached
// extension build is reachable only while its recorded fingerprint appears
// here, because the build's cache key folds the SDK in.
func LiveFingerprints(configRoot string) (map[string]string, error) {
	live := map[string]string{}
	for _, lang := range []string{"go", "rust", "python"} {
		b, ok := bundleFor(lang)
		if !ok {
			continue
		}
		dir := filepath.Join(configRoot, filepath.FromSlash(b.relDir))
		fp, err := subprocess.StagedSDKFingerprint(dir, lang)
		if err != nil {
			// An unstaged SDK has no live builds; leaving it out of the map is
			// the correct answer rather than an error.
			continue
		}
		live[lang] = fp
	}
	return live, nil
}

// anyStagedSDK reports whether any language has a staged directory.
func anyStagedSDK(configRoot string) bool {
	for _, relDir := range stagedSDKDirs {
		if info, err := os.Stat(filepath.Join(configRoot, filepath.FromSlash(relDir))); err == nil && info.IsDir() {
			return true
		}
	}
	return false
}

// Prune drops extension builds the staged SDKs can no longer select.
func Prune(configRoot string, _ bool, dryRun bool) (runtimecell.CacheReport, error) {
	cacheRoot := filepath.Join(configRoot, "cache")
	// Fingerprints only classify existing entries. With none, hashing every staged tree cannot change the report, so a first start skips it and still requires a staged SDK. HasCacheEntries reads outside the cache GC lock, so the empty answer is confirmed by a dry run under that lock; a build published in between takes the classified path below.
	if entries, err := runtimecell.HasCacheEntries(cacheRoot); err == nil && !entries {
		if !anyStagedSDK(configRoot) {
			return runtimecell.CacheReport{}, fmt.Errorf("no staged SDK found under %s; run `pig reload` first", configRoot)
		}
		confirmed, err := runtimecell.PruneCaches(runtimecell.CacheLifecycleOptions{CacheRoot: cacheRoot, DryRun: true})
		if err != nil || len(confirmed.Entries) == 0 {
			return confirmed, err
		}
	}
	live, err := LiveFingerprints(configRoot)
	if err != nil {
		return runtimecell.CacheReport{}, err
	}
	if len(live) == 0 {
		return runtimecell.CacheReport{}, fmt.Errorf("no staged SDK found under %s; run `pig reload` first", configRoot)
	}
	fingerprints := make(map[string]struct{}, len(live))
	for _, fingerprint := range live {
		fingerprints[fingerprint] = struct{}{}
	}
	return runtimecell.PruneCaches(runtimecell.CacheLifecycleOptions{
		CacheRoot: cacheRoot, LiveFingerprints: fingerprints, DryRun: dryRun,
	})
}

// EnsureSynced stages every language SDK whose on-disk marker is missing or
// stale, then drops builds the new SDK can no longer select.
//
// Staging is the event that strands builds: the cache key folds the SDK in, so
// the moment a re-stage happens every build against the previous SDK becomes
// unreachable. Pruning here rather than on a timer means the cache is bounded
// by how often the SDK actually changes.
func EnsureSynced(configRoot string) error {
	return EnsureSyncedContext(context.Background(), configRoot)
}

// EnsureSyncedContext is EnsureSynced with a caller-owned lock-wait lifetime.
func EnsureSyncedContext(ctx context.Context, configRoot string) error {
	current := true
	leftovers := stagingNames(configRoot)
	for _, b := range bundles() {
		bundleCurrent, err := b.current(configRoot)
		if err != nil {
			return err
		}
		current = current && bundleCurrent && !b.ownsAny(leftovers)
	}
	if current {
		return nil
	}
	return withSDKStageLock(ctx, configRoot, func() error {
		restaged := false
		for _, b := range bundles() {
			changed, err := b.ensure(configRoot)
			if err != nil {
				return err
			}
			restaged = restaged || changed
		}
		if restaged {
			// Automatic prune drops only builds proven stale by fingerprint.
			if _, err := Prune(configRoot, false, false); err != nil {
				return fmt.Errorf("prune after SDK restage: %w", err)
			}
		}
		return nil
	})
}

// EnsureSyncedLang stages a single language SDK. Used by scaffolders that only
// need one language on disk.
func EnsureSyncedLang(configRoot, lang string) error {
	b, ok := bundleFor(lang)
	if !ok {
		return fmt.Errorf("pig reload: unknown language %q", lang)
	}
	current, err := b.current(configRoot)
	if err != nil {
		return err
	}
	if current && !b.ownsAny(stagingNames(configRoot)) {
		return nil
	}
	return withSDKStageLock(context.Background(), configRoot, func() error {
		_, err := b.ensure(configRoot)
		return err
	})
}

// Sync force-stages every language SDK, returning the count written per
// language.
func Sync(configRoot string) (map[string]int, error) {
	counts := make(map[string]int, len(bundles()))
	err := withSDKStageLock(context.Background(), configRoot, func() error {
		for _, b := range bundles() {
			written, err := b.sync(configRoot)
			if err != nil {
				return err
			}
			counts[b.lang] = len(written)
		}
		return nil
	})
	return counts, err
}

func withSDKStageLock(ctx context.Context, configRoot string, work func() error) (err error) {
	release, err := pigsdklock.AcquireStage(ctx, configRoot)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, release()) }()
	return work()
}

// writeFile creates path with exactly mode. The file lives in a private staging tree, so no reader observes it before the tree is renamed into place.
func writeFile(path string, data []byte, mode os.FileMode) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return fmt.Errorf("pig reload: create %s: %w", path, err)
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return fmt.Errorf("pig reload: write %s: %w", path, err)
	}
	if err := f.Chmod(mode); err != nil {
		_ = f.Close()
		return fmt.Errorf("pig reload: chmod %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("pig reload: close %s: %w", path, err)
	}
	return nil
}

// RunCommand is the pre-session bundled command entrypoint. Returns -1 when the
// args do not target this command so the dispatcher can try the next.
//
// It implements `pig reload`, the pre-session counterpart to the interactive
// /reload.
//
// Both mean the same thing: make what is loaded match what is on disk. The
// interactive one reloads a live session's resources; this one stages the SDKs
// this binary embeds and drops extension builds their fingerprint proves came
// from an older SDK, so the next build compiles against the current source.
// Staging also happens at startup and inside /reload, so this is the explicit
// form for a shell, not a step a user has to remember.
//
// Reporting lives in `pig diagnose`, which already covers staged-versus-embedded
// alongside paths, auth, models, and environment. Splitting a second report out
// here would mean two places to look for one answer.
func RunCommand(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "reload" {
		return -1
	}
	rest := args[1:]
	root, err := configRoot()
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "pig reload: %v\n", err)
		return 1
	}

	// Accessors for build scripts that need a staged location or the embedded
	// hash. They ensure the SDK is current first, so a script never wires an
	// extension to a directory this binary is about to restage.
	for _, flag := range []struct {
		name string
		emit func(lang string) (string, error)
	}{
		{"--sdk-path", func(lang string) (string, error) { return SDKDirFor(root, lang) }},
		{"--sdk-version", func(lang string) (string, error) {
			b, ok := bundleFor(lang)
			if !ok {
				return "", fmt.Errorf("unknown language %q", lang)
			}
			return b.hash()
		}},
	} {
		lang, ok := flagValue(rest, flag.name)
		if !ok {
			continue
		}
		if err := EnsureSyncedLang(root, lang); err != nil {
			_, _ = fmt.Fprintf(stderr, "pig reload: %v\n", err)
			return 1
		}
		value, err := flag.emit(lang)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "pig reload: %v\n", err)
			return 1
		}
		_, _ = fmt.Fprintln(stdout, value)
		return 0
	}

	switch {
	case slices.Contains(rest, "-h"), slices.Contains(rest, "--help"), slices.Contains(rest, "help"):
		printUsage(stdout)
		return 0
	}

	if slices.Contains(rest, "--dry-run") {
		statuses, err := Status(root)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "pig reload: %v\n", err)
			return 1
		}
		for _, st := range statuses {
			if !st.Staged || !st.Current {
				_, _ = fmt.Fprintf(stdout, "pig reload: would stage %s (embedded %s)\n", st.Lang, st.Embedded)
			}
		}
		// Prune needs a staged SDK to decide what is reachable. On a root that
		// has never been staged there is nothing to drop, and reporting that is
		// the honest answer: erroring here would tell the user to run the very
		// command they are previewing.
		result, err := Prune(root, slices.Contains(rest, "--all"), true)
		if err != nil {
			_, _ = fmt.Fprintf(stdout, "pig reload: would drop no builds (none staged yet)\n")
			return 0
		}
		_, _ = fmt.Fprintf(stdout, "pig reload: would drop %d unreachable build(s), %.1f MB, keeping %d\n",
			result.Removed, float64(result.RemovedBytes)/(1024*1024), len(result.Entries)-result.Removed)
		return 0
	}

	counts, err := Sync(root)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "pig reload: %v\n", err)
		return 1
	}
	for _, b := range bundles() {
		_, _ = fmt.Fprintf(stdout, "pig reload: staged %s (%d files) to %s\n",
			b.lang, counts[b.lang], filepath.Join(root, filepath.FromSlash(b.relDir)))
	}

	// Pruning follows staging in the same command because a restage is what
	// makes a build stale: dropping them separately leaves a window where the
	// SDK is current but the builds selected against it are not.
	result, err := Prune(root, slices.Contains(rest, "--all"), false)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "pig reload: %v\n", err)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "pig reload: dropped %d unreachable build(s), %.1f MB, kept %d\n",
		result.Removed, float64(result.RemovedBytes)/(1024*1024), len(result.Entries)-result.Removed)
	return 0
}

// flagValue reads "--flag value" or "--flag=value", defaulting to go when the
// flag is present with no language.
func flagValue(args []string, name string) (string, bool) {
	for i, arg := range args {
		if value, ok := strings.CutPrefix(arg, name+"="); ok {
			return cmp.Or(value, "go"), true
		}
		if arg == name {
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				return args[i+1], true
			}
			return "go", true
		}
	}
	return "", false
}

func printUsage(w io.Writer) {
	_, _ = fmt.Fprintln(w, "Usage: pig reload [--all] [--dry-run] [--sdk-path [lang]] [--sdk-version [lang]]")
	_, _ = fmt.Fprintln(w, "")
	_, _ = fmt.Fprintln(w, "Stages the extension SDKs this binary embeds and drops extension builds")
	_, _ = fmt.Fprintln(w, "that came from an older one, so the next build compiles against current")
	_, _ = fmt.Fprintln(w, "source. The pre-session form of /reload; both make what is loaded match")
	_, _ = fmt.Fprintln(w, "what is on disk.")
	_, _ = fmt.Fprintln(w, "")
	_, _ = fmt.Fprintln(w, "  --all             also drop builds with no SDK fingerprint")
	_, _ = fmt.Fprintln(w, "  --dry-run         report what would be dropped, stage nothing")
	_, _ = fmt.Fprintln(w, "  --sdk-path [lang]     print a staged SDK directory (go|python|rust, default go)")
	_, _ = fmt.Fprintln(w, "  --sdk-version [lang]  print an embedded SDK content hash")
	_, _ = fmt.Fprintln(w, "")
	_, _ = fmt.Fprintln(w, "Staged-versus-embedded status is reported by `pig diagnose`.")
}

// configRoot mirrors codingagent.ConfigRoot without importing it. The SDK
// command runs before runtime services are constructed.
func configRoot() (string, error) {
	if v := os.Getenv("PIG_HOME"); v != "" {
		return expandTilde(v), nil
	}
	if v := os.Getenv("XDG_CONFIG_HOME"); v != "" {
		return filepath.Join(expandTilde(v), "pig"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".pig"), nil
}

func expandTilde(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			if p == "~" {
				return home
			}
			return filepath.Join(home, p[2:])
		}
	}
	return p
}
