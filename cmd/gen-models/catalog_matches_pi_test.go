package main

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The fixtures are upstream responses (models.dev v2, OpenRouter, Vercel AI Gateway, NVIDIA NIM, Radius) pruned by
// test/parity/testdata/catalog-generation/prune.mjs from a live recording; golden.sha256 lists the sha256 of every file Pi's own
// generator (packages/ai/catalog/generate.ts at the pin) writes for them (equivalence.sh golden). The Go generator must write the
// same bytes: this is the regenerated-catalog evidence of the catalog definitions (packages/ai/catalog/**), and of the
// header-comment-only provider shards and models.generated.ts that depend on them.
func TestCatalogGenerationMatchesPi(t *testing.T) {
	fixtures := t.TempDir()
	unpackFixtures(t, filepath.Join("testdata", "catalog-generation", "fixtures.tar.gz"), fixtures)
	if err := replayResponses(fixtures); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { http.DefaultClient.Transport = nil })

	output := t.TempDir()
	options := generatorOptions{strict: true, dataOutputDir: filepath.Join(output, "data"), jsonOutputDir: filepath.Join(output, "catalog")}
	if err := generateModels(options); err != nil {
		t.Fatal(err)
	}

	want := readGolden(t, filepath.Join("testdata", "catalog-generation", "golden.sha256"))
	got := map[string]string{}
	err := filepath.WalkDir(output, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || entry.Name() == ".manifest.json" {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(output, path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		got[filepath.ToSlash(relative)] = hex.EncodeToString(sum[:])
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for name, digest := range want {
		switch have, ok := got[name]; {
		case !ok:
			t.Errorf("%s: not generated (Pi writes it)", name)
		case have != digest:
			t.Errorf("%s: sha256 %s, Pi's generator wrote %s", name, have, digest)
		}
	}
	for name := range got {
		if _, ok := want[name]; !ok {
			t.Errorf("%s: generated, but Pi's generator does not write it", name)
		}
	}
	if t.Failed() {
		t.Log("compare with Pi's generator: test/parity/testdata/catalog-generation/equivalence.sh compare <fixtures>")
	}
}

func readGolden(t *testing.T, path string) map[string]string {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	golden := map[string]string{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		digest, name, ok := strings.Cut(scanner.Text(), "  ")
		if !ok {
			t.Fatalf("%s: malformed line %q", path, scanner.Text())
		}
		golden[filepath.ToSlash(strings.TrimPrefix(name, "./"))] = digest
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return golden
}

func unpackFixtures(t *testing.T, archive, dir string) {
	t.Helper()
	file, err := os.Open(archive)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	zipped, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	reader := tar.NewReader(zipped)
	for {
		header, err := reader.Next()
		if err == io.EOF {
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		if header.Typeflag != tar.TypeReg {
			continue
		}
		data, err := io.ReadAll(reader)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, filepath.Base(header.Name)), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
