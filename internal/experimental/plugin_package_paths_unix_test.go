//go:build !windows

package experimental

import (
	"path/filepath"
	"testing"
)

// upstream: packages/coding-agent/src/experimental/plugins/package.ts:160-171 (sessionPluginProfilePath: first 24 hex digits of SHA-256 of the Session path; pluginBuildDirectoryName: a label from the package directory's name ("plugin" when that name is empty) with each code point outside [a-zA-Z0-9._-] replaced by "-", a "-plugin" suffix unless present, and the first 12 hex digits of SHA-256 of the normalized package path). The digests were computed independently with SHA-256 over the UTF-8 path; a Pi server shares these files when the D2 namespace selects Pi's directories.
func TestPluginPackageOnDiskNames(t *testing.T) {
	const directory, serverId = "/srv/pi", "00000000-0000-4000-8000-000000000001"
	if got, want := sessionPluginProfilePath(directory, serverId, "/tmp/work/session-a/meta.json"), "/srv/pi/session-plugin-packages-"+serverId+"-860fb03c5f4203cd1bed372a.json"; got != want {
		t.Fatalf("Session profile path = %s, want %s", got, want)
	}
	for _, row := range []struct{ name, packagePath, want string }{
		{"a space becomes a dash and the resulting suffix is kept", "/tmp/plugins/my plugin", "my-plugin-82b258f4b22e"},
		{"package.json names its directory; the hash keeps the file", "/tmp/plugins/Éclair/package.json", "-clair-plugin-7287d8a48f2e"},
		{"a name without the suffix gains it", "/tmp/plugins/example", "example-plugin-5cc951717341"}, {"an existing suffix is kept", "/tmp/plugins/example-plugin", "example-plugin-06fbe997e4bd"},
		{"an astral code point is one dash", "/tmp/plugins/😀x", "-x-plugin-604d25408866"},
		{"dots and underscores are kept", "/tmp/plugins/my.plugin_v1.2", "my.plugin_v1.2-plugin-02443761b26a"},
		// Node's basename of a filesystem root is "", which selects the "plugin" label.
		{"the filesystem root", "/", "plugin-plugin-8a5edab28263"}, {"package.json in the filesystem root", "/package.json", "plugin-plugin-545332ac1501"},
	} {
		t.Run(row.name, func(t *testing.T) {
			plugin, err := CreateServerPluginPackage(directory, serverId, row.packagePath)
			if err != nil {
				t.Fatal(err)
			}
			prefix := filepath.Join(directory, "plugin-builds", serverId) + string(filepath.Separator)
			manifest := plugin.ManifestPath
			if len(manifest) <= len(prefix) || manifest[:len(prefix)] != prefix {
				t.Fatalf("manifest %s is outside %s", manifest, prefix)
			}
			name := filepath.Base(filepath.Dir(manifest))
			if filepath.Base(manifest) != FacetBundleManifestFile {
				t.Fatalf("manifest file = %s", filepath.Base(manifest))
			}
			if name != row.want {
				t.Fatalf("build directory = %s, want %s", name, row.want)
			}
		})
	}
}
