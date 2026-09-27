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
	}
	for _, c := range cases {
		getenv := func(key string) string { return c.env[key] }
		lookup := func(key string) (string, bool) { value, ok := c.env[key]; return value, ok }
		if got := chalkColorLevel(c.args, getenv, lookup, c.tty, "linux", [3]int{}); got != c.level {
			t.Errorf("%s: level %d, want %d", c.name, got, c.level)
		}
	}
}
