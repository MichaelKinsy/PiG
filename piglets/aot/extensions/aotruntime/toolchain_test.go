package aotruntime

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Pi-Bolt's real release signature verifies with the key pinned in the Piglet, the pinned key is the one the release repository publishes, and the
// signed list holds every pinned archive digest (issue B10: the key is pinned, not fetched from the release it verifies).
func TestPinnedReleaseKeyVerifiesTheRealSignedChecksums(t *testing.T) {
	sums, err := os.ReadFile(filepath.Join("testdata", "pi-bolt-v0.7.4-SHA256SUMS"))
	if err != nil {
		t.Fatal(err)
	}
	signature, err := os.ReadFile(filepath.Join("testdata", "pi-bolt-v0.7.4-SHA256SUMS.sig"))
	if err != nil {
		t.Fatal(err)
	}
	if !ed25519.Verify(releaseKey, sums, signature) {
		t.Fatal("the pinned key does not verify the release's SHA256SUMS.sig")
	}
	for _, a := range archives {
		if digest, err := signedDigest(string(sums), a.name); err != nil || digest != a.sha256 {
			t.Errorf("%s: signed digest %q (%v), pinned %s", a.name, digest, err, a.sha256)
		}
	}
	tampered := bytes.Replace(sums, []byte("43b5f41a07a8"), []byte("43b5f41a07a9"), 1)
	if bytes.Equal(tampered, sums) || ed25519.Verify(releaseKey, tampered, signature) {
		t.Error("a changed checksum list verifies")
	}

	// The pinned raw key is the key of the PEM file the release repository publishes (sha256 ca62e435... at commit 6088a1fe).
	pemData, err := os.ReadFile(filepath.Join("testdata", "pi-bolt-release.pub"))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(pemData)
	if got := hex.EncodeToString(sum[:]); got != "ca62e43535443d2bb7235c954d565ec843dabf58a457e4bfaf27de5df4594a51" {
		t.Fatalf("release.pub digest %s", got)
	}
	block, _ := pem.Decode(pemData)
	if block == nil || !bytes.HasSuffix(block.Bytes, releaseKey) {
		t.Error("the pinned key is not the key of release.pub")
	}
}

// release is a fake Pi-Bolt release served over HTTP: a signed checksum list, its signature and a runtime archive.
type release struct {
	key       ed25519.PublicKey
	pinned    []archive
	server    *httptest.Server
	requested []string
}

type releaseOptions struct {
	archiveFiles map[string][]byte
	// tamper adjusts what the server returns.
	signWithOtherKey bool
	listWrongDigest  bool
	archiveBytes     func([]byte) []byte
	status           map[string]int
}

func newRelease(t *testing.T, platform string, o releaseOptions) *release {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	files := o.archiveFiles
	if files == nil {
		files = map[string][]byte{"bun": []byte("#!/bin/sh\necho 1.4.3\n"), "LICENSE": []byte("MIT"), "LICENSE.pi": []byte("MIT"), "THIRD_PARTY_NOTICES.md": []byte("notices")}
	}
	archiveData := tarball(t, "pi-bolt-runtime-"+platform, files)
	archiveSum := sha256.Sum256(archiveData)
	exeSum := sha256.Sum256(files["bun"])
	name := "pi-bolt-runtime-" + platform + ".tar.gz"
	pinned := []archive{{platform: platform, name: name, sha256: hex.EncodeToString(archiveSum[:]), exeSHA: hex.EncodeToString(exeSum[:])}}
	listed := pinned[0].sha256
	if o.listWrongDigest {
		listed = strings.Repeat("0", 64)
	}
	sums := []byte(listed + "  " + name + "\n")
	signer := priv
	if o.signWithOtherKey {
		_, signer, _ = ed25519.GenerateKey(rand.Reader)
	}
	signature := ed25519.Sign(signer, sums)
	served := archiveData
	if o.archiveBytes != nil {
		served = o.archiveBytes(archiveData)
	}
	r := &release{key: pub, pinned: pinned}
	r.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		file := strings.TrimPrefix(req.URL.Path, "/")
		r.requested = append(r.requested, file)
		if status := o.status[file]; status != 0 {
			http.Error(w, "no", status)
			return
		}
		switch file {
		case checksumsFile:
			_, _ = w.Write(sums)
		case signatureFile:
			_, _ = w.Write(signature)
		case name:
			_, _ = w.Write(served)
		default:
			http.NotFound(w, req)
		}
	}))
	t.Cleanup(r.server.Close)
	return r
}

func tarball(t *testing.T, dir string, files map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	for name, data := range files {
		if err := tw.WriteHeader(&tar.Header{Name: dir + "/" + name, Mode: 0o755, Size: int64(len(data)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := errJoin(tw.Close(), zw.Close()); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func errJoin(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

func (r *release) installer(dir, platform string) installer {
	return installer{providerDir: dir, platform: platform, baseURL: r.server.URL, client: r.server.Client(), key: r.key, archives: r.pinned}
}

func TestInstallVerifiesSignatureDigestsAndPublishesAtomically(t *testing.T) {
	dir := t.TempDir()
	r := newRelease(t, "linux-x64", releaseOptions{})
	exe, err := r.installer(dir, "linux-x64").install(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(toolchainDir(dir, "linux-x64"), "bun"); exe != want {
		t.Fatalf("exe = %s, want %s", exe, want)
	}
	for _, name := range []string{"bun", "LICENSE", "LICENSE.pi", "THIRD_PARTY_NOTICES.md", "toolchain.json"} {
		if _, err := os.Stat(filepath.Join(toolchainDir(dir, "linux-x64"), name)); err != nil {
			t.Errorf("the toolchain lacks %s: %v", name, err)
		}
	}
	info, _ := os.Stat(exe)
	if info.Mode().Perm()&0o100 == 0 {
		t.Errorf("the compiler is not executable: %v", info.Mode())
	}
	entries, _ := os.ReadDir(filepath.Join(dir, "toolchain"))
	if len(entries) != 1 {
		t.Errorf("toolchain/ holds %d entries after install, want only the published directory", len(entries))
	}
}

func TestInstallRefusesEveryTamperedRelease(t *testing.T) {
	cases := map[string]releaseOptions{
		"signature by another key":      {signWithOtherKey: true},
		"signed list names another sum": {listWrongDigest: true},
		"archive bytes changed":         {archiveBytes: func(b []byte) []byte { return append(append([]byte(nil), b...), 0) }},
		"checksum list missing":         {status: map[string]int{checksumsFile: http.StatusNotFound}},
		"signature missing":             {status: map[string]int{signatureFile: http.StatusNotFound}},
		"compiler of another digest":    {},
	}
	for name, options := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			r := newRelease(t, "linux-x64", options)
			if name == "compiler of another digest" {
				r.pinned[0].exeSHA = strings.Repeat("1", 64)
			}
			if _, err := r.installer(dir, "linux-x64").install(context.Background()); err == nil {
				t.Fatal("install succeeded")
			}
			if _, err := os.Stat(toolchainDir(dir, "linux-x64")); err == nil {
				t.Error("a failed install left a toolchain directory")
			}
			if entries, _ := os.ReadDir(filepath.Join(dir, "toolchain")); len(entries) != 0 {
				t.Errorf("a failed install left %d staging entries", len(entries))
			}
		})
	}
}

func TestInstallRejectsAnArchiveWithAnUnexpectedEntry(t *testing.T) {
	dir := t.TempDir()
	files := map[string][]byte{"bun": []byte("x"), "../evil": []byte("y")}
	r := newRelease(t, "linux-x64", releaseOptions{archiveFiles: files})
	if _, err := r.installer(dir, "linux-x64").install(context.Background()); err == nil || !strings.Contains(err.Error(), "unexpected entry") {
		t.Fatalf("install = %v, want an unexpected entry error", err)
	}
}

func TestInstallOnAnUnsupportedPlatformDownloadsNothing(t *testing.T) {
	r := newRelease(t, "linux-x64", releaseOptions{})
	if _, err := r.installer(t.TempDir(), "windows-x64").install(context.Background()); err == nil {
		t.Fatal("install succeeded on windows-x64")
	}
	if len(r.requested) != 0 {
		t.Errorf("an unsupported platform made requests: %v", r.requested)
	}
}

func TestVerifyToolchainChecksDigestAndRecord(t *testing.T) {
	dir := t.TempDir()
	r := newRelease(t, "linux-x64", releaseOptions{})
	if _, err := r.installer(dir, "linux-x64").install(context.Background()); err != nil {
		t.Fatal(err)
	}
	// The fake archive's pin names toolchainVersion's record; verify against the fake pins.
	if _, err := verifyToolchain(context.Background(), dir, "linux-x64", r.pinned); err != nil {
		t.Fatalf("verify: %v", err)
	}
	exe := filepath.Join(toolchainDir(dir, "linux-x64"), "bun")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\necho changed\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := verifyToolchain(context.Background(), dir, "linux-x64", r.pinned); err == nil || !strings.Contains(err.Error(), "SHA-256") {
		t.Fatalf("verify after a change = %v, want a digest error", err)
	}
	if _, err := verifyToolchain(context.Background(), t.TempDir(), "linux-x64", r.pinned); err == nil {
		t.Fatal("verify of a missing toolchain succeeded")
	}
}
