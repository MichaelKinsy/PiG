package cli

import (
	"os"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/codingagent"

	"golang.org/x/term"
)

// chalkColorLevel is chalk's supports-color level for a stream with env as the process environment and argv as the arguments
// (codingagent.ChalkColorLevel, the one port of chalk/source/vendor/supports-color/index.js _supportsColor).
func chalkColorLevel(env map[string]string, argv []string, isTTY bool) int {
	lookup := func(key string) (string, bool) { value, ok := env[key]; return value, ok }
	return codingagent.ChalkColorLevel(argv, func(key string) string { return env[key] }, lookup, isTTY)
}

// environMap splits os.Environ-style entries into a map (last value wins).
func environMap(environ []string) map[string]string {
	env := make(map[string]string, len(environ))
	for _, kv := range environ {
		if k, v, ok := strings.Cut(kv, "="); ok {
			env[k] = v
		}
	}
	return env
}

// chalkStdoutColor reports whether upstream's default chalk instance emits colors in this process. chalk sizes its level from stdout
// (supports-color's stdout check), whatever stream the text is written to, so a red error on stderr is plain when stdout is redirected
// and colored under FORCE_COLOR.
func chalkStdoutColor() bool {
	return chalkColorLevel(environMap(os.Environ()), os.Args[1:], term.IsTerminal(int(os.Stdout.Fd()))) > 0
}

// chalkStyle is `chalk.<style>(text)` on upstream's default chalk instance: applyStyle with open and closeSeq at a nonzero stdout level, the text unchanged otherwise.
func chalkStyle(open, closeSeq, text string) string {
	if !chalkStdoutColor() {
		return text
	}
	return codingagent.ChalkApply(open, closeSeq, text)
}

func chalkRed(text string) string    { return chalkStyle("\x1b[31m", "\x1b[39m", text) }
func chalkGreen(text string) string  { return chalkStyle("\x1b[32m", "\x1b[39m", text) }
func chalkYellow(text string) string { return chalkStyle("\x1b[33m", "\x1b[39m", text) }
func chalkDim(text string) string    { return chalkStyle("\x1b[2m", "\x1b[22m", text) }
func chalkBold(text string) string   { return chalkStyle("\x1b[1m", "\x1b[22m", text) }
