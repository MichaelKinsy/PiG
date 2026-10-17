package bunruntime

import (
	"archive/zip"
	"bytes"
	"crypto/ed25519"
	"crypto/sha1" //nolint:gosec // OpenPGP v4 fingerprints are SHA-1 by definition (RFC 4880 section 12.2); the test recomputes the published fingerprint.
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// Bun's real signed checksums for 1.4.2 verify with the pinned key, and the signed list holds every pinned archive digest.
func TestBunReleaseSignatureVerifiesWithThePinnedKey(t *testing.T) {
	armored, err := os.ReadFile(filepath.Join("testdata", "bun-v1.4.2-SHASUMS256.txt.asc"))
	if err != nil {
		t.Fatal(err)
	}
	signed, err := verifyClearsigned(armored, signingKey, signingFingerprint)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range assets {
		if digest, err := signedDigest(signed, a.name+".zip"); err != nil || digest != a.zipSHA {
			t.Errorf("%s: signed digest %q (%v), pinned %s", a.name, digest, err, a.zipSHA)
		}
	}
	// A changed digest, a changed file name, a dropped line or another key fails.
	tampered := map[string][]byte{
		"digest": bytes.Replace(armored, []byte("36368faef7527875d5ffa52e53cd48021741f2a83eb6208a8dd64068d422a913"), []byte("36368faef7527875d5ffa52e53cd48021741f2a83eb6208a8dd64068d422a914"), 1),
		"name":   bytes.Replace(armored, []byte("  bun-linux-x64.zip"), []byte("  bun-linux-x65.zip"), 1),
		"line":   bytes.Replace(armored, []byte("ce4c17497b2f29712a99d3d53f028de28cd42e3bacb8589599e7f000e49b6405  bun-windows-x64.zip\n"), nil, 1),
	}
	for name, data := range tampered {
		if bytes.Equal(data, armored) {
			t.Fatalf("%s: tampering changed nothing", name)
		}
		if _, err := verifyClearsigned(data, signingKey, signingFingerprint); err == nil {
			t.Errorf("%s: tampered checksums verified", name)
		}
	}
	other, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := verifyClearsigned(armored, other, signingFingerprint); err == nil {
		t.Error("another key verified the signature")
	}
	if _, err := verifyClearsigned(armored, signingKey, bytes.Repeat([]byte{1}, 20)); err == nil {
		t.Error("another issuer fingerprint verified the signature")
	}
}

// The pinned key is Robobun's published OpenPGP primary key: its key packet holds the pinned Ed25519 point and hashes to the pinned
// fingerprint.
func TestPinnedKeyIsRobobunsPublishedKey(t *testing.T) {
	armored, err := os.ReadFile(filepath.Join("testdata", "robobun-key.asc"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(armored)
	_, body, _ := strings.Cut(text, "\n\n")
	body, _, _ = strings.Cut(body, "\n=")
	data, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(body, "\n", ""))
	if err != nil {
		t.Fatal(err)
	}
	if data[0] != 0xc6 {
		t.Fatalf("first packet header %#x is not a new-format public key packet", data[0])
	}
	packet := data[2 : 2+int(data[1])]
	fingerprint := sha1.Sum(append([]byte{0x99, 0, byte(len(packet))}, packet...)) //nolint:gosec // see the import: the v4 fingerprint definition
	if !bytes.Equal(fingerprint[:], signingFingerprint) {
		t.Fatalf("fingerprint %x, pinned %x", fingerprint, signingFingerprint)
	}
	if !bytes.HasSuffix(packet, append([]byte{0x40}, signingKey...)) {
		t.Fatalf("key packet %x does not hold the pinned point %x", packet, []byte(signingKey))
	}
}

// Every pinned asset is one of PiG's release targets, and each target has an archive.
func TestAssetsCoverPigReleaseTargets(t *testing.T) {
	targets := []string{"linux-x64", "linux-arm64", "darwin-x64", "darwin-arm64", "android-x64", "android-arm64", "windows-x64", "windows-arm64"}
	for _, target := range targets {
		if _, ok := assetFor(assets, target, false); !ok {
			t.Errorf("no archive for %s", target)
		}
	}
	for _, a := range assets {
		if !slices.Contains(targets, a.platform) || len(a.zipSHA) != 64 || len(a.exeSHA) != 64 {
			t.Errorf("asset %+v", a)
		}
	}
}

// clearsign signs text as Bun's release does: a version 4 canonical-text EdDSA SHA-512 signature with the issuer fingerprint.
func clearsign(t *testing.T, text string, key ed25519.PrivateKey, fingerprint []byte) []byte {
	t.Helper()
	hashed := append([]byte{byte(2 + len(fingerprint)), subpacketIssuerFpr, 4}, fingerprint...)
	hashed = append(hashed, 5, 2, 0x65, 0, 0, 0)
	header := []byte{4, sigTypeCanonicalText, pubkeyAlgoEdDSA, hashAlgoSHA512, 0, byte(len(hashed))}
	header = append(header, hashed...)
	hash := sha512.New()
	hash.Write([]byte(canonicalText(text)))
	hash.Write(header)
	trailer := []byte{4, 0xff, 0, 0, 0, 0}
	binary.BigEndian.PutUint32(trailer[2:], uint32(len(header)))
	hash.Write(trailer)
	digest := hash.Sum(nil)
	sig := ed25519.Sign(key, digest)
	mpi := func(b []byte) []byte {
		b = bytes.TrimLeft(b, "\x00")
		bits := len(b) * 8
		if len(b) > 0 {
			for mask := byte(0x80); b[0]&mask == 0; mask >>= 1 {
				bits--
			}
		}
		return append([]byte{byte(bits >> 8), byte(bits)}, b...)
	}
	body := slices.Concat(header, []byte{0, 0, digest[0], digest[1]})
	body = append(body, mpi(sig[:32])...)
	body = append(body, mpi(sig[32:])...)
	packet := append([]byte{0xc2, byte(len(body))}, body...)
	crc := crc24(packet)
	var escaped []string
	for line := range strings.SplitSeq(text, "\n") {
		if strings.HasPrefix(line, "-") {
			line = "- " + line
		}
		escaped = append(escaped, line)
	}
	return fmt.Appendf(nil, "-----BEGIN PGP SIGNED MESSAGE-----\nHash: SHA512\n\n%s\n-----BEGIN PGP SIGNATURE-----\n\n%s\n=%s\n-----END PGP SIGNATURE-----\n",
		strings.Join(escaped, "\n"), base64.StdEncoding.EncodeToString(packet), base64.StdEncoding.EncodeToString([]byte{byte(crc >> 16), byte(crc >> 8), byte(crc)}))
}

func TestVerifyClearsignedHandlesDashEscapesAndTrailingWhitespace(t *testing.T) {
	public, private, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	fingerprint := bytes.Repeat([]byte{7}, 20)
	text := "-----leading dashes\ntrailing spaces   \n  bun.zip"
	got, err := verifyClearsigned(clearsign(t, text, private, fingerprint), public, fingerprint)
	if err != nil || got != text {
		t.Fatalf("verify = %q, %v", got, err)
	}
}

type release struct {
	key      ed25519.PrivateKey
	public   ed25519.PublicKey
	fpr      []byte
	exe      []byte
	archive  []byte
	asset    asset
	checksum []byte
}

// fakeRelease builds a signed release of one archive whose executable is a script that prints Bun's version.
func fakeRelease(t *testing.T, platform string) *release {
	t.Helper()
	public, private, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	r := &release{key: private, public: public, fpr: bytes.Repeat([]byte{9}, 20), exe: []byte("#!/bin/sh\necho " + bunVersion + "\n")}
	var archive bytes.Buffer
	zw := zip.NewWriter(&archive)
	w, err := zw.Create("bun-test/bun")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(r.exe); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	r.archive = archive.Bytes()
	zipSum, exeSum := sha256.Sum256(r.archive), sha256.Sum256(r.exe)
	r.asset = asset{name: "bun-test", zipSHA: hex.EncodeToString(zipSum[:]), exeSHA: hex.EncodeToString(exeSum[:]), exeName: "bun", platform: platform}
	r.checksum = clearsign(t, r.asset.zipSHA+"  bun-test.zip\nffff  bun-other.zip", private, r.fpr)
	return r
}

func (r *release) serve(t *testing.T) *httptest.Server {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case "/SHASUMS256.txt.asc":
			_, _ = w.Write(r.checksum)
		case "/bun-test.zip":
			_, _ = w.Write(r.archive)
		default:
			http.NotFound(w, req)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func (r *release) installer(root, baseURL string) installer {
	return installer{root: root, platform: r.asset.platform, baseURL: baseURL, client: http.DefaultClient, key: r.public, fingerprint: r.fpr, assets: []asset{r.asset}}
}

// install writes <root>/bun with the verified executable and a runtime.json in PiG's slot shape; status and verify accept it, and
// remove deletes it.
func TestInstallVerifyRemove(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake Bun is a shell script")
	}
	r := fakeRelease(t, "linux-x64")
	root := t.TempDir()
	in := r.installer(root, r.serve(t).URL)

	message, err := runAction(t.Context(), "install", root, "linux-x64", in)
	if err != nil || !strings.Contains(message, "Installed Bun "+bunVersion) {
		t.Fatalf("install = %q, %v", message, err)
	}
	data, err := os.ReadFile(filepath.Join(root, "bun", "runtime.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"id": "bun", "kind": "bun", "version": bunVersion, "platform": "linux-x64", "provider": provider, "image": map[string]any{"executable": "bun", "sha256": r.asset.exeSHA}}
	if fmt.Sprint(fields) != fmt.Sprint(want) {
		t.Fatalf("runtime.json = %v, want %v", fields, want)
	}
	if entries, _ := os.ReadDir(root); len(entries) != 1 {
		t.Fatalf("slot root holds %d entries, want only bun", len(entries))
	}
	if message, err := runAction(t.Context(), "verify", root, "linux-x64", in); err != nil || !strings.Contains(message, "matches its pinned digest") {
		t.Fatalf("verify = %q, %v", message, err)
	}
	if message, err := runAction(t.Context(), "", root, "linux-x64", in); err != nil || !strings.Contains(message, "Bun runtime: Bun "+bunVersion) {
		t.Fatalf("status = %q, %v", message, err)
	}

	// A modified executable fails verify.
	if err := os.WriteFile(filepath.Join(root, "bun", "bun"), []byte("#!/bin/sh\necho 1.4.2 \n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := runAction(t.Context(), "verify", root, "linux-x64", in); err == nil || !strings.Contains(err.Error(), "SHA-256") {
		t.Fatalf("verify of a modified executable = %v", err)
	}
	// A second install replaces it.
	if _, err := runAction(t.Context(), "install", root, "linux-x64", in); err != nil {
		t.Fatal(err)
	}
	if _, err := runAction(t.Context(), "verify", root, "linux-x64", in); err != nil {
		t.Fatalf("verify after reinstall = %v", err)
	}

	if message, err := runAction(t.Context(), "remove", root, "linux-x64", in); err != nil || !strings.Contains(message, "Removed") {
		t.Fatalf("remove = %q, %v", message, err)
	}
	if _, err := os.Stat(filepath.Join(root, "bun")); !os.IsNotExist(err) {
		t.Fatalf("bun remains after remove: %v", err)
	}
	if message, _ := runAction(t.Context(), "remove", root, "linux-x64", in); message != "Bun runtime: nothing to remove." {
		t.Fatalf("second remove = %q", message)
	}
	if _, err := runAction(t.Context(), "upgrade", root, "linux-x64", in); err == nil || err.Error() != usage {
		t.Fatalf("unknown action = %v", err)
	}
}

// An install that fails verification leaves no runtime in the slot.
func TestInstallRejectsUntrustedReleases(t *testing.T) {
	cases := map[string]func(r *release){
		"archive differs from pin": func(r *release) { r.archive = append(r.archive, 0) },
		"signature by another key": func(r *release) {
			_, other, _ := ed25519.GenerateKey(nil)
			r.checksum = clearsign(t, r.asset.zipSHA+"  bun-test.zip", other, r.fpr)
		},
		"signed digest differs":        func(r *release) { r.checksum = clearsign(t, strings.Repeat("0", 64)+"  bun-test.zip", r.key, r.fpr) },
		"archive not in signed list":   func(r *release) { r.checksum = clearsign(t, r.asset.zipSHA+"  bun-other.zip", r.key, r.fpr) },
		"executable differs from pin":  func(r *release) { r.asset.exeSHA = strings.Repeat("1", 64) },
		"no archive for this platform": func(r *release) { r.asset.platform = "plan9-x64" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			r := fakeRelease(t, "linux-x64")
			pinned := r.asset
			mutate(r)
			if name == "executable differs from pin" || name == "no archive for this platform" {
				pinned = r.asset
			}
			root := t.TempDir()
			in := r.installer(root, r.serve(t).URL)
			in.assets = []asset{pinned}
			in.platform = "linux-x64"
			if _, err := in.install(t.Context()); err == nil {
				t.Fatal("install succeeded")
			}
			if entries, _ := os.ReadDir(root); len(entries) != 0 {
				t.Fatalf("a failed install left %d entries in the slot", len(entries))
			}
		})
	}
}

func TestSlotRootFollowsPigsRule(t *testing.T) {
	t.Setenv("PIG_TS_RUNTIMES_DIR", "")
	root, err := slotRoot(func() (string, error) { return "/agent", nil })
	if err != nil || root != filepath.Join("/agent", "runtimes", "typescript") {
		t.Fatalf("slotRoot = %q, %v", root, err)
	}
	t.Setenv("PIG_TS_RUNTIMES_DIR", "/elsewhere")
	if root, _ := slotRoot(func() (string, error) { return "/agent", nil }); root != "/elsewhere" {
		t.Fatalf("slotRoot with PIG_TS_RUNTIMES_DIR = %q", root)
	}
}

// PiG runs a Bun executable only when its digest is in PiG's runtime lock, so every executable this Piglet installs must be in that lock
// for its platform and version, and the lock must pin nothing this Piglet does not install. The Piglet imports only the Go SDK, so the
// test reads the lock's source.
func TestAssetsEqualPigsRuntimeLock(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "coding", "extension", "host", "tsruntime", "lock.go"))
	if err != nil {
		t.Fatal(err)
	}
	entry := regexp.MustCompile(`\{Runtime: "bun", Version: "([^"]+)", Platform: "([^"]+)", Variant: "([^"]+)", SHA256: "([0-9a-f]{64})"\}`)
	var lock []string
	for _, m := range entry.FindAllSubmatch(source, -1) {
		lock = append(lock, strings.Join([]string{string(m[1]), string(m[2]), string(m[3]), string(m[4])}, " "))
	}
	var pinned []string
	for _, a := range assets {
		pinned = append(pinned, strings.Join([]string{bunVersion, a.platform, a.name, a.exeSHA}, " "))
	}
	slices.Sort(lock)
	slices.Sort(pinned)
	if len(lock) == 0 || !slices.Equal(lock, pinned) {
		t.Fatalf("PiG's runtime lock pins\n%s\nthis Piglet installs\n%s", strings.Join(lock, "\n"), strings.Join(pinned, "\n"))
	}
}
