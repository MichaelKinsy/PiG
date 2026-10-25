//go:build !pig_strip_node_extensions

package subprocess

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pigsdklock"
)

// A binary whose SDKs are installed runs isolated and packed Node extensions on the installed runtime tree: every tool call is answered,
// no launcher publishes a copy of the runtime, and the installed tree is not written.
func TestNodeExtensionsRunOnTheInstalledRuntime(t *testing.T) {
	nodeCellRequireNode(t)
	installed := filepath.Join(t.TempDir(), "sdk")
	if err := os.MkdirAll(installed, 0o755); err != nil {
		t.Fatal(err)
	}
	tree, err := InstallNodeRuntime(installed)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(installed, "node-runtime-"+nodeRuntimeIdentity()[:16]); tree != want {
		t.Fatalf("InstallNodeRuntime = %s, want %s", tree, want)
	}
	if _, err := InstallNodeRuntime(installed); err == nil {
		t.Fatal("a second install over the tree succeeded")
	}
	if leftovers, _ := filepath.Glob(filepath.Join(installed, ".stage-*")); len(leftovers) != 0 {
		t.Fatalf("install left staging trees %v", leftovers)
	}
	digestBefore := treeDigest(t, tree)
	keyBefore := string(nodeRuntimeKey())
	t.Cleanup(pigsdklock.SetInstalledSDKRootForTest(installed))
	if string(nodeRuntimeKey()) == keyBefore {
		t.Fatal("an installed launcher's cache key equals a materializing launcher's")
	}

	root := filepath.Join(t.TempDir(), "exts")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	var configs []ExtConfig
	for _, tc := range []struct{ name, placement string }{{"packed-one", ""}, {"packed-two", ""}, {"isolated-one", "isolated"}} {
		entry := filepath.Join(root, tc.name+".mjs")
		nodeCellTrivialExtension(t, entry, tc.name)
		configs = append(configs, ExtConfig{Name: tc.name, Source: entry, Enabled: true, Isolation: tc.placement})
	}
	configRoot := filepath.Join(root, "config")
	host := NewHostWithConfigRoot(root, configRoot)
	t.Cleanup(func() { host.Shutdown("test done") })
	loaded, errs := host.LoadAll(t.Context(), configs)
	if len(errs) != 0 || len(loaded) != len(configs) {
		t.Fatalf("LoadAll = %d extensions, %v; want %d", len(loaded), errs, len(configs))
	}
	for _, ext := range host.Extensions() {
		tool, ok := ext.Tools[ext.Name]
		if !ok {
			t.Fatalf("missing tool for %s", ext.Name)
		}
		result, err := tool.Definition.Execute(t.Context(), "installed-call-"+ext.Name, json.RawMessage(`{}`), nil)
		if err != nil {
			t.Fatal(err)
		}
		if data, err := json.Marshal(result); err != nil || !strings.Contains(string(data), ext.Name) {
			t.Fatalf("tool %s = %s, %v", ext.Name, data, err)
		}
	}
	host.Shutdown("test done")

	var copies []string
	_ = filepath.WalkDir(configRoot, func(path string, entry fs.DirEntry, err error) error {
		if err == nil && entry.Name() == "runtime.mjs" {
			copies = append(copies, path)
		}
		return nil
	})
	if len(copies) != 0 {
		t.Fatalf("launchers published runtime copies %v", copies)
	}
	if !bytes.Equal(treeDigest(t, tree), digestBefore) {
		t.Fatal("running extensions wrote into the installed runtime tree")
	}

	if err := os.WriteFile(filepath.Join(tree, nodeRuntimeMarker), []byte("another runtime\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installedNodeRuntime(); err == nil || !strings.Contains(err.Error(), "reinstall pig") {
		t.Fatalf("a mismatched installed runtime reports %v", err)
	}
	if err := materializeNodeRuntime(t.Context(), t.TempDir(), t.TempDir()); err == nil {
		t.Fatal("a launcher built against a mismatched installed runtime")
	}
}

// treeDigest lists every file under root with its mode, size, and contents.
func treeDigest(t *testing.T, root string) []byte {
	t.Helper()
	var out bytes.Buffer
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		out.WriteString(rel + "\x00" + info.Mode().String() + "\x00" + info.ModTime().String() + "\x00")
		if info.Mode().IsRegular() {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			out.Write(data)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}
