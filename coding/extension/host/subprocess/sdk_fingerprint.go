package subprocess

const sdkFingerprintFile = ".sdk-fingerprint"

// StagedSDKFingerprint hashes the source files that define one staged SDK. The digest is memoized per directory (sdkSourceDigest), so the
// fingerprint a build records after compiling costs nothing once the build's cache key has read the SDK.
func StagedSDKFingerprint(sdkDir, buildType string) (string, error) {
	digest, err := sdkSourceDigest(sdkDir, buildType)
	if err != nil {
		return "", err
	}
	return digest[:16], nil
}
