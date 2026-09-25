package subprocess

import (
	"crypto/sha256"
	"encoding/hex"
)

const sdkFingerprintFile = ".sdk-fingerprint"

// StagedSDKFingerprint hashes the source files that define one staged SDK.
func StagedSDKFingerprint(sdkDir, buildType string) (string, error) {
	exts := []string{".go", ".mod", ".sum"}
	switch buildType {
	case "rust":
		exts = []string{".rs", ".toml", ".lock"}
	case "python":
		exts = []string{".py", ".toml", ".lock"}
	case "node":
		exts = []string{".js", ".mjs", ".cjs", ".ts", ".json"}
	}
	hash := sha256.New()
	if err := hashDirSources(hash, sdkDir, exts); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil))[:16], nil
}
