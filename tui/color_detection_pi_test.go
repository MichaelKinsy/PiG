package tui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

type piColorCase struct {
	Env      map[string]string `json:"env"`
	Platform string            `json:"platform"`
	// TmuxProbe is what the tmux hyperlink probe reports; false when absent.
	TmuxProbe bool `json:"tmuxProbe,omitempty"`
}

type piColorResult struct {
	Capabilities TerminalCapabilities
	Themes       map[string]struct {
		Mode   TerminalColorMode
		Colors map[string]string
	}
	ColorFgBg TerminalTheme
}

// Upstream 0.99.1 terminal-image.ts detectCapabilities and theme.ts createTheme select only truecolor
// or 256color. NO_COLOR/FORCE_COLOR affect Chalk, not these theme escapes.
func TestColorDetectionMatchesPi(t *testing.T) {
	cases := colorDetectionCases()
	input, err := json.Marshal(cases)
	if err != nil {
		t.Fatal(err)
	}
	runColorDetectionCases(t, cases, input)
}

// colorDetectionCases is the environments of the terminal capability and theme comparison: TERM and COLORTERM, named terminals with and without tmux, session variables, the PI_* overrides and the tmux hyperlink probe both ways, on three platforms.
func colorDetectionCases() []piColorCase {
	var cases []piColorCase
	add := func(platform string, env map[string]string) {
		cases = append(cases, piColorCase{Env: env, Platform: platform})
	}
	for _, platform := range []string{"linux", "darwin", "win32"} {
		for _, term := range []string{"", "dumb", "ansi", "xterm", "xterm-256color", "screen-256color", "tmux-256color", "xterm-kitty", "xterm-ghostty", "wezterm"} {
			for _, colorterm := range []string{"", "truecolor", "24bit", "TRUECOLOR", "false"} {
				add(platform, map[string]string{"TERM": term, "COLORTERM": colorterm})
			}
		}
		for _, program := range []string{"Apple_Terminal", "iTerm.app", "vscode", "JetBrains-JediTerm", "ghostty", "kitty", "WezTerm", "Alacritty", "WarpTerminal", "zed"} {
			for _, tmux := range []string{"", "1"} {
				add(platform, map[string]string{"TERM_PROGRAM": program, "TMUX": tmux})
			}
		}
		for _, key := range []string{"WT_SESSION", "ITERM_SESSION_ID", "KITTY_WINDOW_ID", "WEZTERM_PANE", "GHOSTTY_RESOURCES_DIR", "SSH_TTY", "SSH_CONNECTION", "NO_COLOR", "FORCE_COLOR", "CI", "PI_TRUE_COLOR"} {
			for _, value := range []string{"", "0", "1", "3"} {
				for _, hint := range []string{"", "truecolor"} {
					add(platform, map[string]string{key: value, "COLORTERM": hint})
				}
			}
		}
		add(platform, map[string]string{"TERMINAL_EMULATOR": "JetBrains-JediTerm"})
	}
	// Environment overrides and the terminals the detector names, with the tmux hyperlink probe both ways.
	addProbe := func(platform string, probe bool, env map[string]string) {
		cases = append(cases, piColorCase{Env: env, Platform: platform, TmuxProbe: probe})
	}
	for _, platform := range []string{"linux", "win32"} {
		for _, probe := range []bool{false, true} {
			for _, base := range []map[string]string{{}, {"TMUX": "/tmp/tmux-1/default,1,0"}, {"TERM": "tmux-256color"}, {"TERM": "TMUX"}, {"TERM": "screen"}, {"TERM": "SCREEN-256color"}, {"TERM_PROGRAM": "herdr"}, {"TERM_PROGRAM": "Herdr", "KITTY_WINDOW_ID": "1"}, {"TERM_PROGRAM": "rex"}, {"TERM_PROGRAM": "Rex", "KITTY_WINDOW_ID": "1"}, {"TERM_PROGRAM": "rex", "TERM": "xterm-direct"}, {"TERM_PROGRAM": "herdr", "TERM": "xterm-direct"}, {"TERM": "xterm-direct"}, {"TERM": "XTERM-DIRECT"}, {"WARP_SESSION_ID": "x"}, {"WARP_TERMINAL_SESSION_UUID": "x"}, {"TERM_PROGRAM": "warpterminal"}, {"TERM": "xterm-ghostty"}, {"TERM_PROGRAM": "GhosTTY"}, {"TERMINAL_EMULATOR": "jetbrains-jediterm", "WT_SESSION": "1"}, {"TERM_PROGRAM": "kitty", "TMUX": "1"}, {"TERM_PROGRAM": "iTerm.app", "TERM": "screen"}} {
				for _, override := range []map[string]string{{}, {"PI_IMAGE_PROTOCOL": "kitty"}, {"PI_IMAGE_PROTOCOL": "KITTY"}, {"PI_IMAGE_PROTOCOL": "iterm2"}, {"PI_IMAGE_PROTOCOL": "iTerm2"}, {"PI_IMAGE_PROTOCOL": "none"}, {"PI_IMAGE_PROTOCOL": "NONE"}, {"PI_IMAGE_PROTOCOL": "0"}, {"PI_IMAGE_PROTOCOL": "1"}, {"PI_IMAGE_PROTOCOL": "sixel"}, {"PI_HYPERLINKS": "1"}, {"PI_HYPERLINKS": "0"}, {"PI_HYPERLINKS": "2"}, {"PI_HYPERLINKS": ""}, {"PI_TRUE_COLOR": "1"}, {"PI_TRUE_COLOR": "0"}, {"PI_TRUE_COLOR": "true"}, {"PI_IMAGE_PROTOCOL": "kitty", "PI_HYPERLINKS": "0", "PI_TRUE_COLOR": "0"}} {
					env := map[string]string{}
					maps.Copy(env, base)
					maps.Copy(env, override)
					addProbe(platform, probe, env)
				}
			}
		}
	}
	for _, value := range []string{"", "15;0", "0;15", "0;15suffix", "15;0suffix", "0;  +15.5", "15;256;invalid", "0;255", "15;-1", "0;1e2", "0;0xF"} {
		add("linux", map[string]string{"COLORFGBG": value})
	}
	return cases
}

func runColorDetectionCases(t *testing.T, cases []piColorCase, input []byte) {
	t.Helper()
	// Importing Pi's theme initializes Node's stdin stream through node:process. A later readFileSync(0) can hit EAGAIN on a partially filled pipe. Give the oracle its own completed regular file, never the test process's stdin.
	inputPath := filepath.Join(t.TempDir(), "color-cases.json")
	if err := os.WriteFile(inputPath, input, 0o600); err != nil {
		t.Fatal(err)
	}
	inputFile, err := os.Open(inputPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = inputFile.Close() })
	cmd := exec.CommandContext(t.Context(), "node", "testdata/color_detection.mjs", pigversion.UpstreamPackageVersion)
	cmd.Stdin = inputFile
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v: %s", err, stderr.String())
	}
	var results []piColorResult
	if err := json.Unmarshal(output, &results); err != nil {
		t.Fatal(err)
	}
	if len(results) != len(cases) {
		t.Fatalf("Pi returned %d results for %d cases", len(results), len(cases))
	}
	oldOS, previous := capabilityDetectGOOS, ActiveTheme()
	t.Cleanup(func() { capabilityDetectGOOS = oldOS; ResetCapabilitiesCache(); activeTheme.Store(previous) })
	// Empty all variables used by either detector; each case is hermetic.
	keys := []string{"TERM_PROGRAM", "TERMINAL_EMULATOR", "TERM", "COLORTERM", "TMUX", "KITTY_WINDOW_ID", "GHOSTTY_RESOURCES_DIR", "WEZTERM_PANE", "WARP_SESSION_ID", "WARP_TERMINAL_SESSION_UUID", "ITERM_SESSION_ID", "WT_SESSION", "PI_TRUE_COLOR", "PI_HYPERLINKS", "PI_IMAGE_PROTOCOL", "HERDR_ENV", "HERDR_KITTY_GRAPHICS", "COLORFGBG", "SSH_TTY", "SSH_CONNECTION", "NO_COLOR", "FORCE_COLOR", "CI"}
	for i, tc := range cases {
		t.Run(fmt.Sprintf("%d/%s/%v", i, tc.Platform, tc.Env), func(t *testing.T) {
			for _, key := range keys {
				t.Setenv(key, tc.Env[key])
			}
			capabilityDetectGOOS = tc.Platform
			if tc.Platform == "win32" {
				capabilityDetectGOOS = "windows"
			}
			want := results[i]
			caps := DetectCapabilities(func() bool { return tc.TmuxProbe })
			if caps != want.Capabilities {
				t.Errorf("capabilities = %+v, Pi = %+v", caps, want.Capabilities)
			}
			SetCapabilities(caps)
			for name, expected := range want.Themes {
				SetTheme(name)
				got := ActiveTheme()
				if got.GetColorMode() != expected.Mode {
					t.Errorf("%s mode = %s, Pi = %s", name, got.GetColorMode(), expected.Mode)
				}
				for token, ansi := range expected.Colors {
					// Like Pi's getFgAnsi, GetFgAnsi panics for a background token, so each token is read from its own table.
					var actual string
					if strings.HasSuffix(token, "Bg") {
						actual = got.GetBgAnsi(token)
					} else {
						actual = got.GetFgAnsi(token)
					}
					if actual != ansi {
						t.Errorf("%s %s = %q, Pi = %q", name, token, actual, ansi)
					}
				}
			}
			if got := DetectColorFgBgTheme(tc.Env); got != want.ColorFgBg {
				t.Errorf("COLORFGBG theme = %q, Pi = %q", got, want.ColorFgBg)
			}
		})
	}
}

// colorDetectionKeys are the variables either detector reads; each case sets them all.
var colorDetectionKeys = []string{"TERM_PROGRAM", "TERMINAL_EMULATOR", "TERM", "COLORTERM", "TMUX", "KITTY_WINDOW_ID", "GHOSTTY_RESOURCES_DIR", "WEZTERM_PANE", "WARP_SESSION_ID", "WARP_TERMINAL_SESSION_UUID", "ITERM_SESSION_ID", "WT_SESSION", "PI_TRUE_COLOR", "PI_HYPERLINKS", "PI_IMAGE_PROTOCOL", "HERDR_ENV", "HERDR_KITTY_GRAPHICS", "COLORFGBG", "SSH_TTY", "SSH_CONNECTION", "NO_COLOR", "FORCE_COLOR", "CI"}

// TestColorDetectionProbesDump prints the environments for the Pi side of the tui-components/37 parity scenario.
func TestColorDetectionProbesDump(t *testing.T) {
	line, err := json.Marshal(colorDetectionCases())
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("capability-probes:%s\n", line)
}

// TestColorDetectionProbesParity prints PiG's detected capabilities of each environment as one JSON line [images, trueColor, hyperlinks] for the tui-components/37 parity scenario.
func TestColorDetectionProbesParity(t *testing.T) {
	oldOS := capabilityDetectGOOS
	t.Cleanup(func() { capabilityDetectGOOS = oldOS; ResetCapabilitiesCache() })
	for _, tc := range colorDetectionCases() {
		func() {
			for _, key := range colorDetectionKeys {
				t.Setenv(key, tc.Env[key])
			}
			capabilityDetectGOOS = tc.Platform
			if tc.Platform == "win32" {
				capabilityDetectGOOS = "windows"
			}
			caps := DetectCapabilities(func() bool { return tc.TmuxProbe })
			var images any
			if caps.Images != "" {
				images = string(caps.Images)
			}
			line, err := json.Marshal([]any{images, caps.TrueColor, caps.Hyperlinks})
			if err != nil {
				t.Fatal(err)
			}
			fmt.Printf("capability-observation:%s\n", line)
		}()
	}
}
