// SPDX-License-Identifier: MIT

package ai

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestBundledCatalogNotOlderThanPinnedPi guards against bumping the Pi pin while leaving
// ai/providers_generated.go on an older recording: GeneratedModelDataGeneratedAt must be at
// or after the pinned @earendil-works/pi-ai package's own bundled catalog manifest generatedAt
// (third_party/pi-packs).
func TestBundledCatalogNotOlderThanPinnedPi(t *testing.T) {
	if GeneratedModelDataGeneratedAt == nil || *GeneratedModelDataGeneratedAt <= 0 {
		t.Fatal("GeneratedModelDataGeneratedAt is unset or non-positive")
	}
	bundledTime := time.UnixMilli(int64(*GeneratedModelDataGeneratedAt)).UTC()

	root := filepath.Join("..", "third_party", "pi-packs")
	var manifest struct {
		Version string `json:"version"`
		Packs   map[string]struct {
			Name string `json:"name"`
		} `json:"packs"`
	}
	raw, err := os.ReadFile(filepath.Join(root, "manifest.json"))
	if err != nil {
		t.Fatalf("read third_party/pi-packs/manifest.json: %v", err)
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatalf("parse third_party/pi-packs/manifest.json: %v", err)
	}
	var packFile string
	for name, info := range manifest.Packs {
		if info.Name == "@earendil-works/pi-ai" {
			packFile = name
			break
		}
	}
	if packFile == "" {
		t.Fatal("third_party/pi-packs/manifest.json does not list @earendil-works/pi-ai")
	}
	pinnedTime, err := readPiAIManifestGeneratedAt(filepath.Join(root, packFile))
	if err != nil {
		t.Fatalf("read pinned @earendil-works/pi-ai catalog generatedAt from %s: %v", packFile, err)
	}
	if bundledTime.Before(pinnedTime) {
		t.Fatalf("bundled catalog generatedAt (%s) is older than pinned @earendil-works/pi-ai@%s catalog (%s); regenerate with `make model-catalogs`",
			bundledTime.Format(time.RFC3339Nano), manifest.Version, pinnedTime.Format(time.RFC3339Nano))
	}
}

func readPiAIManifestGeneratedAt(tgzPath string) (time.Time, error) {
	f, err := os.Open(tgzPath)
	if err != nil {
		return time.Time{}, err
	}
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return time.Time{}, err
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return time.Time{}, errors.New("package/dist/providers/data/.manifest.json not found in tarball")
		}
		if err != nil {
			return time.Time{}, err
		}
		if hdr.Name != "package/dist/providers/data/.manifest.json" {
			continue
		}
		var mf struct {
			GeneratedAt string `json:"generatedAt"`
		}
		if err := json.NewDecoder(tr).Decode(&mf); err != nil {
			return time.Time{}, err
		}
		return time.Parse(time.RFC3339Nano, mf.GeneratedAt)
	}
}
