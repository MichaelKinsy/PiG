package tui

import (
	"slices"
	"testing"
)

func fakeEnv(values map[string]string) func(string) string {
	return func(name string) string { return values[name] }
}

// Ports "Windows keybinding defaults" (coding-agent test/keybindings.test.ts).
func TestUseWindowsKeybindings(t *testing.T) {
	for _, c := range []struct {
		name string
		goos string
		env  map[string]string
		want bool
	}{
		{"native Windows", "windows", nil, true},
		{"WSL distro name", "linux", map[string]string{"WSL_DISTRO_NAME": "Ubuntu"}, true},
		{"WSL interop", "linux", map[string]string{"WSL_INTEROP": "/run/WSL/123_interop"}, true},
		{"Windows Terminal alone", "linux", map[string]string{"WT_SESSION": "session"}, false},
		{"plain Linux", "linux", nil, false},
		{"macOS", "darwin", nil, false},
		{"macOS with WSL variables", "darwin", map[string]string{"WSL_DISTRO_NAME": "Ubuntu"}, false},
	} {
		if got := UseWindowsKeybindings(c.goos, fakeEnv(c.env)); got != c.want {
			t.Errorf("%s: UseWindowsKeybindings = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestKeybindingPlatformFor(t *testing.T) {
	for _, c := range []struct {
		goos string
		env  map[string]string
		want KeybindingPlatform
	}{
		{"darwin", nil, KeybindingPlatformDarwin},
		{"linux", nil, KeybindingPlatformLinux},
		{"linux", map[string]string{"WSL_INTEROP": "/run/WSL/1_interop"}, KeybindingPlatformLinuxWSL},
		{"windows", nil, KeybindingPlatformWin32},
		{"freebsd", nil, KeybindingPlatformLinux},
	} {
		if got := KeybindingPlatformFor(c.goos, fakeEnv(c.env)); got != c.want {
			t.Errorf("KeybindingPlatformFor(%q, %v) = %q, want %q", c.goos, c.env, got, c.want)
		}
	}
}

func TestPlatformKeysPicksTheNarrowestColumn(t *testing.T) {
	keys := PlatformKeys{
		Other:    []string{"other"},
		Darwin:   []string{"darwin"},
		Windows:  []string{"windows"},
		LinuxWSL: []string{"wsl"},
	}
	want := map[KeybindingPlatform][]string{
		KeybindingPlatformDarwin:   {"darwin"},
		KeybindingPlatformLinux:    {"other"},
		KeybindingPlatformLinuxWSL: {"wsl"},
		KeybindingPlatformWin32:    {"windows"},
	}
	for platform, keys2 := range want {
		if got := keys.For(platform); !slices.Equal(got, keys2) {
			t.Errorf("For(%s) = %v, want %v", platform, got, keys2)
		}
	}
	unbound := PlatformKeys{Other: []string{"ctrl+z"}, Win32: []string{}}.For(KeybindingPlatformWin32)
	if unbound == nil || len(unbound) != 0 {
		t.Errorf("an empty Win32 column must unbind the key, got %#v", unbound)
	}
}

func TestTUIUndoDefaultsFollowThePlatform(t *testing.T) {
	for platform, want := range map[KeybindingPlatform][]string{
		KeybindingPlatformDarwin:   {"ctrl+-"},
		KeybindingPlatformLinux:    {"ctrl+-"},
		KeybindingPlatformLinuxWSL: {"alt+z"},
		KeybindingPlatformWin32:    {"ctrl+z"},
	} {
		kb := NewKeybindingsManager(TUIKeybindingDefinitionsFor(platform), nil)
		if got := kb.GetKeys(KBEditorUndo); !slices.Equal(got, want) {
			t.Errorf("%s undo = %v, want %v", platform, got, want)
		}
	}
}
