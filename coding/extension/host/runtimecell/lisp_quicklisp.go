package runtimecell

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"context"
	"crypto/md5" //nolint:gosec // Quicklisp's releases.txt publishes MD5 digests; the dist file itself is pinned by SHA-256 and every archive also gets a recorded SHA-256
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// pig additive (D-1): Lisp extensions may depend on Quicklisp systems. The set is closed over one pinned dist: the two index files below are
// verified against their recorded SHA-256 before they are read, every release archive is verified against the size and MD5 the index lists and
// against a SHA-256 recorded the first time the archive was fetched, and nothing outside the dist is ever fetched. Any failure stops the build.
const (
	quicklispDist    = "2026-01-01"
	quicklispDistURL = "https://beta.quicklisp.org/dist/quicklisp/" + quicklispDist + "/"

	quicklispMaxArchive  = 256 << 20
	quicklispMaxUnpacked = 1 << 30
)

// The recorded digests of the pinned dist's index files; tests replace them to serve a small dist.
var (
	quicklispReleasesSHA256 = "9cad59398e760032de0053cafcfdcf16eb437caf1a20d65668a8e3b726485da4"
	quicklispSystemsSHA256  = "cb8309321840887b542fa853dc4fe95f9a4ea2d7ca028c521d32b67d2bdf51c7"
)

// quicklispBuiltins are system names SBCL and ASDF provide themselves; a dist row may list them as dependencies.
func quicklispBuiltin(name string) bool {
	name = strings.ToLower(name)
	return name == "asdf" || name == "uiop" || strings.HasPrefix(name, "uiop/") || strings.HasPrefix(name, "asdf/") || strings.HasPrefix(name, "sb-")
}

type quicklispRelease struct {
	Project string
	URL     string
	Size    int64
	MD5     string
	Prefix  string
}

type quicklispSystem struct {
	Project string
	Name    string
	Deps    []string
}

type quicklispIndex struct {
	releases map[string]quicklispRelease
	systems  map[string]quicklispSystem
}

// quicklispFetcher downloads one URL; tests replace it.
type quicklispFetcher func(ctx context.Context, url string, limit int64) ([]byte, error)

var quicklispFetch quicklispFetcher = fetchQuicklispHTTP

func fetchQuicklispHTTP(ctx context.Context, url string, limit int64) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: 5 * time.Minute}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: HTTP %d", url, response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%s: larger than %d bytes", url, limit)
	}
	return data, nil
}

func quicklispHex(h hash.Hash, data []byte) string {
	_, _ = h.Write(data)
	return hex.EncodeToString(h.Sum(nil))
}

// quicklispDistFile returns one pinned index file, from the cache when present, else fetched. Its SHA-256 is checked on every use.
func quicklispDistFile(ctx context.Context, root, name, want string) ([]byte, error) {
	file := filepath.Join(root, name)
	if data, err := os.ReadFile(file); err == nil {
		if quicklispHex(sha256.New(), data) == want {
			return data, nil
		}
	}
	if quicklispOffline() {
		return nil, fmt.Errorf("Quicklisp dist %s: %s is not cached and PIG_OFFLINE is set", quicklispDist, name)
	}
	data, err := quicklispFetch(ctx, quicklispDistURL+name, 64<<20)
	if err != nil {
		return nil, fmt.Errorf("Quicklisp dist %s: %w", quicklispDist, err)
	}
	if got := quicklispHex(sha256.New(), data); got != want {
		return nil, fmt.Errorf("Quicklisp dist %s: %s has sha256 %s, expected the pinned %s", quicklispDist, name, got, want)
	}
	if err := writeFileAtomic(file, data); err != nil {
		return nil, err
	}
	return data, nil
}

func writeFileAtomic(file string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(file), ".tmp-*")
	if err != nil {
		return err
	}
	_, werr := temp.Write(data)
	cerr := temp.Close()
	if werr != nil || cerr != nil {
		_ = os.Remove(temp.Name())
		return errors.Join(werr, cerr)
	}
	if err := os.Rename(temp.Name(), file); err != nil {
		_ = os.Remove(temp.Name())
		return err
	}
	return nil
}

func parseQuicklispIndex(releases, systems []byte) (*quicklispIndex, error) {
	index := &quicklispIndex{releases: map[string]quicklispRelease{}, systems: map[string]quicklispSystem{}}
	scan := bufio.NewScanner(strings.NewReader(string(releases)))
	scan.Buffer(make([]byte, 1<<20), 1<<20)
	for scan.Scan() {
		line := strings.TrimSpace(scan.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 6 {
			return nil, fmt.Errorf("Quicklisp releases.txt: malformed line %q", line)
		}
		size, err := strconv.ParseInt(fields[2], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("Quicklisp releases.txt: bad size in %q", line)
		}
		index.releases[fields[0]] = quicklispRelease{Project: fields[0], URL: fields[1], Size: size, MD5: fields[3], Prefix: fields[5]}
	}
	if err := scan.Err(); err != nil {
		return nil, err
	}
	scan = bufio.NewScanner(strings.NewReader(string(systems)))
	scan.Buffer(make([]byte, 1<<20), 1<<20)
	for scan.Scan() {
		line := strings.TrimSpace(scan.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 3 {
			return nil, fmt.Errorf("Quicklisp systems.txt: malformed line %q", line)
		}
		index.systems[fields[2]] = quicklispSystem{Project: fields[0], Name: fields[2], Deps: fields[3:]}
	}
	return index, scan.Err()
}

// closure returns the projects needed for the named systems, in name order.
func (index *quicklispIndex) closure(names []string) ([]string, error) {
	projects := map[string]bool{}
	seen := map[string]bool{}
	var visit func(name, from string) error
	visit = func(name, from string) error {
		name = strings.ToLower(name)
		if seen[name] {
			return nil
		}
		seen[name] = true
		system, ok := index.systems[name]
		if !ok {
			if from != "" && quicklispBuiltin(name) {
				return nil
			}
			if from == "" {
				return fmt.Errorf("system %q is not in the Quicklisp dist %s", name, quicklispDist)
			}
			return fmt.Errorf("system %q (needed by %q) is not in the Quicklisp dist %s", name, from, quicklispDist)
		}
		projects[system.Project] = true
		for _, dep := range system.Deps {
			if err := visit(dep, name); err != nil {
				return err
			}
		}
		return nil
	}
	for _, name := range names {
		if err := visit(name, ""); err != nil {
			return nil, err
		}
	}
	out := make([]string, 0, len(projects))
	for project := range projects {
		out = append(out, project)
	}
	sort.Strings(out)
	return out, nil
}

type quicklispRecord map[string]string

func readQuicklispRecord(file string) quicklispRecord {
	record := quicklispRecord{}
	if data, err := os.ReadFile(file); err == nil {
		_ = json.Unmarshal(data, &record)
	}
	return record
}

// ResolveQuicklisp makes the source of every Quicklisp system the named systems need available under cacheRoot and returns the directories that
// hold their .asd files. names are the systems the extension's own source depends on that ASDF cannot find. It fetches only through the pinned
// dist, honours PIG_OFFLINE, and fails closed on any mismatch.
func ResolveQuicklisp(ctx context.Context, cacheRoot string, names []string) ([]string, error) {
	if len(names) == 0 {
		return nil, nil
	}
	root := filepath.Join(cacheRoot, "quicklisp", quicklispDist)
	releasesData, err := quicklispDistFile(ctx, root, "releases.txt", quicklispReleasesSHA256)
	if err != nil {
		return nil, err
	}
	systemsData, err := quicklispDistFile(ctx, root, "systems.txt", quicklispSystemsSHA256)
	if err != nil {
		return nil, err
	}
	index, err := parseQuicklispIndex(releasesData, systemsData)
	if err != nil {
		return nil, err
	}
	projects, err := index.closure(names)
	if err != nil {
		return nil, err
	}
	recordFile := filepath.Join(root, "resolved.json")
	record := readQuicklispRecord(recordFile)
	changed := false
	var roots []string
	for _, project := range projects {
		release, ok := index.releases[project]
		if !ok {
			return nil, fmt.Errorf("Quicklisp dist %s: project %q has no release", quicklispDist, project)
		}
		if release.Prefix == "" || strings.Contains(release.Prefix, "..") || !filepath.IsLocal(release.Prefix) || strings.ContainsAny(release.Prefix, `/\`) {
			return nil, fmt.Errorf("Quicklisp release %s has an unsafe prefix %q", project, release.Prefix)
		}
		dir := filepath.Join(root, "src", project+"-"+release.MD5)
		unpacked := filepath.Join(dir, release.Prefix)
		if _, err := os.Stat(filepath.Join(dir, ".verified")); err == nil {
			roots = append(roots, unpacked)
			continue
		}
		if quicklispOffline() {
			return nil, fmt.Errorf("Quicklisp release %s is not cached and PIG_OFFLINE is set", project)
		}
		data, err := quicklispFetch(ctx, quicklispArchiveURL(release.URL), min(release.Size, quicklispMaxArchive))
		if err != nil {
			return nil, fmt.Errorf("fetch Quicklisp release %s: %w", project, err)
		}
		if int64(len(data)) != release.Size {
			return nil, fmt.Errorf("Quicklisp release %s is %d bytes, the dist lists %d", project, len(data), release.Size)
		}
		if got := quicklispHex(md5.New(), data); got != release.MD5 { //nolint:gosec // see the import
			return nil, fmt.Errorf("Quicklisp release %s has md5 %s, the dist lists %s", project, got, release.MD5)
		}
		sum := quicklispHex(sha256.New(), data)
		if recorded, ok := record[project+" "+release.MD5]; ok && recorded != sum {
			return nil, fmt.Errorf("Quicklisp release %s has sha256 %s, but %s was recorded for it", project, sum, recorded)
		}
		_ = os.RemoveAll(dir)
		staging := dir + ".part"
		_ = os.RemoveAll(staging)
		if err := extractQuicklisp(data, staging, release.Prefix); err != nil {
			_ = os.RemoveAll(staging)
			return nil, fmt.Errorf("unpack Quicklisp release %s: %w", project, err)
		}
		if err := writeFileAtomic(filepath.Join(staging, ".verified"), []byte(sum)); err != nil {
			return nil, err
		}
		if err := os.Rename(staging, dir); err != nil {
			return nil, err
		}
		record[project+" "+release.MD5] = sum
		changed = true
		roots = append(roots, unpacked)
	}
	if changed {
		data, err := json.MarshalIndent(record, "", "  ")
		if err != nil {
			return nil, err
		}
		if err := writeFileAtomic(recordFile, data); err != nil {
			return nil, err
		}
	}
	return roots, nil
}

// quicklispArchiveURL upgrades the dist's plain-http archive URL to https; the digests, not the transport, are what is trusted.
func quicklispArchiveURL(raw string) string {
	if rest, ok := strings.CutPrefix(raw, "http://"); ok {
		return "https://" + rest
	}
	return raw
}

// extractQuicklisp unpacks a release archive into dest. Every entry must lie under the release's prefix directory; links, devices and
// absolute or parent-relative names are refused.
func extractQuicklisp(data []byte, dest, prefix string) error {
	gz, err := gzip.NewReader(strings.NewReader(string(data)))
	if err != nil {
		return err
	}
	defer func() { _ = gz.Close() }()
	reader := tar.NewReader(gz)
	var total int64
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		switch header.Typeflag {
		case tar.TypeXGlobalHeader:
			continue
		case tar.TypeDir, tar.TypeReg:
		default:
			return fmt.Errorf("entry %q has unsupported type %q", header.Name, header.Typeflag)
		}
		name := strings.TrimPrefix(path.Clean(strings.TrimPrefix(header.Name, "./")), "/")
		if name == "." {
			continue
		}
		if strings.Contains(header.Name, "..") || strings.ContainsRune(header.Name, '\\') || !filepath.IsLocal(filepath.FromSlash(name)) || (name != prefix && !strings.HasPrefix(name, prefix+"/")) {
			return fmt.Errorf("entry %q is outside the release directory %q", header.Name, prefix)
		}
		target := filepath.Join(dest, filepath.FromSlash(name))
		if header.Typeflag == tar.TypeDir {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		total += header.Size
		if total > quicklispMaxUnpacked {
			return fmt.Errorf("archive unpacks to more than %d bytes", int64(quicklispMaxUnpacked))
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		file, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(file, io.LimitReader(reader, header.Size))
		if closeErr := file.Close(); copyErr != nil || closeErr != nil {
			return errors.Join(copyErr, closeErr)
		}
	}
	if _, err := os.Stat(filepath.Join(dest, prefix)); err != nil {
		return fmt.Errorf("archive has no %q directory", prefix)
	}
	return nil
}

// quicklispOffline reports the offline switch with the semantics of packagemanager.IsOfflineModeEnabled (PI_OFFLINE wins over PIG_OFFLINE),
// which this package cannot import: packagemanager depends on it through the extension host.
func quicklispOffline() bool {
	if value := os.Getenv("PI_OFFLINE"); value != "" {
		lower := strings.ToLower(value)
		return value == "1" || lower == "true" || lower == "yes"
	}
	switch strings.ToLower(strings.TrimSpace(os.Getenv("PIG_OFFLINE"))) {
	case "1", "true", "yes":
		return true
	}
	return false
}
