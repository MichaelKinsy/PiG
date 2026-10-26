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
// resolvers (coding/extension/host/runtimecell) use the staged copy, found
// through pigsdklock.StagedSDKDir.
//
// The SDK bytes are embedded in the pig binary, so a staged copy always matches
// the running binary. Each build stages each language into its own directory
// named for the SDK's content hash, so PiG builds that share a config root (two
// installed versions, or stock PiG beside a Piglet Binary) never restage each
// other's SDKs or strand each other's extension builds. A warm start does a
// single marker read per language.
//
// After a stage, the least recently used SDK directories of other builds beyond
// contentdirs.Keep per language are removed, and extension builds whose SDK is
// no longer staged are pruned. The unhashed directories a PiG that predates
// per-build directories stages into are never removed, and their builds are
// kept.
//
// # Boundary
//
// Staging only writes source files to disk. It does not install or require a
// language toolchain. This is generic extension-host infrastructure in Stock
// Pig, not an optional product extension.
//
//	<config-root>/state/pigsdk/
//	  sdk-<hash16>/      go.mod + *.go              (Go)
//	  sdk-py-<hash16>/   pig_sdk/__init__.py + ...  (Python)
//	  sdk-rs-<hash16>/   Cargo.toml + src/*.rs      (Rust)
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
	"github.com/MichaelKinsy/PiG/internal/configroot"
	"github.com/MichaelKinsy/PiG/internal/contentdirs"
	"github.com/MichaelKinsy/PiG/internal/pigsdklock"
)

// markerFile records a staged SDK's content hash so warm starts skip work and
// any SDK change forces a re-stage. One lives in each language's staged dir.
const markerFile = ".pig-sdk-version"

// sdkBundle describes one language's embedded SDK.
type sdkBundle struct {
	lang  string // "go" | "python" | "rust"
	files []string
	fsys  fs.FS
}

type bundleHashCache struct {
	once sync.Once
	hash string
	err  error
}

var bundleHashes = map[string]*bundleHashCache{
	"go": {}, "python": {}, "rust": {},
}

// sdkBases names each language's staged directories under <config-root>/state/pigsdk: a build stages into <base>-<hash16>, and a PiG that predates per-build directories stages into <base> itself. It covers every language, embedded by this build or not, because another build sharing the config root may embed one this build strips.
var sdkBases = [...]struct{ lang, base string }{{"go", "sdk"}, {"python", "sdk-py"}, {"rust", "sdk-rs"}}

// sdkBase returns lang's directory base from sdkBases.
func sdkBase(lang string) string {
	for _, entry := range sdkBases {
		if entry.lang == lang {
			return entry.base
		}
	}
	return ""
}

// registerBundle makes lang's staged directory resolvable through pigsdklock.StagedSDKDir. Its hash fails with the strip error while the active Piglet strips lang at run time.
func registerBundle(lang string, bundle func() (sdkBundle, bool)) {
	pigsdklock.RegisterSDK(lang, sdkBase(lang), func() (string, error) {
		b, ok := bundle()
		if !ok {
			return "", unknownLanguage("", lang)
		}
		return b.hash()
	})
}

// bundles lists the embedded SDKs in staging order (go, python, rust).
// pig additive (D92): each language's SDK lives in its sdk_<lang>_on.go shim, which a Piglet Binary compiles out with pig_strip_extension_sdk_<lang>; a stripped language is left out.
func bundles() []sdkBundle {
	out := make([]sdkBundle, 0, 3)
	for _, bundle := range [...]func() (sdkBundle, bool){goBundle, pythonBundle, rustBundle} {
		if b, ok := bundle(); ok {
			out = append(out, b)
		}
	}
	return out
}

func bundleFor(lang string) (sdkBundle, bool) {
	for _, b := range bundles() {
		if b.lang == lang {
			return b, true
		}
	}
	return sdkBundle{}, false
}

// unknownLanguage is the error for a language with no embedded SDK; prefix
// leads the stock unknown-language message.
// pig additive (D92): a language the active Piglet strips reports the strip, not an unknown language.
func unknownLanguage(prefix, lang string) error {
	if err := runtimecell.StrippedSDKError(lang); err != nil {
		return err
	}
	return fmt.Errorf("%sunknown language %q", prefix, lang)
}

// SDKDirFor returns the directory this binary stages a language's SDK in, or an error for an unknown language.
func SDKDirFor(configRoot, lang string) (string, error) {
	b, ok := bundleFor(lang)
	if !ok {
		return "", unknownLanguage("pig reload: ", lang)
	}
	return b.stagedDir(configRoot)
}

// stagedDir is the directory this binary stages b in: <config-root>/state/pigsdk/<base>-<hash16>.
func (b sdkBundle) stagedDir(configRoot string) (string, error) {
	return pigsdklock.StagedSDKDir(configRoot, b.lang)
}

func (b sdkBundle) hash() (string, error) {
	cache := bundleHashes[b.lang]
	cache.once.Do(func() {
		cache.hash, cache.err = b.computeHash()
	})
	return cache.hash, cache.err
}

// hashBuffers shares the copy buffer of the bundle hashes: the three bundles are hashed one after another at startup. Reads from the embedded files are memory copies, so a larger buffer would only add to every start's allocations.
var hashBuffers = sync.Pool{New: func() any { buffer := make([]byte, 8<<10); return &buffer }}

func (b sdkBundle) computeHash() (string, error) {
	h := sha256.New()
	_, _ = h.Write([]byte("staging-v2\x00"))
	buffer := hashBuffers.Get().(*[]byte)
	defer hashBuffers.Put(buffer)
	for _, name := range b.files {
		if err := hashEmbeddedFile(h, b, name, *buffer); err != nil {
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

// sync replaces this build's staged directory with the embedded SDK. It builds the new tree beside the old one and swaps it in by renames that keep the previous tree until the new one is in place, so the marker only ever appears with every file it vouches for and a failed swap leaves the previous SDK. Callers hold the SDK stage lock, which excludes builds reading the tree.
func (b sdkBundle) sync(configRoot string) ([]string, error) {
	dir, err := b.stagedDir(configRoot)
	if err != nil {
		return nil, err
	}
	hash, err := b.hash()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return nil, fmt.Errorf("pig reload: mkdir %s: %w", filepath.Dir(dir), err)
	}
	settleStaging(dir)
	staging, err := os.MkdirTemp(filepath.Dir(dir), stagePrefix(dir))
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

// stagePrefix names the private trees syncs build beside the staged directory dir, and the aside copies of the tree they replace. It names dir itself, so a sync never touches another build's trees.
func stagePrefix(dir string) string { return ".stage-" + filepath.Base(dir) + "-" }

// settleStaging removes the trees that syncs of dir stranded by dying or failing mid-swap. The caller holds the SDK stage lock, so no tree found is owned by a live sync. An aside copy (`.old`) may be the only intact SDK, when the swap died or failed to restore it after moving it aside, so a missing target is restored from one before any tree is removed. If that restore fails, the aside copies stay.
func settleStaging(dir string) {
	leftovers, _ := filepath.Glob(filepath.Join(filepath.Dir(dir), stagePrefix(dir)+"*"))
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

// ownsStaging reports whether one of names is a staging entry of the staged directory dir.
func ownsStaging(dir string, names []string) bool {
	return slices.ContainsFunc(names, func(name string) bool { return strings.HasPrefix(name, stagePrefix(dir)) })
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

// current reports whether the staged directory dir holds b's embedded SDK.
func (b sdkBundle) current(dir string) (bool, error) {
	hash, err := b.hash()
	if err != nil {
		return false, err
	}
	data, err := os.ReadFile(filepath.Join(dir, markerFile))
	if err != nil {
		return false, nil
	}
	return strings.TrimSpace(string(data)) == hash, nil
}

// ensure stages the bundle into this build's directory when its marker is missing or stale, and otherwise records the directory's use. It reports whether a stage actually happened. Other builds' directories are never touched.
func (b sdkBundle) ensure(configRoot string) (bool, error) {
	dir, err := b.stagedDir(configRoot)
	if err != nil {
		return false, err
	}
	current, err := b.current(dir)
	if err != nil {
		return false, err
	}
	if current {
		contentdirs.Touch(dir)
		settleStaging(dir)
		return false, nil
	}
	if _, err := b.sync(configRoot); err != nil {
		return false, err
	}
	return true, nil
}

// StageStatus reports one language SDK's staged state against the SDK embedded
// in the running binary. This build reads only its own staged directory, so it
// is either current or not staged yet.
type StageStatus struct {
	Lang     string // "go" | "python" | "rust"
	Dir      string // absolute directory this build stages the SDK in
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
		dir, err := b.stagedDir(configRoot)
		if err != nil {
			return nil, err
		}
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

// A program that links the SDKs is the only one whose hosts can build Go and Rust extensions (pigsdklock.StagedSDKDir names no SDK
// otherwise), so linking them also installs the check that a staged copy matches them for the hosts library entry points start
// (coding.DiscoverAndLoadExtensions).
func init() { subprocess.SetDefaultStagedSDKVerifier(VerifyStaged) }

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

// stagedSDK is one staged SDK directory and the language it holds.
type stagedSDK struct{ lang, dir string }

// stagedSDKs lists every staged SDK directory under configRoot, whichever build staged it: each language's per-build directories and the unhashed directory a PiG that predates them stages into.
func stagedSDKs(configRoot string) ([]stagedSDK, error) {
	parent := filepath.Join(configRoot, "state", "pigsdk")
	var out []stagedSDK
	for _, entry := range sdkBases {
		dirs, err := contentdirs.Dirs(parent, entry.base)
		if err != nil {
			return nil, err
		}
		for _, dir := range dirs {
			out = append(out, stagedSDK{entry.lang, dir})
		}
		unhashed := filepath.Join(parent, entry.base)
		if info, err := os.Stat(unhashed); err == nil && info.IsDir() {
			out = append(out, stagedSDK{entry.lang, unhashed})
		}
	}
	return out, nil
}

// LiveFingerprints returns the fingerprints of every staged SDK still on disk:
// this build's, the other builds' retained ones, and the unhashed ones an older
// PiG uses. A cached extension build stays reachable while its recorded
// fingerprint is among them, because the build's cache key folds the SDK in.
func LiveFingerprints(configRoot string) (map[string]struct{}, error) {
	sdks, err := stagedSDKs(configRoot)
	if err != nil {
		return nil, err
	}
	live := make(map[string]struct{}, len(sdks))
	for _, sdk := range sdks {
		fp, err := subprocess.StagedSDKFingerprint(sdk.dir, sdk.lang)
		if err != nil {
			// A directory that cannot be read has no live builds; leaving it
			// out is the correct answer rather than an error.
			continue
		}
		live[fp] = struct{}{}
	}
	return live, nil
}

// Prune drops extension builds no staged SDK can select.
func Prune(configRoot string, _ bool, dryRun bool) (runtimecell.CacheReport, error) {
	return prune(configRoot, dryRun, true)
}

// prune is Prune, with the report's byte counts left zero unless sized is set.
func prune(configRoot string, dryRun, sized bool) (runtimecell.CacheReport, error) {
	cacheRoot := filepath.Join(configRoot, "cache")
	// Fingerprints only classify existing entries. With none, hashing every staged tree cannot change the report, so a first start skips it and still requires a staged SDK. HasCacheEntries reads outside the cache GC lock, so the empty answer is confirmed by a dry run under that lock; a build published in between takes the classified path below.
	if entries, err := runtimecell.HasCacheEntries(cacheRoot); err == nil && !entries {
		sdks, err := stagedSDKs(configRoot)
		if err != nil {
			return runtimecell.CacheReport{}, err
		}
		if len(sdks) == 0 {
			return runtimecell.CacheReport{}, fmt.Errorf("no staged SDK found under %s; run `pig reload` first", configRoot)
		}
		confirmed, err := runtimecell.PruneCaches(runtimecell.CacheLifecycleOptions{CacheRoot: cacheRoot, DryRun: true, Unsized: !sized})
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
	return runtimecell.PruneCaches(runtimecell.CacheLifecycleOptions{
		CacheRoot: cacheRoot, LiveFingerprints: live, DryRun: dryRun, Unsized: !sized,
	})
}

// evictAndPrune follows a stage under the SDK stage lock. It removes other builds' staged SDK directories beyond the contentdirs.Keep most recently used of each language this build stages, never this build's own, then drops the extension builds no remaining SDK can select.
// The report's byte counts are read only when sized is set.
func evictAndPrune(configRoot string, sized bool) (runtimecell.CacheReport, error) {
	var errs []error
	for _, b := range bundles() {
		own, err := b.stagedDir(configRoot)
		if err != nil {
			return runtimecell.CacheReport{}, err
		}
		if _, err := contentdirs.Evict(filepath.Dir(own), sdkBase(b.lang), own); err != nil {
			errs = append(errs, err)
		}
	}
	if err := errors.Join(errs...); err != nil {
		return runtimecell.CacheReport{}, fmt.Errorf("evict staged SDKs: %w", err)
	}
	return prune(configRoot, false, sized)
}

// EnsureSynced stages every language SDK whose marker in this build's staged
// directory is missing or stale, then evicts the least recently used staged
// SDKs of other builds and drops the builds no remaining SDK can select.
//
// Staging is the event that changes which builds are reachable, so eviction
// and pruning run only after a stage rather than on a timer. A warm start only
// records that this build's directories are in use.
func EnsureSynced(configRoot string) error {
	return EnsureSyncedContext(context.Background(), configRoot)
}

// EnsureSyncedContext is EnsureSynced with a caller-owned lock-wait lifetime.
func EnsureSyncedContext(ctx context.Context, configRoot string) error {
	current := true
	leftovers := stagingNames(configRoot)
	embedded := bundles()
	dirs := make([]string, 0, len(embedded))
	for _, b := range embedded {
		dir, err := b.stagedDir(configRoot)
		if err != nil {
			return err
		}
		bundleCurrent, err := b.current(dir)
		if err != nil {
			return err
		}
		current = current && bundleCurrent && !ownsStaging(dir, leftovers)
		dirs = append(dirs, dir)
	}
	if current {
		for _, dir := range dirs {
			contentdirs.Touch(dir)
		}
		return nil
	}
	return withSDKStageLock(ctx, configRoot, func() error {
		restaged := false
		for _, b := range embedded {
			changed, err := b.ensure(configRoot)
			if err != nil {
				return err
			}
			restaged = restaged || changed
		}
		if restaged {
			// Automatic prune drops only builds proven stale by fingerprint. Nothing reads its report, so it does not stat every cached file for sizes.
			if _, err := evictAndPrune(configRoot, false); err != nil {
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
		return unknownLanguage("pig reload: ", lang)
	}
	dir, err := b.stagedDir(configRoot)
	if err != nil {
		return err
	}
	current, err := b.current(dir)
	if err != nil {
		return err
	}
	if current && !ownsStaging(dir, stagingNames(configRoot)) {
		contentdirs.Touch(dir)
		return nil
	}
	return withSDKStageLock(context.Background(), configRoot, func() error {
		_, err := b.ensure(configRoot)
		return err
	})
}

// Sync force-stages every language SDK into this build's directories, then
// evicts and prunes as EnsureSynced does after a stage, all under the SDK stage
// lock. It returns the count written per language and the prune report.
func Sync(configRoot string, _ bool) (map[string]int, runtimecell.CacheReport, error) {
	counts := make(map[string]int, len(bundles()))
	var report runtimecell.CacheReport
	err := withSDKStageLock(context.Background(), configRoot, func() error {
		for _, b := range bundles() {
			written, err := b.sync(configRoot)
			if err != nil {
				return err
			}
			counts[b.lang] = len(written)
		}
		var err error
		report, err = evictAndPrune(configRoot, true)
		return err
	})
	return counts, report, err
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
// this binary embeds and drops extension builds no staged SDK can select, so the
// next build compiles against the current source.
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
	root, err := configroot.Resolve()
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
				return "", unknownLanguage("", lang)
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
				_, _ = fmt.Fprintf(stdout, "pig reload: would stage %s to %s (embedded %s)\n", st.Lang, st.Dir, st.Embedded)
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

	// Pruning follows staging under the same stage lock because a stage is
	// what changes which builds are reachable: dropping them separately leaves
	// a window where the SDKs are current but the builds selected against them
	// are not.
	counts, result, err := Sync(root, slices.Contains(rest, "--all"))
	for _, b := range bundles() {
		written, ok := counts[b.lang]
		if !ok {
			continue
		}
		dir, dirErr := b.stagedDir(root)
		if dirErr != nil {
			err = errors.Join(err, dirErr)
			continue
		}
		_, _ = fmt.Fprintf(stdout, "pig reload: staged %s (%d files) to %s\n", b.lang, written, dir)
	}
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
	_, _ = fmt.Fprintln(w, "Stages the extension SDKs this binary embeds into its own directories and")
	_, _ = fmt.Fprintln(w, "drops extension builds whose SDK is no longer staged, so the next build")
	_, _ = fmt.Fprintln(w, "compiles against current source. The pre-session form of /reload; both")
	_, _ = fmt.Fprintln(w, "make what is loaded match what is on disk.")
	_, _ = fmt.Fprintln(w, "")
	_, _ = fmt.Fprintln(w, "  --all             also drop builds with no SDK fingerprint")
	_, _ = fmt.Fprintln(w, "  --dry-run         report what would be dropped, stage nothing")
	_, _ = fmt.Fprintln(w, "  --sdk-path [lang]     print this binary's staged SDK directory (go|python|rust, default go)")
	_, _ = fmt.Fprintln(w, "  --sdk-version [lang]  print an embedded SDK content hash")
	_, _ = fmt.Fprintln(w, "")
	_, _ = fmt.Fprintln(w, "Staged-versus-embedded status is reported by `pig diagnose`.")
}
