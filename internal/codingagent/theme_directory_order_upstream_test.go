package codingagent

import (
	"runtime"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

// Pi lists a theme directory with fs.readdirSync (resource-loader.ts:1093 for configured theme paths, theme.ts:507 getCustomThemeInfos for
// the agent directory's themes), in libuv's scandir order: strcmp on Unix, the file system's order on Windows, which on NTFS ignores case.
// The first theme of a name wins, so the order decides which file a name loads from.
func TestThemeDirectoriesKeepNodesReaddirOrder(t *testing.T) {
	want := "Zsun.json"
	if runtime.GOOS == "windows" {
		want = "asun.json"
	}
	mode := tui.GetTerminalColorMode()

	dir := t.TempDir()
	paths := map[string]string{"Zsun.json": writeNamedTheme(t, dir, "Zsun.json", "sunset"), "asun.json": writeNamedTheme(t, dir, "asun.json", "sunset")}
	registry := tui.NewThemeRegistry()
	loadThemeResources(registry, []string{dir}, mode)
	if got := registry.PathOf("sunset"); got != paths[want] {
		t.Errorf("configured theme directory: sunset loaded from %q, want %q", got, paths[want])
	}

	custom := t.TempDir()
	customPaths := map[string]string{"Zsun.json": writeNamedTheme(t, custom, "Zsun.json", "sunset"), "asun.json": writeNamedTheme(t, custom, "asun.json", "sunset")}
	registry = tui.NewThemeRegistry()
	addCustomDirectoryThemes(registry, custom, mode)
	if got := registry.PathOf("sunset"); got != customPaths[want] {
		t.Errorf("custom themes directory: sunset loaded from %q, want %q", got, customPaths[want])
	}
}
