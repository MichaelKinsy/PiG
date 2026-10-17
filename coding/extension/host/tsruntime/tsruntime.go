// Package tsruntime selects the runtime that runs PiG's TypeScript and JavaScript extension host.
//
// pig additive (D19): Pi runs extensions inside its own Node process. PiG runs them in a subprocess, which Node runs by default. A
// user may request another runtime that an extension or Piglet installed into the runtime slot; PiG itself downloads nothing. The
// slot is a directory per runtime under Root, each holding a runtime.json Manifest. A requested runtime that cannot be used falls
// back to Node once, with a typed Reason.
package tsruntime

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/MichaelKinsy/PiG/internal/lazyregexp"
)

// Node is the default runtime: the node executable on PATH.
const Node = "node"

// EnvRuntime names the requested runtime for this process. It takes precedence over the setting.
const EnvRuntime = "PIG_TS_RUNTIME"

// EnvRoot replaces the slot root, for CI rows and evaluation arms that keep runtimes outside the agent directory.
const EnvRoot = "PIG_TS_RUNTIMES_DIR"

// ManifestFile is the manifest file name inside a runtime's directory.
const ManifestFile = "runtime.json"

// RequestsDir is the directory inside an executable runtime's directory where PiG records the extension sets it lacked an image for.
const RequestsDir = "requests"

// Kind is how PiG launches a runtime.
type Kind string

const (
	// KindBun is a stock Bun executable that runs PiG's runtime tree: <exe> --no-install --no-env-file --config=<empty> --preload <loader> <cli.mjs|cell.mjs> <args>.
	KindBun Kind = "bun"
	// KindExecutable is a host executable built from PiG's runtime tree: <exe> cli|cell <args>, with the runtime tree in PIG_TS_RUNTIME_TREE.
	KindExecutable Kind = "executable"
)

// Source is where a request came from.
type Source string

const (
	SourceDefault Source = "default"
	SourceSetting Source = "setting"
	SourceEnv     Source = "env"
)

// Reason is why a requested runtime was not used.
type Reason string

const (
	// ReasonInvalidRequest: the requested name is not a runtime name.
	ReasonInvalidRequest Reason = "invalid_request"
	// ReasonNotInstalled: the slot has no manifest for the requested runtime, or the slot root cannot be resolved.
	ReasonNotInstalled Reason = "not_installed"
	// ReasonInvalidManifest: the manifest cannot be read or does not have the current shape.
	ReasonInvalidManifest Reason = "invalid_manifest"
	// ReasonUnsupportedPlatform: the runtime was installed for another platform.
	ReasonUnsupportedPlatform Reason = "unsupported_platform"
	// ReasonNoImage: an executable runtime has no image for this extension set.
	ReasonNoImage Reason = "no_image"
	// ReasonStale: a file an executable image was built from changed since the build.
	ReasonStale Reason = "stale"
	// ReasonVerifyFailed: the executable's digest differs from its manifest, or a Bun executable is not in the lock.
	ReasonVerifyFailed Reason = "verify_failed"
	// ReasonStartFailed: the executable did not answer --version.
	ReasonStartFailed Reason = "start_failed"
	// ReasonNodeOnly: a launch of this extension set failed to start on a slot runtime and loaded on Node, and nothing it was built from changed since, so the set runs on Node.
	ReasonNodeOnly Reason = "node_only"
)

// Request is the runtime a process asks for.
type Request struct {
	ID     string `json:"id"`
	Source Source `json:"source"`
}

// RequestFrom returns the request of a process: PIG_TS_RUNTIME when set, else the setting, else Node.
func RequestFrom(env, setting string) Request {
	if env = strings.TrimSpace(env); env != "" {
		return Request{ID: env, Source: SourceEnv}
	}
	if setting = strings.TrimSpace(setting); setting != "" {
		return Request{ID: setting, Source: SourceSetting}
	}
	return Request{ID: Node, Source: SourceDefault}
}

var idPattern = lazyregexp.New(`^[a-z0-9][a-z0-9._-]{0,63}$`)

// ValidID reports whether id can name a runtime in the slot. Node is a valid request but never a slot directory.
func ValidID(id string) bool {
	return idPattern.MatchString(id) && id != "." && id != ".."
}

var hexPattern = lazyregexp.New(`^[0-9a-f]{64}$`)

// Root is the slot root for an agent directory: PIG_TS_RUNTIMES_DIR when set, else <agentDir>/runtimes/typescript. It returns "" when neither is known.
func Root(agentDir string) string {
	if root := os.Getenv(EnvRoot); root != "" {
		return root
	}
	if agentDir == "" {
		return ""
	}
	return filepath.Join(agentDir, "runtimes", "typescript")
}

// Platform names the running platform as PiG's release targets do: <os>-<arch> with os linux, darwin, windows or android and arch x64 or arm64.
func Platform() string {
	return platformOf(runtime.GOOS, runtime.GOARCH, os.Getenv("TERMUX_VERSION") != "")
}

func platformOf(goos, goarch string, termux bool) string {
	if goos == "linux" && termux {
		goos = "android"
	}
	arch := map[string]string{"amd64": "x64", "arm64": "arm64"}[goarch]
	if arch == "" {
		arch = goarch
	}
	return goos + "-" + arch
}

// Input is a file an executable image was built from. PiG treats the image as stale once the file's size or modification time differs.
type Input struct {
	Path    string `json:"path"`
	Size    int64  `json:"size"`
	MtimeNs int64  `json:"mtimeNs"`
}

// Image is one executable of a runtime.
type Image struct {
	// Executable is the path of the executable relative to the runtime's directory.
	Executable string `json:"executable"`
	// SHA256 is the lowercase hex SHA-256 digest of the executable.
	SHA256 string `json:"sha256"`
	// Inputs are the files the image was built from; empty for a runtime that compiles nothing in.
	//portlint:allow emptydrop PiG-only format with no Pi counterpart (Pi has no Piglets or runtime slot)
	Inputs []Input `json:"inputs,omitempty"`
}

// Manifest is <root>/<id>/runtime.json. The shape is strict: an unknown field is an invalid manifest.
type Manifest struct {
	ID       string `json:"id"`
	Kind     Kind   `json:"kind"`
	Version  string `json:"version"`
	Platform string `json:"platform"`
	// Provider names the extension or Piglet that installed the runtime, for status output.
	Provider string `json:"provider"`
	// Image is the executable for any extension set. A Bun runtime has exactly this one.
	Image *Image `json:"image,omitempty"`
	// Sets holds an executable runtime's images per extension set, keyed by SetKey.
	//portlint:allow emptydrop PiG-only format with no Pi counterpart (Pi has no Piglets or runtime slot)
	Sets map[string]Image `json:"sets,omitempty"`
	// Fallback names the slot runtime PiG tries when this one cannot run a set, before Node. Empty means Node.
	Fallback string `json:"fallback,omitempty"`
}

// ReadManifest reads and validates the manifest of runtime id under root.
func ReadManifest(root, id string) (Manifest, error) {
	data, err := os.ReadFile(filepath.Join(root, id, ManifestFile))
	if err != nil {
		return Manifest{}, err
	}
	return ParseManifest(id, data)
}

// ParseManifest decodes and validates a manifest for runtime id.
func ParseManifest(id string, data []byte) (Manifest, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var m Manifest
	if err := decoder.Decode(&m); err != nil {
		return Manifest{}, fmt.Errorf("decode %s: %w", ManifestFile, err)
	}
	if decoder.More() {
		return Manifest{}, fmt.Errorf("decode %s: trailing data", ManifestFile)
	}
	return m, m.validate(id)
}

func (m Manifest) validate(id string) error {
	switch {
	case m.ID != id:
		return fmt.Errorf("id %q does not name its directory %q", m.ID, id)
	case !ValidID(m.ID) || m.ID == Node:
		return fmt.Errorf("id %q is not a runtime name", m.ID)
	case m.Version == "":
		return errors.New("version is required")
	case m.Platform == "":
		return errors.New("platform is required")
	case m.Provider == "":
		return errors.New("provider is required")
	case m.Fallback != "" && (!ValidID(m.Fallback) || m.Fallback == m.ID):
		return fmt.Errorf("fallback %q is not another runtime's name", m.Fallback)
	}
	switch m.Kind {
	case KindBun:
		if m.Image == nil || len(m.Sets) != 0 {
			return errors.New("a bun runtime has one image and no sets")
		}
		if len(m.Image.Inputs) != 0 {
			return errors.New("a bun runtime image has no inputs")
		}
	case KindExecutable:
		// A provider installs the manifest before its first build, so that launches record build requests; until then the runtime holds no image.
	default:
		return fmt.Errorf("kind %q is not bun or executable", m.Kind)
	}
	if m.Image != nil {
		if err := m.Image.validate(); err != nil {
			return fmt.Errorf("image: %w", err)
		}
	}
	for _, key := range slices.Sorted(maps.Keys(m.Sets)) {
		if !hexPattern.MatchString(key) {
			return fmt.Errorf("set key %q is not a SetKey", key)
		}
		if err := m.Sets[key].validate(); err != nil {
			return fmt.Errorf("set %s: %w", key, err)
		}
	}
	return nil
}

func (i Image) validate() error {
	if i.Executable == "" || !filepath.IsLocal(filepath.FromSlash(i.Executable)) {
		return fmt.Errorf("executable %q is not a path inside the runtime directory", i.Executable)
	}
	if !hexPattern.MatchString(i.SHA256) {
		return fmt.Errorf("sha256 %q is not a lowercase hex SHA-256 digest", i.SHA256)
	}
	for _, input := range i.Inputs {
		if !filepath.IsAbs(input.Path) {
			return fmt.Errorf("input %q is not an absolute path", input.Path)
		}
	}
	return nil
}

// Fallback records why a requested runtime was not used.
type Fallback struct {
	Reason Reason `json:"reason"`
	Detail string `json:"detail"`
}

// Skip is one runtime that a selection did not use, with the reason.
type Skip struct {
	Runtime string `json:"runtime"`
	Reason  Reason `json:"reason"`
	Detail  string `json:"detail"`
}

// Selection is the runtime that runs one extension set.
type Selection struct {
	Request Request `json:"request"`
	// Runtime is "node" or the id of the slot runtime that runs the set.
	Runtime string `json:"runtime"`
	Kind    Kind   `json:"kind,omitempty"`
	Version string `json:"version,omitempty"`
	// Executable is the absolute path of the slot runtime's executable; empty for Node.
	Executable string `json:"executable,omitempty"`
	// Fallback is set when the requested runtime does not run this set: why it does not.
	Fallback *Fallback `json:"fallback,omitempty"`
	// Skipped lists every runtime tried before the one that runs the set, in order: the requested one, then its manifest's fallback.
	//portlint:allow emptydrop PiG-only format with no Pi counterpart (Pi has no Piglets or runtime slot)
	Skipped []Skip `json:"skipped,omitempty"`
}

// IsNode reports whether the set runs on Node.
func (s Selection) IsNode() bool { return s.Runtime == Node }

// Member is one extension of a set, as the host admits it.
type Member struct {
	Name  string `json:"name"`
	Entry string `json:"entry"`
}

// Set is the extension set one runtime process hosts.
type Set struct {
	// Key identifies the set across processes: SetKey of the runtime tree's digest and the members.
	Key     string   `json:"key"`
	Members []Member `json:"members"`
	// RuntimeTree is the materialized runtime tree the process runs.
	RuntimeTree string `json:"runtimeTree"`
}

// Resolver resolves requests against one slot root and caches verification for the life of the process.
type Resolver struct {
	Root     string
	Platform string
	verifier *Verifier
}

// NewResolver returns a resolver over root for this platform that verifies through v.
func NewResolver(root string, v *Verifier) *Resolver {
	return &Resolver{Root: root, Platform: Platform(), verifier: v}
}

// Resolve selects the runtime for set. A request for a slot runtime that cannot run the set falls back to Node with the reason.
// For an executable runtime without a current image for the set, it records the set under the runtime's requests directory so the
// runtime's provider can build one; the request file's write error, if any, is part of the fallback detail.
func (r *Resolver) Resolve(ctx context.Context, req Request, set Set) Selection {
	sel := Selection{Request: req, Runtime: Node}
	if req.ID == Node {
		return sel
	}
	// The requested runtime, then the one its manifest names as fallback, then Node: a fallback's own fallback is not followed.
	id := req.ID
	for tier := 0; tier < 2 && id != "" && id != Node; tier++ {
		image, next, skip := r.tryRuntime(ctx, id, set)
		if skip == nil {
			sel.Runtime, sel.Kind, sel.Version, sel.Executable = image.manifest.ID, image.manifest.Kind, image.manifest.Version, image.executable
			return sel
		}
		if sel.Fallback == nil {
			sel.Fallback = &Fallback{Reason: skip.Reason, Detail: skip.Detail}
		}
		sel.Skipped = append(sel.Skipped, *skip)
		id = next
	}
	return sel
}

type verifiedImage struct {
	manifest   Manifest
	executable string
}

// tryRuntime checks runtime id for set. It returns the verified image, or why the runtime cannot run the set with the runtime its
// manifest names as fallback.
func (r *Resolver) tryRuntime(ctx context.Context, id string, set Set) (verifiedImage, string, *Skip) {
	skip := func(reason Reason, format string, args ...any) *Skip {
		return &Skip{Runtime: id, Reason: reason, Detail: fmt.Sprintf(format, args...)}
	}
	if !ValidID(id) {
		return verifiedImage{}, "", skip(ReasonInvalidRequest, "%q is not a runtime name", id)
	}
	if r.Root == "" {
		return verifiedImage{}, "", skip(ReasonNotInstalled, "the runtime slot root is unknown (set %s or the agent directory)", EnvRoot)
	}
	dir := filepath.Join(r.Root, id)
	manifest, err := ReadManifest(r.Root, id)
	if errors.Is(err, fs.ErrNotExist) {
		return verifiedImage{}, "", skip(ReasonNotInstalled, "%s has no %s", dir, ManifestFile)
	}
	if err != nil {
		return verifiedImage{}, "", skip(ReasonInvalidManifest, "%s: %v", filepath.Join(dir, ManifestFile), err)
	}
	next := manifest.Fallback
	if manifest.Platform != r.Platform {
		return verifiedImage{}, next, skip(ReasonUnsupportedPlatform, "%s is installed for %s, this platform is %s", id, manifest.Platform, r.Platform)
	}
	image, ok := manifest.Image, manifest.Image != nil
	if set.Key != "" {
		if setImage, found := manifest.Sets[set.Key]; found {
			image, ok = &setImage, true
		}
	}
	if !ok {
		return verifiedImage{}, next, skip(ReasonNoImage, "%s has no image for extension set %s%s", id, set.Key, recordRequest(dir, manifest, set))
	}
	if changed := staleInput(image.Inputs); changed != "" {
		return verifiedImage{}, next, skip(ReasonStale, "%s changed since %s was built%s", changed, id, recordRequest(dir, manifest, set))
	}
	executable := filepath.Join(dir, filepath.FromSlash(image.Executable))
	if reason, err := r.verifier.Verify(ctx, manifest, executable, image.SHA256); err != nil {
		return verifiedImage{}, next, skip(reason, "%s: %v", executable, err)
	}
	if manifest.Kind == KindExecutable {
		touchUsed(filepath.Dir(executable))
	}
	return verifiedImage{manifest: manifest, executable: executable}, "", nil
}

// UsedFile is the file inside an image's directory whose modification time records when PiG last started a set from the image. A provider that
// prunes images reads it to evict the least recently used first.
const UsedFile = ".used"

// usedGranularity bounds how often one image's use is recorded.
// pig additive (D19): Pi has no runtime slot whose images a provider prunes.
const usedGranularity = time.Minute

// touchUsed records that a launch selected the image in dir. It is best effort: a read-only slot only loses the recency.
func touchUsed(dir string) {
	path := filepath.Join(dir, UsedFile)
	if info, err := os.Stat(path); err == nil && time.Since(info.ModTime()) < usedGranularity {
		return
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	_ = file.Close()
	now := time.Now()
	_ = os.Chtimes(path, now, now)
}

// staleInput returns the first input whose size or modification time differs, or "".
func staleInput(inputs []Input) string {
	for _, input := range inputs {
		info, err := os.Stat(input.Path)
		if err != nil || info.Size() != input.Size || info.ModTime().UnixNano() != input.MtimeNs {
			return input.Path
		}
	}
	return ""
}

// requestRefresh is how long a recorded build request stands before a launch of the same set writes it again, so a provider that
// polls requests/ sees one write per set and minute however often the set launches.
// pig additive (D19): Pi has no runtime slot to request builds from.
const requestRefresh = time.Minute

// recordRequest writes <dir>/requests/<key>.json for an executable runtime, unless a request for the key was written within
// requestRefresh, and returns "" or the write error as a detail suffix.
func recordRequest(dir string, manifest Manifest, set Set) string {
	if manifest.Kind != KindExecutable || !hexPattern.MatchString(set.Key) {
		return ""
	}
	path := filepath.Join(dir, RequestsDir, set.Key+".json")
	if info, err := os.Stat(path); err == nil && time.Since(info.ModTime()) < requestRefresh {
		return ""
	}
	//portlint:allow jsonescape PiG-only build-request record for an executable runtime's image builder, with no Pi counterpart (Pi has no runtime slot); any JSON parser reads \u003c and \u0026 as the same text
	data, err := json.MarshalIndent(set, "", "  ")
	if err == nil {
		err = writeFileAtomic(path, append(data, '\n'))
	}
	if err != nil {
		return fmt.Sprintf(" (recording the build request failed: %v)", err)
	}
	return ""
}

func writeFileAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".request-*")
	if err != nil {
		return err
	}
	_, writeErr := tmp.Write(data)
	closeErr := tmp.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		return errors.Join(err, os.Remove(tmp.Name()))
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return errors.Join(err, os.Remove(tmp.Name()))
	}
	return nil
}

// SetKey identifies an extension set: the digest of the runtime tree it runs with and its members in admission order.
func SetKey(runtimeDigest []byte, members []Member) string {
	hash := sha256.New()
	_, _ = hash.Write(runtimeDigest)
	for _, member := range members {
		_, _ = hash.Write([]byte("\x00" + member.Name + "\x00" + member.Entry))
	}
	return hex.EncodeToString(hash.Sum(nil))
}

// Installed is one directory of the slot.
type Installed struct {
	ID       string    `json:"id"`
	Manifest *Manifest `json:"manifest,omitempty"`
	// Error is why the directory's manifest is not usable.
	Error string `json:"error,omitempty"`
}

// List returns the slot's runtime directories in name order. A missing root lists nothing.
func List(root string) ([]Installed, error) {
	if root == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(root)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var installed []Installed
	for _, entry := range entries {
		if !entry.IsDir() || !ValidID(entry.Name()) || entry.Name() == Node {
			continue
		}
		item := Installed{ID: entry.Name()}
		if manifest, err := ReadManifest(root, entry.Name()); err != nil {
			item.Error = err.Error()
		} else {
			item.Manifest = &manifest
		}
		installed = append(installed, item)
	}
	return installed, nil
}

// ResolveAfter continues a selection whose runtime failed to start the set: the failed runtime is skipped with ReasonStartFailed and
// detail, then, when it was the requested runtime, the runtime its manifest names as fallback is tried, then Node.
func (r *Resolver) ResolveAfter(ctx context.Context, sel Selection, set Set, detail string) Selection {
	next := Selection{Request: sel.Request, Runtime: Node, Fallback: sel.Fallback, Skipped: slices.Clone(sel.Skipped)}
	if next.Fallback == nil {
		next.Fallback = &Fallback{Reason: ReasonStartFailed, Detail: detail}
	}
	next.Skipped = append(next.Skipped, Skip{Runtime: sel.Runtime, Reason: ReasonStartFailed, Detail: detail})
	if sel.IsNode() || len(sel.Skipped) != 0 {
		return next
	}
	manifest, err := ReadManifest(r.Root, sel.Runtime)
	if err != nil || manifest.Fallback == "" || manifest.Fallback == Node {
		return next
	}
	image, _, skip := r.tryRuntime(ctx, manifest.Fallback, set)
	if skip != nil {
		next.Skipped = append(next.Skipped, *skip)
		return next
	}
	next.Runtime, next.Kind, next.Version, next.Executable = image.manifest.ID, image.manifest.Kind, image.manifest.Version, image.executable
	return next
}
