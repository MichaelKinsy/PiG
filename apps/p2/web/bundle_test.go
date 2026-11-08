package web

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// MANIFEST is the checksum list of every file carried verbatim from Pi (vendor.sh): the vendored tree is what Pi's commit
// holds, byte for byte.
func TestVendoredFilesMatchTheManifest(t *testing.T) {
	file, err := os.Open("MANIFEST")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	listed := map[string]bool{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		sum, path, ok := strings.Cut(line, "  ")
		if !ok {
			t.Fatalf("bad MANIFEST line %q", line)
		}
		local := filepath.Join("upstream", path)
		if rest, root := strings.CutPrefix(path, "../../"); root {
			local = filepath.Join("upstream-root", rest)
		}
		body, err := os.ReadFile(local)
		if err != nil {
			t.Fatal(err)
		}
		if got := sha256.Sum256(body); hex.EncodeToString(got[:]) != sum {
			t.Errorf("%s differs from the MANIFEST: edited? vendored files are never changed", path)
		}
		listed[local] = true
	}
	// Every file under upstream/ and upstream-root/ is listed.
	for _, root := range []string{"upstream", "upstream-root"} {
		err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
			if err == nil && !entry.IsDir() && !listed[path] {
				t.Errorf("%s is not in the MANIFEST", path)
			}
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

// upstream-files.txt is the list MANIFEST is made from.
func TestManifestListsTheSameFilesAsUpstreamFiles(t *testing.T) {
	listing, err := os.ReadFile("upstream-files.txt")
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := os.ReadFile("MANIFEST")
	if err != nil {
		t.Fatal(err)
	}
	inManifest := map[string]bool{}
	for _, line := range strings.Split(string(manifest), "\n") {
		if _, path, ok := strings.Cut(line, "  "); ok && !strings.HasPrefix(line, "#") {
			inManifest[path] = true
		}
	}
	paths := strings.Fields(string(listing))
	if len(paths) != len(inManifest) {
		t.Fatalf("upstream-files.txt has %d paths, MANIFEST %d", len(paths), len(inManifest))
	}
	for _, path := range paths {
		if !inManifest[path] {
			t.Errorf("%s is in upstream-files.txt, not in MANIFEST", path)
		}
	}
}

// The embedded bundle is whole: every file bundle.json names exists with the recorded checksum, the entry script and
// stylesheet are among them, and the version is the hash over the file names, as Pi computes it.
func TestEmbeddedBundleIsWhole(t *testing.T) {
	bundle, err := Embedded()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("dist/bundle.json")
	if err != nil {
		t.Fatal(err)
	}
	var described struct {
		Files []struct {
			Name   string `json:"name"`
			Sha256 string `json:"sha256"`
			Size   int    `json:"size"`
		} `json:"files"`
	}
	if err := json.Unmarshal(raw, &described); err != nil {
		t.Fatal(err)
	}
	if len(described.Files) == 0 || len(described.Files) != len(bundle.Files) {
		t.Fatalf("bundle.json lists %d files, the bundle has %d", len(described.Files), len(bundle.Files))
	}
	names := make([]string, 0, len(described.Files))
	for _, file := range described.Files {
		body := bundle.Files[file.Name].Body()
		if got := sha256.Sum256(body); hex.EncodeToString(got[:]) != file.Sha256 || len(body) != file.Size {
			t.Errorf("%s differs from bundle.json", file.Name)
		}
		names = append(names, file.Name)
	}
	// names are sorted by bundle.json, as `[...files.keys()].sort()` orders them for the version.
	sum := sha256.Sum256([]byte(strings.Join(names, "\n")))
	if hex.EncodeToString(sum[:]) != bundle.Version {
		t.Errorf("version %s is not the hash of the names", bundle.Version)
	}
	if !strings.HasPrefix(bundle.Script, "app-") || !strings.HasSuffix(bundle.Script, ".js") {
		t.Errorf("entry script %q", bundle.Script)
	}
	if bundle.Style == "" || bundle.Files[bundle.Style].Type != "text/css; charset=utf-8" {
		t.Errorf("stylesheet %q", bundle.Style)
	}
	manifest := bundle.Manifest()
	if manifest.Script != "/__p2_web/"+bundle.Script || manifest.Style == nil || *manifest.Style != "/__p2_web/"+bundle.Style {
		t.Errorf("manifest %+v", manifest)
	}
	var bytes int
	for name, file := range bundle.Files {
		if !strings.HasSuffix(name, ".map") {
			bytes += file.Size
		}
	}
	if bytes > 8*1024*1024 {
		t.Errorf("the bundle is %d bytes: Pi refuses more than 8 MiB", bytes)
	}
}

// Every public file the table names is embedded, and nothing outside the table is served by name.
func TestPublicFilesAreEmbedded(t *testing.T) {
	for path, file := range publicFileTable {
		body, err := (&Bundle{}).ReadPublicFile(file)
		if err != nil || len(body) == 0 {
			t.Errorf("%s: %v", path, err)
		}
		if _, ok := LookupPublicFile(path); !ok {
			t.Errorf("%s is not looked up", path)
		}
	}
	for _, path := range []string{"/", "/icons/other.png", "/manifest.webmanifest/x", "/__proto__", "/constructor", "/toString"} {
		if _, ok := LookupPublicFile(path); ok {
			t.Errorf("%s is a public file", path)
		}
	}
}

// The embedded bundle is the PiG-branded build, made from the unbranded one (Pi's own, the D472 oracle target): it names the
// version it was branded from and carries the brand's public files.
func TestEmbeddedBundleIsBranded(t *testing.T) {
	bundle, err := Embedded()
	if err != nil {
		t.Fatal(err)
	}
	if bundle.Brand.Name != "PiG" || bundle.Brand.Light != "#ffffff" || bundle.Brand.Dark != "#101c1a" {
		t.Errorf("brand %+v", bundle.Brand)
	}
	if len(bundle.UpstreamVersion) != 64 || bundle.UpstreamVersion == bundle.Version {
		t.Errorf("upstream version %q (bundle %q)", bundle.UpstreamVersion, bundle.Version)
	}
	for _, name := range []string{"icons/favicon.svg", "icons/apple-touch-icon.png", "icons/icon-192.png", "icons/icon-512.png", "manifest.webmanifest"} {
		if len(bundle.Public[name]) == 0 {
			t.Errorf("no public %s", name)
		}
	}
	var manifest struct {
		Name      string `json:"name"`
		ShortName string `json:"short_name"`
	}
	if err := json.Unmarshal(bundle.Public["manifest.webmanifest"], &manifest); err != nil {
		t.Fatal(err)
	}
	// `name` stays Pi's: test/p2/health.test.ts reads it.
	if manifest.Name != "Pi" || manifest.ShortName != "PiG" {
		t.Errorf("manifest %+v", manifest)
	}
}

// countingFS counts the whole-file reads by name (a Stat opens a file without reading its content).
type countingFS struct {
	fs.FS
	opened map[string]int
}

func (c *countingFS) ReadFile(name string) ([]byte, error) {
	c.opened[name]++
	return fs.ReadFile(c.FS, name)
}

// Loading the bundle reads its manifest (and the brand's small public files), not its files: the content is read when a file is
// first served, once. The startup of a server nobody opens the page of must not hold the bundle.
func TestBundleFilesAreReadWhenFirstServed(t *testing.T) {
	counting := &countingFS{FS: distFiles, opened: map[string]int{}}
	bundle, err := load(counting, "dist")
	if err != nil {
		t.Fatal(err)
	}
	for name := range bundle.Files {
		if counting.opened["dist/"+name] != 0 {
			t.Errorf("%s was read at load", name)
		}
	}
	script := bundle.Files[bundle.Script]
	if script.Size == 0 || len(script.Body()) != script.Size {
		t.Fatalf("script size %d, body %d", script.Size, len(script.Body()))
	}
	script.Body()
	if got := counting.opened["dist/"+bundle.Script]; got != 1 {
		t.Errorf("the script was read %d times, want once", got)
	}
}
