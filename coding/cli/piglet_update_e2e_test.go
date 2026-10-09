package cli

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/piglet"
	pigletrelease "github.com/MichaelKinsy/PiG/coding/piglet/release"
	"github.com/MichaelKinsy/PiG/coding/piglet/signature"
)

// pigletReleaseServer serves a GitHub-shaped release tree for acme/porter: the
// releases API page and each version's signed index and Binary.
type pigletReleaseServer struct {
	mu       sync.Mutex
	tags     []string
	assets   map[string][]byte // path under /acme/porter/releases/download/
	requests []string
}

func (s *pigletReleaseServer) set(path string, data []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.assets[path] = data
}

func (s *pigletReleaseServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = append(s.requests, r.URL.Path)
	if r.URL.Path == "/repos/acme/porter/releases" {
		releases := make([]map[string]any, 0, len(s.tags))
		for _, tag := range s.tags {
			releases = append(releases, map[string]any{"tag_name": tag, "draft": false, "prerelease": false})
		}
		_ = json.NewEncoder(w).Encode(releases)
		return
	}
	if data, ok := s.assets[strings.TrimPrefix(r.URL.Path, "/acme/porter/releases/download/")]; ok {
		_, _ = w.Write(data)
		return
	}
	http.NotFound(w, r)
}

// signedPigletRelease returns a signed Binary and its signed index for version.
func signedPigletRelease(t *testing.T, key ed25519.PrivateKey, version, target string) (index, binary []byte) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "pig-porter")
	if err := os.WriteFile(path, []byte("porter executable "+version+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	digest := "sha256:" + strings.Repeat("a", 64)
	if _, err := signature.Sign(path, signature.Manifest{
		Piglet: "porter", ReleaseVersion: version, Target: target, PigVersion: "pig-test",
		PigletDigest: digest, SourceDigest: digest, ResolutionDigest: digest, ComponentPlanDigest: digest,
	}, key); err != nil {
		t.Fatal(err)
	}
	binary, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(binary)
	index, err = pigletrelease.Sign(pigletrelease.Index{
		Piglet: "porter", Version: version, PigVersion: "pig-test", SourceRef: "npm:porter@" + version,
		GitHub:   &pigletrelease.GitHubRelease{Repository: "acme/porter"},
		Binaries: map[string]pigletrelease.Binary{target: {URL: "pig-porter", SHA256: hex.EncodeToString(sum[:]), Size: int64(len(binary))}},
	}, key)
	if err != nil {
		t.Fatal(err)
	}
	return index, binary
}

// alterSignedPayload changes the signed index payload and keeps the old signature.
func alterSignedPayload(t *testing.T, envelope []byte) []byte {
	t.Helper()
	var decoded map[string]any
	if err := json.Unmarshal(envelope, &decoded); err != nil {
		t.Fatal(err)
	}
	payload, err := base64.StdEncoding.DecodeString(decoded["payload"].(string))
	if err != nil {
		t.Fatal(err)
	}
	altered := strings.Replace(string(payload), `"pigVersion":"pig-test"`, `"pigVersion":"pig-evil"`, 1)
	if altered == string(payload) {
		t.Fatal("payload unchanged")
	}
	decoded["payload"] = base64.StdEncoding.EncodeToString([]byte(altered))
	data, err := json.Marshal(decoded)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// snapshotTree returns every entry under root: the SHA-256 of a regular file, the target of a symlink, and a marker for a directory or any other entry, so an added empty directory or lock file also changes the snapshot.
func snapshotTree(t *testing.T, root string) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		switch {
		case entry.IsDir():
			files[path] = "dir"
			return nil
		case entry.Type()&fs.ModeSymlink != 0:
			target, err := os.Readlink(path)
			files[path] = "symlink " + target
			return err
		case !entry.Type().IsRegular():
			files[path] = entry.Type().String()
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		files[path] = hex.EncodeToString(sum[:])
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

// loopbackOnlyTransport sends a request only to host and refuses every other request.
type loopbackOnlyTransport struct {
	host    string
	next    http.RoundTripper
	escaped func(string)
}

func (l loopbackOnlyTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.URL.Host != l.host {
		l.escaped(request.URL.String())
		return nil, fmt.Errorf("test transport refuses %s", request.URL)
	}
	return l.next.RoundTrip(request)
}

func runPiglet(args ...string) (int, string, string) {
	var stdout, stderr strings.Builder
	code := piglet.RunCommand(append([]string{"piglet"}, args...), &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

// TestPigletUpdateEndToEndThroughLoopbackGitHub runs the real pull and update
// commands against a loopback GitHub release server.
func TestPigletUpdateEndToEndThroughLoopbackGitHub(t *testing.T) {
	home := shortTempDir(t)
	t.Setenv("PIG_HOME", home)
	t.Setenv("PIG_OFFLINE", "")
	t.Setenv("PI_OFFLINE", "")
	t.Setenv("PIG_PIGLET_PULL_ALLOW_LOOPBACK_HTTP", "1")
	t.Chdir(t.TempDir())
	const target = "testos/testarch"
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	_, otherKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	index100, binary100 := signedPigletRelease(t, key, "1.0.0", target)
	index110, binary110 := signedPigletRelease(t, key, "1.1.0", target)
	releases := &pigletReleaseServer{tags: []string{"v1.0.0", "v1.1.0", "other/v9.0.0"}, assets: map[string][]byte{
		"v1.0.0/piglet-release.json": index100, "v1.0.0/pig-porter": binary100,
		"v1.1.0/piglet-release.json": index110, "v1.1.0/pig-porter": binary110,
	}}
	server := httptest.NewServer(releases)
	defer server.Close()
	t.Setenv(pigletrelease.GitHubURLEnv, server.URL)
	// The commands build their HTTP client without a Transport, so every request goes through http.DefaultTransport. The guard refuses and records any request for another host; a proxy variable would not do, because net/http reads the proxy environment once per process.
	var escapedMu sync.Mutex
	var escaped []string
	defaultTransport := http.DefaultTransport
	http.DefaultTransport = loopbackOnlyTransport{host: strings.TrimPrefix(server.URL, "http://"), next: defaultTransport, escaped: func(url string) {
		escapedMu.Lock()
		defer escapedMu.Unlock()
		escaped = append(escaped, url)
	}}
	t.Cleanup(func() {
		http.DefaultTransport = defaultTransport
		escapedMu.Lock()
		defer escapedMu.Unlock()
		if len(escaped) != 0 {
			t.Errorf("requests left the loopback release server: %v", escaped)
		}
	})

	if code, stdout, stderr := runPiglet("pull", "github:acme/porter@1.0.0", "--target", target, "--no-input"); code != 0 || !strings.Contains(stdout, "Pulled porter 1.0.0") {
		t.Fatalf("pull code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	installed := snapshotTree(t, home)
	var current string
	for path := range installed {
		if strings.HasSuffix(path, string(filepath.Separator)+filepath.Join("porter", "current")) {
			current = path
		}
	}
	if pointer, err := os.ReadFile(current); err != nil || !strings.Contains(string(pointer), "1.0.0") {
		t.Fatalf("current pointer %q = %s: %v (files %v)", current, pointer, err, installed)
	}

	refused := []struct {
		name, want string
		env        map[string]string
		args       []string
		index      []byte
		binary     []byte
	}{
		{name: "offline", want: "cannot update Piglet while offline", env: map[string]string{"PIG_OFFLINE": "1"}},
		{name: "rollback", want: "refuses rollback from 1.0.0 to 0.9.0", args: []string{"--version", "0.9.0"}},
		{name: "wrong checksum", want: "downloaded Piglet Binary SHA256", binary: append([]byte{binary110[0] ^ 1}, binary110[1:]...)},
		{name: "oversized asset", want: fmt.Sprintf("size is %d, release index says %d", len(binary110)+1, len(binary110)), binary: append(append([]byte{}, binary110...), make([]byte, 4096)...)},
		{name: "altered index", want: "does not verify", index: alterSignedPayload(t, index110)},
		{name: "different signer", want: "--accept-signer", index: func() []byte { index, _ := signedPigletRelease(t, otherKey, "1.1.0", target); return index }()},
		{name: "non-loopback override", want: "must name a loopback host", env: map[string]string{pigletrelease.GitHubURLEnv: "http://example.com"}},
		{name: "override without opt-in", want: "requires PIG_PIGLET_PULL_ALLOW_LOOPBACK_HTTP=1", env: map[string]string{"PIG_PIGLET_PULL_ALLOW_LOOPBACK_HTTP": ""}},
	}
	for _, tc := range refused {
		t.Run(tc.name, func(t *testing.T) {
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			if tc.index != nil {
				releases.set("v1.1.0/piglet-release.json", tc.index)
				defer releases.set("v1.1.0/piglet-release.json", index110)
			}
			if tc.binary != nil {
				releases.set("v1.1.0/pig-porter", tc.binary)
				defer releases.set("v1.1.0/pig-porter", binary110)
			}
			code, stdout, stderr := runPiglet(append([]string{"update", "porter", "--no-input"}, tc.args...)...)
			if code != 1 || !strings.Contains(stderr, tc.want) {
				t.Fatalf("code=%d stdout=%s stderr=%s, want refusal containing %q", code, stdout, stderr, tc.want)
			}
			if after := snapshotTree(t, home); !maps.Equal(after, installed) {
				t.Fatalf("refused update changed installed state:\nbefore %v\nafter  %v", installed, after)
			}
		})
	}

	code, stdout, stderr := runPiglet("update", "porter", "--no-input")
	if code != 0 || stderr != "" || !strings.HasPrefix(stdout, "Current porter 1.1.0 for "+target+", signed by "+signature.KeyID(key.Public().(ed25519.PublicKey))) {
		t.Fatalf("update code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	pointer, err := os.ReadFile(current)
	if err != nil || !strings.Contains(string(pointer), "1.1.0") {
		t.Fatalf("current pointer %s: %v", pointer, err)
	}
	var artifact string
	for line := range strings.SplitSeq(stdout, "\n") {
		if value, ok := strings.CutPrefix(strings.TrimSpace(line), "binary: "); ok {
			artifact = value
		}
	}
	if data, err := os.ReadFile(artifact); err != nil || string(data) != string(binary110) {
		t.Fatalf("installed Binary %s does not match the v1.1.0 asset: %v", artifact, err)
	}
	updated := snapshotTree(t, home)

	releases.mu.Lock()
	releases.requests = nil
	releases.mu.Unlock()
	if code, again, stderr := runPiglet("update", "porter", "--no-input"); code != 0 || again != stdout {
		t.Fatalf("rerun code=%d stdout=%s stderr=%s, want %s", code, again, stderr, stdout)
	}
	if after := snapshotTree(t, home); !maps.Equal(after, updated) {
		t.Fatal("rerun rewrote installed state")
	}
	if got := strings.Join(releases.requests, " "); got != "/repos/acme/porter/releases" {
		t.Fatalf("rerun requests = %q, want discovery only", got)
	}
	if code, _, stderr := runPiglet("update", "porter", "--version", "1.0.0", "--no-input"); code != 1 || !strings.Contains(stderr, "refuses rollback from 1.1.0 to 1.0.0") {
		t.Fatalf("rollback code=%d stderr=%s", code, stderr)
	}
}
