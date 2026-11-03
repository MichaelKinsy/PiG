package tui

import "maps"

// detectCapabilitiesTriggers is one environment per branch of terminal-image.ts detectCapabilitiesFromEnvironment, in addition to the unset environment.
// Every pair of them runs, so the order in which the branches are checked decides each result.
var detectCapabilitiesTriggers = []map[string]string{
	{"TMUX": "/tmp/tmux-1/default,1,0"},
	{"TERM": "tmux-256color"},
	{"TERM": "screen-256color"},
	{"TERM_PROGRAM": "herdr"},
	{"TERM_PROGRAM": "rex"},
	{"TERM_PROGRAM": "orca", "ORCA_IMAGE_PROTOCOL": "kitty"},
	{"TERM_PROGRAM": "orca"},
	{"TERM_PROGRAM": "orca", "ORCA_IMAGE_PROTOCOL": "sixel"},
	{"ORCA_IMAGE_PROTOCOL": "kitty"},
	{"KITTY_WINDOW_ID": "7"},
	{"TERM_PROGRAM": "kitty"},
	{"TERM_PROGRAM": "ghostty"},
	{"TERM": "xterm-ghostty"},
	{"GHOSTTY_RESOURCES_DIR": "/usr/share/ghostty"},
	{"WEZTERM_PANE": "3"},
	{"TERM_PROGRAM": "WezTerm"},
	{"TERM_PROGRAM": "WarpTerminal"},
	{"WARP_SESSION_ID": "x"},
	{"WARP_TERMINAL_SESSION_UUID": "x"},
	{"ITERM_SESSION_ID": "w0t0p0"},
	{"TERM_PROGRAM": "iTerm.app"},
	{"WT_SESSION": "1"},
	{"TERM_PROGRAM": "alacritty"},
	{"TERM_PROGRAM": "vscode"},
	{"TERM_PROGRAM": "zed"},
	{"TERMINAL_EMULATOR": "JetBrains-JediTerm"},
	{"COLORTERM": "truecolor"},
	{"TERM": "xterm-direct"},
}

// detectCapabilitiesBranchOrderCases is every environment, every pair of environments, with the tmux hyperlink probe answering both ways, on Linux and on a Windows console.
// colorDetectionCases includes it, so the Pi comparison of TestColorDetectionMatchesPi and the tui-components/37 scenario check the order in which detectCapabilitiesFromEnvironment tests its branches.
func detectCapabilitiesBranchOrderCases() []piColorCase {
	var cases []piColorCase
	add := func(platform string, probe bool, env map[string]string) {
		cases = append(cases, piColorCase{Env: env, Platform: platform, TmuxProbe: probe})
	}
	for _, platform := range []string{"linux", "win32"} {
		for _, probe := range []bool{false, true} {
			add(platform, probe, map[string]string{})
			for first, one := range detectCapabilitiesTriggers {
				add(platform, probe, maps.Clone(one))
				for _, two := range detectCapabilitiesTriggers[first+1:] {
					env := maps.Clone(one)
					maps.Copy(env, two)
					if len(env) < len(one)+len(two) {
						// The two environments set one variable differently; the later one would replace the earlier, so the pair adds nothing.
						continue
					}
					add(platform, probe, env)
				}
			}
		}
	}
	return cases
}
