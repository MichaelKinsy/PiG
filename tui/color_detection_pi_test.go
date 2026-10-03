package tui

import (
	"bytes"
	"encoding/json"
	"fmt"
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
	for _, value := range []string{"", "15;0", "0;15", "0;15suffix", "15;0suffix", "0;  +15.5", "15;256;invalid", "0;255", "15;-1", "0;1e2", "0;0xF"} {
		add("linux", map[string]string{"COLORFGBG": value})
	}
	input, err := json.Marshal(cases)
	if err != nil {
		t.Fatal(err)
	}
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
	cmd := exec.CommandContext(t.Context(), "node", "testdata/color_detection.mjs", pigversion.UpstreamVersion)
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
			caps := DetectCapabilities(func() bool { return false })
			if caps != want.Capabilities {
				t.Errorf("capabilities = %+v, Pi = %+v", caps, want.Capabilities)
			}
			SetCapabilities(caps)
			for name, expected := range want.Themes {
				SetTheme(name)
				got := ActiveTheme()
				if got.ColorMode() != expected.Mode {
					t.Errorf("%s mode = %s, Pi = %s", name, got.ColorMode(), expected.Mode)
				}
				for token, ansi := range expected.Colors {
					actual := got.Fg(token)
					if strings.HasSuffix(token, "Bg") {
						actual = got.Bg(token)
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
