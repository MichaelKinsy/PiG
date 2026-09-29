package experimental

import (
	"os"
	"path/filepath"
	"testing"
)

func nodeFacetTestDirectory(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, key := range []string{"HOME", "USERPROFILE", "PIG_CODING_AGENT_DIR", "PI_CODING_AGENT_DIR", "PIG_HOME", "PI_HOME", "XDG_CONFIG_HOME", "XDG_CACHE_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_RUNTIME_DIR"} {
		path := filepath.Join(root, key)
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
		t.Setenv(key, path)
	}
	return root
}

func writeNodeFacetFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

const nodeFacetManifestJSON = `{"format":"chord.facet-bundle","formatVersion":2,"plugin":{"id":"test-bundle","version":"1"},"entries":{"worker":{"file":"entry.cjs","integrity":"sha256-pending","externalImports":["@earendil-works/chord"]}}}`
