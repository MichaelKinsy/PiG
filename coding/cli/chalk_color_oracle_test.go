package cli

import (
	"math/rand"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// pi: chalk 6 source/vendor/supports-color/index.js createSupportsColor

type chalkCase struct {
	Env  map[string]string `json:"env"`
	Args []string          `json:"args"`
	TTY  bool              `json:"tty"`
}

// chalkLevelCases draws environments and flags from the decisions supports-color makes: force flags and FORCE_COLOR, CI providers, Azure, TeamCity, COLORTERM, TERM names,
// TERM_PROGRAM with its version text, and the stream being a TTY or not.
func chalkLevelCases(seed int64, count int) []chalkCase {
	rng := rand.New(rand.NewSource(seed))
	pick := func(items ...string) string { return items[rng.Intn(len(items))] }
	var cases []chalkCase
	for range count {
		env := map[string]string{}
		set := func(key string, odds int, values ...string) {
			if rng.Intn(odds) == 0 {
				env[key] = pick(values...)
			}
		}
		set("FORCE_COLOR", 4, "", "true", "false", "0", "1", "2", "3", "4", "12", "999", "1a", "٣", "01", " 1", "1\n", "-1", "1e1", "99999999999999999999")
		set("TERM", 1, "dumb", "xterm", "xterm-256color", "xterm-256", "screen", "screen-256color", "vt100", "vt220", "linux", "", "ANSI", "rxvt-unicode-256color", "foo-256", "XTERM-256COLOR", "xterm-kitty", "wezterm", "xterm-ghostty", "tmux", "tmux-256color", "alacritty", "cygwin", "mycolor", "color", "-256color", "foo-256colors", "Xterm", "ansi.sys", "\u017fcreen", "an\u017fi")
		set("COLORTERM", 3, "truecolor", "", "24bit", "yes", "TRUECOLOR")
		set("TERM_PROGRAM", 3, "iTerm.app", "Apple_Terminal", "vscode", "", "iterm.app")
		set("TERM_PROGRAM_VERSION", 2, "3.4.19", "2.9", "3beta.1", " 3", "", "10", "-1", "3.x", "\ufeff3", "\u00a03", "\u00853", "+3", "3e1", "0x3", "٣", "99999999999999999999999", ".5", "3,5", "2.99")
		set("CI", 5, "", "true", "1")
		set("GITHUB_ACTIONS", 6, "", "true")
		set("GITEA_ACTIONS", 8, "true")
		set("CIRCLECI", 8, "true")
		set("TRAVIS", 8, "true")
		set("GITLAB_CI", 8, "true")
		set("BUILDKITE", 8, "true")
		set("CI_NAME", 8, "codeship", "other", "")
		set("TF_BUILD", 8, "True")
		set("AGENT_NAME", 8, "a")
		set("TEAMCITY_VERSION", 8, "9.0.1.", "9.1.0.", "9.01.2.", "10.0.", "2023.1.", "9.0.0.", "9.0", "8.1.1.", "99.", "9.", "123.4", "1.", "\n12.")
		var args []string
		for _, flag := range []string{"--no-color", "--no-colors", "--color=false", "--color=never", "--color", "--colors", "--color=true", "--color=always", "--color=16m", "--color=full", "--color=truecolor", "--color=256", "--", "-color", "--other"} {
			if rng.Intn(9) == 0 {
				args = append(args, flag)
			}
		}
		rng.Shuffle(len(args), func(i, j int) { args[i], args[j] = args[j], args[i] })
		cases = append(cases, chalkCase{env, args, rng.Intn(2) == 0})
	}
	return cases
}

// chalkLevelWant runs every case through a fresh instance of Pi's supports-color module with the case's process.env and process.argv; the module reads the flags when it loads.
func chalkLevelWant(t *testing.T, cases []chalkCase) []int {
	t.Helper()
	module, err := filepath.Abs("../extension/host/subprocess/runtime-node/shims/chalk/source/vendor/supports-color/index.js")
	if err != nil {
		t.Fatal(err)
	}
	var want []int
	pioracle.Run(t, `
const { pathToFileURL } = await import("node:url");
const out = [];
let n = 0;
for (const c of input.cases) {
	for (const key of Object.keys(process.env)) delete process.env[key];
	for (const [key, value] of Object.entries(c.env)) process.env[key] = value;
	process.argv = ["node", "pi", ...(c.args ?? [])];
	const mod = await import(pathToFileURL(input.module).href + "?case=" + n++);
	const result = mod.createSupportsColor({ isTTY: c.tty });
	out.push(result ? result.level : 0);
}
emit(out);`, map[string]any{"module": module, "cases": cases}, &want)
	return want
}

func TestCLIChalkColorLevelMatchesChalk(t *testing.T) {
	cases := chalkLevelCases(701, 4000)
	want := chalkLevelWant(t, cases)
	failures := 0
	levels := map[int]int{}
	for i, c := range cases {
		got := chalkColorLevel(c.Env, c.Args, c.TTY)
		levels[want[i]]++
		if got != want[i] {
			if failures++; failures <= 10 {
				t.Errorf("env=%q args=%q tty=%v: PiG %d, chalk %d", c.Env, c.Args, c.TTY, got, want[i])
			}
		}
	}
	if failures > 0 {
		t.Errorf("%d of %d cases differ", failures, len(cases))
	}
	for level := range 4 {
		if levels[level] < 100 {
			t.Fatalf("level %d reached by only %d cases: %v", level, levels[level], levels)
		}
	}
}
