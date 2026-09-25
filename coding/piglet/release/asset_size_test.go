package release

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/piglet/signature"
)

// Publication and pull must agree at the limit of the complete signed file,
// not only at the limit of the executable body before its signature trailer.
func TestVerifyAssetAndDownloadShareCompleteSizeBoundary(t *testing.T) {
	for _, tc := range []struct {
		name string
		size int64
	}{
		{"at limit", maxBinaryBytes},
		{"over limit", maxBinaryBytes + 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path, verified := signedAssetOfSize(t, tc.size)
			err := VerifyAsset(path, verified, testTarget)
			if tc.size == maxBinaryBytes {
				if err != nil {
					t.Fatalf("asset at limit: %v", err)
				}
			} else if err == nil || !strings.Contains(err.Error(), "above the") {
				t.Errorf("asset over limit: %v; want size-limit rejection", err)
			}

			t.Setenv("PIG_PIGLET_PULL_ALLOW_LOOPBACK_HTTP", "1")
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				http.ServeFile(w, r, path)
			}))
			defer server.Close()
			binary := verified.Index.Binaries[testTarget]
			stage, digest, size, err := downloadBinary(context.Background(), server.Client(), server.URL+"/asset", binary)
			if stage != "" {
				defer func() { _ = os.Remove(stage) }()
			}
			if tc.size == maxBinaryBytes {
				if err != nil || size != tc.size || digest != "sha256:"+binary.SHA256 || requests.Load() != 1 {
					t.Fatalf("download at limit: size=%d digest=%s requests=%d err=%v", size, digest, requests.Load(), err)
				}
			} else if err == nil || !strings.Contains(err.Error(), "above the") || stage != "" || requests.Load() != 0 {
				t.Fatalf("download over limit: stage=%s requests=%d err=%v", stage, requests.Load(), err)
			}
		})
	}
}

func signedAssetOfSize(t *testing.T, size int64) (string, VerifiedIndex) {
	t.Helper()
	key := newKey(t)
	path := filepath.Join(t.TempDir(), "asset")
	digest := "sha256:" + strings.Repeat("a", 64)
	manifest := signature.Manifest{
		Piglet: "porter", ReleaseVersion: "1.2.3", Target: testTarget, PigVersion: "pig-test",
		PigletDigest: digest, SourceDigest: digest, ResolutionDigest: digest, ComponentPlanDigest: digest,
	}
	// Measure the trailer using the same number of body-size digits, then
	// recreate the sparse body so the complete signed file has exactly size bytes.
	bodySize := size
	for range 2 {
		if err := os.WriteFile(path, nil, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Truncate(path, bodySize); err != nil {
			t.Fatal(err)
		}
		if _, err := signature.Sign(path, manifest, key); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		bodySize = size - (info.Size() - bodySize)
	}
	sum, actualSize, err := fileSHA256(path)
	if err != nil || actualSize != size {
		t.Fatalf("signed fixture size=%d, want %d: %v", actualSize, size, err)
	}
	data, err := Sign(Index{
		Piglet: manifest.Piglet, Version: manifest.ReleaseVersion, PigVersion: manifest.PigVersion,
		SourceRef: "git:github.com/acme/porter@v1.2.3",
		Binaries:  map[string]Binary{testTarget: {URL: "asset", SHA256: sum, Size: size}},
	}, key)
	if err != nil {
		t.Fatal(err)
	}
	verified, err := Verify(data)
	if err != nil {
		t.Fatal(err)
	}
	return path, verified
}
