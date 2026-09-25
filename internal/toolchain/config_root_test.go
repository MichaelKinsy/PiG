package toolchain_test

import (
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/toolchain"
)

// The managed toolchain lives under the one PiG configuration root:
// $PIG_HOME, else $XDG_CONFIG_HOME/pig, else ~/.pig, with ~ expanded, as
// codingagent.ConfigRoot resolves it.
func TestConfigRootMatchesPigConfigurationRoot(t *testing.T) {
	home := t.TempDir()
	xdg := t.TempDir()
	cases := []struct {
		name, pigHome, xdgConfigHome, want string
	}{
		{name: "PIG_HOME wins", pigHome: filepath.Join(home, "p"), xdgConfigHome: xdg, want: filepath.Join(home, "p")},
		{name: "PIG_HOME tilde", pigHome: "~/p", want: filepath.Join(home, "p")},
		{name: "XDG_CONFIG_HOME", xdgConfigHome: xdg, want: filepath.Join(xdg, "pig")},
		{name: "XDG_CONFIG_HOME tilde", xdgConfigHome: "~/cfg", want: filepath.Join(home, "cfg", "pig")},
		{name: "default", want: filepath.Join(home, ".pig")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			t.Setenv("PIG_HOME", tc.pigHome)
			t.Setenv("XDG_CONFIG_HOME", tc.xdgConfigHome)
			got, err := toolchain.ConfigRoot()
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want || got != codingagent.ConfigRoot() {
				t.Fatalf("toolchain.ConfigRoot() = %q, want %q (codingagent.ConfigRoot() = %q)", got, tc.want, codingagent.ConfigRoot())
			}
		})
	}
}
