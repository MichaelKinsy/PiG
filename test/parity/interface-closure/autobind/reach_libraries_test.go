package main

import (
	"encoding/json"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/test/parity/interface-closure/autobind/rules"
)

// TestReachLibrariesPortPublishedEntryPoints holds libraryEntries to the pinned Pi and PORT_MAP:
//   - every entry names an exports subpath of the Pi package's package.json, and every published entry point of env, durable and
//     chord except test support (./testing) and the manifest (./package.json) has an entry;
//   - every listed Go package exists;
//   - every Go package that PORT_MAP records as porting a file the entry point publishes (its source and the `export ... from`
//     closure of it) is listed for that entry, so the seed does not leave out part of an entry point's API.
func TestReachLibrariesPortPublishedEntryPoints(t *testing.T) {
	root := filepath.Join("..", "..", "..", "..")
	mirror := filepath.Join(root, ".upstream", "current", "packages")
	ports := portMapGoDirs(t, root)
	published := map[string]bool{}
	for _, pkg := range []string{"env", "durable", "chord"} {
		data, err := os.ReadFile(filepath.Join(mirror, pkg, "package.json"))
		if err != nil {
			t.Skipf("pinned Pi mirror absent: %v", err)
		}
		var manifest struct {
			Exports map[string]json.RawMessage `json:"exports"`
		}
		if err := json.Unmarshal(data, &manifest); err != nil {
			t.Fatal(err)
		}
		for subpath, raw := range manifest.Exports {
			if subpath == "./package.json" || subpath == "./testing" || strings.HasPrefix(subpath, "./testing/") {
				continue
			}
			entry := pkg + ":" + subpath
			published[entry] = true
			var target struct {
				Source string `json:"source"`
			}
			if err := json.Unmarshal(raw, &target); err != nil || target.Source == "" {
				t.Errorf("%s: no source in package.json exports", entry)
				continue
			}
			for file := range exportClosure(filepath.Join(mirror, pkg), []string{path.Clean(target.Source)}) {
				for _, dir := range ports["packages/"+pkg+"/"+file] {
					if !slices.Contains(libraryEntries[entry], "./"+dir) {
						t.Errorf("%s publishes packages/%s/%s, which PORT_MAP ports in ./%s; libraryEntries does not list it", entry, pkg, file, dir)
					}
				}
			}
		}
	}
	for entry, dirs := range libraryEntries {
		if !published[entry] {
			t.Errorf("%s is not an entry point Pi publishes (or is test support)", entry)
		}
		for _, dir := range dirs {
			matches, _ := filepath.Glob(filepath.Join(root, dir, "*.go"))
			if !slices.ContainsFunc(matches, func(f string) bool { return !strings.HasSuffix(f, "_test.go") }) {
				t.Errorf("%s: %s holds no Go package", entry, dir)
			}
		}
	}
	for entry := range published {
		if _, ok := libraryEntries[entry]; !ok {
			t.Errorf("Pi publishes %s, which libraryEntries does not map to a Go package", entry)
		}
	}
}

// portMapGoDirs maps each Pi file PORT_MAP records (rules.ParsePortMap, as the library route reads it) to the directories of the Go
// files its cell cites, public packages only: a bare file name sits beside a cited path, and an internal package is not an entry
// point's API.
func portMapGoDirs(t *testing.T, root string) map[string][]string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, "docs", "parity", "PORT_MAP.md"))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string][]string{}
	for pi, files := range rules.ParsePortMap(string(data)) {
		for _, file := range files {
			dir := path.Dir(file)
			if dir == "." || strings.HasSuffix(file, "_test.go") || strings.HasPrefix(file, "internal/") || strings.Contains(dir, "*") {
				continue
			}
			if !slices.Contains(out[pi], dir) {
				out[pi] = append(out[pi], dir)
			}
		}
	}
	return out
}
