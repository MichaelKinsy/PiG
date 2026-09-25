package piglet

import "path"

// cleanContainerPath cleans a path inside the Linux agent container. The
// container's separator is / on every host, so host path rules (filepath on
// Windows) never apply to it.
func cleanContainerPath(p string) string { return path.Clean(p) }
