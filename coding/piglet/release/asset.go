package release

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/MichaelKinsy/PiG/coding/piglet/signature"
)

// AssetName is the release asset file name of one target's Piglet Binary:
// pig-<piglet>-<goos>-<goarch>, with .exe for Windows targets.
func AssetName(piglet, target string) string {
	goos, goarch, _ := strings.Cut(target, "/")
	name := "pig-" + piglet + "-" + goos + "-" + goarch
	if goos == "windows" {
		name += ".exe"
	}
	return name
}

// ValidGitHubRepository reports whether repository is an owner/name pair that
// a github:owner/repo@version release reference accepts.
func ValidGitHubRepository(repository string) bool {
	owner, name, ok := strings.Cut(repository, "/")
	return ok && validGitHubPart(owner) && validGitHubPart(name)
}

// VerifyAsset checks a local release asset with the checks Pull applies to a download of it: the complete-file size limit, size and SHA-256 named by the signed index, a valid signature trailer by the index signer, and a signed manifest that names the index's Piglet, version, target, and PiG version.
func VerifyAsset(path string, verified VerifiedIndex, target string) error {
	binary, ok := verified.Index.Binaries[target]
	if !ok {
		return fmt.Errorf("Piglet release %s %s has no binary for target %s", verified.Index.Piglet, verified.Index.Version, target)
	}
	if err := checkBinarySize(binary.Size); err != nil {
		return err
	}
	digest, size, err := fileSHA256(path)
	if err != nil {
		return fmt.Errorf("read release asset %s: %w", path, err)
	}
	if size != binary.Size || digest != binary.SHA256 {
		return fmt.Errorf("release asset %s is %d bytes with SHA256 %s; the signed index says %d bytes with SHA256 %s", path, size, digest, binary.Size, binary.SHA256)
	}
	status, err := signature.Check(path, signature.Policy{})
	if err != nil {
		return fmt.Errorf("release asset %s: %w", path, err)
	}
	if !status.Signed {
		return fmt.Errorf("release asset %s is unsigned", path)
	}
	return matchBinaryManifest(status, verified, target)
}

// pig additive (D18): publication and pull share the complete signed Binary size limit.
func checkBinarySize(size int64) error {
	if size > maxBinaryBytes {
		return fmt.Errorf("Piglet Binary declares size %d above the %d-byte limit", size, maxBinaryBytes)
	}
	return nil
}

func fileSHA256(path string) (string, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer func() { _ = file.Close() }() // Read-only: close cannot lose data.
	info, err := file.Stat()
	if err != nil {
		return "", 0, err
	}
	if !info.Mode().IsRegular() {
		return "", 0, fmt.Errorf("%s is not a regular file", path)
	}
	hash := sha256.New()
	size, err := io.Copy(hash, file)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(hash.Sum(nil)), size, nil
}
