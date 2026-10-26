package subprocess

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"

	"github.com/MichaelKinsy/PiG/internal/pigsdklock"
)

// pig additive (D201): a binary whose SDKs are installed runs its TypeScript extensions on the runtime tree installed beside them instead of materializing a copy per launcher.

// nodeRuntimeMarker records, in an installed runtime tree, the identity of the runtime the tree holds ([nodeRuntimeIdentity]).
const nodeRuntimeMarker = ".pig-node-runtime"

// nodeRuntimeIdentity is the content identity of the embedded runtime: the hex SHA-256 of its format version and generated digest.
func nodeRuntimeIdentity() string {
	digest := sha256.Sum256(append([]byte(nodeRuntimeVersion), nodeRuntimeDigest()...))
	return hex.EncodeToString(digest[:])
}

// nodeRuntimeDirName is the directory name of the installed runtime tree.
func nodeRuntimeDirName() string { return "node-runtime-" + nodeRuntimeIdentity()[:16] }

// installedNodeRuntime returns the runtime tree installed under the installed SDK root, or "" when this binary materializes the runtime
// for each launcher instead. It fails when the installed tree is missing or holds another runtime.
func installedNodeRuntime() (string, error) {
	root := pigsdklock.InstalledSDKRoot()
	if root == "" {
		return "", nil
	}
	dir := filepath.Join(root, nodeRuntimeDirName())
	if err := pigsdklock.CheckInstalledDir("TypeScript extension runtime", dir, nodeRuntimeMarker, nodeRuntimeIdentity()); err != nil {
		return "", err
	}
	return dir, nil
}

// nodeRuntimeTree is the runtime tree a launcher whose published tree is published runs: the installed tree, when there is one, else
// published.
func nodeRuntimeTree(published string) string {
	if root := pigsdklock.InstalledSDKRoot(); root != "" {
		return filepath.Join(root, nodeRuntimeDirName())
	}
	return published
}

// nodeRuntimeKey is the runtime identity a launcher's cache key covers. A launcher of an installed binary publishes no runtime tree and
// runs the installed one, so its key also names that tree: a binary that materializes its runtime must never reuse it.
func nodeRuntimeKey() []byte {
	key := nodeRuntimeDigest()
	if root := pigsdklock.InstalledSDKRoot(); root != "" {
		key = append(append(append([]byte{}, key...), "\x00installed\x00"...), filepath.Join(root, nodeRuntimeDirName())...)
	}
	return key
}
