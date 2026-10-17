package codingagent

import "testing"

// chalkColorLevel follows chalk's supports-color decisions for the cases
// that decide whether theme.bold draws: a TTY, a pipe, FORCE_COLOR, the
// color flags and CI.
func TestChalkColorLevel(t *testing.T) {
	cases := []struct {
		name  string
		args  []string
		env   map[string]string
		tty   bool
		level int
	}{
		{"tty xterm", nil, map[string]string{"TERM": "xterm-256color"}, true, 2},
		{"pipe", nil, map[string]string{"TERM": "xterm-256color"}, false, 0},
		{"pipe forced", nil, map[string]string{"FORCE_COLOR": "1"}, false, 1},
		{"pipe forced true", nil, map[string]string{"FORCE_COLOR": "true", "TERM": "dumb"}, false, 1},
		{"forced off", nil, map[string]string{"FORCE_COLOR": "0", "TERM": "xterm"}, true, 0},
		{"no-color flag", []string{"--no-color"}, map[string]string{"TERM": "xterm"}, true, 0},
		{"github actions pipe", nil, map[string]string{"CI": "true", "GITHUB_ACTIONS": "true"}, false, 0},
		{"github actions tty", nil, map[string]string{"CI": "true", "GITHUB_ACTIONS": "true"}, true, 3},
		{"azure pipelines pipe", nil, map[string]string{"TF_BUILD": "1", "AGENT_NAME": "a"}, false, 1},
		{"dumb tty", nil, map[string]string{"TERM": "dumb"}, true, 0},
		{"truecolor", nil, map[string]string{"COLORTERM": "truecolor"}, true, 3},
		// supports-color: Number.parseInt(TERM_PROGRAM_VERSION.split('.', 1)[0], 10) >= 3 for iTerm.app. parseInt reads a decimal prefix after JS whitespace.
		{"iTerm 3", nil, map[string]string{"TERM_PROGRAM": "iTerm.app", "TERM_PROGRAM_VERSION": "3.4.19"}, true, 3},
		{"iTerm 2", nil, map[string]string{"TERM_PROGRAM": "iTerm.app", "TERM_PROGRAM_VERSION": "2.9"}, true, 2},
		{"iTerm version with a suffix", nil, map[string]string{"TERM_PROGRAM": "iTerm.app", "TERM_PROGRAM_VERSION": "3beta.1"}, true, 3},
		{"iTerm version after JS whitespace", nil, map[string]string{"TERM_PROGRAM": "iTerm.app", "TERM_PROGRAM_VERSION": "\ufeff\u00a03.1"}, true, 3},
		{"iTerm version beyond int64", nil, map[string]string{"TERM_PROGRAM": "iTerm.app", "TERM_PROGRAM_VERSION": "99999999999999999999999"}, true, 3},
		{"iTerm negative version", nil, map[string]string{"TERM_PROGRAM": "iTerm.app", "TERM_PROGRAM_VERSION": "-3"}, true, 2},
		{"iTerm version after NEL is NaN", nil, map[string]string{"TERM_PROGRAM": "iTerm.app", "TERM_PROGRAM_VERSION": "\u00853"}, true, 2},
		{"iTerm without a version", nil, map[string]string{"TERM_PROGRAM": "iTerm.app"}, true, 2},
	}
	for _, c := range cases {
		getenv := func(key string) string { return c.env[key] }
		lookup := func(key string) (string, bool) { value, ok := c.env[key]; return value, ok }
		if got := chalkColorLevel(c.args, getenv, lookup, c.tty, "linux", [3]int{}); got != c.level {
			t.Errorf("%s: level %d, want %d", c.name, got, c.level)
		}
	}
}
