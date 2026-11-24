package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
	"github.com/MichaelKinsy/PiG/internal/modelgen/modeldata"
)

// snapshotTree maps every file below root to its content.
func snapshotTree(t *testing.T, root string) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		relative, _ := filepath.Rel(root, path)
		files[relative] = string(data)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func readSourceMetaOf(t *testing.T, dir string) sourceMeta {
	t.Helper()
	meta, err := readSourceMeta(dir)
	if err != nil {
		t.Fatal(err)
	}
	return meta
}

func manifestGeneratedAt(t *testing.T, dataDir string) string {
	t.Helper()
	var manifest struct {
		GeneratedAt string `json:"generatedAt"`
	}
	if err := json.Unmarshal(mustRead(t, filepath.Join(dataDir, modeldata.ModelDataManifestFile)), &manifest); err != nil {
		t.Fatal(err)
	}
	return manifest.GeneratedAt
}

// A catalog source is one live run packed: `-record-source` stores it, `-replay-source` generates from it offline, and the data a replay
// writes is the same on any day because its generatedAt is the recorded fetch time.
func TestRecordSourceThenReplaySource(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	status, stdout, stderr := runGeneratorSubprocess(t, root, idOrderCatalog(), "--record-source", source, "--json-only", "--json-output", filepath.Join(root, "live"))
	requireSuccess(t, status, stdout, stderr)

	meta := readSourceMetaOf(t, source)
	if meta.PiCommit != pigversion.UpstreamCommit {
		t.Errorf("meta piCommit = %q, want the pin %q", meta.PiCommit, pigversion.UpstreamCommit)
	}
	if meta.PiOverlayCommit != piOverlayCommit || len(meta.PiOverlayCommit) != 40 {
		t.Errorf("meta piOverlayCommit = %q, want %q", meta.PiOverlayCommit, piOverlayCommit)
	}
	archive := mustRead(t, filepath.Join(source, sourceArchiveFile))
	if sum := sha256.Sum256(archive); meta.ArchiveSHA256 != hex.EncodeToString(sum[:]) {
		t.Errorf("meta archiveSha256 = %s, want the sha256 of %s", meta.ArchiveSHA256, sourceArchiveFile)
	}
	var urls, wantURLs []string
	for _, response := range meta.Responses {
		urls = append(urls, response.URL)
		if response.Status != 200 || response.Bytes == 0 || len(response.SHA256) != 64 {
			t.Errorf("response %+v: want status 200, a body and its sha256", response)
		}
	}
	for url := range upstreamMockResponses(nil) {
		wantURLs = append(wantURLs, url)
	}
	if len(urls) != len(wantURLs) {
		t.Errorf("recorded %d responses (%v), want one per upstream source (%v)", len(urls), urls, wantURLs)
	}
	if _, err := time.Parse(isoMillis, meta.FetchedAt); err != nil {
		t.Errorf("meta fetchedAt %q: %v", meta.FetchedAt, err)
	}

	// The mock now serves an empty catalog, so data equal to the live run's can only come from the recording; the clock moved on, so
	// a generatedAt equal to the fetch time can only come from meta.json.
	time.Sleep(5 * time.Millisecond)
	first, second := filepath.Join(root, "first"), filepath.Join(root, "second")
	for _, out := range []string{first, second} {
		status, stdout, stderr = runGeneratorSubprocess(t, root, map[string]any{}, "--replay-source", source, "--data-output", out)
		requireSuccess(t, status, stdout, stderr)
	}
	if got := manifestGeneratedAt(t, first); got != meta.FetchedAt {
		t.Errorf("replayed manifest generatedAt = %q, want the fetch time %q", got, meta.FetchedAt)
	}
	if a, b := snapshotTree(t, first), snapshotTree(t, second); !reflect.DeepEqual(a, b) || len(a) == 0 {
		t.Errorf("two replays of one source differ (%d vs %d files)", len(a), len(b))
	}
	live := filepath.Join(root, "live-data")
	status, stdout, stderr = runGeneratorSubprocess(t, root, idOrderCatalog(), "--data-output", live)
	requireSuccess(t, status, stdout, stderr)
	liveFiles := snapshotTree(t, live)
	for name, replayed := range snapshotTree(t, first) {
		if name == modeldata.ModelDataManifestFile {
			continue
		}
		if replayed != liveFiles[name] {
			t.Errorf("%s: the replay of the recording differs from the live run it recorded", name)
		}
	}
}

// A failed run leaves the committed source as it was: the source is replaced only after the generation succeeded.
func TestRecordSourceKeepsTheCommittedSourceWhenTheRunFails(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	status, stdout, stderr := runGeneratorSubprocess(t, root, idOrderCatalog(), "--record-source", source, "--json-only", "--json-output", filepath.Join(root, "live"))
	requireSuccess(t, status, stdout, stderr)
	before := snapshotTree(t, source)

	status, stdout, stderr = runGeneratorSubprocess(t, root, idOrderCatalog(), "--record-source", source, "--replay-source", source, "--json-only", "--json-output", filepath.Join(root, "again"))
	if status == 0 {
		t.Fatalf("-record-source with -replay-source succeeded\nstdout=%s", stdout)
	}
	if after := snapshotTree(t, source); !reflect.DeepEqual(after, before) {
		t.Errorf("a failed run changed the committed source (stderr %q)", stderr)
	}
}

func TestUnpackSourceRequiresTheBytesMetaRecords(t *testing.T) {
	recorded := t.TempDir()
	if err := os.WriteFile(filepath.Join(recorded, "0.body"), []byte(`{"a":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	index := `[{"url":"https://example.test/a","status":200,"file":"0.body"}]` + "\n"
	if err := os.WriteFile(filepath.Join(recorded, fixtureIndexFile), []byte(index), 0o644); err != nil {
		t.Fatal(err)
	}
	pack := func() string {
		dir := t.TempDir()
		if err := packSource(recorded, dir, "2026-11-15T08:45:18.072Z", nil); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	unpack := func(dir string) error {
		return unpackSource(filepath.Join(dir, sourceArchiveFile), t.TempDir(), readSourceMetaOf(t, dir))
	}
	if err := unpack(pack()); err != nil {
		t.Fatalf("a source just packed does not unpack: %v", err)
	}
	t.Run("a changed archive", func(t *testing.T) {
		dir := pack()
		path := filepath.Join(dir, sourceArchiveFile)
		data := mustRead(t, path)
		data[len(data)/2] ^= 1
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := unpack(dir); err == nil || !strings.Contains(err.Error(), "is not the archive") {
			t.Errorf("error = %v, want the archive hash mismatch", err)
		}
	})
	t.Run("a body meta does not hash", func(t *testing.T) {
		dir := pack()
		meta := readSourceMetaOf(t, dir)
		meta.Responses[0].SHA256 = strings.Repeat("0", 64)
		if err := unpackSource(filepath.Join(dir, sourceArchiveFile), t.TempDir(), meta); err == nil || !strings.Contains(err.Error(), "is not the one") {
			t.Errorf("error = %v, want the response mismatch", err)
		}
	})
	t.Run("a fetch time that is not an instant", func(t *testing.T) {
		dir := pack()
		meta := readSourceMetaOf(t, dir)
		meta.FetchedAt = "yesterday"
		data, err := json.Marshal(meta)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, sourceMetaFile), data, 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := readSourceMeta(dir); err == nil || !strings.Contains(err.Error(), "fetchedAt") {
			t.Errorf("error = %v, want the fetchedAt complaint", err)
		}
	})
}
