package tui

import (
	"os"
	"testing"
)

// isolateCapabilityEnv isolates the environment variables read by terminal_image.go, the capability cache, and overrides.
func isolateCapabilityEnv(t *testing.T) {
	t.Helper()
	preserveCapabilityState(t)
	for _, name := range []string{
		"TERM_PROGRAM", "TERMINAL_EMULATOR", "TERM", "COLORTERM",
		"HERDR_ENV", "HERDR_KITTY_GRAPHICS", "TMUX", "KITTY_WINDOW_ID",
		"GHOSTTY_RESOURCES_DIR", "WEZTERM_PANE", "WARP_SESSION_ID",
		"WARP_TERMINAL_SESSION_UUID", "ITERM_SESSION_ID", "WT_SESSION",
		"PI_HYPERLINKS", "PI_IMAGE_PROTOCOL", "PI_TRUE_COLOR",
	} {
		t.Setenv(name, "")
	}
	ResetCapabilitiesCache()
	SetCapabilityOverrides(CapabilityOverrides{})
	detectCapabilitiesAs(t, "linux")
}

// detectCapabilitiesAs makes DetectCapabilities treat goos as the platform.
// On Windows an unknown terminal is a Windows console, which supports
// truecolor as in upstream's win32 branch, so a case that expects the
// unknown-terminal result names a platform instead of taking the host's.
func detectCapabilitiesAs(t *testing.T, goos string) {
	t.Helper()
	saved := capabilityDetectGOOS
	capabilityDetectGOOS = goos
	t.Cleanup(func() { capabilityDetectGOOS = saved })
}

func setenvs(t *testing.T, pairs ...string) {
	t.Helper()
	for i := 0; i+1 < len(pairs); i += 2 {
		t.Setenv(pairs[i], pairs[i+1])
	}
}

// preserveCapabilityState restores both an absent cache and explicitly pinned capabilities without triggering detection.
func preserveCapabilityState(t testing.TB) {
	t.Helper()
	capabilityMu.Lock()
	cached := clonePtr(cachedCapabilities.Load())
	overrides := capabilityOverrides.clone()
	capabilityMu.Unlock()
	t.Cleanup(func() {
		capabilityMu.Lock()
		cachedCapabilities.Store(cached)
		capabilityOverrides = overrides
		capabilityMu.Unlock()
	})
}

func TestIsolateCapabilityEnvPreservesUnrelatedEnvironment(t *testing.T) {
	preserveCapabilityState(t)
	t.Setenv("PIG_TEST_UNRELATED_CAPABILITY_ENV", "keep")
	t.Setenv("TERM", "outer-terminal")
	t.Run("isolated", func(t *testing.T) {
		isolateCapabilityEnv(t)
		if got := os.Getenv("PIG_TEST_UNRELATED_CAPABILITY_ENV"); got != "keep" {
			t.Errorf("unrelated environment variable = %q, want keep", got)
		}
		if got := os.Getenv("TERM"); got != "" {
			t.Errorf("capability environment variable TERM = %q, want empty", got)
		}
	})
	if got := os.Getenv("TERM"); got != "outer-terminal" {
		t.Errorf("restored TERM = %q, want outer-terminal", got)
	}
}

func TestIsolateCapabilityEnvRestoresState(t *testing.T) {
	preserveCapabilityState(t)
	for _, cached := range []*TerminalCapabilities{nil, {Images: ImageProtocolKitty, TrueColor: true}} {
		name := "uncached"
		if cached != nil {
			name = "cached"
		}
		t.Run(name, func(t *testing.T) {
			preserveCapabilityState(t)
			overrides := CapabilityOverrides{Images: new(ImageProtocolITerm2), Hyperlinks: new(true), TrueColor: new(false)}
			SetCapabilityOverrides(overrides)
			ResetCapabilitiesCache()
			if cached != nil {
				SetCapabilities(*cached)
			}
			t.Run("isolated", func(t *testing.T) {
				isolateCapabilityEnv(t)
				capabilityMu.Lock()
				isolated := capabilityOverrides.equal(CapabilityOverrides{}) && cachedCapabilities.Load() == nil
				capabilityMu.Unlock()
				if !isolated {
					t.Fatal("capability overrides and cache were not isolated")
				}
				SetCapabilityOverrides(CapabilityOverrides{Hyperlinks: new(false)})
				SetCapabilities(TerminalCapabilities{Hyperlinks: true})
			})
			capabilityMu.Lock()
			gotOverrides := capabilityOverrides.clone()
			gotCached := clonePtr(cachedCapabilities.Load())
			capabilityMu.Unlock()
			if !gotOverrides.equal(overrides) {
				t.Error("capability overrides were not restored")
			}
			if !ptrValueEqual(gotCached, cached) {
				t.Errorf("cached capabilities = %+v, want %+v", gotCached, cached)
			}
		})
	}
}

func failProbe(t *testing.T) func() bool {
	return func() bool {
		t.Helper()
		t.Fatal("tmux hyperlink probe ran although an override decided hyperlinks")
		return false
	}
}

// Mirrors terminal-image.ts detectCapabilitiesFromEnvironment in 0.87.1.
func TestDetectCapabilitiesFromEnvironmentMatchesPi(t *testing.T) {
	cases := []struct {
		name      string
		env       []string
		probe     bool
		goos      string
		want      TerminalCapabilities
		wantProbe bool
	}{
		{name: "tmux forwards hyperlinks when the probe confirms", env: []string{"TMUX", "/tmp/tmux", "COLORTERM", "truecolor"}, probe: true, wantProbe: true,
			want: TerminalCapabilities{TrueColor: true, Hyperlinks: true}},
		{name: "tmux TERM without forwarding", env: []string{"TERM", "tmux-256color", "KITTY_WINDOW_ID", "1"}, probe: false, wantProbe: true,
			want: TerminalCapabilities{}},
		{name: "screen never forwards hyperlinks", env: []string{"TERM", "screen-256color", "COLORTERM", "24bit"},
			want: TerminalCapabilities{TrueColor: true}},
		{name: "warp uses kitty images", env: []string{"TERM_PROGRAM", "WarpTerminal"},
			want: TerminalCapabilities{Images: ImageProtocolKitty, TrueColor: true, Hyperlinks: true}},
		{name: "warp session id", env: []string{"WARP_SESSION_ID", "1"},
			want: TerminalCapabilities{Images: ImageProtocolKitty, TrueColor: true, Hyperlinks: true}},
		{name: "warp terminal session uuid", env: []string{"WARP_TERMINAL_SESSION_UUID", "x"},
			want: TerminalCapabilities{Images: ImageProtocolKitty, TrueColor: true, Hyperlinks: true}},
		{name: "windows terminal session", env: []string{"WT_SESSION", "1"},
			want: TerminalCapabilities{TrueColor: true, Hyperlinks: true}},
		{name: "zed", env: []string{"TERM_PROGRAM", "zed"},
			want: TerminalCapabilities{TrueColor: true, Hyperlinks: true}},
		{name: "jetbrains", env: []string{"TERMINAL_EMULATOR", "JetBrains-JediTerm"},
			want: TerminalCapabilities{TrueColor: true}},
		{name: "windows console without WT_SESSION", goos: "windows",
			want: TerminalCapabilities{TrueColor: true}},
		{name: "apple terminal follows the unknown-terminal truecolor hint", env: []string{"TERM_PROGRAM", "Apple_Terminal"},
			want: TerminalCapabilities{}},
		{name: "unknown terminal with truecolor hint", env: []string{"COLORTERM", "truecolor"},
			want: TerminalCapabilities{TrueColor: true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			isolateCapabilityEnv(t)
			setenvs(t, tc.env...)
			probed := false
			goos := tc.goos
			if goos == "" {
				goos = "linux"
			}
			got := detectCapabilitiesFromEnvironment(func() bool { probed = true; return tc.probe }, goos)
			if got != tc.want {
				t.Fatalf("detectCapabilitiesFromEnvironment() = %+v, want %+v", got, tc.want)
			}
			if probed != tc.wantProbe {
				t.Fatalf("probe called = %v, want %v", probed, tc.wantProbe)
			}
		})
	}
}

// Mirrors terminal-image.ts detectCapabilities: PI_HYPERLINKS, PI_IMAGE_PROTOCOL,
// and PI_TRUE_COLOR override auto-detection, and an explicit PI_HYPERLINKS
// replaces the tmux probe.
func TestDetectCapabilitiesEnvironmentOverrides(t *testing.T) {
	cases := []struct {
		name  string
		env   []string
		probe func(*testing.T) func() bool
		want  TerminalCapabilities
	}{
		{name: "PI_HYPERLINKS=1 inside tmux skips the probe", env: []string{"TMUX", "1", "PI_HYPERLINKS", "1"}, probe: failProbe,
			want: TerminalCapabilities{Hyperlinks: true}},
		{name: "PI_HYPERLINKS=0 disables kitty hyperlinks", env: []string{"KITTY_WINDOW_ID", "1", "PI_HYPERLINKS", "0"},
			want: TerminalCapabilities{Images: ImageProtocolKitty, TrueColor: true}},
		{name: "PI_HYPERLINKS=auto keeps detection", env: []string{"KITTY_WINDOW_ID", "1", "PI_HYPERLINKS", "auto"},
			want: TerminalCapabilities{Images: ImageProtocolKitty, TrueColor: true, Hyperlinks: true}},
		{name: "PI_HYPERLINKS=true is not a boolean override", env: []string{"PI_HYPERLINKS", "true"},
			want: TerminalCapabilities{}},
		{name: "PI_IMAGE_PROTOCOL is case-insensitive", env: []string{"PI_IMAGE_PROTOCOL", "KiTTy"},
			want: TerminalCapabilities{Images: ImageProtocolKitty}},
		{name: "PI_IMAGE_PROTOCOL=iterm2", env: []string{"KITTY_WINDOW_ID", "1", "PI_IMAGE_PROTOCOL", "iterm2"},
			want: TerminalCapabilities{Images: ImageProtocolITerm2, TrueColor: true, Hyperlinks: true}},
		{name: "PI_IMAGE_PROTOCOL=none", env: []string{"KITTY_WINDOW_ID", "1", "PI_IMAGE_PROTOCOL", "none"},
			want: TerminalCapabilities{TrueColor: true, Hyperlinks: true}},
		{name: "PI_IMAGE_PROTOCOL=0", env: []string{"ITERM_SESSION_ID", "1", "PI_IMAGE_PROTOCOL", "0"},
			want: TerminalCapabilities{TrueColor: true, Hyperlinks: true}},
		{name: "PI_IMAGE_PROTOCOL=auto keeps detection", env: []string{"ITERM_SESSION_ID", "1", "PI_IMAGE_PROTOCOL", "auto"},
			want: TerminalCapabilities{Images: ImageProtocolITerm2, TrueColor: true, Hyperlinks: true}},
		{name: "PI_IMAGE_PROTOCOL=sixel is ignored", env: []string{"ITERM_SESSION_ID", "1", "PI_IMAGE_PROTOCOL", "sixel"},
			want: TerminalCapabilities{Images: ImageProtocolITerm2, TrueColor: true, Hyperlinks: true}},
		{name: "PI_TRUE_COLOR=1 on an unknown terminal", env: []string{"PI_TRUE_COLOR", "1"},
			want: TerminalCapabilities{TrueColor: true}},
		{name: "PI_TRUE_COLOR=0 on kitty", env: []string{"KITTY_WINDOW_ID", "1", "PI_TRUE_COLOR", "0"},
			want: TerminalCapabilities{Images: ImageProtocolKitty, Hyperlinks: true}},
		{name: "PI_TRUE_COLOR=yes is ignored", env: []string{"COLORTERM", "truecolor", "PI_TRUE_COLOR", "yes"},
			want: TerminalCapabilities{TrueColor: true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			isolateCapabilityEnv(t)
			setenvs(t, tc.env...)
			probe := func() bool { return false }
			if tc.probe != nil {
				probe = tc.probe(t)
			}
			if got := DetectCapabilities(probe); got != tc.want {
				t.Fatalf("DetectCapabilities() = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// Mirrors terminal-image.ts getCapabilities: settings overrides win over the
// PI_* environment, which wins over auto-detection, and a hyperlinks setting
// replaces the tmux probe.
func TestGetCapabilitiesSettingOverridesBeatEnvironment(t *testing.T) {
	isolateCapabilityEnv(t)
	setenvs(t, "KITTY_WINDOW_ID", "1", "PI_HYPERLINKS", "1", "PI_IMAGE_PROTOCOL", "iterm2", "PI_TRUE_COLOR", "1")
	none := ImageProtocol("")
	SetCapabilityOverrides(CapabilityOverrides{Images: &none, TrueColor: new(false), Hyperlinks: new(false)})
	got := GetCapabilities()
	want := TerminalCapabilities{}
	if got != want {
		t.Fatalf("GetCapabilities() = %+v, want %+v", got, want)
	}

	// Only the set fields override; the rest come from env then detection.
	kitty := ImageProtocolKitty
	SetCapabilityOverrides(CapabilityOverrides{Images: &kitty})
	got = GetCapabilities()
	want = TerminalCapabilities{Images: ImageProtocolKitty, TrueColor: true, Hyperlinks: true}
	if got != want {
		t.Fatalf("partial override GetCapabilities() = %+v, want %+v", got, want)
	}
}

func TestGetCapabilitiesHyperlinksSettingReplacesTmuxProbe(t *testing.T) {
	isolateCapabilityEnv(t)
	setenvs(t, "TMUX", "1", "PATH", "")
	restore := tmuxHyperlinkProbe
	tmuxHyperlinkProbe = failProbe(t)
	t.Cleanup(func() { tmuxHyperlinkProbe = restore })
	SetCapabilityOverrides(CapabilityOverrides{Hyperlinks: new(true)})
	if got := GetCapabilities(); !got.Hyperlinks {
		t.Fatalf("GetCapabilities() = %+v, want hyperlinks from the setting", got)
	}
}

func TestGetCapabilitiesUsesTmuxProbeWithoutOverride(t *testing.T) {
	isolateCapabilityEnv(t)
	setenvs(t, "TMUX", "1")
	restore := tmuxHyperlinkProbe
	calls := 0
	tmuxHyperlinkProbe = func() bool { calls++; return true }
	t.Cleanup(func() { tmuxHyperlinkProbe = restore })
	if got := GetCapabilities(); !got.Hyperlinks {
		t.Fatalf("GetCapabilities() = %+v, want probed hyperlinks", got)
	}
	_ = GetCapabilities()
	if calls != 1 {
		t.Fatalf("tmux probe ran %d times, want once per cached detection", calls)
	}
}

// Mirrors setCapabilityOverrides: an unchanged override set keeps the cache
// (including a setCapabilities value); a changed set drops it.
func TestSetCapabilityOverridesInvalidatesOnlyOnChange(t *testing.T) {
	isolateCapabilityEnv(t)
	pinned := TerminalCapabilities{Images: ImageProtocolKitty, TrueColor: true, Hyperlinks: true}
	SetCapabilities(pinned)
	SetCapabilityOverrides(CapabilityOverrides{})
	if got := GetCapabilities(); got != pinned {
		t.Fatalf("unchanged overrides dropped the cache: %+v", got)
	}
	SetCapabilityOverrides(CapabilityOverrides{TrueColor: new(true)})
	if got := GetCapabilities(); got != (TerminalCapabilities{TrueColor: true}) {
		t.Fatalf("changed overrides kept the stale cache: %+v", got)
	}
	SetCapabilities(pinned)
	SetCapabilityOverrides(CapabilityOverrides{TrueColor: new(true)})
	if got := GetCapabilities(); got != pinned {
		t.Fatalf("equal pointer values must compare by value: %+v", got)
	}
}

func TestTmuxTermfeaturesIncludeHyperlinks(t *testing.T) {
	cases := map[string]bool{
		"":                                  false,
		"256,RGB,title\n":                   false,
		"256, hyperlinks ,title\n":          true,
		"hyperlinks":                        true,
		"clipboard,extkeys,hyperlinksx\n":   false,
		"bpaste,ccolour,hyperlinks,usstyle": true,
	}
	for input, want := range cases {
		if got := tmuxTermfeaturesIncludeHyperlinks(input); got != want {
			t.Errorf("tmuxTermfeaturesIncludeHyperlinks(%q) = %v, want %v", input, got, want)
		}
	}
}
