package tui

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/extensions/sdk/tui/internal/engine"
	"github.com/MichaelKinsy/PiG/extensions/sdk/tui/internal/jsstring"
)

// ImageProtocol is pi-tui's ImageProtocol: "kitty", "iterm2", or "" for upstream's null.
type ImageProtocol string

// The image protocols a terminal can offer.
const (
	ImageProtocolKitty  ImageProtocol = "kitty"
	ImageProtocolITerm2 ImageProtocol = "iterm2"
)

// TerminalCapabilities is pi-tui's TerminalCapabilities.
type TerminalCapabilities struct {
	Images     ImageProtocol
	TrueColor  bool
	Hyperlinks bool
}

// CapabilityOverrides is pi-tui's Partial<TerminalCapabilities> for setCapabilityOverrides; a nil field is absent, and Images
// pointing at "" is upstream's null.
type CapabilityOverrides struct {
	Images     *ImageProtocol
	TrueColor  *bool
	Hyperlinks *bool
}

// Hyperlink is pi-tui's hyperlink: text wrapped in an OSC 8 link to url.
func Hyperlink(text, url string) string { return "\x1b]8;;" + url + "\x1b\\" + text + "\x1b]8;;\x1b\\" }

// probeTmuxHyperlinks is pi-tui's probeTmuxHyperlinks: tmux re-emits OSC 8 only when the attached client's client_termfeatures lists
// hyperlinks. Any error, including the 250 ms timeout, reports false.
func probeTmuxHyperlinks() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	out, err := exec.CommandContext(ctx, "tmux", "display-message", "-p", "#{client_termfeatures}").Output()
	if err != nil {
		return false
	}
	for feature := range strings.SplitSeq(string(out), ",") {
		if strings.TrimSpace(feature) == "hyperlinks" {
			return true
		}
	}
	return false
}

// detectFromEnvironment is pi-tui's detectCapabilitiesFromEnvironment over env, with goos standing in for process.platform.
func detectFromEnvironment(env func(string) string, goos string, tmuxForwardsHyperlink func() bool) TerminalCapabilities {
	termProgram := jsstring.ToLower(env("TERM_PROGRAM"))
	terminalEmulator := jsstring.ToLower(env("TERMINAL_EMULATOR"))
	term := jsstring.ToLower(env("TERM"))
	colorTerm := jsstring.ToLower(env("COLORTERM"))
	hasTrueColorHint := colorTerm == "truecolor" || colorTerm == "24bit" || strings.HasSuffix(term, "-direct")
	switch {
	case env("TMUX") != "" || strings.HasPrefix(term, "tmux"):
		return TerminalCapabilities{TrueColor: hasTrueColorHint, Hyperlinks: tmuxForwardsHyperlink()}
	case strings.HasPrefix(term, "screen"):
		return TerminalCapabilities{TrueColor: hasTrueColorHint}
	case env("KITTY_WINDOW_ID") != "" || termProgram == "kitty",
		termProgram == "ghostty" || strings.Contains(term, "ghostty") || env("GHOSTTY_RESOURCES_DIR") != "",
		env("WEZTERM_PANE") != "" || termProgram == "wezterm",
		termProgram == "warpterminal" || env("WARP_SESSION_ID") != "" || env("WARP_TERMINAL_SESSION_UUID") != "":
		return TerminalCapabilities{Images: ImageProtocolKitty, TrueColor: true, Hyperlinks: true}
	case env("ITERM_SESSION_ID") != "" || termProgram == "iterm.app":
		return TerminalCapabilities{Images: ImageProtocolITerm2, TrueColor: true, Hyperlinks: true}
	case env("WT_SESSION") != "":
		return TerminalCapabilities{TrueColor: true, Hyperlinks: true}
	case termProgram == "alacritty" || termProgram == "vscode" || termProgram == "zed":
		return TerminalCapabilities{TrueColor: true, Hyperlinks: true}
	case terminalEmulator == "jetbrains-jediterm":
		return TerminalCapabilities{TrueColor: true}
	case goos == "windows":
		return TerminalCapabilities{TrueColor: true}
	}
	return TerminalCapabilities{TrueColor: hasTrueColorHint}
}

func parseBooleanCapabilityOverride(value string) *bool {
	switch value {
	case "1":
		t := true
		return &t
	case "0":
		f := false
		return &f
	}
	return nil
}

func detect(env func(string) string, goos string, tmuxForwardsHyperlink func() bool) TerminalCapabilities {
	if tmuxForwardsHyperlink == nil {
		tmuxForwardsHyperlink = probeTmuxHyperlinks
	}
	hyperlinks := parseBooleanCapabilityOverride(env("PI_HYPERLINKS"))
	probe := tmuxForwardsHyperlink
	if hyperlinks != nil {
		probe = func() bool { return *hyperlinks }
	}
	detected := detectFromEnvironment(env, goos, probe)
	switch protocol := jsstring.ToLower(env("PI_IMAGE_PROTOCOL")); protocol {
	case "kitty", "iterm2":
		detected.Images = ImageProtocol(protocol)
	case "none", "0":
		detected.Images = ""
	}
	if trueColor := parseBooleanCapabilityOverride(env("PI_TRUE_COLOR")); trueColor != nil {
		detected.TrueColor = *trueColor
	}
	if hyperlinks != nil {
		detected.Hyperlinks = *hyperlinks
	}
	return detected
}

// DetectCapabilities is pi-tui's detectCapabilities: the terminal's capabilities from the environment, with PI_HYPERLINKS,
// PI_IMAGE_PROTOCOL and PI_TRUE_COLOR overriding detection. A nil tmuxForwardsHyperlink runs the default tmux probe.
func DetectCapabilities(tmuxForwardsHyperlink func() bool) TerminalCapabilities {
	return detect(os.Getenv, runtime.GOOS, tmuxForwardsHyperlink)
}

var capabilityState struct {
	sync.Mutex
	cached    *TerminalCapabilities
	overrides CapabilityOverrides
}

func applyOverrides(caps TerminalCapabilities, o CapabilityOverrides) TerminalCapabilities {
	if o.Images != nil {
		caps.Images = *o.Images
	}
	if o.TrueColor != nil {
		caps.TrueColor = *o.TrueColor
	}
	if o.Hyperlinks != nil {
		caps.Hyperlinks = *o.Hyperlinks
	}
	return caps
}

// GetCapabilities is pi-tui's getCapabilities: the detected capabilities with the overrides on top, cached until
// [ResetCapabilitiesCache] or a changed [SetCapabilityOverrides].
func GetCapabilities() TerminalCapabilities {
	capabilityState.Lock()
	defer capabilityState.Unlock()
	if capabilityState.cached != nil {
		return *capabilityState.cached
	}
	overrides := capabilityState.overrides
	var probe func() bool
	if overrides.Hyperlinks != nil {
		hyperlinks := *overrides.Hyperlinks
		probe = func() bool { return hyperlinks }
	}
	caps := applyOverrides(DetectCapabilities(probe), overrides)
	capabilityState.cached = &caps
	return caps
}

// GetTerminalColorMode is pi-tui's getTerminalColorMode: truecolor when the capabilities report it, else 256 colors. Omit the
// argument for upstream's default, getCapabilities().
func GetTerminalColorMode(capabilities ...TerminalCapabilities) TerminalColorMode {
	var caps TerminalCapabilities
	if len(capabilities) > 0 {
		caps = capabilities[0]
	} else {
		caps = GetCapabilities()
	}
	if caps.TrueColor {
		return engine.TerminalColorModeTrueColor
	}
	return engine.TerminalColorMode256
}

// ResetCapabilitiesCache is pi-tui's resetCapabilitiesCache.
func ResetCapabilitiesCache() {
	capabilityState.Lock()
	capabilityState.cached = nil
	capabilityState.Unlock()
}

func sameBool(a, b *bool) bool { return (a == nil) == (b == nil) && (a == nil || *a == *b) }

// SetCapabilityOverrides is pi-tui's setCapabilityOverrides: it replaces the overrides, and drops the cache only when one changed.
func SetCapabilityOverrides(overrides CapabilityOverrides) {
	capabilityState.Lock()
	defer capabilityState.Unlock()
	cur := capabilityState.overrides
	sameImages := (cur.Images == nil) == (overrides.Images == nil) && (cur.Images == nil || *cur.Images == *overrides.Images)
	if sameImages && sameBool(cur.TrueColor, overrides.TrueColor) && sameBool(cur.Hyperlinks, overrides.Hyperlinks) {
		return
	}
	capabilityState.overrides = overrides
	capabilityState.cached = nil
}

// SetCapabilities is pi-tui's setCapabilities: it replaces the cached capabilities.
func SetCapabilities(caps TerminalCapabilities) {
	capabilityState.Lock()
	capabilityState.cached = &caps
	capabilityState.Unlock()
}
