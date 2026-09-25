package release

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAssetNameAddsExeOnlyForWindows(t *testing.T) {
	for target, want := range map[string]string{
		"linux/amd64":   "pig-porter-linux-amd64",
		"darwin/arm64":  "pig-porter-darwin-arm64",
		"windows/amd64": "pig-porter-windows-amd64.exe",
	} {
		if got := AssetName("porter", target); got != want {
			t.Errorf("AssetName(porter, %s) = %q, want %q", target, got, want)
		}
	}
}

// Publication must accept exactly the repositories that a github: pull
// reference accepts, or a published release could not be pulled by name.
func TestValidGitHubRepositoryMatchesPullReferences(t *testing.T) {
	for repository, want := range map[string]bool{
		"acme/porter":        true,
		"Acme-1/porter.go_x": true,
		"acme":               false,
		"acme/":              false,
		"/porter":            false,
		"acme/porter/extra":  false,
		"acme/..":            false,
		"acme?x/porter":      false,
		"acme/por ter":       false,
	} {
		if got := ValidGitHubRepository(repository); got != want {
			t.Errorf("ValidGitHubRepository(%q) = %v, want %v", repository, got, want)
		}
		_, _, err := resolveIndexURL("github:"+repository+"@1.2.3", "")
		if (err == nil) != want {
			t.Errorf("pull reference github:%s@1.2.3 error = %v, publication validity %v", repository, err, want)
		}
	}
}

func TestVerifyAssetAppliesPullChecksToLocalAssets(t *testing.T) {
	key := newKey(t)
	binary := signedBinary(t, key, "porter", "1.2.3", testTarget, "pig-test")
	sum := sha256.Sum256(binary)
	signed, err := Sign(Index{
		Piglet: "porter", Version: "1.2.3", PigVersion: "pig-test", SourceRef: "git:github.com/acme/porter@v1.2.3",
		Binaries: map[string]Binary{testTarget: {URL: AssetName("porter", testTarget), SHA256: hex.EncodeToString(sum[:]), Size: int64(len(binary))}},
	}, key)
	if err != nil {
		t.Fatal(err)
	}
	verified, err := Verify(signed)
	if err != nil {
		t.Fatal(err)
	}
	write := func(t *testing.T, data []byte) string {
		t.Helper()
		path := filepath.Join(t.TempDir(), AssetName("porter", testTarget))
		if err := os.WriteFile(path, data, 0o755); err != nil {
			t.Fatal(err)
		}
		return path
	}
	if err := VerifyAsset(write(t, binary), verified, testTarget); err != nil {
		t.Fatalf("VerifyAsset(valid asset) = %v", err)
	}

	otherKey := newKey(t)
	cases := []struct {
		name   string
		data   []byte
		target string
		want   string
	}{
		{"missing target", binary, "other/target", "no binary for target"},
		{"truncated", binary[:len(binary)-1], testTarget, "the signed index says"},
		{"executable byte changed", flipByte(binary, 0), testTarget, "the signed index says"},
		{"other signer", signedBinary(t, otherKey, "porter", "1.2.3", testTarget, "pig-test"), testTarget, "the signed index says"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := VerifyAsset(write(t, tc.data), verified, tc.target); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("VerifyAsset() error = %v, want %q", err, tc.want)
			}
		})
	}

	t.Run("manifest disagrees with index", func(t *testing.T) {
		wrong := signedBinary(t, key, "porter", "9.9.9", testTarget, "pig-test")
		wrongSum := sha256.Sum256(wrong)
		data, err := Sign(Index{
			Piglet: "porter", Version: "1.2.3", PigVersion: "pig-test", SourceRef: "git:github.com/acme/porter@v1.2.3",
			Binaries: map[string]Binary{testTarget: {URL: "asset", SHA256: hex.EncodeToString(wrongSum[:]), Size: int64(len(wrong))}},
		}, key)
		if err != nil {
			t.Fatal(err)
		}
		mismatched, err := Verify(data)
		if err != nil {
			t.Fatal(err)
		}
		if err := VerifyAsset(write(t, wrong), mismatched, testTarget); err == nil || !strings.Contains(err.Error(), `release version "9.9.9"`) {
			t.Fatalf("VerifyAsset() error = %v", err)
		}
	})

	t.Run("unsigned", func(t *testing.T) {
		plain := []byte("plain executable\n")
		plainSum := sha256.Sum256(plain)
		data, err := Sign(Index{
			Piglet: "porter", Version: "1.2.3", PigVersion: "pig-test", SourceRef: "git:github.com/acme/porter@v1.2.3",
			Binaries: map[string]Binary{testTarget: {URL: "asset", SHA256: hex.EncodeToString(plainSum[:]), Size: int64(len(plain))}},
		}, key)
		if err != nil {
			t.Fatal(err)
		}
		unsigned, err := Verify(data)
		if err != nil {
			t.Fatal(err)
		}
		if err := VerifyAsset(write(t, plain), unsigned, testTarget); err == nil || !strings.Contains(err.Error(), "is unsigned") {
			t.Fatalf("VerifyAsset() error = %v", err)
		}
	})
}

func flipByte(data []byte, offset int) []byte {
	out := append([]byte(nil), data...)
	out[offset] ^= 0xff
	return out
}
