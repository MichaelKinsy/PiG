package evals

import "testing"

// TestNodeColorDepthAboveTwoMatchesNode pins getColorDepth(env) > 2 as measured with Node v24.19 on Linux through
// tty.WriteStream.prototype.getColorDepth.call({}, env), the depth that util.styleText's shouldColorize compares.
func TestNodeColorDepthAboveTwoMatchesNode(t *testing.T) {
	for _, testCase := range []struct {
		env  map[string]string
		want bool
	}{
		{map[string]string{}, false},
		{map[string]string{"TERM": "xterm"}, true},
		{map[string]string{"TERM": "dumb"}, false},
		{map[string]string{"TERM": "dumb", "COLORTERM": "truecolor"}, false},
		{map[string]string{"TERM": "foo"}, false},
		{map[string]string{"TERM": "xterm-256color"}, true},
		{map[string]string{"TERM": "Xterm-256color"}, true},
		{map[string]string{"TERM": "XTERM"}, true},
		{map[string]string{"TERM": "screen.xterm"}, true},
		{map[string]string{"TERM": "linux"}, true},
		{map[string]string{"TERM": "vt220"}, true},
		{map[string]string{"TERM": "rxvt"}, true},
		{map[string]string{"TERM": "Eterm"}, true},
		{map[string]string{"TERM": "TRUECOLOR"}, true},
		{map[string]string{"TERM": "con80x25"}, true},
		{map[string]string{"TERM": "con"}, false},
		{map[string]string{"TERM": "mosh"}, true},
		{map[string]string{"TERM": "constructor", "COLORTERM": "x"}, false},
		{map[string]string{"TERM": "__proto__", "COLORTERM": "x"}, false},
		{map[string]string{"TERM": "foo", "COLORTERM": "x"}, true},
		{map[string]string{"COLORTERM": "x"}, true},
		{map[string]string{"COLORTERM": "24bit"}, true},
		{map[string]string{"TMUX": "1"}, true},
		{map[string]string{"TMUX": ""}, false},
		{map[string]string{"CI": "1"}, false},
		{map[string]string{"CI": "", "TERM": "xterm"}, false},
		{map[string]string{"CI": "1", "GITHUB_ACTIONS": ""}, true},
		{map[string]string{"CI": "1", "CI_NAME": "codeship"}, true},
		{map[string]string{"TF_BUILD": "", "AGENT_NAME": ""}, true},
		{map[string]string{"TF_BUILD": "1"}, false},
		{map[string]string{"TEAMCITY_VERSION": "9.1.0"}, true},
		{map[string]string{"TEAMCITY_VERSION": "9.0.1"}, false},
		{map[string]string{"TEAMCITY_VERSION": "10.0"}, true},
		{map[string]string{"TEAMCITY_VERSION": "8.0", "TERM": "xterm"}, false},
		{map[string]string{"TERM_PROGRAM": "iTerm.app"}, true},
		{map[string]string{"TERM_PROGRAM": "MacTerm"}, true},
		{map[string]string{"TERM_PROGRAM": "Apple_Terminal"}, true},
		{map[string]string{"TERM_PROGRAM": "vscode"}, false},
		{map[string]string{"NO_COLOR": "", "TERM": "xterm"}, true},
		{map[string]string{"NO_COLOR": "0", "TERM": "xterm"}, false},
		{map[string]string{"NODE_DISABLE_COLORS": "", "TERM": "xterm"}, true},
		{map[string]string{"NODE_DISABLE_COLORS": "1", "TERM": "xterm"}, false},
		{map[string]string{"FORCE_COLOR": ""}, true},
		{map[string]string{"FORCE_COLOR": "0", "TERM": "xterm"}, false},
		{map[string]string{"FORCE_COLOR": "1"}, true},
		{map[string]string{"FORCE_COLOR": "true"}, true},
		{map[string]string{"FORCE_COLOR": "2"}, true},
		{map[string]string{"FORCE_COLOR": "3"}, true},
		{map[string]string{"FORCE_COLOR": "4", "TERM": "xterm"}, false},
		{map[string]string{"FORCE_COLOR": "false"}, false},
		{map[string]string{"FORCE_COLOR": "yes"}, false},
	} {
		lookup := func(name string) (string, bool) {
			value, ok := testCase.env[name]
			return value, ok
		}
		if got := nodeColorDepthAboveTwo(lookup, "linux"); got != testCase.want {
			t.Errorf("getColorDepth(%v) > 2 = %v, want %v", testCase.env, got, testCase.want)
		}
	}
	// Windows reports at least 16 colors unless the environment disables color first.
	windows := map[string]string{"TERM": "foo"}
	if !nodeColorDepthAboveTwo(func(name string) (string, bool) { value, ok := windows[name]; return value, ok }, "windows") {
		t.Error("Windows color depth must be above two")
	}
	windows["NO_COLOR"] = "1"
	if nodeColorDepthAboveTwo(func(name string) (string, bool) { value, ok := windows[name]; return value, ok }, "windows") {
		t.Error("NO_COLOR must disable Windows color")
	}
}
