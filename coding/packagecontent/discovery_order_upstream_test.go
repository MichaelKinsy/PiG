package packagecontent

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
)

// Pi's package-manager.ts collects package and conventional resources with fs.readdirSync (collectFiles :333, collectSkillEntries :385,
// collectAutoPromptEntries :497, collectAutoThemeEntries :534, collectAutoExtensionEntries :608), in libuv's scandir order: strcmp on
// Unix, the file system's order on Windows, which on NTFS ignores case.
func TestDiscoveryKeepsNodesReaddirOrder(t *testing.T) {
	root := t.TempDir()
	write := func(rel string) string {
		t.Helper()
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("---\nname: s\ndescription: d\n---\nbody\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	unixFirst := func(upper, lower string) []string {
		if runtime.GOOS == "windows" {
			return []string{lower, upper}
		}
		return []string{upper, lower}
	}
	prompts := unixFirst(write("prompts/Zp.md"), write("prompts/ap.md"))
	themes := unixFirst(write("themes/Zt.json"), write("themes/at.json"))
	skills := unixFirst(filepath.Dir(write("skills/Zs/SKILL.md")), filepath.Dir(write("skills/as/SKILL.md")))
	extensions := unixFirst(write("extensions/Zx.ts"), write("extensions/ax.ts"))
	nested := unixFirst(write("nested/Zn.md"), write("nested/an.md"))
	for _, tc := range []struct {
		name string
		got  []string
		want []string
	}{
		{"conventional prompts", DiscoverAutomatic(filepath.Join(root, "prompts"), Prompts), prompts},
		{"conventional themes", DiscoverAutomatic(filepath.Join(root, "themes"), Themes), themes},
		{"conventional skills", DiscoverAutomatic(filepath.Join(root, "skills"), Skills), skills},
		{"conventional extensions", DiscoverAutomatic(filepath.Join(root, "extensions"), Extensions), extensions},
		{"configured prompt directory", Collect([]string{filepath.Join(root, "nested")}, Prompts), nested},
	} {
		if !slices.Equal(tc.got, tc.want) {
			t.Errorf("%s = %v, want %v", tc.name, tc.got, tc.want)
		}
	}
}
