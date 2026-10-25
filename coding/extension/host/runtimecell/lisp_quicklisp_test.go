package runtimecell

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/md5" //nolint:gosec // the dist lists md5
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeDist struct {
	files   map[string][]byte
	fetched []string
}

func tgz(t *testing.T, entries map[string]string, headers ...tar.Header) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range entries {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		_, _ = tw.Write([]byte(body))
	}
	for _, h := range headers {
		h := h
		if err := tw.WriteHeader(&h); err != nil {
			t.Fatal(err)
		}
	}
	_ = tw.Close()
	_ = gz.Close()
	return buf.Bytes()
}

func installFakeDist(t *testing.T, archives map[string][]byte, systems string) *fakeDist {
	t.Helper()
	d := &fakeDist{files: map[string][]byte{}}
	var releases strings.Builder
	releases.WriteString("# project url size file-md5 content-sha1 prefix\n")
	for project, data := range archives {
		sum := md5.Sum(data) //nolint:gosec // see the import
		fmt.Fprintf(&releases, "%s http://example.test/%s.tgz %d %s 0 %s-1\n", project, project, len(data), hex.EncodeToString(sum[:]), project)
		d.files["https://example.test/"+project+".tgz"] = data
	}
	d.files[quicklispDistURL+"releases.txt"] = []byte(releases.String())
	d.files[quicklispDistURL+"systems.txt"] = []byte(systems)
	oldFetch, oldR, oldS := quicklispFetch, quicklispReleasesSHA256, quicklispSystemsSHA256
	rs, ss := sha256.Sum256(d.files[quicklispDistURL+"releases.txt"]), sha256.Sum256(d.files[quicklispDistURL+"systems.txt"])
	quicklispReleasesSHA256, quicklispSystemsSHA256 = hex.EncodeToString(rs[:]), hex.EncodeToString(ss[:])
	quicklispFetch = func(_ context.Context, url string, _ int64) ([]byte, error) {
		d.fetched = append(d.fetched, url)
		if data, ok := d.files[url]; ok {
			return data, nil
		}
		return nil, fmt.Errorf("%s: HTTP 404", url)
	}
	t.Cleanup(func() { quicklispFetch, quicklispReleasesSHA256, quicklispSystemsSHA256 = oldFetch, oldR, oldS })
	return d
}

const fakeSystems = "# project system-file system-name dep...\nalpha alpha alpha beta sb-posix asdf\nbeta beta beta\nbeta beta beta/extra beta\n"

func fakeArchives(t *testing.T) map[string][]byte {
	return map[string][]byte{
		"alpha": tgz(t, map[string]string{"alpha-1/alpha.asd": "(defsystem alpha)"}),
		"beta":  tgz(t, map[string]string{"beta-1/beta.asd": "(defsystem beta)"}),
	}
}

func TestResolveQuicklispFetchesTheClosureAndReusesTheCache(t *testing.T) {
	d := installFakeDist(t, fakeArchives(t), fakeSystems)
	cache := t.TempDir()
	roots, err := ResolveQuicklisp(context.Background(), cache, []string{"alpha"})
	if err != nil {
		t.Fatal(err)
	}
	if len(roots) != 2 {
		t.Fatalf("roots = %v, want alpha and beta", roots)
	}
	for _, root := range roots {
		if matches, _ := filepath.Glob(filepath.Join(root, "*.asd")); len(matches) != 1 {
			t.Fatalf("%s holds no .asd", root)
		}
	}
	record, err := os.ReadFile(filepath.Join(cache, "quicklisp", quicklispDist, "resolved.json"))
	if err != nil || !strings.Contains(string(record), "alpha ") || !strings.Contains(string(record), "beta ") {
		t.Fatalf("resolved set record = %q, %v", record, err)
	}
	before := len(d.fetched)
	if _, err := ResolveQuicklisp(context.Background(), cache, []string{"alpha"}); err != nil {
		t.Fatal(err)
	}
	if len(d.fetched) != before {
		t.Fatalf("a warm cache fetched %v", d.fetched[before:])
	}
}

func TestResolveQuicklispFailsClosed(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(d *fakeDist, t *testing.T)
		names   []string
		offline bool
		want    string
	}{
		{name: "unknown system", names: []string{"gamma"}, want: `system "gamma" is not in the Quicklisp dist`},
		{name: "archive md5", names: []string{"beta"}, want: "md5", mutate: func(d *fakeDist, t *testing.T) {
			data := d.files["https://example.test/beta.tgz"]
			d.files[quicklispDistURL+"releases.txt"] = []byte(fmt.Sprintf("beta http://example.test/beta.tgz %d %s 0 beta-1\n", len(data), strings.Repeat("0", 32)))
			rs := sha256.Sum256(d.files[quicklispDistURL+"releases.txt"])
			quicklispReleasesSHA256 = hex.EncodeToString(rs[:])
		}},
		{name: "offline with an empty cache", names: []string{"beta"}, offline: true, want: "PIG_OFFLINE"},
		{name: "entry outside the release", names: []string{"beta"}, want: "outside the release directory", mutate: func(d *fakeDist, t *testing.T) {
			data := tgz(t, map[string]string{"evil/escape.asd": "x"})
			sum := md5.Sum(data) //nolint:gosec // see the import
			d.files[quicklispDistURL+"releases.txt"] = []byte(fmt.Sprintf("beta http://example.test/beta.tgz %d %s 0 beta-1\n", len(data), hex.EncodeToString(sum[:])))
			d.files["https://example.test/beta.tgz"] = data
			rs := sha256.Sum256(d.files[quicklispDistURL+"releases.txt"])
			quicklispReleasesSHA256 = hex.EncodeToString(rs[:])
		}},
		{name: "symlink", names: []string{"beta"}, want: "unsupported type", mutate: func(d *fakeDist, t *testing.T) {
			data := tgz(t, nil, tar.Header{Name: "beta-1/link", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"})
			sum := md5.Sum(data) //nolint:gosec // see the import
			d.files[quicklispDistURL+"releases.txt"] = []byte(fmt.Sprintf("beta http://example.test/beta.tgz %d %s 0 beta-1\n", len(data), hex.EncodeToString(sum[:])))
			d.files["https://example.test/beta.tgz"] = data
			rs := sha256.Sum256(d.files[quicklispDistURL+"releases.txt"])
			quicklispReleasesSHA256 = hex.EncodeToString(rs[:])
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := installFakeDist(t, fakeArchives(t), fakeSystems)
			if tc.mutate != nil {
				tc.mutate(d, t)
			}
			if tc.offline {
				t.Setenv("PIG_OFFLINE", "1")
			}
			_, err := ResolveQuicklisp(context.Background(), t.TempDir(), tc.names)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestResolveQuicklispRejectsAChangedDistIndex(t *testing.T) {
	d := installFakeDist(t, fakeArchives(t), fakeSystems)
	d.files[quicklispDistURL+"systems.txt"] = []byte(fakeSystems + "evil evil evil\n")
	if _, err := ResolveQuicklisp(context.Background(), t.TempDir(), []string{"alpha"}); err == nil || !strings.Contains(err.Error(), "pinned") {
		t.Fatalf("error = %v, want a pinned-digest failure", err)
	}
}

func TestResolveQuicklispRefusesARecordedDigestChange(t *testing.T) {
	d := installFakeDist(t, fakeArchives(t), fakeSystems)
	cache := t.TempDir()
	if _, err := ResolveQuicklisp(context.Background(), cache, []string{"beta"}); err != nil {
		t.Fatal(err)
	}
	// the cache entry is lost; the archive now unpacks to different bytes with the same listed size and md5 is impossible, so corrupt the record instead
	record := filepath.Join(cache, "quicklisp", quicklispDist, "resolved.json")
	data, _ := os.ReadFile(record)
	data = bytes.ReplaceAll(data, []byte(`": "`), []byte(`": "0000`))
	_ = os.WriteFile(record, data, 0o644)
	_ = os.RemoveAll(filepath.Join(cache, "quicklisp", quicklispDist, "src"))
	if _, err := ResolveQuicklisp(context.Background(), cache, []string{"beta"}); err == nil || !strings.Contains(err.Error(), "was recorded for it") {
		t.Fatalf("error = %v, want a recorded-digest failure", err)
	}
	_ = d
}

func TestQuicklispClosureOverThePinnedDistShape(t *testing.T) {
	index, err := parseQuicklispIndex([]byte("beta http://x/b.tgz 1 m 0 beta-1\n"), []byte(fakeSystems))
	if err != nil {
		t.Fatal(err)
	}
	projects, err := index.closure([]string{"ALPHA", "beta/extra"})
	if err != nil || strings.Join(projects, ",") != "alpha,beta" {
		t.Fatalf("closure = %v, %v", projects, err)
	}
}
