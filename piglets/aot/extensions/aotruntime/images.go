package aotruntime

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// runtimeID is the slot directory and the name users select with extensionsTypescriptRuntime or PIG_TS_RUNTIME.
const runtimeID = "aot"

// providerName names this Piglet in runtime.json.
const providerName = "pig-aot/aot-runtime"

// fallbackRuntime is the runtime that runs a set while its image is missing or stale: stock Bun, which PiG's own TypeScript runtime slot pins. When
// Bun is not installed the set runs on Node.
const fallbackRuntime = "bun"

// manifest is PiG's runtime slot manifest (docs/site/docs/typescript-runtimes.md) for the compiled host.
type manifest struct {
	ID       string           `json:"id"`
	Kind     string           `json:"kind"`
	Version  string           `json:"version"`
	Platform string           `json:"platform"`
	Provider string           `json:"provider"`
	Sets     map[string]image `json:"sets,omitempty"`
	Fallback string           `json:"fallback,omitempty"`
}

type image struct {
	Executable string      `json:"executable"`
	SHA256     string      `json:"sha256"`
	Inputs     []inputFile `json:"inputs,omitempty"`
}

// slotRoot is the runtime slot root PiG reads: PIG_TS_RUNTIMES_DIR, else <agent dir>/runtimes/typescript.
func slotRoot(agentDir func() (string, error)) (string, error) {
	if root := os.Getenv("PIG_TS_RUNTIMES_DIR"); root != "" {
		return root, nil
	}
	dir, err := agentDir()
	if err != nil {
		return "", fmt.Errorf("resolve the agent directory: %w", err)
	}
	return filepath.Join(dir, "runtimes", "typescript"), nil
}

// store is the provider's directory in the runtime slot and everything under it.
type store struct {
	dir      string
	platform string
	// now is the clock; tests replace it.
	now func() time.Time
}

func newStore(root, platform string) *store {
	return &store{dir: filepath.Join(root, runtimeID), platform: platform, now: time.Now}
}

func (s *store) manifestPath() string { return filepath.Join(s.dir, "runtime.json") }
func (s *store) requestsDir() string  { return filepath.Join(s.dir, "requests") }
func (s *store) imagesDir() string    { return filepath.Join(s.dir, "images") }
func (s *store) workDir() string      { return filepath.Join(s.dir, "work") }
func (s *store) logsDir() string      { return filepath.Join(s.dir, "logs") }
func (s *store) failuresDir() string  { return filepath.Join(s.dir, "failures") }

// withManifest runs update on the manifest under the provider's lock and writes the result back by rename. A missing manifest starts empty. update
// returns false to leave the file as it is.
func (s *store) withManifest(update func(*manifest) (bool, error)) error {
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return err
	}
	lock, _, err := lockFile(filepath.Join(s.dir, "manifest.lock"), true)
	if err != nil {
		return err
	}
	defer lock.Unlock()
	m, err := s.readManifest()
	fresh := errors.Is(err, fs.ErrNotExist)
	if fresh {
		m = manifest{ID: runtimeID, Kind: "executable", Version: toolchainVersion, Platform: s.platform, Provider: providerName, Fallback: fallbackRuntime}
	} else if err != nil {
		return err
	}
	changed, err := update(&m)
	if err != nil || (!changed && !fresh) {
		return err
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.dir, ".runtime-*")
	if err != nil {
		return err
	}
	_, writeErr := tmp.Write(append(data, '\n'))
	if err := errors.Join(writeErr, tmp.Close()); err != nil {
		return errors.Join(err, os.Remove(tmp.Name()))
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return errors.Join(err, os.Remove(tmp.Name()))
	}
	return os.Rename(tmp.Name(), s.manifestPath())
}

func (s *store) readManifest() (manifest, error) {
	data, err := os.ReadFile(s.manifestPath())
	if err != nil {
		return manifest{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var m manifest
	if err := decoder.Decode(&m); err != nil {
		return manifest{}, fmt.Errorf("runtime.json: %w", err)
	}
	return m, nil
}

// ensureManifest creates the manifest of an installed provider with no images. PiG records a build request for a set only once the manifest exists.
func (s *store) ensureManifest() error {
	return s.withManifest(func(m *manifest) (bool, error) {
		if m.ID == runtimeID && m.Kind == "executable" && m.Platform == s.platform && m.Provider == providerName && m.Fallback == fallbackRuntime {
			return false, nil
		}
		m.ID, m.Kind, m.Version, m.Platform, m.Provider, m.Fallback = runtimeID, "executable", toolchainVersion, s.platform, providerName, fallbackRuntime
		return true, nil
	})
}

// imageSet is one image the manifest holds, with what the store knows about it on disk.
type imageSet struct {
	Key      string
	Image    image
	Dir      string
	Size     int64
	LastUsed time.Time
}

// publish moves a finished build's directory into the store as the image of key and points the manifest at it. The directory is renamed into place
// before the manifest changes, so the host never reads a manifest for an image that is not there. It returns the previous image directory of key.
func (s *store) publish(key string, built *buildResult) (previous string, err error) {
	if err := os.MkdirAll(s.imagesDir(), 0o755); err != nil {
		return "", err
	}
	id := fmt.Sprintf("%s-%d", key[:16], s.now().UnixNano())
	dir := filepath.Join(s.imagesDir(), id)
	if err := os.Rename(built.Dir, dir); err != nil {
		return "", fmt.Errorf("publish the image: %w", err)
	}
	digest, err := fileSHA256(filepath.Join(dir, filepath.Base(built.Executable)))
	if err != nil {
		return "", errors.Join(err, os.RemoveAll(dir))
	}
	now := s.now()
	_ = os.WriteFile(filepath.Join(dir, ".used"), nil, 0o644)
	_ = os.Chtimes(filepath.Join(dir, ".used"), now, now)
	rel := filepath.ToSlash(filepath.Join("images", id, filepath.Base(built.Executable)))
	err = s.withManifest(func(m *manifest) (bool, error) {
		if m.Sets == nil {
			m.Sets = map[string]image{}
		}
		if old, ok := m.Sets[key]; ok {
			previous = filepath.Join(s.dir, filepath.FromSlash(filepath.Dir(old.Executable)))
		}
		m.Sets[key] = image{Executable: rel, SHA256: digest, Inputs: built.Inputs}
		return true, nil
	})
	if err != nil {
		return "", errors.Join(err, os.RemoveAll(dir))
	}
	return previous, nil
}

// images lists the manifest's images with their size and last use.
func (s *store) images() ([]imageSet, error) {
	m, err := s.readManifest()
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var sets []imageSet
	for _, key := range sortedKeys(m.Sets) {
		img := m.Sets[key]
		dir := filepath.Join(s.dir, filepath.FromSlash(filepath.Dir(img.Executable)))
		size, _ := dirSize(dir)
		used := time.Time{}
		if info, err := os.Stat(filepath.Join(dir, ".used")); err == nil {
			used = info.ModTime()
		}
		sets = append(sets, imageSet{Key: key, Image: img, Dir: dir, Size: size, LastUsed: used})
	}
	return sets, nil
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

func dirSize(dir string) (int64, error) {
	var total int64
	err := filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		info, err := d.Info()
		if err == nil {
			total += info.Size()
		}
		return err
	})
	return total, err
}

// prune removes least recently used images until the images fit the budget, never the image of keep and never an image a running process executes. It
// returns the keys it removed. Directories under images/ that no manifest entry names, left by an interrupted publish, go first.
func (s *store) prune(budget int64, keep string) ([]string, error) {
	sets, err := s.images()
	if err != nil {
		return nil, err
	}
	referenced := map[string]bool{}
	for _, set := range sets {
		referenced[set.Dir] = true
	}
	if entries, err := os.ReadDir(s.imagesDir()); err == nil {
		for _, entry := range entries {
			dir := filepath.Join(s.imagesDir(), entry.Name())
			if entry.IsDir() && !referenced[dir] && !inUse(dir) {
				_ = os.RemoveAll(dir)
			}
		}
	}
	var total int64
	for _, set := range sets {
		total += set.Size
	}
	slices.SortFunc(sets, func(a, b imageSet) int { return a.LastUsed.Compare(b.LastUsed) })
	var removed []string
	for _, set := range sets {
		if total <= budget {
			break
		}
		if set.Key == keep || inUse(set.Dir) {
			continue
		}
		err := s.withManifest(func(m *manifest) (bool, error) {
			delete(m.Sets, set.Key)
			return true, nil
		})
		if err != nil {
			return removed, err
		}
		if err := os.RemoveAll(set.Dir); err != nil {
			return removed, err
		}
		total -= set.Size
		removed = append(removed, set.Key)
	}
	return removed, nil
}

// removeAll deletes every image, request and log, and the manifest, but not the toolchain.
func (s *store) removeAll() error {
	var errs []error
	for _, dir := range []string{s.imagesDir(), s.requestsDir(), s.workDir(), s.logsDir(), s.failuresDir()} {
		errs = append(errs, os.RemoveAll(dir))
	}
	errs = append(errs, os.Remove(s.manifestPath()))
	if err := errors.Join(errs...); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// failure is the memo of a build that failed: the same inputs are not built again.
type failure struct {
	Reason string      `json:"reason"`
	Detail string      `json:"detail"`
	When   time.Time   `json:"when"`
	Inputs []inputFile `json:"inputs"`
	Log    string      `json:"log,omitempty"`
}

func (s *store) failurePath(key string) string { return filepath.Join(s.failuresDir(), key+".json") }

// recordFailure remembers that key failed to build with the given member inputs.
func (s *store) recordFailure(key string, f failure) error {
	if err := os.MkdirAll(s.failuresDir(), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.failurePath(key), append(data, '\n'), 0o644)
}

// knownFailure returns the memo for key while the inputs it recorded are unchanged.
func (s *store) knownFailure(key string, current []inputFile) (failure, bool) {
	data, err := os.ReadFile(s.failurePath(key))
	if err != nil {
		return failure{}, false
	}
	var f failure
	if json.Unmarshal(data, &f) != nil || !slices.Equal(f.Inputs, current) {
		_ = os.Remove(s.failurePath(key))
		return failure{}, false
	}
	return f, true
}

// failures lists the recorded failures by key.
func (s *store) failures() map[string]failure {
	out := map[string]failure{}
	entries, _ := os.ReadDir(s.failuresDir())
	for _, entry := range entries {
		key, ok := strings.CutSuffix(entry.Name(), ".json")
		if !ok {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.failuresDir(), entry.Name()))
		var f failure
		if err == nil && json.Unmarshal(data, &f) == nil {
			out[key] = f
		}
	}
	return out
}

// memberInputs stats each member's entry and its nearest package.json. It is what a failure memo compares to find the extension changed.
func memberInputs(members []Member) []inputFile {
	var inputs []inputFile
	for _, member := range members {
		for _, path := range []string{member.Entry, nearestPackageJSON(member.Entry)} {
			if path == "" {
				continue
			}
			if info, err := os.Stat(path); err == nil {
				inputs = append(inputs, inputFile{Path: path, Size: info.Size(), MtimeNs: info.ModTime().UnixNano()})
			}
		}
	}
	return inputs
}

// readRequests returns the build requests the host recorded, oldest first.
func (s *store) readRequests() ([]Request, error) {
	entries, err := os.ReadDir(s.requestsDir())
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	type dated struct {
		req  Request
		when time.Time
	}
	var found []dated
	for _, entry := range entries {
		key, ok := strings.CutSuffix(entry.Name(), ".json")
		if !ok || len(key) != 64 {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.requestsDir(), entry.Name()))
		if err != nil {
			continue
		}
		var req Request
		if json.Unmarshal(data, &req) != nil || req.Key != key || len(req.Members) == 0 {
			_ = os.Remove(filepath.Join(s.requestsDir(), entry.Name()))
			continue
		}
		info, _ := entry.Info()
		when := time.Time{}
		if info != nil {
			when = info.ModTime()
		}
		found = append(found, dated{req, when})
	}
	slices.SortFunc(found, func(a, b dated) int { return a.when.Compare(b.when) })
	out := make([]Request, len(found))
	for i, f := range found {
		out[i] = f.req
	}
	return out, nil
}

func (s *store) deleteRequest(key string) { _ = os.Remove(filepath.Join(s.requestsDir(), key+".json")) }

// haveCurrentImage reports whether the manifest holds an image for key whose inputs are unchanged.
func (s *store) haveCurrentImage(key string) bool {
	m, err := s.readManifest()
	if err != nil {
		return false
	}
	img, ok := m.Sets[key]
	if !ok {
		return false
	}
	for _, input := range img.Inputs {
		info, err := os.Stat(input.Path)
		if err != nil || info.Size() != input.Size || info.ModTime().UnixNano() != input.MtimeNs {
			return false
		}
	}
	return true
}
