package packagemanager

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/nodepath"
)

// ResolveManagedPackagePath is package-manager.ts resolveManagedPath: path.resolve(resolve(root), ...parts), so an absolute component replaces the preceding root, then a check that the result is the root or starts with the root and a separator.
// Ports packages/coding-agent/src/core/package-manager.ts.
func ResolveManagedPackagePath(root string, parts ...string) (string, error) {
	resolvedRoot, err := nodepath.Resolve(root)
	if err != nil {
		return "", err
	}
	resolved, err := nodepath.Resolve(append([]string{resolvedRoot}, parts...)...)
	if err != nil {
		return "", err
	}
	if resolved != resolvedRoot && !strings.HasPrefix(resolved, resolvedRoot+string(filepath.Separator)) {
		return "", fmt.Errorf("Refusing to use path outside package install root: %s", resolved)
	}
	return resolved, nil
}
