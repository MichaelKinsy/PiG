package terminals

import (
	"errors"
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// Ports apps/p2/plugins/terminals/commands.ts. The terminal tool's commands (worker): a tmux command name and its argv,
// parsed getopt-style as tmux parses them (options first, flags combinable as `-lp`, values attached or separate, `--`
// ends options), into a typed command. Nothing is split or unquoted: every argument arrives as the script passed it.

// Wait kinds.
const (
	WaitNone  = "none"
	WaitQuiet = "quiet"
	WaitExit  = "exit"
	WaitMatch = "match"
)

// WaitFor is what a wait waits for: no output for a while, the program's exit, a regular expression over the new output.
type WaitFor struct {
	Kind       string
	Pattern    string
	IgnoreCase bool
}

// WaitOptions is a wait as asked: what for, and the timeout in seconds when given.
type WaitOptions struct {
	Wait    *WaitFor
	Timeout *float64
}

// TerminalCommand is a parsed command (a union keyed by Command).
type TerminalCommand struct {
	// Command is one of Commands.
	Command   string
	Name      string
	Directory string
	// ShellCommand is new-window's command, set when HasShellCommand.
	ShellCommand    string
	HasShellCommand bool
	Target          string
	Literal         bool
	Keys            []string
	Scrollback      int
	Escapes         bool
	WaitOptions
}

// Commands are the tool's command names.
var Commands = []string{"new-window", "list-windows", "send-keys", "read", "capture-pane", "kill-window"}

// MaxTimeoutSeconds is the longest timeout a wait may ask for, in seconds.
const MaxTimeoutSeconds = 120

// CommandError is a command line tmux would refuse: the message says why, tmux-style (`usage: …`).
type CommandError struct{ Message string }

func (err *CommandError) Error() string { return err.Message }

type commandSpec struct{ options, usage string }

var specs = map[string]commandSpec{
	"new-window": {
		options: "n:c:w:T:",
		usage:   "new-window [-n window-name] [-c start-directory] [-w wait] [-T seconds] [shell-command ...]",
	},
	"list-windows": {options: "", usage: "list-windows"},
	"send-keys":    {options: "lt:w:T:", usage: "send-keys [-l] [-w wait] [-T seconds] -t target-window key ..."},
	"read":         {options: "t:w:T:", usage: "read [-w wait] [-T seconds] -t target-window"},
	"capture-pane": {options: "ept:S:", usage: "capture-pane [-ep] [-S start-line] -t target-window"},
	"kill-window":  {options: "t:", usage: "kill-window -t target-window"},
}

// allScrollback is the lines of scrollback `capture-pane -S -` asks for: all the host returns.
const allScrollback = 100_000

// ParseCommand parses one command; it returns a *CommandError for anything tmux would refuse.
func ParseCommand(command string, args []string) (TerminalCommand, error) {
	spec := specs[command]
	options, rest, err := getopt(command, spec, args)
	if err != nil {
		return TerminalCommand{}, err
	}
	usage := &CommandError{Message: "usage: " + spec.usage}
	target := func() (string, error) {
		value, present := options["t"]
		if !present || value == "" {
			return "", usage
		}
		return value, nil
	}
	waits := func() (WaitOptions, error) {
		var out WaitOptions
		if wait, ok := options["w"]; ok {
			parsed, err := parseWait(command, wait)
			if err != nil {
				return out, err
			}
			out.Wait = &parsed
		}
		if timeout, ok := options["T"]; ok {
			seconds, err := parseTimeout(command, timeout)
			if err != nil {
				return out, err
			}
			out.Timeout = &seconds
		}
		return out, nil
	}
	_, literal := options["l"]
	_, escapes := options["e"]
	parsed := TerminalCommand{Command: command}
	switch command {
	case "new-window":
		parsed.Name = options["n"]
		parsed.Directory = options["c"]
		if len(rest) > 0 {
			parsed.ShellCommand, parsed.HasShellCommand = strings.Join(rest, " "), true
		}
		if parsed.WaitOptions, err = waits(); err != nil {
			return TerminalCommand{}, err
		}
	case "list-windows":
		if len(rest) > 0 {
			return TerminalCommand{}, usage
		}
	case "send-keys":
		if len(rest) == 0 {
			return TerminalCommand{}, usage
		}
		if parsed.Target, err = target(); err != nil {
			return TerminalCommand{}, err
		}
		parsed.Literal, parsed.Keys = literal, append([]string(nil), rest...)
		if parsed.WaitOptions, err = waits(); err != nil {
			return TerminalCommand{}, err
		}
	case "read":
		if len(rest) > 0 {
			return TerminalCommand{}, usage
		}
		if parsed.Target, err = target(); err != nil {
			return TerminalCommand{}, err
		}
		if parsed.WaitOptions, err = waits(); err != nil {
			return TerminalCommand{}, err
		}
	case "capture-pane":
		if len(rest) > 0 {
			return TerminalCommand{}, usage
		}
		if parsed.Target, err = target(); err != nil {
			return TerminalCommand{}, err
		}
		start, present := options["S"]
		if parsed.Scrollback, err = parseStart(command, start, present); err != nil {
			return TerminalCommand{}, err
		}
		parsed.Escapes = escapes
	case "kill-window":
		if len(rest) > 0 {
			return TerminalCommand{}, usage
		}
		if parsed.Target, err = target(); err != nil {
			return TerminalCommand{}, err
		}
	}
	return parsed, nil
}

// getopt is tmux's: options until the first argument that is not one (or `--`); flags combine (`-ep`); an option's value
// is the rest of its argument or the next argument, even one starting with `-` (`-S -50`).
func getopt(command string, spec commandSpec, args []string) (map[string]string, []string, error) {
	options := map[string]string{}
	index := 0
	for ; index < len(args); index++ {
		arg := args[index]
		if arg == "--" {
			index++
			break
		}
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			break
		}
		// The argument is read by code unit like JavaScript's `arg[at]`; options are ASCII letters.
		units := []rune(arg)
		for at := 1; at < len(units); at++ {
			letter := units[at]
			position := strings.IndexRune(spec.options, letter)
			if position < 0 || letter == ':' {
				return nil, nil, &CommandError{Message: command + ": unknown option -- " + string(letter) + "\nusage: " + spec.usage}
			}
			if position+1 >= len(spec.options) || spec.options[position+1] != ':' {
				options[string(letter)] = ""
				continue
			}
			var value string
			if at+1 < len(units) {
				value = string(units[at+1:])
			} else {
				index++
				if index >= len(args) {
					return nil, nil, &CommandError{Message: command + ": option requires an argument -- " + string(letter) + "\nusage: " + spec.usage}
				}
				value = args[index]
			}
			options[string(letter)] = value
			break
		}
	}
	if index > len(args) {
		index = len(args)
	}
	return options, args[index:], nil
}

var waitRegex = regexp.MustCompile(`(?s)^/(.*)/(i?)$`)

// parseWait is `quiet`, `exit`, `none` or `/regex/` (`/regex/i` ignores case).
func parseWait(command, value string) (WaitFor, error) {
	if value == WaitQuiet || value == WaitExit || value == WaitNone {
		return WaitFor{Kind: value}, nil
	}
	match := waitRegex.FindStringSubmatch(value)
	if match == nil {
		return WaitFor{}, &CommandError{Message: command + ": invalid wait: " + value + " (quiet, exit, none or /regex/)"}
	}
	if _, err := CompileJSRegExp(match[1], false); err != nil {
		return WaitFor{}, &CommandError{Message: command + ": invalid regex: " + err.Error()}
	}
	return WaitFor{Kind: WaitMatch, Pattern: match[1], IgnoreCase: match[2] == "i"}, nil
}

func parseTimeout(command, value string) (float64, error) {
	seconds, ok := jsNumberOf(value)
	if strings.TrimSpace(jsstring.Trim(value)) == "" || !ok || math.IsInf(seconds, 0) || math.IsNaN(seconds) || seconds < 0 || seconds > MaxTimeoutSeconds {
		return 0, &CommandError{Message: command + ": invalid timeout: " + value + " (0 to " + strconv.Itoa(MaxTimeoutSeconds) + " seconds)"}
	}
	return seconds, nil
}

var (
	decimalNumber = regexp.MustCompile(`^[+-]?(?:\d+\.?\d*|\.\d+)(?:[eE][+-]?\d+)?$`)
	radixNumber   = regexp.MustCompile(`^0(?:[xX][0-9a-fA-F]+|[oO][0-7]+|[bB][01]+)$`)
)

// jsNumberOf is JavaScript's `Number(text)` for text that is a finite number; false for NaN and the infinities.
func jsNumberOf(text string) (float64, bool) {
	text = jsstring.Trim(text)
	switch {
	case radixNumber.MatchString(text):
		base := map[byte]int{'x': 16, 'X': 16, 'o': 8, 'O': 8, 'b': 2, 'B': 2}[text[1]]
		number, err := strconv.ParseUint(text[2:], base, 64)
		if err != nil {
			var big float64
			for _, digit := range text[2:] {
				value, _ := strconv.ParseUint(string(digit), 16, 8)
				big = big*float64(base) + float64(value)
			}
			return big, true
		}
		return float64(number), true
	case decimalNumber.MatchString(text):
		number, err := strconv.ParseFloat(text, 64)
		if err != nil && !errors.Is(err, strconv.ErrRange) {
			return 0, false
		}
		return number, true
	}
	return 0, false
}

var startNumber = regexp.MustCompile(`^-?\d+$`)

// parseStart is `-S`: the first line, `-N` for N lines of scrollback, `-` for all of it; 0 or none for the screen only.
func parseStart(command, value string, present bool) (int, error) {
	if !present {
		return 0, nil
	}
	if value == "-" {
		return allScrollback, nil
	}
	if !startNumber.MatchString(value) {
		return 0, &CommandError{Message: command + ": invalid start line: " + value}
	}
	number, _ := strconv.ParseFloat(value, 64)
	return int(math.Min(allScrollback, math.Max(0, -number))), nil
}
