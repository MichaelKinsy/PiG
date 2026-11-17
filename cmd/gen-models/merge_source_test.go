package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/modelgen"
)

const (
	mergeCommittedBody = `{"openai":{"id":"openai","models":{"a":{"id":"a","v":1},"b":{"id":"b","v":1}}},"other":{"id":"other","models":{"x":{"id":"x"}}}}`
	// The live response differs from the committed one in rows nobody named (a, b, x) and has rows the committed one lacks (n0, n1, n2, y).
	mergeLiveBody  = `{"openai":{"id":"openai","models":{"n0":{"id":"n0","v":7},"a":{"id":"a","v":2},"n1":{"id":"n1","v":9},"b":{"id":"b","v":2},"n2":{"id":"n2","v":8}}},"other":{"id":"other","models":{"x":{"id":"x2"},"y":{"id":"y"}}}}`
	mergeOtherBody = `{"unrelated":true}`
	mergeFetchedAt = "2026-11-15T15:26:04.771Z"
	mergeLiveAt    = "2026-11-17T20:00:00.000Z"
)

func writeMergeSource(t *testing.T) string {
	t.Helper()
	scratch := t.TempDir()
	index := `[{"url":"` + modelgen.ModelsDevURL + `","status":200,"file":"0.body"},{"url":"https://example.invalid/other","status":200,"file":"1.body"}]`
	for name, body := range map[string]string{"index.json": index, "0.body": mergeCommittedBody, "1.body": mergeOtherBody} {
		if err := os.WriteFile(filepath.Join(scratch, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := t.TempDir()
	if err := packSource(scratch, dir, mergeFetchedAt, nil); err != nil {
		t.Fatal(err)
	}
	return dir
}

func mergeFetch(url string) ([]byte, error) {
	if url != modelgen.ModelsDevURL {
		return nil, errors.New("fetched " + url)
	}
	return []byte(mergeLiveBody), nil
}

func mergeNow() time.Time {
	at, _ := time.Parse(isoMillis, mergeLiveAt)
	return at
}

// unpackMergeSource returns the bodies of the source in dir by file name, after unpackSource has checked them against meta.json.
func unpackMergeSource(t *testing.T, dir string) (sourceMeta, map[string]string) {
	t.Helper()
	meta, err := readSourceMeta(dir)
	if err != nil {
		t.Fatal(err)
	}
	scratch := t.TempDir()
	if err := unpackSource(filepath.Join(dir, sourceArchiveFile), scratch, meta); err != nil {
		t.Fatal(err)
	}
	bodies := map[string]string{}
	for _, name := range []string{"0.body", "1.body"} {
		data, err := os.ReadFile(filepath.Join(scratch, name))
		if err != nil {
			t.Fatal(err)
		}
		bodies[name] = string(data)
	}
	return meta, bodies
}

// The named rows are the live ones, byte for byte, placed where the live listing has them; everything else is the committed recording's.
func TestMergeSourceTakesOnlyTheNamedRowsFromTheLiveResponse(t *testing.T) {
	dir := writeMergeSource(t)
	before, oldBodies := unpackMergeSource(t, dir)
	if err := mergeSource(dir, []string{"openai/n0", "openai/n1", "openai/n2"}, mergeFetch, mergeNow); err != nil {
		t.Fatal(err)
	}
	meta, bodies := unpackMergeSource(t, dir)
	want := `{"openai":{"id":"openai","models":{"n0":{"id":"n0","v":7},"a":{"id":"a","v":1},"n1":{"id":"n1","v":9},"b":{"id":"b","v":1},"n2":{"id":"n2","v":8}}},"other":{"id":"other","models":{"x":{"id":"x"}}}}`
	if bodies["0.body"] != want {
		t.Errorf("merged models.dev body =\n%s\nwant\n%s", bodies["0.body"], want)
	}
	if bodies["1.body"] != oldBodies["1.body"] {
		t.Errorf("another response changed: %s", bodies["1.body"])
	}
	if meta.FetchedAt != mergeFetchedAt {
		t.Errorf("fetchedAt = %s, want the committed recording's %s", meta.FetchedAt, mergeFetchedAt)
	}
	wantMerges := []sourceMerge{{Response: modelgen.ModelsDevURL, FetchedAt: mergeLiveAt, IDs: []string{"openai/n0", "openai/n1", "openai/n2"}}}
	if !reflect.DeepEqual(meta.Merges, wantMerges) {
		t.Errorf("merges = %+v, want %+v", meta.Merges, wantMerges)
	}
	if meta.Responses[1] != before.Responses[1] || meta.Responses[0].SHA256 == before.Responses[0].SHA256 || meta.Responses[0].Bytes != len(want) {
		t.Errorf("responses = %+v, before %+v", meta.Responses, before.Responses)
	}
	if meta.PiCommit != before.PiCommit || meta.PiOverlayCommit != piOverlayCommit {
		t.Errorf("meta = %+v", meta)
	}
}

// A second merge into a merged source adds its own record and keeps the first.
func TestMergeSourceKeepsEarlierMerges(t *testing.T) {
	dir := writeMergeSource(t)
	if err := mergeSource(dir, []string{"openai/n1"}, mergeFetch, mergeNow); err != nil {
		t.Fatal(err)
	}
	if err := mergeSource(dir, []string{"openai/n2"}, mergeFetch, mergeNow); err != nil {
		t.Fatal(err)
	}
	meta, bodies := unpackMergeSource(t, dir)
	if len(meta.Merges) != 2 || !reflect.DeepEqual(meta.Merges[0].IDs, []string{"openai/n1"}) || !reflect.DeepEqual(meta.Merges[1].IDs, []string{"openai/n2"}) {
		t.Errorf("merges = %+v", meta.Merges)
	}
	if want := `{"openai":{"id":"openai","models":{"a":{"id":"a","v":1},"n1":{"id":"n1","v":9},"b":{"id":"b","v":1},"n2":{"id":"n2","v":8}}},"other":{"id":"other","models":{"x":{"id":"x"}}}}`; bodies["0.body"] != want {
		t.Errorf("body = %s", bodies["0.body"])
	}
}

// The same inputs give the same bytes: the archive's member order, headers and timestamps do not move its hash.
func TestMergeSourceIsDeterministic(t *testing.T) {
	var archives, metas [][]byte
	for range 2 {
		dir := writeMergeSource(t)
		if err := mergeSource(dir, []string{"openai/n1", "openai/n2"}, mergeFetch, mergeNow); err != nil {
			t.Fatal(err)
		}
		archive, err := os.ReadFile(filepath.Join(dir, sourceArchiveFile))
		if err != nil {
			t.Fatal(err)
		}
		meta, err := os.ReadFile(filepath.Join(dir, sourceMetaFile))
		if err != nil {
			t.Fatal(err)
		}
		archives, metas = append(archives, archive), append(metas, meta)
	}
	if !bytes.Equal(archives[0], archives[1]) || !bytes.Equal(metas[0], metas[1]) {
		t.Error("two merges of the same inputs wrote different bytes")
	}
}

// A model the live response lacks, one the recording already holds, a provider the recording lacks and a malformed id are errors that
// leave the source as it was.
func TestMergeSourceRefusesWhatItCannotTake(t *testing.T) {
	for _, tc := range []struct{ id, want string }{
		{"openai/missing", "the live response holds no model openai/missing"},
		{"openai/a", "the committed recording already holds openai/a"},
		{"other/y", ""},
		{"absent/y", "live response"},
		{"openai", "is not <provider>/<model>"},
	} {
		t.Run(tc.id, func(t *testing.T) {
			dir := writeMergeSource(t)
			archive, _ := os.ReadFile(filepath.Join(dir, sourceArchiveFile))
			meta, _ := os.ReadFile(filepath.Join(dir, sourceMetaFile))
			err := mergeSource(dir, []string{"openai/n1", tc.id}, mergeFetch, mergeNow)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("a model of another provider: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
			if after, _ := os.ReadFile(filepath.Join(dir, sourceArchiveFile)); !bytes.Equal(after, archive) {
				t.Error("the archive changed")
			}
			if after, _ := os.ReadFile(filepath.Join(dir, sourceMetaFile)); !bytes.Equal(after, meta) {
				t.Error("meta.json changed")
			}
		})
	}
	if err := mergeSource(writeMergeSource(t), nil, mergeFetch, mergeNow); err == nil {
		t.Error("no ids: want an error")
	}
}
