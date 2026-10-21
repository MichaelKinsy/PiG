package subprocess

import (
	"os"
	"path/filepath"
)

const sdkFingerprintFile = ".sdk-fingerprint"

// recordSDKFingerprint writes the SDK fingerprint marker of the published entry at entryDir, best effort. A reader sees the previous
// marker or the complete new one, never a truncated file: the cache collector reads an empty marker as an obsolete SDK and expires
// the entry, which a peer may be starting. Nothing is written when the marker already holds fp, which is every start of an entry
// whose build recorded it.
func recordSDKFingerprint(entryDir, fp string) {
	marker := filepath.Join(entryDir, sdkFingerprintFile)
	if current, err := os.ReadFile(marker); err == nil && string(current) == fp {
		return
	}
	tmp, err := os.CreateTemp(entryDir, sdkFingerprintFile+"-*")
	if err != nil {
		return
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }() // no-op once the rename succeeds
	if _, err := tmp.WriteString(fp); err != nil {
		_ = tmp.Close()
		return
	}
	if err := tmp.Chmod(0o644); err != nil {
		_ = tmp.Close()
		return
	}
	if err := tmp.Close(); err != nil {
		return
	}
	if err := os.Rename(name, marker); err != nil {
		return // the entry stays unfingerprinted, which only costs the collector its proof
	}
}

// StagedSDKFingerprint hashes the source files that define one staged SDK. The digest is memoized per directory (sdkSourceDigest), so the
// fingerprint a build records after compiling costs nothing once the build's cache key has read the SDK.
func StagedSDKFingerprint(sdkDir, buildType string) (string, error) {
	digest, err := sdkSourceDigest(sdkDir, buildType)
	if err != nil {
		return "", err
	}
	return digest[:16], nil
}
